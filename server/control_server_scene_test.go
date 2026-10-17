package server

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/charmbracelet/log"
	"github.com/hubertat/swkit/app"
)

// sceneToggleController answers toggles like the real provider: scenes carry
// the selected index, other devices do not.
type sceneToggleController struct {
	stateController
}

func (s *sceneToggleController) ToggleDevice(index int) app.ControlResult {
	d := s.state.Devices[index]
	if d.Type != app.DeviceTypeScene {
		return app.ControlResult{DeviceName: d.Name, Action: "toggle", NewState: !d.IsOn}
	}
	idx := 0 // e.g. the toggle timeout expired: back to Off
	return app.ControlResult{DeviceName: d.Name, Action: "toggle", NewState: idx != 0, SceneIndex: &idx}
}

func newSceneControlServer(t *testing.T) *ControlServer {
	t.Helper()
	ctrl := &sceneToggleController{stateController{state: app.AppState{Devices: []app.DeviceState{
		{Name: "Hall", Type: app.DeviceTypeLight, IsHealthy: true},
		{Name: "Evening", Type: app.DeviceTypeScene, IsHealthy: true, IsOn: true,
			SceneStateIndex: 1, SceneStateNames: []string{"Off", "On", "Late"}, SceneNextIndex: 0},
		{Name: "Movie", Type: app.DeviceTypeScene, IsHealthy: true,
			SceneStateIndex: 0, SceneStateNames: []string{"Off", "On"}, SceneNextIndex: 1},
	}}}}
	cs, err := NewControlServer(ctrl, "/control", "t", log.New(nil))
	if err != nil {
		t.Fatalf("NewControlServer: %v", err)
	}
	return cs
}

func TestControlServerDeviceListSceneNext(t *testing.T) {
	cs := newSceneControlServer(t)
	w := httptest.NewRecorder()
	cs.dispatch(w, httptest.NewRequest("GET", "/control/api/devices", nil))
	if w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}
	var got []map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, ok := got[0]["scene_next"]; ok {
		t.Errorf("light carries scene_next: %s", w.Body.String())
	}
	// A real 0 (timeout due) must still be sent for a scene.
	if v, ok := got[1]["scene_next"]; !ok || string(v) != "0" {
		t.Errorf("Evening scene_next = %s (present %v), want 0", v, ok)
	}
	if v := string(got[2]["scene_next"]); v != "1" {
		t.Errorf("Movie scene_next = %s, want 1", v)
	}
}

func TestControlServerToggleReportsSceneIndex(t *testing.T) {
	cs := newSceneControlServer(t)

	post := func(path string) map[string]json.RawMessage {
		t.Helper()
		w := httptest.NewRecorder()
		cs.dispatch(w, httptest.NewRequest("POST", path, nil))
		if w.Code != 200 {
			t.Fatalf("POST %s: status = %d (%s)", path, w.Code, w.Body.String())
		}
		var got map[string]json.RawMessage
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return got
	}

	if v, ok := post("/control/api/devices/1/toggle")["scene_index"]; !ok || string(v) != "0" {
		t.Errorf("scene toggle scene_index = %s (present %v), want 0", v, ok)
	}
	if _, ok := post("/control/api/devices/0/toggle")["scene_index"]; ok {
		t.Error("light toggle response carries scene_index")
	}
}
