package swkit

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/log"
)

// fakeSceneTimers replaces a scene's afterFunc with manually fired timers.
type fakeSceneTimers struct {
	timers []*fakeSceneTimer
}

type fakeSceneTimer struct {
	d         time.Duration
	f         func()
	cancelled bool
}

func (ft *fakeSceneTimers) install(sc *Scene) {
	sc.afterFunc = func(d time.Duration, f func()) func() {
		t := &fakeSceneTimer{d: d, f: f}
		ft.timers = append(ft.timers, t)
		return func() { t.cancelled = true }
	}
}

// fireAll runs every timer, including cancelled ones: a real timer can fire
// just before it is stopped, and the scene must still ignore it.
func (ft *fakeSceneTimers) fireAll() {
	for _, t := range ft.timers {
		t.f()
	}
}

func newDelayScene(t *testing.T, devs ...Controllable) (*Scene, *fakeSceneTimers) {
	t.Helper()
	sc, err := NewScene(SceneConfig{
		Name: "Evening",
		States: []SceneStateConfig{
			{Name: "off", Actions: []string{"off:A"}},
			{Name: "evening", Actions: []string{"on:A", "after:10m:off:A", "after:5s:on:B"}},
		},
	}, resolverFrom(devs...), log.New(nil))
	if err != nil {
		t.Fatalf("NewScene: %v", err)
	}
	ft := &fakeSceneTimers{}
	ft.install(sc)
	return sc, ft
}

func TestSceneDelayedStepRunsLater(t *testing.T) {
	a := newTestLight("A", false)
	b := newTestLight("B", false)
	sc, ft := newDelayScene(t, a, b)

	if err := sc.Activate(1); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	if on, _ := a.GetState(); !on {
		t.Error("immediate step: A should be on right after activation")
	}
	if on, _ := b.GetState(); on {
		t.Error("delayed step: B must not switch on before its delay")
	}
	if len(ft.timers) != 2 {
		t.Fatalf("scheduled %d timers, want 2", len(ft.timers))
	}
	if ft.timers[0].d != 10*time.Minute || ft.timers[1].d != 5*time.Second {
		t.Errorf("delays = %v, %v; want 10m, 5s", ft.timers[0].d, ft.timers[1].d)
	}

	ft.fireAll()
	if on, _ := a.GetState(); on {
		t.Error("after:10m:off:A did not switch A off when it fired")
	}
	if on, _ := b.GetState(); !on {
		t.Error("after:5s:on:B did not switch B on when it fired")
	}
}

func TestSceneReactivationCancelsPendingSteps(t *testing.T) {
	a := newTestLight("A", false)
	b := newTestLight("B", false)
	sc, ft := newDelayScene(t, a, b)

	sc.Activate(1)
	sc.Activate(0) // "off" must stop the running evening sequence

	for i, tm := range ft.timers {
		if !tm.cancelled {
			t.Errorf("timer %d not cancelled by re-activation", i)
		}
	}
	ft.fireAll() // even if they fire anyway, they must be ignored
	if on, _ := b.GetState(); on {
		t.Error("a delayed step from a superseded activation still ran")
	}
}

func TestSceneCloseCancelsPendingSteps(t *testing.T) {
	a := newTestLight("A", false)
	b := newTestLight("B", false)
	sc, ft := newDelayScene(t, a, b)

	sc.Activate(1)
	sc.Close()
	ft.fireAll()
	if on, _ := b.GetState(); on {
		t.Error("a delayed step ran after Close")
	}

	// A closed scene still applies immediate steps but schedules nothing.
	before := len(ft.timers)
	sc.Activate(1)
	if len(ft.timers) != before {
		t.Error("a closed scene scheduled new delayed steps")
	}
}

// recordingDevice is a Controllable+Dimmable that logs every call in order
// and can be made to block inside one call, to pin down interleavings.
type recordingDevice struct {
	name string
	mu   sync.Mutex
	log  []string
	on   bool
	bri  int
	// blockOn, if set, makes the next call whose log entry equals it wait
	// until release is closed; entered is closed once that call has begun.
	blockOn string
	entered chan struct{}
	release chan struct{}
}

func newRecordingDevice(name string) *recordingDevice {
	return &recordingDevice{name: name, entered: make(chan struct{}), release: make(chan struct{})}
}

func (d *recordingDevice) record(entry string, apply func()) {
	d.mu.Lock()
	block := d.blockOn != "" && d.blockOn == entry
	if block {
		d.blockOn = ""
	}
	d.mu.Unlock()
	if block {
		close(d.entered)
		<-d.release
	}
	d.mu.Lock()
	d.log = append(d.log, entry)
	apply()
	d.mu.Unlock()
}

func (d *recordingDevice) Name() string { return d.name }
func (d *recordingDevice) SetValue(v bool) {
	d.record(map[bool]string{true: "on", false: "off"}[v], func() { d.on = v })
}
func (d *recordingDevice) Toggle() { d.record("toggle", func() { d.on = !d.on }) }
func (d *recordingDevice) SetBrightness(p int) {
	d.record(fmt.Sprintf("brightness:%d", p), func() { d.bri = p })
}
func (d *recordingDevice) GetBrightness() (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.bri, nil
}
func (d *recordingDevice) state() (bool, []string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.on, append([]string(nil), d.log...)
}

// A delayed step already running when the scene is switched off must not be
// able to land after the "off": Activate waits for it, then applies its own
// steps on top.
func TestSceneOffWinsOverInFlightDelayedStep(t *testing.T) {
	a := newRecordingDevice("A")
	sc, err := NewScene(SceneConfig{
		Name: "Night",
		States: []SceneStateConfig{
			{Name: "off", Actions: []string{"off:A"}},
			{Name: "on", Actions: []string{"after:1m:on:A"}},
		},
	}, resolverFrom(a), log.New(nil))
	if err != nil {
		t.Fatal(err)
	}
	ft := &fakeSceneTimers{}
	ft.install(sc)

	sc.Activate(1)
	a.blockOn = "on"

	// The timer fires and its step starts (and stalls) inside the driver.
	fired := make(chan struct{})
	go func() { ft.timers[0].f(); close(fired) }()
	<-a.entered

	// The user switches the scene off meanwhile.
	offDone := make(chan struct{})
	go func() { sc.Activate(0); close(offDone) }()
	select {
	case <-offDone:
		// Old behaviour: Activate(0) applied "off" while the stale "on" was
		// still in flight, and the "on" then landed last.
	case <-time.After(50 * time.Millisecond):
	}
	close(a.release)
	<-fired
	<-offDone

	on, calls := a.state()
	if on {
		t.Fatalf("A ended up on after switching the scene off; call order %v", calls)
	}
}

// Delayed steps sharing a delay run in config order, as one batch, so a
// brightness set before "on" really lands first.
func TestSceneSameDelayStepsRunInOrder(t *testing.T) {
	a := newRecordingDevice("A")
	sc, err := NewScene(SceneConfig{
		Name: "Evening",
		States: []SceneStateConfig{
			{Name: "off", Actions: []string{"off:A"}},
			{Name: "on", Actions: []string{"after:5s:brightness:60:A", "after:5s:on:A", "after:10s:off:A"}},
		},
	}, resolverFrom(a), log.New(nil))
	if err != nil {
		t.Fatal(err)
	}
	ft := &fakeSceneTimers{}
	ft.install(sc)

	sc.Activate(1)
	if len(ft.timers) != 2 {
		t.Fatalf("scheduled %d timers, want one per distinct delay (2)", len(ft.timers))
	}
	if ft.timers[0].d != 5*time.Second || ft.timers[1].d != 10*time.Second {
		t.Fatalf("timer delays = %v, %v; want 5s, 10s", ft.timers[0].d, ft.timers[1].d)
	}
	ft.timers[0].f()
	_, calls := a.state()
	want := []string{"brightness:60", "on"}
	if fmt.Sprint(calls) != fmt.Sprint(want) {
		t.Errorf("5s batch ran %v, want %v", calls, want)
	}
}
