package swkit

import (
	"testing"

	"github.com/charmbracelet/log"
	"github.com/hubertat/swkit/app"
	drivers "github.com/hubertat/swkit/drivers"
)

// newDimmableTestSwKit builds a SwKit with one light, one dimmable light and one
// outlet so index ordering (lights -> dimmable -> outlets) can be exercised.
func newDimmableTestSwKit(t *testing.T) (*SwKit, *drivers.MockAnalogOutput) {
	t.Helper()
	logger := log.New(nil)

	lightOut := drivers.NewMockOutput("l1")
	dimOn := drivers.NewMockOutput("dl_on")
	dimBri := drivers.NewMockAnalogOutput("dl_bri", 0, 32767)
	outletOut := drivers.NewMockOutput("o1")

	sw := &SwKit{Name: "t"}
	sw.lights = []*Light{NewLight(LightConfig{Name: "L1"}, lightOut, logger)}
	sw.dimmableLights = []*DimmableLight{
		NewDimmableLight(DimmableLightConfig{Name: "Dim1"}, dimOn, dimBri, logger),
	}
	sw.outlets = []*Outlet{NewOutlet(OutletConfig{Name: "O1"}, outletOut, logger)}
	sw.ioDrivers = map[string]drivers.IoDriver{}
	return sw, dimBri
}

func TestStateProviderDimmableLightIndexAndBrightness(t *testing.T) {
	sw, dimBri := newDimmableTestSwKit(t)
	p := NewStateProvider(sw)

	// Index 1 should be the dimmable light (after the single plain light).
	dev, name, dtype, err := p.getControllableByIndex(1)
	if err != nil {
		t.Fatalf("getControllableByIndex(1): %v", err)
	}
	if _, ok := dev.(*DimmableLight); !ok {
		t.Fatalf("index 1 is %T, want *DimmableLight", dev)
	}
	if name != "Dim1" || dtype != app.DeviceTypeDimmableLight {
		t.Errorf("index 1 = (%q,%s), want (Dim1, dimmable_light)", name, dtype)
	}

	// Setting brightness to 50% should scale to ~half of the 0-32767 range.
	res := p.SetDeviceBrightness(1, 50)
	if res.Error != nil {
		t.Fatalf("SetDeviceBrightness: %v", res.Error)
	}
	got, _ := dimBri.GetState()
	if got < 16000 || got > 16767 {
		t.Errorf("brightness native = %d, want ~16383", got)
	}

	// Brightness on a non-dimmable device (the plain light at index 0) must error.
	if res := p.SetDeviceBrightness(0, 50); res.Error == nil {
		t.Error("expected error setting brightness on a plain light")
	}
}

func TestStateProviderAdjustDeviceBrightness(t *testing.T) {
	sw, dimBri := newDimmableTestSwKit(t)
	p := NewStateProvider(sw)
	dim := sw.dimmableLights[0]

	// Start at 50%, then nudge up by 20. Round-trip scaling through the native
	// range may read back 49%, so the result lands within a percent of 70%.
	if res := p.SetDeviceBrightness(1, 50); res.Error != nil {
		t.Fatalf("SetDeviceBrightness: %v", res.Error)
	}
	if res := p.AdjustDeviceBrightness(1, 20); res.Error != nil {
		t.Fatalf("AdjustDeviceBrightness: %v", res.Error)
	}
	got, err := dim.GetBrightness()
	if err != nil {
		t.Fatalf("GetBrightness: %v", err)
	}
	if got < 68 || got > 71 {
		t.Errorf("brightness = %d%%, want ~70%% after +20", got)
	}

	// Dimming past the floor clamps to 0.
	if res := p.AdjustDeviceBrightness(1, -100); res.Error != nil {
		t.Fatalf("AdjustDeviceBrightness: %v", res.Error)
	}
	if native, _ := dimBri.GetState(); native != 0 {
		t.Errorf("brightness native = %d, want 0 after clamp", native)
	}

	// Relative brightness on a non-dimmable device (plain light at index 0) errors.
	if res := p.AdjustDeviceBrightness(0, 10); res.Error == nil {
		t.Error("expected error adjusting brightness on a plain light")
	}
}

func TestStateProviderBuildsDimmableLightState(t *testing.T) {
	sw, dimBri := newDimmableTestSwKit(t)
	_ = dimBri
	p := NewStateProvider(sw)

	state := p.GetState()
	var found *app.DeviceState
	for i := range state.Devices {
		if state.Devices[i].Type == app.DeviceTypeDimmableLight {
			found = &state.Devices[i]
			break
		}
	}
	if found == nil {
		t.Fatal("no dimmable_light device in state")
	}
	if found.AnalogIoId == "" {
		t.Error("dimmable light state missing AnalogIoId")
	}
}
