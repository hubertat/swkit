package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/log"
	"github.com/hubertat/swkit/app"
)

// fakeController is a minimal app.DeviceController for handler tests.
type fakeController struct {
	lastIndex   int
	lastState   bool
	lastSeconds int
	lastDelta   int
}

func (f *fakeController) GetState() app.AppState { return app.AppState{} }
func (f *fakeController) Subscribe(ctx context.Context, interval time.Duration) <-chan app.AppState {
	return nil
}
func (f *fakeController) ToggleDevice(int) app.ControlResult             { return app.ControlResult{} }
func (f *fakeController) SetDevice(int, bool) app.ControlResult          { return app.ControlResult{} }
func (f *fakeController) SetDeviceBrightness(int, int) app.ControlResult { return app.ControlResult{} }
func (f *fakeController) AdjustDeviceBrightness(index, delta int) app.ControlResult {
	f.lastIndex, f.lastDelta = index, delta
	return app.ControlResult{DeviceName: "X", Action: "brightness"}
}
func (f *fakeController) SetDeviceValueFor(index int, state bool, seconds int) app.ControlResult {
	f.lastIndex, f.lastState, f.lastSeconds = index, state, seconds
	return app.ControlResult{DeviceName: "X", Action: "on for", NewState: state}
}

func TestControlServerSetForRoutesToTimedControl(t *testing.T) {
	fake := &fakeController{}
	cs, err := NewControlServer(fake, "/control", "t", log.New(nil))
	if err != nil {
		t.Fatalf("NewControlServer: %v", err)
	}

	body := `{"value":true,"seconds":45}`
	req := httptest.NewRequest("POST", "/control/api/devices/2/set_for", strings.NewReader(body))
	w := httptest.NewRecorder()

	cs.handleDeviceAction(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200 (body: %s)", w.Code, w.Body.String())
	}
	if fake.lastIndex != 2 || !fake.lastState || fake.lastSeconds != 45 {
		t.Errorf("controller got (index=%d, state=%v, seconds=%d), want (2, true, 45)",
			fake.lastIndex, fake.lastState, fake.lastSeconds)
	}
}

func TestControlServerAdjustBrightnessRoutesToController(t *testing.T) {
	fake := &fakeController{}
	cs, err := NewControlServer(fake, "/control", "t", log.New(nil))
	if err != nil {
		t.Fatalf("NewControlServer: %v", err)
	}

	body := `{"value":-15}`
	req := httptest.NewRequest("POST", "/control/api/devices/3/adjust_brightness", strings.NewReader(body))
	w := httptest.NewRecorder()

	cs.handleDeviceAction(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200 (body: %s)", w.Code, w.Body.String())
	}
	if fake.lastIndex != 3 || fake.lastDelta != -15 {
		t.Errorf("controller got (index=%d, delta=%d), want (3, -15)", fake.lastIndex, fake.lastDelta)
	}
}

// stateController returns a fixed AppState from GetState.
type stateController struct {
	fakeController
	state app.AppState
}

func (s *stateController) GetState() app.AppState { return s.state }

func TestControlServerVariantRoutes(t *testing.T) {
	cs, err := NewControlServer(&fakeController{}, "/control", "Home", log.New(nil))
	if err != nil {
		t.Fatalf("NewControlServer: %v", err)
	}
	mux := http.NewServeMux()
	cs.RegisterOn(mux)

	cases := []struct {
		path        string
		wantStatus  int
		wantVariant string
	}{
		{"/control", 200, `window.__VARIANT = ""`},
		{"/control/", 200, `window.__VARIANT = ""`},
		{"/control/tiles", 200, `window.__VARIANT = "tiles"`},
		{"/control/list", 200, `window.__VARIANT = "list"`},
		{"/control/compact", 200, `window.__VARIANT = "compact"`},
		{"/control/nope", 404, ""},
		{"/control/list/extra", 404, ""},
	}
	for _, tc := range cases {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("GET", tc.path, nil))
		if w.Code != tc.wantStatus {
			t.Errorf("GET %s: status = %d, want %d", tc.path, w.Code, tc.wantStatus)
			continue
		}
		if tc.wantVariant != "" && !strings.Contains(w.Body.String(), tc.wantVariant) {
			t.Errorf("GET %s: body missing %q", tc.path, tc.wantVariant)
		}
	}
}

func TestControlServerDeviceListExposesUncertainState(t *testing.T) {
	ctrl := &stateController{state: app.AppState{Devices: []app.DeviceState{
		{Name: "Hall", Type: app.DeviceTypeLight, IsHealthy: true, StateError: "wago driver: state is stale"},
		{Name: "Evening", Type: app.DeviceTypeScene, IsHealthy: true, IsOn: true,
			SceneStateIndex: 2, SceneStateNames: []string{"off", "bright", "movie"}},
		{Name: "Door", Type: app.DeviceTypeButton, IsHealthy: true,
			LastEventType: "single_press", LastEventTime: time.Now().Add(-3 * time.Second)},
	}}}
	cs, err := NewControlServer(ctrl, "/control", "t", log.New(nil))
	if err != nil {
		t.Fatalf("NewControlServer: %v", err)
	}
	w := httptest.NewRecorder()
	cs.dispatch(w, httptest.NewRequest("GET", "/control/api/devices", nil))
	if w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}
	var got []controlDeviceResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d devices, want 3", len(got))
	}
	if got[0].StateError == "" {
		t.Error("light: state_error not propagated")
	}
	if got[1].SceneState != "movie" || got[1].SceneIndex != 2 || got[1].SceneCount != 3 {
		t.Errorf("scene: got (%q, %d, %d), want (movie, 2, 3)", got[1].SceneState, got[1].SceneIndex, got[1].SceneCount)
	}
	if got[2].LastEventAgeMs < 2500 || got[2].LastEventAgeMs > 60000 {
		t.Errorf("button: last_event_age_ms = %d, want ~3000", got[2].LastEventAgeMs)
	}
}
