package swkit

import (
	"testing"

	"github.com/charmbracelet/log"
	drivers "github.com/hubertat/swkit/drivers"
)

func newTestDimmableLight(t *testing.T, min, max int) (*DimmableLight, *drivers.MockAnalogOutput) {
	t.Helper()
	dOut := drivers.NewMockOutput("dl_on")
	aOut := drivers.NewMockAnalogOutput("dl_bri", min, max)
	dl := NewDimmableLight(
		DimmableLightConfig{Name: "Test Dim", DigitalOutName: "x", AnalogOutName: "y"},
		dOut, aOut, log.New(nil),
	)
	return dl, aOut
}

func TestDimmableLightUpdateBrightnessScalesToNativeRange(t *testing.T) {
	dl, aOut := newTestDimmableLight(t, 0, 32767)

	cases := []struct {
		pct      int
		wantNear int
	}{
		{0, 0},
		{50, 16383},
		{100, 32767},
	}
	for _, c := range cases {
		dl.updateBrightness(c.pct)
		got, _ := aOut.GetState()
		// allow rounding slack of a couple LSBs
		if got < c.wantNear-2 || got > c.wantNear+2 {
			t.Errorf("updateBrightness(%d): native = %d, want ~%d", c.pct, got, c.wantNear)
		}
	}
}

func TestDimmableLightUpdateBrightnessIdentityRange(t *testing.T) {
	dl, aOut := newTestDimmableLight(t, 0, 100)
	dl.updateBrightness(73)
	got, _ := aOut.GetState()
	if got != 73 {
		t.Errorf("identity range: native = %d, want 73", got)
	}
}

func TestDimmableLightSetValueDrivesDigitalOutput(t *testing.T) {
	dOut := drivers.NewMockOutput("dl_on")
	aOut := drivers.NewMockAnalogOutput("dl_bri", 0, 100)
	dl := NewDimmableLight(DimmableLightConfig{Name: "Test"}, dOut, aOut, log.New(nil))

	dl.SetValue(true)
	if on, _ := dOut.GetState(); !on {
		t.Error("SetValue(true): digital output should be on")
	}
	dl.SetValue(false)
	if on, _ := dOut.GetState(); on {
		t.Error("SetValue(false): digital output should be off")
	}
}

func TestDimmableLightSyncWithoutHomekitIsNoop(t *testing.T) {
	dl, _ := newTestDimmableLight(t, 0, 32767)
	// hk is nil until InitHk; Sync must be a safe no-op.
	if err := dl.Sync(true); err != nil {
		t.Errorf("Sync without homekit returned error: %v", err)
	}
}
