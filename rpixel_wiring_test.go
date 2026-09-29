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
	"time"

	"github.com/charmbracelet/log"

	"github.com/hubertat/swkit/drivers/rpixel"
	"github.com/hubertat/swkit/drivers/rpixel/rpixeltest"
)

func newFakeRing(t *testing.T) *rpixeltest.Device {
	t.Helper()
	fake, err := rpixeltest.New()
	if err != nil {
		t.Fatalf("rpixeltest.New: %v", err)
	}
	t.Cleanup(func() { fake.Close() })
	return fake
}

// TestSetupWiresRpixelDevices exercises the real config -> SwKit.Setup path
// for two rings, one used as a Light and one as a DimmableLight (one device
// per ring: the web config editor rejects two devices sharing an io), against
// fake devices: commands reach the right ring, the state provider annotates
// the IO debug points, and a config save round-trips the Rpixel block.
func TestSetupWiresRpixelDevices(t *testing.T) {
	fakeA, fakeB := newFakeRing(t), newFakeRing(t)

	configJSON := fmt.Sprintf(`{
		"Name": "t",
		"Lights": [{"Name": "Ring Light", "DigitalOutName": "rpixel|d_out|ringA"}],
		"DimmableLights": [{"Name": "Ring Dim", "DigitalOutName": "rpixel|d_out|ringB", "AnalogOutName": "rpixel|a_out|ringB"}],
		"Rpixel": {"Devices": [
			{"Name": "ringA", "Address": %q, "Effect": "immediate"},
			{"Name": "ringB", "Address": %q, "Effect": "immediate"}
		]}
	}`, fakeA.Address(), fakeB.Address())

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
	if _, _, on := fakeA.State(); on {
		t.Fatal("light.SetValue(false) did not reach ring A")
	}
	if _, _, on := fakeB.State(); !on || len(fakeB.Animations()) != 0 {
		t.Fatal("light.SetValue(false) reached ring B")
	}
	rec, ok := fakeA.LastAnimation()
	if !ok || rec.Animation.Stages[0].Colour != (rpixel.Colour{}) || rec.Animation.StageDurationMs != 0 {
		t.Fatalf("last animation = %+v, want an immediate fade to zero", rec.Animation)
	}

	dim.SetBrightness(50) // 50% -> native 127
	if _, bri, _ := fakeB.State(); bri != 127 {
		t.Fatalf("ring B brightness = %d, want 127", bri)
	}
	dim.SetValue(true)
	if c, bri, on := fakeB.State(); !on || bri != 127 || c != (rpixel.Colour{W: 255}) {
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
			if !ds.Ready || ds.StatusInfo != "2 devices, 2 online" || !strings.Contains(string(ds.Details), `"rings"`) {
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
			configuredAs[pt.Type+"/"+pt.Name] = pt.ConfiguredAs
		}
	}
	wantConfiguredAs := map[string]string{
		"output/ringA":        "Ring Light",
		"analog_output/ringA": "",
		"output/ringB":        "Ring Dim",
		"analog_output/ringB": "Ring Dim",
	}
	if !reflect.DeepEqual(configuredAs, wantConfiguredAs) {
		t.Errorf("rpixel ConfiguredAs = %v, want %v", configuredAs, wantConfiguredAs)
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

// TestSetupFailureClosesRpixel: when SwKit.Setup fails after the rpixel
// driver is already set up (here: a light with the wrong io type), the driver
// is closed again rather than left polling, as a failed hot reload would.
func TestSetupFailureClosesRpixel(t *testing.T) {
	fake := newFakeRing(t)
	sw := &SwKit{
		Name:   "t",
		Rpixel: &rpixel.Driver{Devices: []rpixel.DeviceConfig{{Name: "ring", Address: fake.Address()}}, PollIntervalMs: 20},
		// a_out is a valid rpixel io, so the driver sets up; the Light then
		// rejects it for not being a d_out.
		Lights: []LightConfig{{Name: "L", DigitalOutName: "rpixel|a_out|ring"}},
	}
	err := sw.Setup(context.Background(), log.New(io.Discard))
	if err == nil {
		t.Fatal("Setup succeeded")
	}
	if fake.Received(rpixel.TypeStatusRequest) == 0 {
		t.Fatal("driver never polled; the test did not exercise a set-up driver")
	}
	if sw.Rpixel.IsReady() {
		t.Fatal("rpixel driver still ready after a failed Setup")
	}
	polls := fake.Received(rpixel.TypeStatusRequest)
	time.Sleep(150 * time.Millisecond) // several poll intervals
	if got := fake.Received(rpixel.TypeStatusRequest); got != polls {
		t.Fatalf("driver kept polling after a failed Setup: %d more G", got-polls)
	}
	if err := sw.Close(); err != nil {
		t.Fatalf("Close after a failed Setup: %v", err)
	}
}

// TestRpixelReload mirrors a hot reload: Setup, Close, then a fresh SwKit
// from the same config set up against the same ring works.
func TestRpixelReload(t *testing.T) {
	fake := newFakeRing(t)
	configJSON := fmt.Sprintf(`{
		"Name": "t",
		"Lights": [{"Name": "L", "DigitalOutName": "rpixel|d_out|ring"}],
		"Rpixel": {"Devices": [{"Name": "ring", "Address": %q, "Effect": "immediate"}]}
	}`, fake.Address())

	for round, want := range []bool{false, true} {
		sw := &SwKit{}
		if err := json.Unmarshal([]byte(configJSON), sw); err != nil {
			t.Fatalf("round %d: unmarshal: %v", round, err)
		}
		if err := sw.Setup(context.Background(), log.New(io.Discard)); err != nil {
			t.Fatalf("round %d: Setup: %v", round, err)
		}
		if on, err := sw.lights[0].GetState(); err != nil || on == want {
			t.Fatalf("round %d: initial GetState = %v, %v", round, on, err)
		}
		sw.lights[0].SetValue(want)
		if _, _, on := fake.State(); on != want {
			t.Fatalf("round %d: ring on = %v, want %v", round, on, want)
		}
		if err := sw.Close(); err != nil {
			t.Fatalf("round %d: Close: %v", round, err)
		}
	}
}
