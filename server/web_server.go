package server

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/log"
	"github.com/hubertat/swkit/app"
	"github.com/hubertat/swkit/logging"
)

//go:embed web_static/*
var staticFiles embed.FS

//go:embed web_templates/*
var templateFiles embed.FS

const defaultWebPort = 8080

// ServicesConfig holds configuration info about running services for the dashboard.
type ServicesConfig struct {
	WebPort      int
	SSHEnabled   bool
	SSHPort      int
	AgentEnabled bool
	AgentModel   string
}

// WebServerOptions holds optional configuration for the web server.
type WebServerOptions struct {
	GetRawConfig func() json.RawMessage
	Version      string
	Services     ServicesConfig
	Broadcaster  *logging.Broadcaster
}

// WebServer serves the diagnostic web UI
type WebServer struct {
	provider    app.StateProvider
	broadcaster *logging.Broadcaster
	server      *http.Server
	mux         *http.ServeMux
	logger      *log.Logger
	tmpl        *template.Template
	opts        WebServerOptions
}

// NewWebServer creates a new web server for the diagnostic UI
func NewWebServer(provider app.StateProvider, port int, logger *log.Logger) (*WebServer, error) {
	return NewWebServerWithConfig(provider, port, logger, WebServerOptions{})
}

// NewWebServerWithConfig creates a new web server with options
func NewWebServerWithConfig(provider app.StateProvider, port int, logger *log.Logger, opts WebServerOptions) (*WebServer, error) {
	if port == 0 {
		port = defaultWebPort
	}

	tmpl, err := template.ParseFS(templateFiles, "web_templates/*.html")
	if err != nil {
		return nil, errors.Join(err, errors.New("failed to parse web templates"))
	}

	ws := &WebServer{
		provider:    provider,
		broadcaster: opts.Broadcaster,
		logger:      logger,
		tmpl:        tmpl,
		opts:        opts,
	}

	mux := http.NewServeMux()

	// Static files
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticFiles))))

	// Pages
	mux.HandleFunc("/", ws.handlePage)
	mux.HandleFunc("/drivers", ws.handlePage)
	mux.HandleFunc("/devices", ws.handlePage)
	mux.HandleFunc("/io-debug", ws.handlePage)
	mux.HandleFunc("/config", ws.handlePage)
	mux.HandleFunc("/logs", ws.handlePage)

	// API
	mux.HandleFunc("/api/state", ws.handleApiState)
	mux.HandleFunc("/api/logs/stream", ws.handleLogsStream)

	ws.mux = mux
	ws.server = &http.Server{
		Addr:         ":" + strconv.Itoa(port),
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	return ws, nil
}

// Start starts the web server and blocks until context is cancelled
func (ws *WebServer) Start(ctx context.Context) error {
	ws.logger.Info("starting web UI server", "address", ws.server.Addr)

	errCh := make(chan error, 1)
	go func() {
		errCh <- ws.server.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		ws.logger.Info("shutting down web UI server")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := ws.server.Shutdown(shutdownCtx); err != nil {
			return errors.Join(err, errors.New("failed to shutdown web server"))
		}
		return nil
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return errors.Join(err, errors.New("web server error"))
		}
		return nil
	}
}

// Addr returns the server address
func (ws *WebServer) Addr() string {
	return ws.server.Addr
}

// Mux returns the underlying ServeMux for mounting additional handlers
func (ws *WebServer) Mux() *http.ServeMux {
	return ws.mux
}

// handlePage renders the HTML shell for any page route
func (ws *WebServer) handlePage(w http.ResponseWriter, r *http.Request) {
	data := struct {
		Tab     string
		Version string
	}{
		Tab:     pageTab(r.URL.Path),
		Version: ws.opts.Version,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := ws.tmpl.ExecuteTemplate(w, "layout.html", data); err != nil {
		ws.logger.Error("template render error", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

// handleApiState returns the current state as JSON
func (ws *WebServer) handleApiState(w http.ResponseWriter, r *http.Request) {
	state := ws.provider.GetState()
	summary := state.Summary()

	resp := apiStateResponse{
		Name:      state.Name,
		Timestamp: state.Timestamp,
		Summary: apiSummary{
			DriversTotal:     summary.DriversTotal,
			DriversReady:     summary.DriversReady,
			LightsCount:      summary.LightsCount,
			ColorLightsCount: summary.ColorLightsCount,
			OutletsCount:     summary.OutletsCount,
			ButtonsCount:     summary.ButtonsCount,
			HomeKitEnabled:   summary.HomeKitEnabled,
			HomeKitDevices:   summary.HomeKitDevices,
		},
		Drivers: make([]apiDriver, 0, len(state.Drivers)),
		Devices: make([]apiDevice, 0, len(state.Devices)),
		IoDebug: make([]apiIoPoint, 0, len(state.IoDebug)),
		HomeKit: apiHomeKit{
			Enabled:     state.HomeKit.Enabled,
			Pin:         state.HomeKit.Pin,
			Address:     state.HomeKit.Address,
			DeviceCount: state.HomeKit.DeviceCount,
		},
	}

	for _, d := range state.Drivers {
		resp.Drivers = append(resp.Drivers, apiDriver{
			Name:       d.Name,
			Ready:      d.Ready,
			StatusInfo: d.StatusInfo,
			Details:    d.Details,
		})
	}

	for _, d := range state.Devices {
		dev := apiDevice{
			Name:           d.Name,
			Type:           string(d.Type),
			IsOn:           d.IsOn,
			IsHealthy:      d.IsHealthy,
			IsFaulty:       d.IsFaulty,
			HomeKitEnabled: d.HomeKitEnabled,
			OutputIoId:     d.OutputIoId,
			RgbwIoId:       d.RgbwIoId,
			EventInputId:   d.EventInputId,
			LastEventType:  d.LastEventType,
		}
		if !d.LastEventTime.IsZero() {
			dev.LastEventTime = &d.LastEventTime
		}
		for _, rel := range d.ControlRelations {
			dev.ControlRelations = append(dev.ControlRelations, apiControlRelation{
				EventType:  rel.EventType,
				Action:     rel.Action,
				DeviceName: rel.DeviceName,
			})
		}
		resp.Devices = append(resp.Devices, dev)
	}

	for _, pt := range state.IoDebug {
		ioPt := apiIoPoint{
			DriverName:   pt.DriverName,
			Index:        pt.Index,
			Name:         pt.Name,
			Type:         pt.Type,
			State:        pt.State,
			Healthy:      pt.Healthy,
			ConfiguredAs: pt.ConfiguredAs,
			CustomName:   pt.CustomName,
		}
		if !pt.LastChanged.IsZero() {
			ioPt.LastChanged = &pt.LastChanged
		}
		if !pt.LastEvent.IsZero() {
			ioPt.LastEvent = &pt.LastEvent
		}
		resp.IoDebug = append(resp.IoDebug, ioPt)
	}

	// Services
	resp.Services = apiServices{
		WebPort:      ws.opts.Services.WebPort,
		SSHEnabled:   ws.opts.Services.SSHEnabled,
		SSHPort:      ws.opts.Services.SSHPort,
		AgentEnabled: ws.opts.Services.AgentEnabled,
		AgentModel:   ws.opts.Services.AgentModel,
	}

	// Config JSON
	if ws.opts.GetRawConfig != nil {
		resp.ConfigJSON = ws.opts.GetRawConfig()
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache")
	json.NewEncoder(w).Encode(resp)
}

// handleLogsStream serves log lines as Server-Sent Events.
func (ws *WebServer) handleLogsStream(w http.ResponseWriter, r *http.Request) {
	if ws.broadcaster == nil {
		http.Error(w, "log broadcasting not enabled", http.StatusServiceUnavailable)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	// Disable write timeout for this long-lived connection.
	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Time{})

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	ch, unsub := ws.broadcaster.Subscribe(logging.FormatPlain)
	defer unsub()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case line, ok := <-ch:
			if !ok {
				return
			}
			// Trim trailing newline to avoid double blank lines in SSE.
			data := strings.TrimRight(string(line), "\n")
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
	}
}

func pageTab(path string) string {
	switch path {
	case "/drivers":
		return "drivers"
	case "/devices":
		return "devices"
	case "/io-debug":
		return "io-debug"
	case "/config":
		return "config"
	case "/logs":
		return "logs"
	default:
		return "dashboard"
	}
}

// API response types

type apiStateResponse struct {
	Name       string          `json:"name"`
	Timestamp  time.Time       `json:"timestamp"`
	Summary    apiSummary      `json:"summary"`
	Drivers    []apiDriver     `json:"drivers"`
	Devices    []apiDevice     `json:"devices"`
	IoDebug    []apiIoPoint    `json:"io_debug"`
	HomeKit    apiHomeKit      `json:"homekit"`
	Services   apiServices     `json:"services"`
	ConfigJSON json.RawMessage `json:"config_json,omitempty"`
}

type apiServices struct {
	WebPort      int    `json:"web_port"`
	SSHEnabled   bool   `json:"ssh_enabled"`
	SSHPort      int    `json:"ssh_port,omitempty"`
	AgentEnabled bool   `json:"agent_enabled"`
	AgentModel   string `json:"agent_model,omitempty"`
}

type apiSummary struct {
	DriversTotal     int  `json:"drivers_total"`
	DriversReady     int  `json:"drivers_ready"`
	LightsCount      int  `json:"lights_count"`
	ColorLightsCount int  `json:"color_lights_count"`
	OutletsCount     int  `json:"outlets_count"`
	ButtonsCount     int  `json:"buttons_count"`
	HomeKitEnabled   bool `json:"homekit_enabled"`
	HomeKitDevices   int  `json:"homekit_devices"`
}

type apiDriver struct {
	Name       string          `json:"name"`
	Ready      bool            `json:"ready"`
	StatusInfo string          `json:"status_info,omitempty"`
	Details    json.RawMessage `json:"details,omitempty"`
}

type apiDevice struct {
	Name             string               `json:"name"`
	Type             string               `json:"type"`
	IsOn             bool                 `json:"is_on"`
	IsHealthy        bool                 `json:"is_healthy"`
	IsFaulty         bool                 `json:"is_faulty"`
	HomeKitEnabled   bool                 `json:"homekit_enabled"`
	OutputIoId       string               `json:"output_io_id,omitempty"`
	RgbwIoId         string               `json:"rgbw_io_id,omitempty"`
	EventInputId     string               `json:"event_input_id,omitempty"`
	ControlRelations []apiControlRelation `json:"control_relations,omitempty"`
	LastEventType    string               `json:"last_event_type,omitempty"`
	LastEventTime    *time.Time           `json:"last_event_time,omitempty"`
}

type apiControlRelation struct {
	EventType  string `json:"event_type"`
	Action     string `json:"action"`
	DeviceName string `json:"device_name"`
}

type apiIoPoint struct {
	DriverName   string     `json:"driver_name"`
	Index        int        `json:"index"`
	Name         string     `json:"name"`
	Type         string     `json:"type"`
	State        bool       `json:"state"`
	Healthy      bool       `json:"healthy"`
	LastChanged  *time.Time `json:"last_changed,omitempty"`
	LastEvent    *time.Time `json:"last_event,omitempty"`
	ConfiguredAs string     `json:"configured_as,omitempty"`
	CustomName   string     `json:"custom_name,omitempty"`
}

type apiHomeKit struct {
	Enabled     bool   `json:"enabled"`
	Pin         string `json:"pin,omitempty"`
	Address     string `json:"address,omitempty"`
	DeviceCount int    `json:"device_count"`
}
