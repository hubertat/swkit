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

func TestDimmableLightDefaultSetpointAppliedOnNew(t *testing.T) {
	dOut := drivers.NewMockOutput("dl_on")
	aOut := drivers.NewMockAnalogOutput("dl_bri", 0, 100)
	NewDimmableLight(DimmableLightConfig{
		Name: "Test", DigitalOutName: "x", AnalogOutName: "y", DefaultSetpoint: 75,
	}, dOut, aOut, log.New(nil))
	got, _ := aOut.GetState()
	if got != 75 {
		t.Errorf("DefaultSetpoint 75 with identity range: native = %d, want 75", got)
	}
}

func TestDimmableLightDefaultSetpointScalesNativeRange(t *testing.T) {
	dOut := drivers.NewMockOutput("dl_on")
	aOut := drivers.NewMockAnalogOutput("dl_bri", 0, 32767)
	NewDimmableLight(DimmableLightConfig{
		Name: "Test", DigitalOutName: "x", AnalogOutName: "y", DefaultSetpoint: 100,
	}, dOut, aOut, log.New(nil))
	got, _ := aOut.GetState()
	if got != 32767 {
		t.Errorf("DefaultSetpoint 100 with range 0-32767: native = %d, want 32767", got)
	}
}

func TestDimmableLightDefaultSetpointZeroSkipsInit(t *testing.T) {
	dOut := drivers.NewMockOutput("dl_on")
	aOut := drivers.NewMockAnalogOutput("dl_bri", 0, 32767)
	NewDimmableLight(DimmableLightConfig{
		Name: "Test", DigitalOutName: "x", AnalogOutName: "y", DefaultSetpoint: 0,
	}, dOut, aOut, log.New(nil))
	got, _ := aOut.GetState()
	if got != 0 {
		t.Errorf("DefaultSetpoint 0 should not init: native = %d, want 0", got)
	}
}

func TestDimmableLightSetBrightnessClamps(t *testing.T) {
	dl, aOut := newTestDimmableLight(t, 0, 100)

	dl.SetBrightness(150)
	if got, _ := aOut.GetState(); got != 100 {
		t.Errorf("SetBrightness(150) should clamp to 100, got %d", got)
	}

	dl.SetBrightness(-20)
	if got, _ := aOut.GetState(); got != 0 {
		t.Errorf("SetBrightness(-20) should clamp to 0, got %d", got)
	}
}

func TestDimmableLightGetBrightnessScalesFromNative(t *testing.T) {
	dl, aOut := newTestDimmableLight(t, 0, 32767)

	// Round-trip: SetBrightness scales to native, GetBrightness scales back.
	for _, pct := range []int{0, 25, 50, 100} {
		dl.SetBrightness(pct)
		got, err := dl.GetBrightness()
		if err != nil {
			t.Fatalf("GetBrightness: %v", err)
		}
		if got < pct-1 || got > pct+1 { // allow rounding slack
			t.Errorf("SetBrightness(%d) then GetBrightness = %d, want ~%d", pct, got, pct)
		}
	}
	_ = aOut
}

func TestDimmableImplementsDimmableCapability(t *testing.T) {
	dl, _ := newTestDimmableLight(t, 0, 100)
	if _, ok := Controllable(dl).(Dimmable); !ok {
		t.Error("DimmableLight should satisfy Dimmable")
	}

	li := NewLight(LightConfig{Name: "L"}, drivers.NewMockOutput("l"), log.New(nil))
	if _, ok := Controllable(li).(Dimmable); ok {
		t.Error("Light should not satisfy Dimmable")
	}

	ou := NewOutlet(OutletConfig{Name: "O"}, drivers.NewMockOutput("o"), log.New(nil))
	if _, ok := Controllable(ou).(Dimmable); ok {
		t.Error("Outlet should not satisfy Dimmable")
	}
}

func TestDimmableLightSyncWithoutHomekitIsNoop(t *testing.T) {
	dl, _ := newTestDimmableLight(t, 0, 32767)
	// hk is nil until InitHk; Sync must be a safe no-op.
	if err := dl.Sync(true); err != nil {
		t.Errorf("Sync without homekit returned error: %v", err)
	}
}
