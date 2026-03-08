package server

import (
	"context"
	"encoding/json"
	"errors"
	"html/template"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/log"
	"github.com/hubertat/swkit/app"

	_ "embed"
	goEmbed "embed"
)

//go:embed control_static/*
var controlStaticFiles goEmbed.FS

//go:embed control_templates/*
var controlTemplateFiles goEmbed.FS

const defaultControlEndpoint = "/control"

// ControlServer serves the device control web UI
type ControlServer struct {
	provider app.DeviceController
	logger   *log.Logger
	tmpl     *template.Template
	name     string
	endpoint string
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

	return &ControlServer{
		provider: provider,
		logger:   logger,
		tmpl:     tmpl,
		name:     name,
		endpoint: endpoint,
	}, nil
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

func (cs *ControlServer) registerRoutes(mux *http.ServeMux) {
	subFS, err := fs.Sub(controlStaticFiles, "control_static")
	if err != nil {
		cs.logger.Error("failed to sub control_static fs", "err", err)
		return
	}

	staticPrefix := cs.endpoint + "/static/"
	mux.Handle(staticPrefix, http.StripPrefix(staticPrefix, http.FileServer(http.FS(subFS))))
	mux.HandleFunc(cs.endpoint, cs.handlePage)
	mux.HandleFunc(cs.endpoint+"/", cs.handlePage)
	mux.HandleFunc(cs.endpoint+"/api/devices", cs.handleDeviceList)
	mux.HandleFunc(cs.endpoint+"/api/devices/", cs.handleDeviceAction)
}

// handlePage renders the HTML control page
func (cs *ControlServer) handlePage(w http.ResponseWriter, r *http.Request) {
	// Only serve the page for exact path matches (not API paths)
	if strings.HasPrefix(r.URL.Path, cs.endpoint+"/api/") {
		http.NotFound(w, r)
		return
	}

	data := struct {
		Name     string
		Endpoint string
	}{
		Name:     cs.name,
		Endpoint: cs.endpoint,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := cs.tmpl.ExecuteTemplate(w, "control.html", data); err != nil {
		cs.logger.Error("control template render error", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
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
	LastEventType string `json:"last_event_type,omitempty"`
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
			d.Type == app.DeviceTypeOutlet

		result = append(result, controlDeviceResponse{
			Index:         i,
			Name:          d.Name,
			Type:          string(d.Type),
			IsOn:          d.IsOn,
			IsHealthy:     d.IsHealthy,
			IsFaulty:      d.IsFaulty,
			Controllable:  controllable,
			LastEventType: d.LastEventType,
		})
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
	default:
		http.NotFound(w, r)
		return
	}

	if result.Error != nil {
		cs.logger.Error("device control error", "index", index, "action", action, "err", result.Error)
		http.Error(w, result.Error.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(controlActionResponse{
		DeviceName: result.DeviceName,
		Action:     result.Action,
		IsOn:       result.NewState,
	})
}
