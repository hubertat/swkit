package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/hubertat/swkit/app"
)

// maxConfigEditBodyBytes caps the size of a POST /api/config/edit request body.
const maxConfigEditBodyBytes = 1 << 20 // 1 MiB

// handleApiConfigEdit dispatches GET/POST /api/config/edit. It requires a
// wired ConfigProvider (503 otherwise, on either method).
func (ws *WebServer) handleApiConfigEdit(w http.ResponseWriter, r *http.Request) {
	if ws.opts.ConfigProvider == nil {
		writeConfigEditErrors(w, http.StatusServiceUnavailable, []string{"config editing is not available (no config provider configured)"})
		return
	}

	switch r.Method {
	case http.MethodGet:
		ws.handleConfigEditGet(w, r)
	case http.MethodPost:
		ws.handleConfigEditPost(w, r)
	default:
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleConfigEditGet returns the current editable config plus metadata the
// frontend needs to build edit forms (event/action vocab, output device
// names, configured drivers, IO suggestions).
func (ws *WebServer) handleConfigEditGet(w http.ResponseWriter, r *http.Request) {
	// Revision is read BEFORE the config snapshot, and the order matters.
	// The two are separate, independently locked provider calls, so a save
	// can land between them; reading the revision first means the revision
	// can only ever be older than (or equal to) the snapshot it is paired
	// with. Pairing a snapshot with an already-newer revision would be the
	// dangerous direction: the POST's compare-and-swap would then match
	// against a config the user never saw and silently overwrite it - the
	// exact lost update this revision handshake exists to prevent. With
	// this order the worst case is the harmless one: a save landing in the
	// window makes the next POST fail its conflict check and ask the user
	// to reload, even though the snapshot they got was already current.
	revision := ws.opts.ConfigProvider.Revision()
	cfg := ws.opts.ConfigProvider.GetEditableConfig()
	state := ws.provider.GetState()

	resp := apiConfigEditResponse{
		Config: cfg,
		Meta:   ws.configEditMeta(cfg, state),
		// Revision lets the frontend detect, on save, whether the config
		// changed underneath this edit session (another tab, or an
		// out-of-band reload) - see handleConfigEditPost.
		Revision: revision,
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache")
	json.NewEncoder(w).Encode(resp)
}

// handleConfigEditPost validates and, on success, persists a full
// EditableConfig replacement via ConfigProvider.SaveConfig.
func (ws *WebServer) handleConfigEditPost(w http.ResponseWriter, r *http.Request) {
	// ---- CSRF hardening ----
	// A real cross-site attacker can point a <form> at this endpoint, but a
	// browser form submission can only carry one of the CORS-safelisted
	// Content-Types (text/plain, application/x-www-form-urlencoded,
	// multipart/form-data) - never application/json - so requiring the JSON
	// type alone defeats the classic "text/plain body that happens to decode
	// as JSON anyway" attack (json.Decoder ignores trailing garbage). The
	// Sec-Fetch-Site/Origin checks are defense in depth for browsers/proxies
	// that don't enforce the Content-Type restriction.
	if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(strings.ToLower(ct), "application/json") {
		writeConfigEditErrors(w, http.StatusUnsupportedMediaType, []string{"Content-Type must be application/json"})
		return
	}
	if err := checkSameOriginPost(r); err != nil {
		writeConfigEditErrors(w, http.StatusForbidden, []string{err.Error()})
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxConfigEditBodyBytes)

	var body apiConfigEditPostBody
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		writeConfigEditErrors(w, http.StatusBadRequest, []string{"invalid request body: " + err.Error()})
		return
	}

	// Trim whitespace once, up front, so validation (which checks for
	// emptiness/duplicates/targeting) and persistence (SaveConfig, which
	// writes exactly what was validated) always agree - see
	// app.NormalizeEditableConfig.
	app.NormalizeEditableConfig(&body.Config)

	state := ws.provider.GetState()
	errs := app.ValidateEditableConfig(body.Config, state)
	if len(errs) > 0 {
		writeConfigEditErrors(w, http.StatusBadRequest, errs)
		return
	}

	// Lost-update protection: the only client is this repo's own schema.js,
	// which always sends back the revision it got from GET, so an
	// empty/absent revision here means either a pre-revision client or a
	// caller that skipped GET entirely - reject it rather than silently
	// saving over whatever is current. This check runs after validation
	// (above) so a payload with both an invalid field and a missing
	// revision reports the field error, not this one - validation errors
	// are about the payload itself and are worth surfacing regardless of
	// staleness.
	if strings.TrimSpace(body.Revision) == "" {
		writeConfigEditErrors(w, http.StatusBadRequest, []string{"revision is required: reload the config editor and try again"})
		return
	}

	// SaveConfigIfRevision checks body.Revision against the provider's
	// current revision and saves in one atomic, lock-held operation (see
	// SwKitConfigProvider.SaveConfigIfRevision) - that is what closes the
	// check-then-act race on the write side; handleConfigEditGet's read
	// order is what keeps the revision it hands out from ever being newer
	// than the snapshot it accompanies.
	if err := ws.opts.ConfigProvider.SaveConfigIfRevision(body.Config, body.Revision); err != nil {
		if errors.Is(err, app.ErrConfigRevisionMismatch) {
			writeConfigEditErrors(w, http.StatusConflict, []string{"config changed elsewhere since you loaded it (another edit session or an out-of-band reload) - reload the config editor to get the latest version, then re-apply your changes"})
			return
		}
		writeConfigEditErrors(w, http.StatusInternalServerError, []string{"save failed: " + err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(apiConfigEditSaveResponse{Ok: true, Reload: true})
}

// checkSameOriginPost applies a best-effort CSRF check to a state-changing
// POST: reject an explicit cross-site fetch (Sec-Fetch-Site, sent by modern
// browsers), and reject a present-but-mismatched Origin header. Requests with
// neither header (older browsers, curl, same-origin fetches that omit
// Origin) are allowed through - the Content-Type check above is the primary
// defense.
func checkSameOriginPost(r *http.Request) error {
	if site := r.Header.Get("Sec-Fetch-Site"); site == "cross-site" {
		return errors.New("cross-site request rejected (Sec-Fetch-Site: cross-site)")
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return nil
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" || u.Host != r.Host {
		return fmt.Errorf("cross-origin request rejected (Origin %q does not match request host %q; if behind a reverse proxy, preserve the Host header)", origin, r.Host)
	}
	return nil
}

// writeConfigEditErrors writes the standard {"ok":false,"errors":[...]} error
// envelope with the given status code.
func writeConfigEditErrors(w http.ResponseWriter, status int, errs []string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(apiConfigEditSaveResponse{Ok: false, Errors: errs})
}

// buildConfigEditMeta builds the "meta" portion of the GET response: the
// vocabularies the frontend needs (event types, action verbs), the current
// output device names (already includes color lights - see
// SwKitConfigProvider.GetEditableConfig), the configured driver names, and IO
// suggestions grouped by config IO type, built from state.IoDebug via the
// shared app.IoPointToIdWithType converter (same logic the TUI IO picker
// uses).
func buildConfigEditMeta(cfg app.EditableConfig, state app.AppState) apiConfigEditMeta {
	meta := apiConfigEditMeta{
		EventTypes:        app.AllEventTypes(),
		ActionVerbs:       app.AllActionVerbs(),
		OutputDeviceNames: append([]string{}, cfg.OutputDeviceNames...),
		Drivers:           make([]string, 0, len(state.Drivers)),
		IoSuggestions:     make(map[string][]apiIoSuggestion),
	}

	for _, d := range state.Drivers {
		meta.Drivers = append(meta.Drivers, d.Name)
	}

	for _, pt := range state.IoDebug {
		var typeStr string
		switch pt.Type {
		case "output":
			typeStr = "d_out"
		case "analog_output":
			typeStr = "a_out"
		case "input":
			typeStr = "push_event"
		default:
			continue
		}

		label := pt.Name
		if pt.CustomName != "" {
			label = pt.CustomName + " [" + pt.Name + "]"
		}

		meta.IoSuggestions[typeStr] = append(meta.IoSuggestions[typeStr], apiIoSuggestion{
			Id:           app.IoPointToIdWithType(pt, typeStr),
			Label:        label,
			ConfiguredAs: pt.ConfiguredAs,
		})
	}

	return meta
}

// ---- API request/response types ----

type apiConfigEditPostBody struct {
	Config app.EditableConfig `json:"config"`
	// Revision must be the value returned by the preceding GET's Revision
	// field. Required (empty/absent is rejected with 400) - see
	// handleConfigEditPost.
	Revision string `json:"revision"`
}

type apiConfigEditResponse struct {
	Config app.EditableConfig `json:"config"`
	Meta   apiConfigEditMeta  `json:"meta"`
	// Revision identifies the returned Config's content; pass it back
	// unchanged in the POST body's revision field to save. See
	// handleConfigEditPost for the conflict check this enables.
	Revision string `json:"revision"`
}

type apiConfigEditMeta struct {
	EventTypes        []string                     `json:"event_types"`
	ActionVerbs       []string                     `json:"action_verbs"`
	OutputDeviceNames []string                     `json:"output_device_names"`
	Drivers           []string                     `json:"drivers"`
	IoSuggestions     map[string][]apiIoSuggestion `json:"io_suggestions"`
	// SceneRun reports whether POST /api/scenes/run is available, so the
	// Scene Studio only offers "Run" when it can actually do it.
	SceneRun bool `json:"scene_run"`
}

type apiIoSuggestion struct {
	Id           string `json:"id"`
	Label        string `json:"label"`
	ConfiguredAs string `json:"configured_as,omitempty"`
}

type apiConfigEditSaveResponse struct {
	Ok     bool     `json:"ok"`
	Reload bool     `json:"reload,omitempty"`
	Errors []string `json:"errors,omitempty"`
}
