package swkit

import (
	"errors"
	"sync"
	"testing"
	"time"

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

// ---- Analog-only dimmable light (no DigitalOutName) ----

func newAnalogOnlyDimmableLight(t *testing.T, min, max, defaultSetpoint int) (*DimmableLight, *drivers.MockAnalogOutput) {
	t.Helper()
	aOut := drivers.NewMockAnalogOutput("dl_bri", min, max)
	dl := NewDimmableLight(
		DimmableLightConfig{Name: "Analog Dim", AnalogOutName: "y", DefaultSetpoint: defaultSetpoint},
		nil, aOut, log.New(nil),
	)
	return dl, aOut
}

func TestDimmableLightAnalogOnlyDefaultSetpointNotAppliedOnNew(t *testing.T) {
	// Writing a non-zero level at startup would switch an analog-only light
	// on after every restart, so the constructor must leave it alone.
	_, aOut := newAnalogOnlyDimmableLight(t, 0, 100, 75)
	if got, _ := aOut.GetState(); got != 0 {
		t.Errorf("analog-only DefaultSetpoint must not be written on startup: native = %d, want 0", got)
	}
}

func TestDimmableLightAnalogOnlyStateFollowsLevel(t *testing.T) {
	dl, aOut := newAnalogOnlyDimmableLight(t, 0, 32767, 0)

	if on, err := dl.GetState(); err != nil || on {
		t.Fatalf("GetState at level 0 = (%v, %v), want (false, nil)", on, err)
	}
	_ = aOut.Set(1)
	if on, _ := dl.GetState(); !on {
		t.Error("GetState above the analog minimum should be on")
	}
}

func TestDimmableLightAnalogOnlySetValueRestoresLastLevel(t *testing.T) {
	dl, aOut := newAnalogOnlyDimmableLight(t, 0, 100, 0)

	dl.SetBrightness(40)
	dl.SetValue(false)
	if got, _ := aOut.GetState(); got != 0 {
		t.Fatalf("SetValue(false): native = %d, want 0", got)
	}
	dl.SetValue(true)
	if got, _ := aOut.GetState(); got != 40 {
		t.Errorf("SetValue(true) after off: native = %d, want restored 40", got)
	}
}

func TestDimmableLightAnalogOnlySetValueOnKeepsCurrentLevel(t *testing.T) {
	dl, aOut := newAnalogOnlyDimmableLight(t, 0, 100, 0)

	dl.SetBrightness(30)
	dl.SetValue(true)
	if got, _ := aOut.GetState(); got != 30 {
		t.Errorf("SetValue(true) on a lit light: native = %d, want unchanged 30", got)
	}
}

func TestDimmableLightAnalogOnlyFirstOnUsesDefaultSetpoint(t *testing.T) {
	dl, aOut := newAnalogOnlyDimmableLight(t, 0, 32767, 50)
	dl.SetValue(true)
	if got, _ := aOut.GetState(); got < 16380 || got > 16387 {
		t.Errorf("first SetValue(true) with DefaultSetpoint 50: native = %d, want ~16383", got)
	}
}

func TestDimmableLightAnalogOnlyFirstOnWithoutSetpointIsFull(t *testing.T) {
	dl, aOut := newAnalogOnlyDimmableLight(t, 0, 32767, 0)
	dl.SetValue(true)
	if got, _ := aOut.GetState(); got != 32767 {
		t.Errorf("first SetValue(true) without DefaultSetpoint: native = %d, want 32767", got)
	}
}

func TestDimmableLightAnalogOnlyToggle(t *testing.T) {
	dl, aOut := newAnalogOnlyDimmableLight(t, 0, 100, 0)

	dl.SetBrightness(60)
	dl.Toggle()
	if got, _ := aOut.GetState(); got != 0 {
		t.Fatalf("Toggle from on: native = %d, want 0", got)
	}
	dl.Toggle()
	if got, _ := aOut.GetState(); got != 60 {
		t.Errorf("Toggle from off: native = %d, want restored 60", got)
	}
}

func TestDimmableLightAnalogOnlyBrightnessZeroIsOff(t *testing.T) {
	dl, aOut := newAnalogOnlyDimmableLight(t, 0, 100, 0)

	dl.SetBrightness(70)
	dl.SetBrightness(0)
	if on, _ := dl.GetState(); on {
		t.Error("SetBrightness(0) should leave an analog-only light off")
	}
	// Brightness 0 is not remembered as a level to restore.
	dl.SetValue(true)
	if got, _ := aOut.GetState(); got != 70 {
		t.Errorf("SetValue(true) after SetBrightness(0): native = %d, want restored 70", got)
	}
}

func TestDimmableLightAnalogOnlyRemembersExternalLevelOnSync(t *testing.T) {
	dl, aOut := newAnalogOnlyDimmableLight(t, 0, 100, 0)
	dl.InitHk()

	// Level changed outside swkit, then picked up by the sync loop.
	_ = aOut.Set(25)
	if err := dl.Sync(true); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if !dl.hk.Lightbulb.On.Value() {
		t.Error("Sync: HomeKit On should be true above the analog minimum")
	}
	dl.SetValue(false)
	if err := dl.Sync(true); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if dl.hk.Lightbulb.On.Value() {
		t.Error("Sync: HomeKit On should be false at the analog minimum")
	}
	dl.SetValue(true)
	if got, _ := aOut.GetState(); got != 25 {
		t.Errorf("SetValue(true): native = %d, want restored external level 25", got)
	}
}

func TestDimmableLightWithDigitalOutBrightnessIndependentOfOn(t *testing.T) {
	// The typical wiring is unchanged: with a digital output, brightness
	// does not touch on/off and on/off does not touch brightness.
	dOut := drivers.NewMockOutput("dl_on")
	aOut := drivers.NewMockAnalogOutput("dl_bri", 0, 100)
	dl := NewDimmableLight(DimmableLightConfig{Name: "Test", DigitalOutName: "x", AnalogOutName: "y"}, dOut, aOut, log.New(nil))

	dl.SetBrightness(40)
	if on, _ := dl.GetState(); on {
		t.Error("SetBrightness must not switch on a light with a digital output")
	}
	dl.SetValue(true)
	dl.SetValue(false)
	if got, _ := aOut.GetState(); got != 40 {
		t.Errorf("SetValue must not touch brightness with a digital output: native = %d, want 40", got)
	}
}

// laggingAnalogOutput models a driver that reports analog state from a cache
// refreshed only on its next poll (as Wago does): Set writes through, but
// GetState returns the old value until poll() is called.
type laggingAnalogOutput struct {
	written, reported int
	min, max          int
}

func (l *laggingAnalogOutput) GetMinMax() (int, int)  { return l.min, l.max }
func (l *laggingAnalogOutput) GetState() (int, error) { return l.reported, nil }
func (l *laggingAnalogOutput) Set(v int) error        { l.written = v; return nil }
func (l *laggingAnalogOutput) String() string         { return "test|a_out|lag" }
func (l *laggingAnalogOutput) IsHealthy() bool        { return true }
func (l *laggingAnalogOutput) poll()                  { l.reported = l.written }

func newLaggingDimmableLight(t *testing.T, level int) (*DimmableLight, *laggingAnalogOutput, *time.Time) {
	t.Helper()
	aOut := &laggingAnalogOutput{written: level, reported: level, min: 0, max: 100}
	dl := NewDimmableLight(DimmableLightConfig{Name: "Lag"}, nil, aOut, log.New(nil))
	clock := time.Unix(1000, 0)
	dl.now = func() time.Time { return clock }
	return dl, aOut, &clock
}

// Off then on before the driver's next poll: the stale readback still shows
// the light lit, which must not make switch-on a no-op.
func TestDimmableLightAnalogOnlyRapidOffOnBeforePoll(t *testing.T) {
	dl, aOut, _ := newLaggingDimmableLight(t, 40)

	dl.SetValue(false)
	dl.SetValue(true)
	if aOut.written != 40 {
		t.Errorf("off then on before a poll: written = %d, want 40 (switch-on must not be skipped)", aOut.written)
	}
}

// A newer level set before the driver's next poll must not be replaced by the
// stale readback when the light is then switched off.
func TestDimmableLightAnalogOnlyStaleReadbackKeepsNewerLevel(t *testing.T) {
	dl, aOut, _ := newLaggingDimmableLight(t, 40)

	dl.SetBrightness(70)
	dl.SetValue(false)
	aOut.poll()
	dl.SetValue(true)
	if aOut.written != 70 {
		t.Errorf("70%% then off before a poll, then on: written = %d, want 70", aOut.written)
	}
}

// Rapid toggles before a poll alternate correctly.
func TestDimmableLightAnalogOnlyRapidTogglesBeforePoll(t *testing.T) {
	dl, aOut, _ := newLaggingDimmableLight(t, 60)

	for i, want := range []int{0, 60, 0, 60} {
		dl.Toggle()
		if aOut.written != want {
			t.Fatalf("toggle %d before a poll: written = %d, want %d", i+1, aOut.written, want)
		}
	}
}

// Once the readback matches, or the pending window expires with the hardware
// disagreeing (lost write, or changed at the dimmer), the readback wins.
func TestDimmableLightPendingLevelExpires(t *testing.T) {
	dl, aOut, clock := newLaggingDimmableLight(t, 40)

	dl.SetBrightness(70)
	if got, _ := dl.readLevel(false); got != 70 {
		t.Fatalf("readLevel within pending window = %d, want written 70", got)
	}

	*clock = clock.Add(analogPendingTimeout)
	if got, _ := dl.readLevel(false); got != 40 {
		t.Fatalf("readLevel after pending window with stale hardware = %d, want readback 40", got)
	}

	dl.SetBrightness(80)
	aOut.poll()
	if got, _ := dl.readLevel(false); got != 80 {
		t.Fatalf("readLevel after the confirming poll = %d, want 80", got)
	}
	aOut.reported = 25 // changed at the dimmer after the write was confirmed
	if got, _ := dl.readLevel(false); got != 25 {
		t.Errorf("readLevel after a later external change = %d, want 25", got)
	}
}

// blockingAnalogOutput reports writes immediately, but its first Set for
// blockOn stops inside the driver call until release is closed, so a test can
// start a second command while the first is mid-write.
type blockingAnalogOutput struct {
	mu      sync.Mutex
	level   int
	blockOn int
	once    sync.Once
	entered chan struct{}
	release chan struct{}
}

func (b *blockingAnalogOutput) GetMinMax() (int, int) { return 0, 100 }
func (b *blockingAnalogOutput) GetState() (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.level, nil
}
func (b *blockingAnalogOutput) Set(v int) error {
	b.mu.Lock()
	b.level = v
	b.mu.Unlock()
	if v == b.blockOn {
		b.once.Do(func() {
			close(b.entered)
			<-b.release
		})
	}
	return nil
}
func (b *blockingAnalogOutput) String() string  { return "test|a_out|block" }
func (b *blockingAnalogOutput) IsHealthy() bool { return true }

// A brightness write and an off command overlap (the control clients send
// power and brightness from independent slots). Whatever the interleaving,
// the bookkeeping must match the hardware: after the off lands, an explicit
// on must switch the light back on rather than see a stale pending level.
func TestDimmableLightAnalogOnlyOverlappingBrightnessAndOff(t *testing.T) {
	aOut := &blockingAnalogOutput{level: 40, blockOn: 70, entered: make(chan struct{}), release: make(chan struct{})}
	dl := NewDimmableLight(DimmableLightConfig{Name: "Overlap"}, nil, aOut, log.New(nil))

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); dl.SetBrightness(70) }()
	<-aOut.entered // brightness is inside the driver write

	offDone := make(chan struct{})
	go func() { defer wg.Done(); dl.SetValue(false); close(offDone) }()
	// Give the off command the chance to run to completion while the
	// brightness write is still stalled, then let the brightness finish.
	select {
	case <-offDone:
	case <-time.After(50 * time.Millisecond):
	}
	close(aOut.release)
	wg.Wait()

	level, _ := aOut.GetState()
	if on, _ := dl.GetState(); on != (level > 0) {
		t.Fatalf("GetState = %v but hardware level is %d", on, level)
	}

	dl.SetValue(true)
	if level, _ := aOut.GetState(); level != 70 {
		t.Errorf("SetValue(true) after overlapping brightness/off: level = %d, want restored 70", level)
	}
}

// failingAnalogOutput accepts writes until fail is set, then rejects them.
type failingAnalogOutput struct {
	level int
	fail  bool
}

func (f *failingAnalogOutput) GetMinMax() (int, int)  { return 0, 100 }
func (f *failingAnalogOutput) GetState() (int, error) { return f.level, nil }
func (f *failingAnalogOutput) Set(v int) error {
	if f.fail {
		return errors.New("write rejected")
	}
	f.level = v
	return nil
}
func (f *failingAnalogOutput) String() string  { return "test|a_out|fail" }
func (f *failingAnalogOutput) IsHealthy() bool { return true }

// The pending and remembered levels are recorded before the driver write, so
// a failed write must roll them back rather than leave the light believing
// in a level the hardware never got.
func TestDimmableLightAnalogOnlyFailedWriteRollsBack(t *testing.T) {
	aOut := &failingAnalogOutput{level: 0}
	dl := NewDimmableLight(DimmableLightConfig{Name: "Fail"}, nil, aOut, log.New(nil))

	dl.SetBrightness(40)
	dl.SetValue(false)

	aOut.fail = true
	dl.SetBrightness(90)
	if on, _ := dl.GetState(); on {
		t.Fatal("GetState after a failed brightness write = on, want off (hardware is at 0)")
	}

	aOut.fail = false
	dl.SetValue(true)
	if aOut.level != 40 {
		t.Errorf("SetValue(true) after a failed write: level = %d, want 40 (the failed 90 must not be remembered)", aOut.level)
	}
}
