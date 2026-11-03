package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/hubertat/swkit/app"
)

// shutterMockController adds window shutter commands to mockController.
type shutterMockController struct {
	*mockController
	calls []string
}

func (s *shutterMockController) res(call string, idx int, ws app.WindowShutterState) app.ControlResult {
	s.calls = append(s.calls, call)
	return app.ControlResult{DeviceName: s.state.Devices[idx].Name, WindowShutter: &ws}
}
func (s *shutterMockController) OpenDevice(i int) app.ControlResult {
	return s.res("open", i, app.WindowShutterState{Motion: "opening", TargetPosition: 100, RemainingTravelMilliseconds: 22000})
}
func (s *shutterMockController) CloseDevice(i int) app.ControlResult {
	return s.res("close", i, app.WindowShutterState{Motion: "closing", TargetPosition: 0})
}
func (s *shutterMockController) StopDevice(i int) app.ControlResult {
	return s.res("stop", i, app.WindowShutterState{Motion: "stopped", TargetPosition: 30, CurrentPosition: 30, PositionKnown: true})
}
func (s *shutterMockController) SetDevicePosition(i, pct int) app.ControlResult {
	return s.res("position", i, app.WindowShutterState{Motion: "closing", TargetPosition: pct, CurrentPosition: 60, PositionKnown: true})
}

func newShutterMock() *shutterMockController {
	m := newMockController()
	m.state.Devices = append(m.state.Devices, app.DeviceState{
		Name: "Bedroom Shutter", Type: app.DeviceTypeWindowShutter, IsHealthy: true,
		WindowShutter: &app.WindowShutterState{Motion: "stopped", TargetPosition: -1, OpenDurationSeconds: 20, CloseDurationSeconds: 20},
	})
	return &shutterMockController{mockController: m}
}

func TestShutterTools(t *testing.T) {
	ctrl := newShutterMock()
	reg := NewToolRegistry()
	registerDeviceTools(reg, ctrl)
	run := func(tool, input string) (string, error) {
		tl, ok := reg.Get(tool)
		if !ok {
			t.Fatalf("tool %s not registered", tool)
		}
		return tl.Execute(context.Background(), json.RawMessage(input))
	}
	out, err := run("open_shutter", `{"name":"bedroom shutter"}`)
	if err != nil || !strings.Contains(out, "opening toward 100%") || !strings.Contains(out, "position unknown") {
		t.Fatalf("open: %q %v", out, err)
	}
	out, err = run("stop_shutter", `{"name":"Shutter"}`)
	if err != nil || !strings.Contains(out, "estimated 30% open") {
		t.Fatalf("stop must describe an estimate: %q %v", out, err)
	}
	if _, err = run("set_shutter_position", `{"name":"Bedroom Shutter","position":101}`); err == nil {
		t.Fatal("out-of-range position must fail")
	}
	if _, err = run("set_shutter_position", `{"name":"Bedroom Shutter","position":"x"}`); err == nil {
		t.Fatal("bad argument type must fail")
	}
	out, err = run("set_shutter_position", `{"name":"Bedroom Shutter","position":40}`)
	if err != nil || !strings.Contains(out, "toward 40%") {
		t.Fatalf("position: %q %v", out, err)
	}
	if _, err = run("close_shutter", `{"name":"Kitchen Light"}`); err == nil || !strings.Contains(err.Error(), "not a window shutter") {
		t.Fatalf("shutter tool on a light: %v", err)
	}
	if strings.Join(ctrl.calls, ",") != "open,stop,position" {
		t.Fatalf("calls: %v", ctrl.calls)
	}
	out, err = run("get_device_state", `{"name":"Bedroom Shutter"}`)
	if err != nil || !strings.Contains(out, `"window_shutter"`) || strings.Contains(out, "estimated_position_percent_open") {
		t.Fatalf("state: %q %v", out, err)
	}
}

func TestShutterToolsAbsentWithoutSupport(t *testing.T) {
	reg := NewToolRegistry()
	registerDeviceTools(reg, newMockController())
	if _, ok := reg.Get("open_shutter"); ok {
		t.Fatal("shutter tools need a WindowShutterController")
	}
}
