package swkit

import (
	"testing"
	"time"

	"github.com/charmbracelet/log"
	drivers "github.com/hubertat/swkit/drivers"
)

func TestProviderSetDeviceValueFor(t *testing.T) {
	logger := log.New(nil)
	li := newTestLight("L", false) // prior state: off

	sw := &SwKit{Name: "t"}
	sw.lights = []*Light{li}
	sw.ioDrivers = map[string]drivers.IoDriver{}
	sw.timed = newTimedController(logger)

	// Make the revert fire on demand.
	var pending func()
	sw.timed.afterFunc = func(_ time.Duration, f func()) func() {
		pending = f
		return func() { pending = nil }
	}

	p := NewStateProvider(sw)

	res := p.SetDeviceValueFor(0, true, 30)
	if res.Error != nil {
		t.Fatalf("SetDeviceValueFor: %v", res.Error)
	}
	if !res.NewState {
		t.Error("expected NewState true")
	}
	if on, _ := li.GetState(); !on {
		t.Fatal("light should be on immediately")
	}

	if pending != nil {
		pending() // fire the revert
	}
	if on, _ := li.GetState(); on {
		t.Error("light should revert to prior state (off)")
	}
}

func TestProviderSetDeviceValueForRejectsNonPositiveDuration(t *testing.T) {
	li := newTestLight("L", false)
	sw := &SwKit{Name: "t"}
	sw.lights = []*Light{li}
	sw.ioDrivers = map[string]drivers.IoDriver{}
	sw.timed = newTimedController(log.New(nil))

	p := NewStateProvider(sw)
	if res := p.SetDeviceValueFor(0, true, 0); res.Error == nil {
		t.Error("expected error for zero duration")
	}
	if on, _ := li.GetState(); on {
		t.Error("light should not have been switched on for an invalid duration")
	}
}
