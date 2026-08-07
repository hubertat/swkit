package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/hubertat/swkit/app"
	"github.com/hubertat/swkit/drivers"
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
	cfg := ws.opts.ConfigProvider.GetEditableConfig()
	state := ws.provider.GetState()

	resp := apiConfigEditResponse{
		Config: cfg,
		Meta:   buildConfigEditMeta(cfg, state),
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
	// normalizeEditableConfig.
	normalizeEditableConfig(&body.Config)

	state := ws.provider.GetState()
	errs := validateEditableConfig(body.Config, state)
	if len(errs) > 0 {
		writeConfigEditErrors(w, http.StatusBadRequest, errs)
		return
	}

	if err := ws.opts.ConfigProvider.SaveConfig(body.Config); err != nil {
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
		return errors.New("cross-origin request rejected (Origin does not match request host)")
	}
	return nil
}

// normalizeEditableConfig trims whitespace from every user-facing string in
// cfg in place: device/scene/state names, io id strings, control-relation
// target names, and scene action strings. Mutating the config once, before
// both validateEditableConfig and SaveConfig see it, is what keeps validation
// (which rejects empty/duplicate/dangling names) and persistence (which
// writes whatever was validated) from disagreeing about a name that only
// differs by leading/trailing whitespace.
func normalizeEditableConfig(cfg *app.EditableConfig) {
	for i := range cfg.Lights {
		cfg.Lights[i].Name = strings.TrimSpace(cfg.Lights[i].Name)
		cfg.Lights[i].DigitalOutName = strings.TrimSpace(cfg.Lights[i].DigitalOutName)
	}
	for i := range cfg.Outlets {
		cfg.Outlets[i].Name = strings.TrimSpace(cfg.Outlets[i].Name)
		cfg.Outlets[i].DigitalOutName = strings.TrimSpace(cfg.Outlets[i].DigitalOutName)
	}
	for i := range cfg.DimmableLights {
		cfg.DimmableLights[i].Name = strings.TrimSpace(cfg.DimmableLights[i].Name)
		cfg.DimmableLights[i].DigitalOutName = strings.TrimSpace(cfg.DimmableLights[i].DigitalOutName)
		cfg.DimmableLights[i].AnalogOutName = strings.TrimSpace(cfg.DimmableLights[i].AnalogOutName)
	}
	for i := range cfg.Buttons {
		cfg.Buttons[i].Name = strings.TrimSpace(cfg.Buttons[i].Name)
		cfg.Buttons[i].EventInputName = strings.TrimSpace(cfg.Buttons[i].EventInputName)
		for j := range cfg.Buttons[i].ControlDevices {
			cfg.Buttons[i].ControlDevices[j].DeviceName = strings.TrimSpace(cfg.Buttons[i].ControlDevices[j].DeviceName)
		}
	}
	for i := range cfg.Scenes {
		cfg.Scenes[i].Name = strings.TrimSpace(cfg.Scenes[i].Name)
		for j := range cfg.Scenes[i].States {
			cfg.Scenes[i].States[j].Name = strings.TrimSpace(cfg.Scenes[i].States[j].Name)
			for k := range cfg.Scenes[i].States[j].Actions {
				cfg.Scenes[i].States[j].Actions[k] = strings.TrimSpace(cfg.Scenes[i].States[j].Actions[k])
			}
		}
	}
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

// ---- Validation ----

// canonicalEventInputId rewrites a d_in-typed io id to its push_event
// equivalent, mirroring SwKitConfigProvider.normalizeButtonEventInputIoType
// (config_provider.go) so duplicate-input detection treats
// "driver|d_in|x" and "driver|push_event|x" as the same input. Ids that don't
// parse, or aren't d_in, are returned unchanged.
func canonicalEventInputId(ioId string) string {
	driverName, ioType, ioName, err := drivers.ResolveIoIdString(ioId)
	if err != nil {
		return ioId
	}
	if ioType != drivers.IoTypeDigitalInput {
		return ioId
	}
	return drivers.GetIoIdString(driverName, drivers.IoTypePushEventEmitter, ioName)
}

// validateEditableConfig validates a full EditableConfig replacement against
// itself and the current runtime state (for configured driver names and color
// light target names, which live outside EditableConfig). It collects every
// violation rather than stopping at the first one, per the API contract.
func validateEditableConfig(cfg app.EditableConfig, state app.AppState) []string {
	var errs []string

	configuredDrivers := make(map[string]bool, len(state.Drivers))
	for _, d := range state.Drivers {
		configuredDrivers[d.Name] = true
	}

	// Valid control/scene-action targets: every device name in the posted
	// config's controllable lists (lights, dimmable lights, outlets, scenes),
	// plus color lights from current state (they're Controllable at runtime
	// but are not part of EditableConfig, so they'd otherwise be invisible
	// here - see controllableTargetTypes in schema.go for the same rule).
	// targetType additionally records each target's device type, used below
	// to reject brightness actions/relations aimed at a non-Dimmable device
	// (see dimmableTargetTypes) and to enforce scene-ordering (see sceneIndex).
	validTargets := make(map[string]bool)
	targetType := make(map[string]string)
	for _, l := range cfg.Lights {
		validTargets[l.Name] = true
		targetType[l.Name] = "light"
	}
	for _, dl := range cfg.DimmableLights {
		validTargets[dl.Name] = true
		targetType[dl.Name] = "dimmable_light"
	}
	for _, o := range cfg.Outlets {
		validTargets[o.Name] = true
		targetType[o.Name] = "outlet"
	}
	for _, s := range cfg.Scenes {
		validTargets[s.Name] = true
		targetType[s.Name] = "scene"
	}
	for _, d := range state.Devices {
		if d.Type == app.DeviceTypeColorLight {
			validTargets[d.Name] = true
			targetType[d.Name] = "color_light"
		}
	}

	// sceneIndex maps scene name -> its position in cfg.Scenes. SwKit.Setup
	// builds scenes sequentially and resolves each action's target via
	// resolveControllable, which only sees scenes already appended - so a
	// scene may target an earlier scene, never itself or a later one (see
	// swkit.go resolveControllable's doc comment). Checked below in the scene
	// action loop.
	sceneIndex := make(map[string]int, len(cfg.Scenes))
	for i, s := range cfg.Scenes {
		sceneIndex[s.Name] = i
	}

	// dimmableTargetTypes are the target types that implement app.Dimmable at
	// runtime (see swkit.go: DimmableLight is the only one - ColorLight and
	// Scene do not implement SetBrightness). Brightness-family actions
	// targeting anything else fail at Setup/apply time, so reject them here.
	dimmableTargetTypes := map[string]bool{"dimmable_light": true}

	// ---- Names: non-empty, unique within each list, unique globally ----
	nameCount := make(map[string]int)
	addName := func(list, name string) {
		trimmed := strings.TrimSpace(name)
		if trimmed == "" {
			errs = append(errs, fmt.Sprintf("%s: name must not be empty", list))
			return
		}
		// Control-device strings and scene action strings are colon-delimited
		// ("<event>:<verb>:<device>"); a colon in a device name is silently
		// accepted here but corrupts that grammar on the next reload (see
		// parseControlDeviceToEdit/app.ParseAction), so it must be rejected
		// up front instead.
		if strings.Contains(trimmed, ":") {
			errs = append(errs, fmt.Sprintf("%s: name %q must not contain ':' (colons delimit the control-relation/action grammar)", list, trimmed))
		}
		nameCount[trimmed]++
	}
	within := func(list string, names []string) {
		seen := make(map[string]bool, len(names))
		for _, n := range names {
			trimmed := strings.TrimSpace(n)
			if trimmed == "" {
				continue // already reported by addName
			}
			if seen[trimmed] {
				errs = append(errs, fmt.Sprintf("%s: duplicate name %q", list, trimmed))
			}
			seen[trimmed] = true
		}
	}

	lightNames := namesOf(cfg.Lights, func(l app.LightEditConfig) string { return l.Name })
	dimNames := namesOf(cfg.DimmableLights, func(d app.DimmableLightEditConfig) string { return d.Name })
	outletNames := namesOf(cfg.Outlets, func(o app.OutletEditConfig) string { return o.Name })
	buttonNames := namesOf(cfg.Buttons, func(b app.ButtonEditConfig) string { return b.Name })
	sceneNames := namesOf(cfg.Scenes, func(s app.SceneEditConfig) string { return s.Name })

	for _, n := range lightNames {
		addName("light", n)
	}
	for _, n := range dimNames {
		addName("dimmable light", n)
	}
	for _, n := range outletNames {
		addName("outlet", n)
	}
	for _, n := range buttonNames {
		addName("button", n)
	}
	for _, n := range sceneNames {
		addName("scene", n)
	}
	within("light", lightNames)
	within("dimmable light", dimNames)
	within("outlet", outletNames)
	within("button", buttonNames)
	within("scene", sceneNames)

	for name, count := range nameCount {
		if count > 1 {
			errs = append(errs, fmt.Sprintf("device name %q is used more than once (names must be unique across lights, dimmable lights, outlets, buttons and scenes)", name))
		}
	}

	// ---- IO ids: parse, type-check, driver membership, dedup ----
	type ioUse struct {
		field  string
		device string
		ioId   string
	}
	var digitalOutUses []ioUse
	var eventInputUses []ioUse

	checkIoField := func(device, field, ioId string, wantTypes []drivers.IoType, wantLabel string) {
		if strings.TrimSpace(ioId) == "" {
			errs = append(errs, fmt.Sprintf("%s: %s must not be empty", device, field))
			return
		}
		driverName, ioType, _, err := drivers.ResolveIoIdString(ioId)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %s %q is not a valid io id: %v", device, field, ioId, err))
			return
		}
		matches := false
		for _, want := range wantTypes {
			if ioType == want {
				matches = true
				break
			}
		}
		if !matches {
			errs = append(errs, fmt.Sprintf("%s: %s %q has io type %q, want %s", device, field, ioId, ioType.IdString(), wantLabel))
			return
		}
		if !configuredDrivers[driverName] {
			errs = append(errs, fmt.Sprintf("%s: %s %q references driver %q, which is not configured", device, field, ioId, driverName))
			return
		}
	}

	for _, l := range cfg.Lights {
		label := deviceLabel("light", l.Name)
		checkIoField(label, "DigitalOutName", l.DigitalOutName, []drivers.IoType{drivers.IoTypeDigitalOutput}, "d_out")
		if strings.TrimSpace(l.DigitalOutName) != "" {
			digitalOutUses = append(digitalOutUses, ioUse{"DigitalOutName", label, l.DigitalOutName})
		}
	}
	for _, o := range cfg.Outlets {
		label := deviceLabel("outlet", o.Name)
		checkIoField(label, "DigitalOutName", o.DigitalOutName, []drivers.IoType{drivers.IoTypeDigitalOutput}, "d_out")
		if strings.TrimSpace(o.DigitalOutName) != "" {
			digitalOutUses = append(digitalOutUses, ioUse{"DigitalOutName", label, o.DigitalOutName})
		}
	}
	for _, dl := range cfg.DimmableLights {
		label := deviceLabel("dimmable light", dl.Name)
		checkIoField(label, "DigitalOutName", dl.DigitalOutName, []drivers.IoType{drivers.IoTypeDigitalOutput}, "d_out")
		if strings.TrimSpace(dl.DigitalOutName) != "" {
			digitalOutUses = append(digitalOutUses, ioUse{"DigitalOutName", label, dl.DigitalOutName})
		}
		checkIoField(label, "AnalogOutName", dl.AnalogOutName, []drivers.IoType{drivers.IoTypeAnalogOutput}, "a_out")
		if strings.TrimSpace(dl.AnalogOutName) != "" {
			digitalOutUses = append(digitalOutUses, ioUse{"AnalogOutName", label, dl.AnalogOutName})
		}
		if dl.DefaultSetpoint < 0 || dl.DefaultSetpoint > 100 {
			errs = append(errs, fmt.Sprintf("%s: DefaultSetpoint %d out of range (0-100)", label, dl.DefaultSetpoint))
		}
	}
	for _, b := range cfg.Buttons {
		label := deviceLabel("button", b.Name)
		checkIoField(label, "EventInputName", b.EventInputName,
			[]drivers.IoType{drivers.IoTypePushEventEmitter, drivers.IoTypeDigitalInput}, "push_event or d_in")
		if strings.TrimSpace(b.EventInputName) != "" {
			eventInputUses = append(eventInputUses, ioUse{"EventInputName", label, canonicalEventInputId(b.EventInputName)})
		}
	}

	// Duplicate assignment: same io id used by more than one device/field.
	reportDuplicates := func(uses []ioUse) {
		byId := make(map[string][]string)
		for _, u := range uses {
			byId[u.ioId] = append(byId[u.ioId], u.device+"."+u.field)
		}
		for ioId, users := range byId {
			if len(users) > 1 {
				errs = append(errs, fmt.Sprintf("io id %q is assigned to more than one field: %s", ioId, strings.Join(users, ", ")))
			}
		}
	}
	reportDuplicates(digitalOutUses)
	reportDuplicates(eventInputUses)

	// ---- Button control relations ----
	eventTypeSet := setOf(app.AllEventTypes())
	verbSet := setOf(app.AllActionVerbs())
	for _, b := range cfg.Buttons {
		label := deviceLabel("button", b.Name)
		for i, cd := range b.ControlDevices {
			relLabel := fmt.Sprintf("%s control relation #%d", label, i+1)
			if !eventTypeSet[cd.EventType] {
				errs = append(errs, fmt.Sprintf("%s: unknown event type %q", relLabel, cd.EventType))
			}
			if !verbSet[cd.Action] {
				errs = append(errs, fmt.Sprintf("%s: unknown action verb %q", relLabel, cd.Action))
			}
			if app.IsBrightnessVerb(cd.Action) && (cd.Level < 0 || cd.Level > 100) {
				errs = append(errs, fmt.Sprintf("%s: level %d out of range (0-100) for brightness action", relLabel, cd.Level))
			}
			if strings.TrimSpace(cd.DeviceName) == "" {
				errs = append(errs, fmt.Sprintf("%s: target device name must not be empty", relLabel))
			} else if !validTargets[cd.DeviceName] {
				errs = append(errs, fmt.Sprintf("%s: target device %q does not exist", relLabel, cd.DeviceName))
			} else if app.IsBrightnessVerb(cd.Action) && !dimmableTargetTypes[targetType[cd.DeviceName]] {
				errs = append(errs, fmt.Sprintf("%s: brightness action target %q does not support brightness (target type: %s)", relLabel, cd.DeviceName, targetType[cd.DeviceName]))
			}
		}
	}

	// ---- Scene actions ----
	for i, s := range cfg.Scenes {
		sceneLabel := deviceLabel("scene", s.Name)

		seenStates := make(map[string]bool, len(s.States))
		for _, st := range s.States {
			stName := strings.TrimSpace(st.Name)
			if stName == "" {
				errs = append(errs, fmt.Sprintf("%s: state name must not be empty", sceneLabel))
			} else if seenStates[stName] {
				errs = append(errs, fmt.Sprintf("%s: duplicate state name %q", sceneLabel, stName))
			} else {
				seenStates[stName] = true
			}

			for j, actionStr := range st.Actions {
				actLabel := fmt.Sprintf("%s state %q action #%d (%q)", sceneLabel, st.Name, j+1, actionStr)
				act, err := app.ParseAction(actionStr)
				if err != nil {
					errs = append(errs, fmt.Sprintf("%s: %v", actLabel, err))
					continue
				}
				if app.IsBrightnessVerb(act.Verb) && (act.Level < 0 || act.Level > 100) {
					errs = append(errs, fmt.Sprintf("%s: level %d out of range (0-100) for brightness action", actLabel, act.Level))
				}
				if !validTargets[act.Device] {
					errs = append(errs, fmt.Sprintf("%s: target device %q does not exist", actLabel, act.Device))
					continue
				}
				// SwKit.Setup builds scenes sequentially: a scene action may
				// only target a scene defined earlier in the list (see
				// sceneIndex above and swkit.go resolveControllable). A
				// forward reference or self-reference builds fine here but
				// fails at reload.
				if targetIdx, isScene := sceneIndex[act.Device]; isScene && targetIdx >= i {
					if targetIdx == i {
						errs = append(errs, fmt.Sprintf("%s: scene %q cannot target itself", actLabel, act.Device))
					} else {
						errs = append(errs, fmt.Sprintf("%s: target scene %q is defined later than this scene; a scene may only target earlier scenes", actLabel, act.Device))
					}
				}
				if app.IsBrightnessVerb(act.Verb) && !dimmableTargetTypes[targetType[act.Device]] {
					errs = append(errs, fmt.Sprintf("%s: brightness action target %q does not support brightness (target type: %s)", actLabel, act.Device, targetType[act.Device]))
				}
			}
		}
	}

	return errs
}

// deviceLabel formats a human-readable identifier for error messages: the
// device type and name, falling back to "(unnamed)" for an empty name so
// error messages stay readable even when the name-non-empty check also fired.
func deviceLabel(kind, name string) string {
	if strings.TrimSpace(name) == "" {
		return kind + " (unnamed)"
	}
	return kind + " " + strconv.Quote(name)
}

// namesOf extracts names from a slice via an accessor, used to keep the
// per-list uniqueness checks above short.
func namesOf[T any](items []T, get func(T) string) []string {
	names := make([]string, 0, len(items))
	for _, it := range items {
		names = append(names, get(it))
	}
	return names
}

// setOf builds a lookup set from a slice of strings.
func setOf(items []string) map[string]bool {
	set := make(map[string]bool, len(items))
	for _, it := range items {
		set[it] = true
	}
	return set
}

// ---- API request/response types ----

type apiConfigEditPostBody struct {
	Config app.EditableConfig `json:"config"`
}

type apiConfigEditResponse struct {
	Config app.EditableConfig `json:"config"`
	Meta   apiConfigEditMeta  `json:"meta"`
}

type apiConfigEditMeta struct {
	EventTypes        []string                     `json:"event_types"`
	ActionVerbs       []string                     `json:"action_verbs"`
	OutputDeviceNames []string                     `json:"output_device_names"`
	Drivers           []string                     `json:"drivers"`
	IoSuggestions     map[string][]apiIoSuggestion `json:"io_suggestions"`
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
