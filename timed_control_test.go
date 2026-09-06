package swkit

import (
	"testing"
	"time"

	"github.com/charmbracelet/log"
	drivers "github.com/hubertat/swkit/drivers"
)

// newTestTimedController returns a controller whose timer firing is manual:
// the returned fire func runs the most recently scheduled callback.
func newTestTimedController(t *testing.T) (*timedController, func()) {
	t.Helper()
	tc := newTimedController(log.New(nil))

	var pending func()
	tc.afterFunc = func(_ time.Duration, f func()) func() {
		pending = f
		return func() { pending = nil } // cancel drops the pending callback
	}

	fire := func() {
		if pending != nil {
			f := pending
			pending = nil
			f()
		}
	}
	return tc, fire
}

func newTestLight(name string, initial bool) *Light {
	out := drivers.NewMockOutput(name)
	out.Set(initial)
	return NewLight(LightConfig{Name: name}, out, log.New(nil))
}

func TestTimedControlRevertsToPriorState(t *testing.T) {
	tc, fire := newTestTimedController(t)
	li := newTestLight("L", false) // prior state: off

	tc.SetValueFor(li, true, time.Minute)
	if on, _ := li.GetState(); !on {
		t.Fatal("expected light on immediately after SetValueFor(true)")
	}

	fire()
	if on, _ := li.GetState(); on {
		t.Error("expected revert to prior state (off) after timer fired")
	}
}

func TestTimedControlPreservesOriginalPriorOnRetrigger(t *testing.T) {
	tc, fire := newTestTimedController(t)
	li := newTestLight("L", false) // original prior: off

	tc.SetValueFor(li, true, time.Minute)
	// Re-trigger before expiry with a different state; original prior (off)
	// must be preserved as the revert target.
	tc.SetValueFor(li, true, time.Minute)

	fire()
	if on, _ := li.GetState(); on {
		t.Error("expected revert to original prior state (off) after retrigger")
	}
}

func TestTimedControlRetriggerCancelsFirstTimer(t *testing.T) {
	tc, fire := newTestTimedController(t)
	li := newTestLight("L", false)

	tc.SetValueFor(li, true, time.Minute) // schedules timer #1
	tc.SetValueFor(li, true, time.Minute) // cancels #1, schedules #2

	// Only one pending revert should remain.
	tc.mu.Lock()
	n := len(tc.pending)
	tc.mu.Unlock()
	if n != 1 {
		t.Errorf("expected 1 pending revert after retrigger, got %d", n)
	}

	fire() // fires #2
	if on, _ := li.GetState(); on {
		t.Error("expected revert after firing")
	}
}

func TestTimedControlCloseCancelsPending(t *testing.T) {
	tc, fire := newTestTimedController(t)
	li := newTestLight("L", false)

	tc.SetValueFor(li, true, time.Minute)
	tc.Close()

	tc.mu.Lock()
	n := len(tc.pending)
	tc.mu.Unlock()
	if n != 0 {
		t.Errorf("expected 0 pending after Close, got %d", n)
	}

	// Firing after Close must be a no-op (callback was cancelled).
	fire()
	if on, _ := li.GetState(); !on {
		t.Error("Close should not fire the revert; light should still be on")
	}
}

func TestTimedControlNonStatefulRevertsToOpposite(t *testing.T) {
	tc, fire := newTestTimedController(t)
	dev := &fakeControllable{name: "F"}

	tc.SetValueFor(dev, true, time.Minute)
	if !dev.value {
		t.Fatal("expected value true after SetValueFor(true)")
	}
	fire()
	if dev.value {
		t.Error("non-Stateful device should revert to opposite (false)")
	}
}

// fakeControllable is Controllable but not Stateful.
type fakeControllable struct {
	name  string
	value bool
}

func (f *fakeControllable) SetValue(state bool) { f.value = state }
func (f *fakeControllable) Toggle()             { f.value = !f.value }
func (f *fakeControllable) Name() string        { return f.name }
