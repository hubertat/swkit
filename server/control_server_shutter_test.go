package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/log"
	"github.com/hubertat/swkit/app"
)

// shutterController is a fake controller with one light (index 0) and one
// window shutter (index 1).
type shutterController struct {
	calls   []string
	lastPct int
	shutter app.WindowShutterState
}

func (f *shutterController) GetState() app.AppState {
	s := f.shutter
	return app.AppState{Devices: []app.DeviceState{
		{Name: "L", Type: app.DeviceTypeLight, IsOn: true, IsHealthy: true},
		{Name: "Sh", Type: app.DeviceTypeWindowShutter, IsHealthy: true, WindowShutter: &s},
	}}
}
func (f *shutterController) Subscribe(context.Context, time.Duration) <-chan app.AppState { return nil }

func (f *shutterController) result(action string, idx int, st app.WindowShutterState) app.ControlResult {
	f.calls = append(f.calls, action)
	if idx != 1 {
		return app.ControlResult{DeviceName: "L", Action: action, Error: errors.New("device \"L\" is not a window shutter")}
	}
	return app.ControlResult{DeviceName: "Sh", Action: action, NewState: st.PositionKnown && st.CurrentPosition > 0, WindowShutter: &st}
}
func (f *shutterController) ToggleDevice(i int) app.ControlResult {
	if i == 0 {
		f.calls = append(f.calls, "toggle")
		return app.ControlResult{DeviceName: "L", Action: "toggle", NewState: false}
	}
	// Toggle on a moving shutter stops it.
	return f.result("toggle", i, app.WindowShutterState{Motion: "stopped", TargetPosition: -1})
}
func (f *shutterController) SetDevice(i int, v bool) app.ControlResult {
	if v {
		return f.result("open", i, app.WindowShutterState{Motion: "opening", TargetPosition: 100, RemainingTravelMilliseconds: 22000})
	}
	return f.result("close", i, app.WindowShutterState{Motion: "closing", TargetPosition: 0})
}
func (f *shutterController) SetDeviceBrightness(int, int) app.ControlResult {
	return app.ControlResult{}
}
func (f *shutterController) AdjustDeviceBrightness(int, int) app.ControlResult {
	return app.ControlResult{}
}
func (f *shutterController) SetDeviceValueFor(int, bool, int) app.ControlResult {
	return app.ControlResult{Error: errors.New("timed on/off is not supported by window shutter")}
}
func (f *shutterController) OpenDevice(i int) app.ControlResult {
	return f.result("open", i, app.WindowShutterState{Motion: "opening", TargetPosition: 100, PositionKnown: true, CurrentPosition: 0})
}
func (f *shutterController) CloseDevice(i int) app.ControlResult {
	return f.result("close", i, app.WindowShutterState{Motion: "closing", TargetPosition: 0, PositionKnown: true, CurrentPosition: 40})
}
func (f *shutterController) StopDevice(i int) app.ControlResult {
	return f.result("stop", i, app.WindowShutterState{Motion: "stopped", TargetPosition: 0, PositionKnown: true, CurrentPosition: 0})
}
func (f *shutterController) SetDevicePosition(i, pct int) app.ControlResult {
	f.lastPct = pct
	return f.result("position", i, app.WindowShutterState{Motion: "closing", TargetPosition: pct, PositionKnown: true, CurrentPosition: 50})
}

func postAction(t *testing.T, cs *ControlServer, path, body string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest("POST", path, strings.NewReader(body))
	w := httptest.NewRecorder()
	cs.handleDeviceAction(w, req)
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

func TestControlServerShutterActions(t *testing.T) {
	f := &shutterController{}
	cs, err := NewControlServer(f, "/control", "t", log.New(nil))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		path, body, motion string
		target             float64
	}{
		{"/control/api/devices/1/open", "", "opening", 100},
		{"/control/api/devices/1/close", "", "closing", 0},
		{"/control/api/devices/1/stop", "", "stopped", 0},
		{"/control/api/devices/1/set_position", `{"value":0}`, "closing", 0},
		{"/control/api/devices/1/set_position", `{"value":40}`, "closing", 40},
		{"/control/api/devices/1/set", `{"value":true}`, "opening", 100},
		{"/control/api/devices/1/toggle", "", "stopped", -1},
	}
	for _, c := range cases {
		code, out := postAction(t, cs, c.path, c.body)
		if code != 200 {
			t.Fatalf("%s: status %d", c.path, code)
		}
		ws, ok := out["window_shutter"].(map[string]any)
		if !ok {
			t.Fatalf("%s: window_shutter payload missing: %v", c.path, out)
		}
		if ws["motion"] != c.motion || ws["target_position"] != c.target {
			t.Fatalf("%s: payload %v", c.path, ws)
		}
		// Zero values are explicit, never omitted.
		for _, k := range []string{"current_position", "position_known", "remaining_travel_ms", "target_position"} {
			if _, present := ws[k]; !present {
				t.Fatalf("%s: %s must always be present", c.path, k)
			}
		}
	}
	if f.lastPct != 40 {
		t.Fatalf("position routed: %d", f.lastPct)
	}
	// Bad bodies.
	for _, body := range []string{"", `{"value":101}`, `{"value":-1}`, `{"value":"x"}`, `{}`} {
		if code, _ := postAction(t, cs, "/control/api/devices/1/set_position", body); code != 400 {
			t.Fatalf("body %q: status %d, want 400", body, code)
		}
	}
	// Shutter-only actions on a light are rejected.
	if code, _ := postAction(t, cs, "/control/api/devices/0/open", ""); code == 200 {
		t.Fatal("open on a light must fail")
	}
	// Non-shutter responses keep their shape.
	code, out := postAction(t, cs, "/control/api/devices/0/toggle", "")
	if code != 200 {
		t.Fatal(code)
	}
	if _, has := out["window_shutter"]; has {
		t.Fatal("non-shutter responses carry no window_shutter payload")
	}
}

func TestControlServerShutterUnsupportedController(t *testing.T) {
	cs, _ := NewControlServer(&fakeController{}, "/control", "t", log.New(nil))
	if code, _ := postAction(t, cs, "/control/api/devices/0/open", ""); code != 501 {
		t.Fatalf("status %d, want 501", code)
	}
	w := httptest.NewRecorder()
	cs.handleInfo(w, httptest.NewRequest("GET", "/control/api/info", nil))
	if strings.Contains(w.Body.String(), "window_shutter") {
		t.Fatal("feature must not be advertised without support")
	}
}

func TestControlServerShutterListAndInfo(t *testing.T) {
	f := &shutterController{shutter: app.WindowShutterState{Motion: "stopped", TargetPosition: 0, CurrentPosition: 0, PositionKnown: true, OpenDurationSeconds: 20, CloseDurationSeconds: 20, OpenIoId: "a", CloseIoId: "b"}}
	cs, _ := NewControlServer(f, "/control", "t", log.New(nil))
	w := httptest.NewRecorder()
	cs.handleDeviceList(w, httptest.NewRequest("GET", "/control/api/devices", nil))
	var list []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	sh := list[1]
	if sh["type"] != "window_shutter" || sh["controllable"] != true {
		t.Fatalf("list entry: %v", sh)
	}
	ws := sh["window_shutter"].(map[string]any)
	if ws["current_position"] != float64(0) || ws["position_known"] != true || ws["open_duration_seconds"] != float64(20) || ws["close_io_id"] != "b" {
		t.Fatalf("0%% must be explicit: %v", ws)
	}
	if _, has := list[0]["window_shutter"]; has {
		t.Fatal("lights have no shutter payload")
	}
	w = httptest.NewRecorder()
	cs.handleInfo(w, httptest.NewRequest("GET", "/control/api/info", nil))
	if !strings.Contains(w.Body.String(), `"features":["window_shutter"]`) {
		t.Fatalf("info: %s", w.Body.String())
	}
}

func TestSchemaGraphShutterEdges(t *testing.T) {
	state := app.AppState{
		Drivers: []app.DriverState{{Name: "wago", Ready: true}},
		Devices: []app.DeviceState{
			{Name: "Sh", Type: app.DeviceTypeWindowShutter, WindowShutter: &app.WindowShutterState{OpenIoId: "wago|d_out|4", CloseIoId: "wago|d_out|5", Motion: "opening"}},
			{Name: "B", Type: app.DeviceTypeButton, ControlRelations: []app.ButtonControlRelation{{EventType: "single_press", Action: "open", DeviceName: "Sh"}}},
		},
	}
	cfg := &app.EditableConfig{Scenes: []app.SceneEditConfig{{Name: "S", Devices: []string{"Sh"}, States: []app.SceneStateEditConfig{{Name: "Off", Actions: []string{"stop:Sh"}}}}}}
	g := buildSchemaGraph(state, cfg)
	roles := map[string]string{}
	var control, scene bool
	for _, e := range g.Edges {
		if e.Kind == "io" {
			roles[e.Role] = e.To
		}
		if e.Kind == "control" && e.To == "device:window_shutter:Sh" && e.Action == "open" {
			control = true
		}
		if e.Kind == "scene_action" && e.To == "device:window_shutter:Sh" && e.Action == "stop" {
			scene = true
		}
	}
	if roles["open_output"] != "io:wago|d_out|4" || roles["close_output"] != "io:wago|d_out|5" {
		t.Fatalf("io edges: %v", roles)
	}
	if !control || !scene {
		t.Fatalf("action edges: control %v scene %v", control, scene)
	}
}
