package swkit

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/charmbracelet/log"

	"github.com/hubertat/swkit/drivers/rpixel"
	"github.com/hubertat/swkit/drivers/rpixel/rpixeltest"
)

// TestSetupWiresRpixelDevices exercises the real config -> SwKit.Setup path
// for an rpixel ring used as a Light and as a DimmableLight, against the fake
// device: commands reach the device, the state provider annotates the IO
// debug points, and a config save round-trips the Rpixel block.
func TestSetupWiresRpixelDevices(t *testing.T) {
	fake, err := rpixeltest.New()
	if err != nil {
		t.Fatalf("rpixeltest.New: %v", err)
	}
	defer fake.Close()

	configJSON := fmt.Sprintf(`{
		"Name": "t",
		"Lights": [{"Name": "Ring Light", "DigitalOutName": "rpixel|d_out|ring"}],
		"DimmableLights": [{"Name": "Ring Dim", "DigitalOutName": "rpixel|d_out|ring", "AnalogOutName": "rpixel|a_out|ring"}],
		"Rpixel": {"Devices": [{"Name": "ring", "Address": %q, "Effect": "immediate"}]}
	}`, fake.Address())

	sw := &SwKit{}
	if err := json.Unmarshal([]byte(configJSON), sw); err != nil {
		t.Fatalf("unmarshal config: %v", err)
	}
	if err := sw.Setup(context.Background(), log.New(io.Discard)); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	defer sw.Close()

	if len(sw.lights) != 1 || len(sw.dimmableLights) != 1 {
		t.Fatalf("built %d lights and %d dimmable lights, want 1 each", len(sw.lights), len(sw.dimmableLights))
	}
	light, dim := sw.lights[0], sw.dimmableLights[0]

	// Setup's initial status round means the first read already has state.
	if on, err := light.GetState(); err != nil || !on {
		t.Fatalf("light.GetState = %v, %v; want on (fake boots on)", on, err)
	}

	light.SetValue(false)
	if _, _, on := fake.State(); on {
		t.Fatal("light.SetValue(false) did not reach the device")
	}
	rec, ok := fake.LastAnimation()
	if !ok || rec.Animation.Stages[0].Colour != (rpixel.Colour{}) || rec.Animation.StageDurationMs != 0 {
		t.Fatalf("last animation = %+v, want an immediate fade to zero", rec.Animation)
	}

	dim.SetBrightness(50) // 50% -> native 127
	if _, bri, _ := fake.State(); bri != 127 {
		t.Fatalf("device brightness = %d, want 127", bri)
	}
	dim.SetValue(true)
	if c, bri, on := fake.State(); !on || bri != 127 || c != (rpixel.Colour{W: 255}) {
		t.Fatalf("device after dim.SetValue(true) = %+v bri %d on %v", c, bri, on)
	}
	if pct, err := dim.GetBrightness(); err != nil || pct != 49 {
		t.Fatalf("dim.GetBrightness = %d, %v; want 49 (127/255)", pct, err)
	}

	// State provider: driver status and IO debug points annotated with the
	// devices that use them.
	state := NewStateProvider(sw).GetState()
	var foundDriver bool
	for _, ds := range state.Drivers {
		if ds.Name == "rpixel" {
			foundDriver = true
			if !ds.Ready || ds.StatusInfo != "1 devices, 1 online" || !strings.Contains(string(ds.Details), `"rings"`) {
				t.Errorf("rpixel driver state = %+v, details %s", ds, ds.Details)
			}
		}
	}
	if !foundDriver {
		t.Fatal("rpixel driver missing from state")
	}
	configuredAs := map[string]string{}
	for _, pt := range state.IoDebug {
		if pt.DriverName == "rpixel" {
			configuredAs[pt.Type] = pt.ConfiguredAs
		}
	}
	if got := configuredAs["output"]; got != "Ring Light" && got != "Ring Dim" {
		t.Errorf("rpixel d_out ConfiguredAs = %q, want one of the ring devices", got)
	}
	if got := configuredAs["analog_output"]; got != "Ring Dim" {
		t.Errorf("rpixel a_out ConfiguredAs = %q, want Ring Dim", got)
	}

	// A config save while the driver runs keeps the Rpixel block as written.
	configPath := filepath.Join(t.TempDir(), "config.json")
	provider := NewConfigProvider(sw, configPath)
	if err := provider.SaveConfig(provider.GetEditableConfig()); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read saved config: %v", err)
	}
	var written SwKit
	if err := json.Unmarshal(data, &written); err != nil {
		t.Fatalf("unmarshal saved config: %v", err)
	}
	if written.Rpixel == nil || !reflect.DeepEqual(written.Rpixel.Devices, sw.Rpixel.Devices) {
		t.Fatalf("saved Rpixel = %+v, want devices %+v", written.Rpixel, sw.Rpixel.Devices)
	}
	if strings.Contains(string(data), "PollIntervalMs") || strings.Contains(string(data), "TransitionMs") {
		t.Errorf("saved config persisted unset rpixel fields:\n%s", data)
	}
}

// TestSetupRpixelUnknownDeviceFails checks a config error in an rpixel io id
// surfaces from SwKit.Setup.
func TestSetupRpixelUnknownDeviceFails(t *testing.T) {
	sw := &SwKit{
		Name:   "t",
		Rpixel: &rpixel.Driver{Devices: []rpixel.DeviceConfig{{Name: "ring", Address: "127.0.0.1:9"}}},
		Lights: []LightConfig{{Name: "L", DigitalOutName: "rpixel|d_out|other"}},
	}
	err := sw.Setup(context.Background(), log.New(io.Discard))
	defer sw.Close()
	if err == nil || !strings.Contains(err.Error(), "unknown device") {
		t.Fatalf("Setup err = %v, want an unknown device error", err)
	}
}
