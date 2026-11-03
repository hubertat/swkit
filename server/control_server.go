package server

import (
	"context"
	"encoding/json"
	"errors"
	"html/template"
	"io/fs"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/log"
	"github.com/hubertat/swkit/app"

	goEmbed "embed"
)

//go:embed control_static/*
var controlStaticFiles goEmbed.FS

//go:embed control_templates/*
var controlTemplateFiles goEmbed.FS

const defaultControlEndpoint = "/control"

// controlVariants are the display styles of the control page, each served
// under its own path (<endpoint>/<variant>). The bare endpoint serves the
// page with no forced variant, and the client picks the viewer's last used
// one (falling back to the first entry).
var controlVariants = []string{"tiles", "list", "compact"}

// ControlServer serves the device control web UI
type ControlServer struct {
	provider      app.DeviceController
	logger        *log.Logger
	tmpl          *template.Template
	name          string
	endpoint      string
	staticHandler http.Handler
}

// NewControlServer creates a new control server
func NewControlServer(provider app.DeviceController, endpoint, name string, logger *log.Logger) (*ControlServer, error) {
	if endpoint == "" {
		endpoint = defaultControlEndpoint
	}
	// Ensure endpoint starts with / and has no trailing slash
	if !strings.HasPrefix(endpoint, "/") {
		endpoint = "/" + endpoint
	}
	endpoint = strings.TrimRight(endpoint, "/")

	tmpl, err := template.ParseFS(controlTemplateFiles, "control_templates/*.html")
	if err != nil {
		return nil, errors.Join(err, errors.New("failed to parse control templates"))
	}

	subFS, err := fs.Sub(controlStaticFiles, "control_static")
	if err != nil {
		return nil, errors.Join(err, errors.New("failed to sub control_static fs"))
	}

	staticPrefix := endpoint + "/static/"
	staticHandler := http.StripPrefix(staticPrefix, http.FileServer(http.FS(subFS)))

	return &ControlServer{
		provider:      provider,
		logger:        logger,
		tmpl:          tmpl,
		name:          name,
		endpoint:      endpoint,
		staticHandler: staticHandler,
	}, nil
}

// Endpoint returns the normalized URL path prefix the server is mounted under
func (cs *ControlServer) Endpoint() string {
	return cs.endpoint
}

// RegisterOn mounts the control server onto an existing mux (port-sharing mode)
func (cs *ControlServer) RegisterOn(mux *http.ServeMux) {
	cs.registerRoutes(mux)
}

// StartOnPort runs the control server on its own port
func (cs *ControlServer) StartOnPort(ctx context.Context, port int) error {
	mux := http.NewServeMux()
	cs.registerRoutes(mux)

	srv := &http.Server{
		Addr:         ":" + strconv.Itoa(port),
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	cs.logger.Info("starting control UI server", "address", srv.Addr, "endpoint", cs.endpoint)

	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()

	select {
	case <-ctx.Done():
		cs.logger.Info("shutting down control UI server")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return errors.Join(err, errors.New("failed to shutdown control server"))
		}
		return nil
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return errors.Join(err, errors.New("control server error"))
		}
		return nil
	}
}

// registerRoutes mounts exactly two patterns and dispatches internally.
// This avoids conflicts with existing patterns on a shared mux (e.g. the
// web server's "/" catch-all swallowing more-specific sub-patterns).
func (cs *ControlServer) registerRoutes(mux *http.ServeMux) {
	mux.HandleFunc(cs.endpoint, cs.dispatch)
	mux.HandleFunc(cs.endpoint+"/", cs.dispatch)
}

// dispatch routes all requests under the endpoint to the correct handler.
func (cs *ControlServer) dispatch(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	staticPrefix := cs.endpoint + "/static/"
	apiDevices := cs.endpoint + "/api/devices"

	switch {
	case strings.HasPrefix(path, staticPrefix):
		cs.staticHandler.ServeHTTP(w, r)
	case path == cs.endpoint+"/api/info":
		cs.handleInfo(w, r)
	case path == apiDevices:
		cs.handleDeviceList(w, r)
	case strings.HasPrefix(path, apiDevices+"/"):
		cs.handleDeviceAction(w, r)
	case path == cs.endpoint || path == cs.endpoint+"/":
		cs.handlePage(w, r, "")
	default:
		variant := strings.TrimPrefix(path, cs.endpoint+"/")
		if !slices.Contains(controlVariants, variant) {
			http.NotFound(w, r)
			return
		}
		cs.handlePage(w, r, variant)
	}
}

// handlePage renders the HTML control page. An empty variant lets the client
// choose (last used, else the default).
func (cs *ControlServer) handlePage(w http.ResponseWriter, r *http.Request, variant string) {
	data := struct {
		Name     string
		Endpoint string
		Variant  string
		Variants []string
	}{
		Name:     cs.name,
		Endpoint: cs.endpoint,
		Variant:  variant,
		Variants: controlVariants,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	if err := cs.tmpl.ExecuteTemplate(w, "control.html", data); err != nil {
		cs.logger.Error("control template render error", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

// handleInfo identifies the controller to API clients (e.g. the Presto
// panel), which otherwise only see an anonymous device list.
func (cs *ControlServer) handleInfo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache")
	// Features lists additive API capabilities (the API version only
	// changes on incompatible changes): "window_shutter" means the
	// open/close/stop/set_position actions and window_shutter payloads are
	// available.
	features := []string{}
	if _, ok := cs.provider.(app.WindowShutterController); ok {
		features = append(features, controlFeatureWindowShutter)
	}
	json.NewEncoder(w).Encode(struct {
		Name     string   `json:"name"`
		API      int      `json:"api"`
		Features []string `json:"features"`
	}{Name: cs.name, API: controlAPIVersion, Features: features})
}

const controlFeatureWindowShutter = "window_shutter"

// controlShutterState is the window shutter payload, in device lists and in
// action responses. Positions are percent open (0 closed, 100 open) and are
// estimated from travel time, never measured; current_position is
// meaningful only when position_known. target_position is -1 when there is
// no target (idle with unknown position). remaining_travel_ms is the
// server-calculated time until the outputs stop (reversal wait and endpoint
// overrun included): clients derive completion deadlines from it rather
// than from position reaching 0/100.
type controlShutterState struct {
	Motion            string `json:"motion"`
	TargetPosition    int    `json:"target_position"`
	CurrentPosition   int    `json:"current_position"`
	PositionKnown     bool   `json:"position_known"`
	PendingStart      bool   `json:"pending_start"`
	OpenIoId          string `json:"open_io_id"`
	CloseIoId         string `json:"close_io_id"`
	OpenDurationSecs  int    `json:"open_duration_seconds"`
	CloseDurationSecs int    `json:"close_duration_seconds"`
	RemainingTravelMs int64  `json:"remaining_travel_ms"`
}

func controlShutterFrom(s *app.WindowShutterState) *controlShutterState {
	if s == nil {
		return nil
	}
	return &controlShutterState{
		Motion:            s.Motion,
		TargetPosition:    s.TargetPosition,
		CurrentPosition:   s.CurrentPosition,
		PositionKnown:     s.PositionKnown,
		PendingStart:      s.PendingStart,
		OpenIoId:          s.OpenIoId,
		CloseIoId:         s.CloseIoId,
		OpenDurationSecs:  s.OpenDurationSeconds,
		CloseDurationSecs: s.CloseDurationSeconds,
		RemainingTravelMs: s.RemainingTravelMilliseconds,
	}
}

// controlDeviceResponse is the response for device list endpoint
type controlDeviceResponse struct {
	Index         int    `json:"index"`
	Name          string `json:"name"`
	Type          string `json:"type"`
	IsOn          bool   `json:"is_on"`
	IsHealthy     bool   `json:"is_healthy"`
	IsFaulty      bool   `json:"is_faulty"`
	Controllable  bool   `json:"controllable"`
	HasBrightness bool   `json:"has_brightness"`
	Brightness    int    `json:"brightness"`
	LastEventType string `json:"last_event_type,omitempty"`
	// StateError is non-empty when the server could not read the device's
	// state; is_on is then meaningless and the UI shows the state as unknown.
	StateError string `json:"state_error,omitempty"`
	SceneState string `json:"scene_state,omitempty"`
	SceneIndex int    `json:"scene_index,omitempty"`
	SceneCount int    `json:"scene_count,omitempty"`
	// SceneNext is the state a toggle would select now (scenes only; a
	// pointer so a real 0, e.g. after a toggle timeout, is still sent). A
	// prediction: the toggle response's scene_index is authoritative.
	SceneNext *int `json:"scene_next,omitempty"`
	// LastEventAgeMs is how long ago the button's last event happened, so the
	// client can show a relative time without trusting its own clock.
	LastEventAgeMs int64 `json:"last_event_age_ms,omitempty"`
	// WindowShutter is present for window shutters only. is_on is then a
	// legacy compatibility value (known position > 0); shutter clients use
	// this payload.
	WindowShutter *controlShutterState `json:"window_shutter,omitempty"`
}

// handleDeviceList returns the list of devices with their current state
func (cs *ControlServer) handleDeviceList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	state := cs.provider.GetState()
	result := make([]controlDeviceResponse, 0, len(state.Devices))

	for i, d := range state.Devices {
		controllable := d.Type == app.DeviceTypeLight ||
			d.Type == app.DeviceTypeColorLight ||
			d.Type == app.DeviceTypeDimmableLight ||
			d.Type == app.DeviceTypeOutlet ||
			d.Type == app.DeviceTypeScene ||
			d.Type == app.DeviceTypeWindowShutter
		hasBrightness := d.Type == app.DeviceTypeDimmableLight

		resp := controlDeviceResponse{
			Index:         i,
			Name:          d.Name,
			Type:          string(d.Type),
			IsOn:          d.IsOn,
			IsHealthy:     d.IsHealthy,
			IsFaulty:      d.IsFaulty,
			Controllable:  controllable,
			HasBrightness: hasBrightness,
			Brightness:    d.Brightness,
			LastEventType: d.LastEventType,
			StateError:    d.StateError,
			WindowShutter: controlShutterFrom(d.WindowShutter),
		}
		if !d.LastEventTime.IsZero() {
			resp.LastEventAgeMs = max(time.Since(d.LastEventTime).Milliseconds(), 1)
		}
		if d.Type == app.DeviceTypeScene {
			resp.SceneIndex = d.SceneStateIndex
			resp.SceneCount = len(d.SceneStateNames)
			if len(d.SceneStateNames) > 0 { // a scene with no states has no next state
				next := d.SceneNextIndex
				resp.SceneNext = &next
			}
			if d.SceneStateIndex >= 0 && d.SceneStateIndex < len(d.SceneStateNames) {
				resp.SceneState = d.SceneStateNames[d.SceneStateIndex]
			}
		}
		result = append(result, resp)
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache")
	json.NewEncoder(w).Encode(result)
}

// controlActionResponse is the response for toggle/set actions
type controlActionResponse struct {
	DeviceName string `json:"device_name"`
	Action     string `json:"action"`
	IsOn       bool   `json:"is_on"`
	// SceneIndex is present only for a scene toggle: the state the server
	// actually selected (authoritative, unlike the list's scene_next).
	SceneIndex *int `json:"scene_index,omitempty"`
	// WindowShutter is present for commands to a window shutter (generic
	// set/toggle included): the state right after the accepted command.
	// Reconcile shutter intent from it, never from is_on.
	WindowShutter *controlShutterState `json:"window_shutter,omitempty"`
}

// handleDeviceAction routes POST requests to toggle or set
func (cs *ControlServer) handleDeviceAction(w http.ResponseWriter, r *http.Request) {
	// Path: /control/api/devices/{index}/{action}
	suffix := strings.TrimPrefix(r.URL.Path, cs.endpoint+"/api/devices/")
	parts := strings.SplitN(suffix, "/", 2)
	if len(parts) < 2 {
		http.NotFound(w, r)
		return
	}

	index, err := strconv.Atoi(parts[0])
	if err != nil {
		http.Error(w, "invalid device index", http.StatusBadRequest)
		return
	}
	action := parts[1]

	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var result app.ControlResult

	switch action {
	case "toggle":
		result = cs.provider.ToggleDevice(index)
	case "set":
		var body struct {
			Value bool `json:"value"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		result = cs.provider.SetDevice(index, body.Value)
	case "set_brightness":
		var body struct {
			Value int `json:"value"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		result = cs.provider.SetDeviceBrightness(index, body.Value)
	case "adjust_brightness":
		var body struct {
			Value int `json:"value"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		result = cs.provider.AdjustDeviceBrightness(index, body.Value)
	case "set_for":
		var body struct {
			Value   bool `json:"value"`
			Seconds int  `json:"seconds"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		result = cs.provider.SetDeviceValueFor(index, body.Value, body.Seconds)
	case "open", "close", "stop", "set_position":
		sc, ok := cs.provider.(app.WindowShutterController)
		if !ok {
			http.Error(w, "window shutters are not supported by this controller", http.StatusNotImplemented)
			return
		}
		switch action {
		case "open":
			result = sc.OpenDevice(index)
		case "close":
			result = sc.CloseDevice(index)
		case "stop":
			result = sc.StopDevice(index)
		case "set_position":
			var body struct {
				Value *int `json:"value"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Value == nil {
				http.Error(w, "invalid request body: expected {\"value\": <0-100>}", http.StatusBadRequest)
				return
			}
			if *body.Value < 0 || *body.Value > 100 {
				http.Error(w, "position must be between 0 and 100", http.StatusBadRequest)
				return
			}
			result = sc.SetDevicePosition(index, *body.Value)
		}
	default:
		http.NotFound(w, r)
		return
	}

	if result.Error != nil {
		cs.logger.Error("device control error", "index", index, "action", action, "err", result.Error)
		status := http.StatusInternalServerError
		if result.WindowShutter != nil {
			// The shutter rejected the command in its current state (e.g.
			// an intermediate position while the position is unknown).
			status = http.StatusConflict
		}
		http.Error(w, result.Error.Error(), status)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(controlActionResponse{
		DeviceName:    result.DeviceName,
		Action:        result.Action,
		IsOn:          result.NewState,
		SceneIndex:    result.SceneIndex,
		WindowShutter: controlShutterFrom(result.WindowShutter),
	})
}
