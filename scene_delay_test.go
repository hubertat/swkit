package swkit

import (
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
