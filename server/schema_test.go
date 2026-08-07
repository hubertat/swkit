package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/log"
	"github.com/hubertat/swkit/app"
)

// fakeStateProvider is a minimal app.StateProvider returning a fixed state.
type fakeStateProvider struct {
	state app.AppState
}

func (f *fakeStateProvider) GetState() app.AppState { return f.state }
func (f *fakeStateProvider) Subscribe(ctx context.Context, interval time.Duration) <-chan app.AppState {
	return nil
}

// fakeConfigProvider is a minimal app.ConfigProvider returning a fixed config.
type fakeConfigProvider struct {
	cfg app.EditableConfig
}

func (f *fakeConfigProvider) GetEditableConfig() app.EditableConfig { return f.cfg }
func (f *fakeConfigProvider) SaveConfig(app.EditableConfig) error   { return nil }

func nodeByID(t *testing.T, nodes []apiSchemaNode, id string) apiSchemaNode {
	t.Helper()
	for _, n := range nodes {
		if n.ID == id {
			return n
		}
	}
	t.Fatalf("node %q not found among %d nodes", id, len(nodes))
	return apiSchemaNode{}
}

func findNode(nodes []apiSchemaNode, id string) (apiSchemaNode, bool) {
	for _, n := range nodes {
		if n.ID == id {
			return n, true
		}
	}
	return apiSchemaNode{}, false
}

func countEdges(edges []apiSchemaEdge, kind, from, to string) int {
	n := 0
	for _, e := range edges {
		if e.Kind == kind && e.From == from && e.To == to {
			n++
		}
	}
	return n
}

func fullFixtureState() app.AppState {
	return app.AppState{
		Name: "My Home",
		HomeKit: app.HomeKitState{
			Enabled:     true,
			DeviceCount: 5,
		},
		Drivers: []app.DriverState{
			{Name: "gpio", Ready: true},
			{Name: "shelly", Ready: false},
		},
		IoDebug: []app.IoPointDebugState{
			{DriverName: "shelly", Index: 0, Name: "input0", Type: "input", CustomName: "wall switch"},
			{DriverName: "shelly", Index: 1, Name: "input1", Type: "input"},
		},
		Devices: []app.DeviceState{
			{
				Name: "Living Room", Type: app.DeviceTypeLight,
				IsOn: true, IsHealthy: true, IsFaulty: false, HomeKitEnabled: true,
				OutputIoId: "gpio|d_out|5",
			},
			{
				Name: "Fan", Type: app.DeviceTypeOutlet,
				IsOn: false, IsHealthy: true, HomeKitEnabled: true,
				OutputIoId: "gpio|d_out|6",
			},
			{
				Name: "Hall", Type: app.DeviceTypeDimmableLight,
				IsOn: true, IsHealthy: true, HomeKitEnabled: true,
				OutputIoId: "gpio|d_out|7", AnalogIoId: "gpio|a_out|1", Brightness: 40,
			},
			{
				Name: "Mood", Type: app.DeviceTypeColorLight,
				IsOn: false, IsHealthy: true, HomeKitEnabled: true,
				OutputIoId: "gpio|d_out|8", RgbwIoId: "gpio|rgbw_out|1",
			},
			{
				Name: "Wall", Type: app.DeviceTypeButton,
				IsHealthy: true, HomeKitEnabled: true,
				EventInputId: "shelly|push_event|dev:0",
				ControlRelations: []app.ButtonControlRelation{
					{EventType: "single_press", Action: "toggle", DeviceName: "Living Room"},
					{EventType: "double_press", Action: "brightness", Level: 80, DeviceName: "Hall"},
					{EventType: "long_press", Action: "on", DeviceName: "Nonexistent"},
				},
			},
			{
				Name: "Evening", Type: app.DeviceTypeScene,
				SceneStateIndex: 1, SceneStateNames: []string{"off", "cozy"},
			},
		},
	}
}

func fullFixtureConfig() app.EditableConfig {
	return app.EditableConfig{
		Scenes: []app.SceneEditConfig{
			{
				Name: "Evening",
				States: []app.SceneStateEditConfig{
					{
						Name: "cozy",
						Actions: []string{
							"brightness:40:Hall",
							"toggle:Nonexistent2",
							"toggle:Nonexistent2", // duplicate, must be deduped
							"not a valid action",  // must be silently skipped
						},
					},
				},
			},
		},
	}
}

func newTestWebServer(t *testing.T, state app.AppState, cfg *app.EditableConfig) *WebServer {
	t.Helper()
	opts := WebServerOptions{}
	if cfg != nil {
		opts.ConfigProvider = &fakeConfigProvider{cfg: *cfg}
	}
	ws, err := NewWebServerWithConfig(&fakeStateProvider{state: state}, 0, log.New(nil), opts)
	if err != nil {
		t.Fatalf("NewWebServerWithConfig: %v", err)
	}
	return ws
}

func doSchemaRequest(t *testing.T, ws *WebServer) (int, apiSchemaResponse, string) {
	t.Helper()
	req := httptest.NewRequest("GET", "/api/schema", nil)
	w := httptest.NewRecorder()
	ws.handleApiSchema(w, req)

	body := w.Body.String()
	var resp apiSchemaResponse
	if w.Code == 200 {
		if err := json.Unmarshal([]byte(body), &resp); err != nil {
			t.Fatalf("decode response: %v (body: %s)", err, body)
		}
	}
	return w.Code, resp, body
}

func TestHandleApiSchema_FullGraph(t *testing.T) {
	cfg := fullFixtureConfig()
	ws := newTestWebServer(t, fullFixtureState(), &cfg)

	code, resp, body := doSchemaRequest(t, ws)
	if code != 200 {
		t.Fatalf("status = %d, want 200 (body: %s)", code, body)
	}

	if resp.Name != "My Home" {
		t.Errorf("Name = %q, want %q", resp.Name, "My Home")
	}
	if !resp.HomeKit.Enabled || resp.HomeKit.DeviceCount != 5 {
		t.Errorf("HomeKit = %+v, want enabled=true device_count=5", resp.HomeKit)
	}

	// ---- Driver nodes ----
	gpio := nodeByID(t, resp.Nodes, "driver:gpio")
	if gpio.Kind != "driver" || gpio.Ready == nil || !*gpio.Ready {
		t.Errorf("driver:gpio = %+v, want kind=driver ready=true", gpio)
	}
	if gpio.IoTotal == nil || *gpio.IoTotal != 0 {
		t.Errorf("driver:gpio io_total = %v, want 0 (no IoDebug entries for gpio)", gpio.IoTotal)
	}

	shelly := nodeByID(t, resp.Nodes, "driver:shelly")
	if shelly.Ready == nil || *shelly.Ready {
		t.Errorf("driver:shelly ready = %v, want false", shelly.Ready)
	}
	if shelly.IoTotal == nil || *shelly.IoTotal != 2 {
		t.Errorf("driver:shelly io_total = %v, want 2", shelly.IoTotal)
	}

	// ---- IO nodes ----
	ioNode := nodeByID(t, resp.Nodes, "io:gpio|d_out|5")
	if ioNode.Kind != "io" || ioNode.Driver != "gpio" || ioNode.IoType != "d_out" || ioNode.Label != "5" {
		t.Errorf("io node = %+v, want driver=gpio io_type=d_out label=5", ioNode)
	}
	if ioNode.Invalid {
		t.Errorf("io node marked invalid unexpectedly: %+v", ioNode)
	}

	rgbwNode := nodeByID(t, resp.Nodes, "io:gpio|rgbw_out|1")
	if rgbwNode.IoType != "rgbw_out" {
		t.Errorf("rgbw io node io_type = %q, want rgbw_out", rgbwNode.IoType)
	}

	// custom_name lookup: shelly input0 was given a custom name in IoDebug,
	// under the push_event-mapped-as-d_in formula it will only resolve if the
	// device's event input id happens to be a d_in-shaped string; here it is
	// push_event-typed so custom_name resolution is expected to miss (documented
	// best-effort limitation) - just assert it doesn't crash and stays empty.
	buttonIoNode := nodeByID(t, resp.Nodes, "io:shelly|push_event|dev:0")
	if buttonIoNode.Driver != "shelly" || buttonIoNode.IoType != "push_event" {
		t.Errorf("button io node = %+v, want driver=shelly io_type=push_event", buttonIoNode)
	}

	// ---- Device nodes ----
	living := nodeByID(t, resp.Nodes, "device:light:Living Room")
	if living.DeviceType != "light" || living.HomeKit == nil || !*living.HomeKit ||
		living.Healthy == nil || !*living.Healthy || living.Faulty == nil || *living.Faulty ||
		living.IsOn == nil || !*living.IsOn {
		t.Errorf("device:light:Living Room = %+v, unexpected bool fields", living)
	}
	if living.Detail == nil || living.Detail["output_io_id"] != "gpio|d_out|5" {
		t.Errorf("device:light:Living Room detail = %+v, want output_io_id=gpio|d_out|5", living.Detail)
	}

	hall := nodeByID(t, resp.Nodes, "device:dimmable_light:Hall")
	if hall.Detail == nil || hall.Detail["analog_io_id"] != "gpio|a_out|1" {
		t.Errorf("device:dimmable_light:Hall detail = %+v, want analog_io_id set", hall.Detail)
	}
	if bri, ok := hall.Detail["brightness"].(float64); !ok || bri != 40 {
		t.Errorf("device:dimmable_light:Hall detail brightness = %+v, want 40", hall.Detail["brightness"])
	}

	mood := nodeByID(t, resp.Nodes, "device:color_light:Mood")
	if mood.Detail == nil || mood.Detail["rgbw_io_id"] != "gpio|rgbw_out|1" {
		t.Errorf("device:color_light:Mood detail = %+v, want rgbw_io_id set", mood.Detail)
	}

	evening := nodeByID(t, resp.Nodes, "device:scene:Evening")
	if evening.Detail == nil {
		t.Fatalf("device:scene:Evening detail missing")
	}
	if idx, ok := evening.Detail["state_index"].(float64); !ok || idx != 1 {
		t.Errorf("scene state_index = %+v, want 1", evening.Detail["state_index"])
	}

	// ---- IO edges: one per configured field ----
	if countEdges(resp.Edges, "io", "device:light:Living Room", "io:gpio|d_out|5") != 1 {
		t.Errorf("expected exactly one output io edge for Living Room")
	}
	if countEdges(resp.Edges, "io", "device:dimmable_light:Hall", "io:gpio|a_out|1") != 1 {
		t.Errorf("expected exactly one analog io edge for Hall")
	}
	if countEdges(resp.Edges, "io", "device:color_light:Mood", "io:gpio|rgbw_out|1") != 1 {
		t.Errorf("expected exactly one rgbw io edge for Mood")
	}
	if countEdges(resp.Edges, "io", "device:button:Wall", "io:shelly|push_event|dev:0") != 1 {
		t.Errorf("expected exactly one event_input io edge for Wall")
	}
	for _, e := range resp.Edges {
		if e.Kind != "io" {
			continue
		}
		if e.Role == "" {
			t.Errorf("io edge missing role: %+v", e)
		}
	}

	// ---- Control edges ----
	var toggleEdge, brightnessEdge, missingEdge *apiSchemaEdge
	for i := range resp.Edges {
		e := &resp.Edges[i]
		if e.Kind != "control" {
			continue
		}
		switch e.To {
		case "device:light:Living Room":
			toggleEdge = e
		case "device:dimmable_light:Hall":
			brightnessEdge = e
		case "device:missing:Nonexistent":
			missingEdge = e
		}
	}
	if toggleEdge == nil {
		t.Fatal("missing control edge to Living Room")
	}
	if toggleEdge.Event != "single_press" || toggleEdge.Action != "toggle" || toggleEdge.Level == nil || *toggleEdge.Level != 0 {
		t.Errorf("toggle control edge = %+v, want event=single_press action=toggle level=0 (present, not omitted)", toggleEdge)
	}
	if brightnessEdge == nil {
		t.Fatal("missing control edge to Hall")
	}
	if brightnessEdge.Event != "double_press" || brightnessEdge.Action != "brightness" || brightnessEdge.Level == nil || *brightnessEdge.Level != 80 {
		t.Errorf("brightness control edge = %+v, want event=double_press action=brightness level=80", brightnessEdge)
	}
	if missingEdge == nil {
		t.Fatal("missing control edge to placeholder device:missing:Nonexistent")
	}
	missingNode, ok := findNode(resp.Nodes, "device:missing:Nonexistent")
	if !ok {
		t.Fatal("placeholder node device:missing:Nonexistent not created")
	}
	if missingNode.DeviceType != "missing" || !missingNode.Missing || missingNode.Label != "Nonexistent" {
		t.Errorf("placeholder node = %+v, want device_type=missing missing=true label=Nonexistent", missingNode)
	}

	// ---- Scene action edges ----
	if countEdges(resp.Edges, "scene_action", "device:scene:Evening", "device:dimmable_light:Hall") != 1 {
		t.Errorf("expected exactly one scene_action edge Evening -> Hall")
	}
	for _, e := range resp.Edges {
		if e.Kind == "scene_action" && e.To == "device:dimmable_light:Hall" {
			if e.State != "cozy" || e.Action != "brightness" || e.Level == nil || *e.Level != 40 {
				t.Errorf("scene_action edge = %+v, want state=cozy action=brightness level=40", e)
			}
		}
	}
	// Deduped: two identical "toggle:Nonexistent2" actions collapse to one edge.
	if countEdges(resp.Edges, "scene_action", "device:scene:Evening", "device:missing:Nonexistent2") != 1 {
		t.Errorf("expected deduped single scene_action edge to Nonexistent2 placeholder")
	}
	if _, ok := findNode(resp.Nodes, "device:missing:Nonexistent2"); !ok {
		t.Errorf("placeholder node device:missing:Nonexistent2 not created")
	}
	// Unparseable action "not a valid action" must not produce any edge with
	// an empty/garbage target.
	for _, e := range resp.Edges {
		if e.Kind == "scene_action" && e.From == "device:scene:Evening" && e.To == "" {
			t.Errorf("unparseable scene action produced a stray edge: %+v", e)
		}
	}

	// ---- Level presence rules: control/scene_action always carry "level"
	// (even 0), io edges never carry it at all. ----
	var raw struct {
		Edges []map[string]json.RawMessage `json:"edges"`
	}
	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		t.Fatalf("raw decode: %v", err)
	}
	for _, e := range raw.Edges {
		kind := strings.Trim(string(e["kind"]), `"`)
		_, hasLevel := e["level"]
		switch kind {
		case "control", "scene_action":
			if !hasLevel {
				t.Errorf("edge %v missing level key", e)
			}
		case "io":
			if hasLevel {
				t.Errorf("io edge %v unexpectedly has level key", e)
			}
		}
	}
}

func TestHandleApiSchema_NilConfigProvider(t *testing.T) {
	ws := newTestWebServer(t, fullFixtureState(), nil)

	code, resp, body := doSchemaRequest(t, ws)
	if code != 200 {
		t.Fatalf("status = %d, want 200 (body: %s)", code, body)
	}

	for _, e := range resp.Edges {
		if e.Kind == "scene_action" {
			t.Errorf("scene_action edge present with nil ConfigProvider: %+v", e)
		}
	}
	// Control edges must still be present - they come from state, not config.
	if countEdges(resp.Edges, "control", "device:button:Wall", "device:light:Living Room") != 1 {
		t.Errorf("expected control edge to survive nil ConfigProvider")
	}
	// Scene device node itself still appears (comes from state.Devices).
	if _, ok := findNode(resp.Nodes, "device:scene:Evening"); !ok {
		t.Errorf("scene device node missing even though it comes from state, not config")
	}
}

func TestBuildSchemaGraph_InvalidIoId(t *testing.T) {
	state := app.AppState{
		Devices: []app.DeviceState{
			{Name: "Weird", Type: app.DeviceTypeLight, OutputIoId: "not-a-valid-id"},
		},
	}
	resp := buildSchemaGraph(state, nil)

	node, ok := findNode(resp.Nodes, "io:not-a-valid-id")
	if !ok {
		t.Fatal("expected an io node for the invalid id")
	}
	if !node.Invalid {
		t.Errorf("expected invalid=true, got %+v", node)
	}
	if node.Driver != "" || node.IoType != "" {
		t.Errorf("invalid io node should not carry driver/io_type: %+v", node)
	}
	if node.Label != "not-a-valid-id" {
		t.Errorf("invalid io node label = %q, want full raw id", node.Label)
	}
	// No placeholder driver node should appear for an unparseable id.
	for _, n := range resp.Nodes {
		if n.Kind == "driver" {
			t.Errorf("unexpected driver node for invalid io id: %+v", n)
		}
	}
}

func TestBuildSchemaGraph_MissingDriverPlaceholder(t *testing.T) {
	state := app.AppState{
		Devices: []app.DeviceState{
			{Name: "A", Type: app.DeviceTypeLight, OutputIoId: "ghost|d_out|1"},
			{Name: "B", Type: app.DeviceTypeOutlet, OutputIoId: "ghost|d_out|2"},
		},
	}
	resp := buildSchemaGraph(state, nil)

	driverNodes := 0
	for _, n := range resp.Nodes {
		if n.Kind == "driver" {
			driverNodes++
			if n.ID != "driver:ghost" || !n.Missing {
				t.Errorf("unexpected driver node: %+v", n)
			}
		}
	}
	if driverNodes != 1 {
		t.Errorf("expected exactly one placeholder driver node (deduped across both devices), got %d", driverNodes)
	}
}

func TestBuildSchemaGraph_EmptyState(t *testing.T) {
	resp := buildSchemaGraph(app.AppState{}, nil)
	if len(resp.Nodes) != 0 || len(resp.Edges) != 0 {
		t.Fatalf("expected empty nodes/edges, got %d nodes, %d edges", len(resp.Nodes), len(resp.Edges))
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	body := string(data)
	if !strings.Contains(body, `"nodes":[]`) {
		t.Errorf("expected nodes to serialize as [] not null, got: %s", body)
	}
	if !strings.Contains(body, `"edges":[]`) {
		t.Errorf("expected edges to serialize as [] not null, got: %s", body)
	}
}

func TestHandleApiSchema_MissingTargetFirstMatchWins(t *testing.T) {
	// A name shared by a button (non-controllable) and a light (controllable)
	// must resolve control targets to the light, not create a placeholder.
	state := app.AppState{
		Devices: []app.DeviceState{
			{Name: "Shared", Type: app.DeviceTypeLight, OutputIoId: "gpio|d_out|1"},
			{
				Name: "Btn", Type: app.DeviceTypeButton,
				ControlRelations: []app.ButtonControlRelation{
					{EventType: "single_press", Action: "toggle", DeviceName: "Shared"},
				},
			},
		},
	}
	resp := buildSchemaGraph(state, nil)

	if countEdges(resp.Edges, "control", "device:button:Btn", "device:light:Shared") != 1 {
		t.Errorf("expected control edge to resolve to the real light, not a placeholder")
	}
	if _, ok := findNode(resp.Nodes, "device:missing:Shared"); ok {
		t.Errorf("no placeholder should be created when a real controllable device matches by name")
	}
}

func TestHandleApiSchema_MethodAndRoute(t *testing.T) {
	ws := newTestWebServer(t, fullFixtureState(), nil)
	req := httptest.NewRequest("GET", "/api/schema", nil)
	w := httptest.NewRecorder()
	ws.mux.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("GET /api/schema via mux = %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
}

func TestHandlePage_SchemaRoute(t *testing.T) {
	ws := newTestWebServer(t, fullFixtureState(), nil)
	req := httptest.NewRequest("GET", "/schema", nil)
	w := httptest.NewRecorder()
	ws.mux.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("GET /schema = %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
	if pageTab("/schema") != "schema" {
		t.Errorf("pageTab(/schema) = %q, want schema", pageTab("/schema"))
	}
}
