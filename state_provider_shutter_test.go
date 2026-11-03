package swkit

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/log"
	"github.com/hubertat/swkit/app"
	drivers "github.com/hubertat/swkit/drivers"
)

// providerWithShutter builds a provider over one light, one scene and one
// harness-driven shutter (fake clock), listed in that order.
func providerWithShutter(t *testing.T) (*SwKitProvider, *shutterHarness, *SwKit) {
	t.Helper()
	logger := log.New(nil)
	h := newShutterHarness(t, shutterConfig())
	li := newTestLight("L", false)
	sc, err := NewScene(SceneConfig{
		Name: "S", Devices: []string{"L"},
		States: []SceneStateConfig{{Name: "Off", Actions: []string{"off:L"}}, {Name: "On", Actions: []string{"on:L"}}},
	}, resolverFrom(li), logger)
	if err != nil {
		t.Fatalf("scene: %v", err)
	}
	sw := &SwKit{Name: "t"}
	sw.lights = []*Light{li}
	sw.scenes = []*Scene{sc}
	sw.windowShutters = []*WindowShutter{h.ws}
	sw.ioDrivers = map[string]drivers.IoDriver{}
	sw.timed = newTimedController(logger)
	return NewStateProvider(sw), h, sw
}

func TestProviderShutterStateAndIndexes(t *testing.T) {
	p, _, _ := providerWithShutter(t)
	st := p.GetState()
	if len(st.Devices) != 3 {
		t.Fatalf("devices: %d", len(st.Devices))
	}
	if st.Devices[0].Type != app.DeviceTypeLight || st.Devices[1].Type != app.DeviceTypeScene || st.Devices[2].Type != app.DeviceTypeWindowShutter {
		t.Fatalf("order must be light, scene, shutter: %v %v %v", st.Devices[0].Type, st.Devices[1].Type, st.Devices[2].Type)
	}
	ds := st.Devices[2]
	if ds.WindowShutter == nil {
		t.Fatal("nested shutter state expected")
	}
	ws := ds.WindowShutter
	if ws.PositionKnown || ws.Motion != "stopped" || ws.TargetPosition != -1 || ds.IsOn || ds.IsFaulty {
		t.Fatalf("unknown startup state: %+v %+v", ds, ws)
	}
	if ws.OpenDurationSeconds != 20 || ws.CloseDurationSeconds != 20 || ws.OpenIoId != "mock|d_out|open" || ws.CloseIoId != "mock|d_out|close" {
		t.Fatalf("durations/io ids: %+v", ws)
	}
	if st.Summary().WindowShuttersCount != 1 {
		t.Fatal("summary count")
	}
	for _, d := range st.Devices[:2] {
		if d.WindowShutter != nil {
			t.Fatal("non-shutter devices have no nested shutter state")
		}
	}
}

func TestProviderShutterCommands(t *testing.T) {
	p, h, _ := providerWithShutter(t)
	const idx = 2

	res := p.OpenDevice(idx)
	if res.Error != nil || res.WindowShutter == nil || res.WindowShutter.Motion != "opening" || res.WindowShutter.TargetPosition != 100 {
		t.Fatalf("open: %+v %+v", res, res.WindowShutter)
	}
	if res.WindowShutter.RemainingTravelMilliseconds != 22000 {
		t.Fatalf("remaining travel: %+v", res.WindowShutter)
	}
	// Generic toggle on a moving shutter stops it, and the result says so.
	h.fc.Advance(10 * time.Second)
	res = p.ToggleDevice(idx)
	if res.Error != nil || res.WindowShutter == nil || res.WindowShutter.Motion != "stopped" {
		t.Fatalf("toggle-to-stop: %+v %+v", res, res.WindowShutter)
	}
	if res.WindowShutter.PositionKnown {
		t.Fatal("run from unknown stays unknown")
	}
	// Intermediate position from unknown: error, nested state still present.
	res = p.SetDevicePosition(idx, 40)
	if res.Error == nil || res.WindowShutter == nil {
		t.Fatalf("position from unknown must fail with state: %+v", res)
	}
	if res := p.SetDevicePosition(idx, 101); res.Error == nil {
		t.Fatal("out-of-range position")
	}
	// Generic set true/false = open/close.
	res = p.SetDevice(idx, false)
	if res.Error != nil || res.Action != "close" || res.WindowShutter == nil {
		t.Fatalf("set false: %+v", res)
	}
	h.fc.Advance(time.Second)
	if s := h.snap(); s.Motion != "closing" {
		t.Fatalf("set false must close: %+v", s)
	}
	res = p.StopDevice(idx)
	if res.Error != nil || res.WindowShutter.Motion != "stopped" {
		t.Fatalf("stop: %+v", res)
	}
	// Shutter commands on other devices are rejected.
	for _, r := range []app.ControlResult{p.OpenDevice(0), p.CloseDevice(1), p.StopDevice(0), p.SetDevicePosition(0, 10)} {
		if r.Error == nil || !strings.Contains(r.Error.Error(), "not a window shutter") {
			t.Fatalf("expected rejection: %+v", r)
		}
	}
	if res := p.OpenDevice(9); res.Error == nil {
		t.Fatal("out of range index")
	}
}

func TestProviderShutterUnsupportedOperations(t *testing.T) {
	p, h, sw := providerWithShutter(t)
	const idx = 2
	var timers int
	sw.timed.afterFunc = func(time.Duration, func()) func() {
		timers++
		return func() {}
	}
	if res := p.SetDeviceValueFor(idx, true, 30); res.Error == nil {
		t.Fatal("set_for on a shutter must fail")
	}
	if res := p.SetDeviceBrightness(idx, 50); res.Error == nil {
		t.Fatal("brightness on a shutter must fail")
	}
	if res := p.AdjustDeviceBrightness(idx, 5); res.Error == nil {
		t.Fatal("brightness adjust on a shutter must fail")
	}
	// Direct internal timed-control call: no write, no timer.
	if err := sw.SetDeviceValueFor(h.ws, true, time.Minute); err == nil {
		t.Fatal("internal timed control must reject shutters")
	}
	if timers != 0 || h.open.setCount()+h.close.setCount() != 0 {
		t.Fatalf("no timer and no write allowed (timers %d, writes %d)", timers, h.open.setCount()+h.close.setCount())
	}
}

func TestProviderShutterConcurrentResultsDescribeOwnCommand(t *testing.T) {
	p, h, _ := providerWithShutter(t)
	h.calibrateOpen(t)
	const idx = 2
	var wg sync.WaitGroup
	results := make([]app.ControlResult, 2)
	wg.Add(2)
	go func() { defer wg.Done(); results[0] = p.SetDevicePosition(idx, 30) }()
	go func() { defer wg.Done(); results[1] = p.StopDevice(idx) }()
	wg.Wait()
	pos := results[0].WindowShutter
	if pos == nil || results[0].Error != nil {
		t.Fatalf("position result: %+v", results[0])
	}
	// Whatever the order, the position result must describe the accepted
	// position command (target 30), never the later stop.
	if pos.TargetPosition != 30 || pos.Motion != "closing" {
		t.Fatalf("position result replaced by another command: %+v", pos)
	}
}

// debugDriver is a driver with a debug snapshot, output toggling and a
// name -> output table in which several names may alias one output (as Wago
// module:port and global-index ids do).
type debugDriver struct {
	*drivers.MockIoDriver
	name    string
	points  []drivers.IoPointState
	outs    map[string]drivers.DigitalOutput
	toggled []int
}

func (d *debugDriver) String() string { return d.name }
func (d *debugDriver) GetIoDebugSnapshot() drivers.IoDebugSnapshot {
	return drivers.IoDebugSnapshot{Points: d.points}
}
func (d *debugDriver) ToggleOutput(i int) error { d.toggled = append(d.toggled, i); return nil }
func (d *debugDriver) GetDigitalOutput(id string) (drivers.DigitalOutput, error) {
	if o, ok := d.outs[id]; ok {
		return o, nil
	}
	return nil, fmt.Errorf("no output %q", id)
}

func TestProviderRawOutputOwnership(t *testing.T) {
	logger := log.New(nil)
	sw0, sw1, li0, sw2 := &fakeOut{name: "AA:0"}, &fakeOut{name: "AA:1"}, &fakeOut{name: "BB:light0"}, &fakeOut{name: "AA:2"}
	w4, w5, w6 := &fakeOut{name: "w4"}, &fakeOut{name: "w5"}, &fakeOut{name: "w6"}
	cases := []struct {
		name              string
		driver            *debugDriver
		openId, closeId   string
		openOut, closeOut drivers.DigitalOutput
		owned, free       []int
	}{
		{
			name: "shelly switch and light",
			driver: &debugDriver{name: "shelly", points: []drivers.IoPointState{
				{Index: 0, Name: "shellyplus2pm-AA:switch0", Type: drivers.IoTypeDigitalOutput},
				{Index: 1, Name: "shellyplus2pm-AA:switch1", Type: drivers.IoTypeDigitalOutput},
				{Index: 2, Name: "dimmer-BB:light0", Type: drivers.IoTypeDigitalOutput},
				{Index: 3, Name: "shellyplus2pm-AA:switch2", Type: drivers.IoTypeDigitalOutput},
			}, outs: map[string]drivers.DigitalOutput{
				"shellyplus2pm-AA:0": sw0, "shellyplus2pm-AA:1": sw1, "dimmer-BB:light:0": li0, "shellyplus2pm-AA:2": sw2,
			}},
			openId: "shelly|d_out|shellyplus2pm-AA:0", closeId: "shelly|d_out|dimmer-BB:light:0",
			openOut: sw0, closeOut: li0,
			owned: []int{0, 2}, free: []int{1, 3},
		},
		{
			// Configured as module:port aliases; the snapshot maps raw indexes
			// to global ids. They must still match.
			name: "wago module:port aliases",
			driver: &debugDriver{name: "wago", points: []drivers.IoPointState{
				{Index: 4, Name: "M2:DO1[4]", Type: drivers.IoTypeDigitalOutput},
				{Index: 5, Name: "M2:DO2[5]", Type: drivers.IoTypeDigitalOutput},
				{Index: 6, Name: "M2:DO3[6]", Type: drivers.IoTypeDigitalOutput},
			}, outs: map[string]drivers.DigitalOutput{
				"4": w4, "2:1": w4, "5": w5, "2:2": w5, "6": w6, "2:3": w6,
			}},
			openId: "wago|d_out|2:1", closeId: "wago|d_out|2:2",
			openOut: w4, closeOut: w5,
			owned: []int{4, 5}, free: []int{6},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ws := NewWindowShutter(WindowShutterConfig{
				Name: "Sh", OpenDigitalOutName: tc.openId, CloseDigitalOutName: tc.closeId, OpenDurationSeconds: 10,
			}, tc.openOut, tc.closeOut, logger)
			sw := &SwKit{Name: "t"}
			sw.windowShutters = []*WindowShutter{ws}
			sw.ioDrivers = map[string]drivers.IoDriver{tc.driver.name: tc.driver}
			p := NewStateProvider(sw)
			for _, i := range tc.owned {
				if err := p.ToggleIoOutput(tc.driver.name, i); err == nil || !strings.Contains(err.Error(), "window shutter") {
					t.Fatalf("output %d must be locked: %v", i, err)
				}
			}
			for _, i := range tc.free {
				if err := p.ToggleIoOutput(tc.driver.name, i); err != nil {
					t.Fatalf("output %d must be free: %v", i, err)
				}
			}
			if err := p.ToggleIoOutput(tc.driver.name, 99); err == nil {
				t.Fatal("unmappable index must be rejected while shutters use the driver")
			}
			if len(tc.driver.toggled) != len(tc.free) {
				t.Fatalf("only free outputs may be toggled: %v", tc.driver.toggled)
			}
		})
	}
}
