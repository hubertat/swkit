package server

import (
	"context"
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
