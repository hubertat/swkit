package swkit

import (
	"sync"
	"time"

	"github.com/charmbracelet/log"
)

// timedController schedules automatic reverts for Controllable devices: it sets
// a device to a target state now and restores its prior state after a duration.
// It is owned by SwKit (one per instance) rather than per-device, so the
// timer/cleanup logic is not duplicated across device types and composes with
// future orchestration (e.g. Scenes).
type timedController struct {
	mu      sync.Mutex
	pending map[string]*pendingRevert
	logger  *log.Logger

	// afterFunc schedules f to run after d and returns a cancel func. Defaults
	// to time.AfterFunc; overridden in tests for deterministic firing.
	afterFunc func(d time.Duration, f func()) (cancel func())
}

type pendingRevert struct {
	cancel   func()
	revertTo bool
}

func newTimedController(logger *log.Logger) *timedController {
	return &timedController{
		pending: make(map[string]*pendingRevert),
		logger:  logger,
		afterFunc: func(d time.Duration, f func()) func() {
			t := time.AfterFunc(d, f)
			return func() { t.Stop() }
		},
	}
}

// SetValueFor sets dev to state immediately and schedules a revert after d.
// The revert target is the device's prior state (if it implements Stateful),
// otherwise the logical opposite of state. Re-triggering before expiry resets
// the timer while preserving the originally captured revert target, so a chain
// of overrides still restores the true prior state.
func (tc *timedController) SetValueFor(dev Controllable, state bool, d time.Duration) {
	name := dev.Name()

	tc.mu.Lock()
	defer tc.mu.Unlock()

	revertTo := !state
	if existing, ok := tc.pending[name]; ok {
		existing.cancel()
		revertTo = existing.revertTo // preserve the original prior state
	} else if sf, ok := dev.(Stateful); ok {
		if prior, err := sf.GetState(); err == nil {
			revertTo = prior
		} else {
			tc.logger.Debug("timed control could not read prior state, using opposite", "device", name, "err", err)
		}
	}

	dev.SetValue(state)

	cancel := tc.afterFunc(d, func() {
		tc.mu.Lock()
		delete(tc.pending, name)
		tc.mu.Unlock()
		tc.logger.Debug("timed control reverting", "device", name, "revertTo", revertTo)
		dev.SetValue(revertTo)
	})
	tc.pending[name] = &pendingRevert{cancel: cancel, revertTo: revertTo}

	tc.logger.Debug("timed control scheduled", "device", name, "state", state, "duration", d, "revertTo", revertTo)
}

// Close cancels all pending reverts without firing them.
func (tc *timedController) Close() {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	for name, p := range tc.pending {
		p.cancel()
		delete(tc.pending, name)
	}
}
