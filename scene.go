package swkit

import (
	"errors"
	"fmt"
	"hash/fnv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/log"
	"github.com/hubertat/swkit/app"
)

// SceneConfig configures a multi-state scene. A scene has N states; index 0 is
// "off" by convention. Toggle() advances to the next state and wraps around.
type SceneConfig struct {
	Name   string
	States []SceneStateConfig
	// DisableHomekit is reserved; scenes have no HomeKit representation yet.
	DisableHomekit bool
}

// SceneStateConfig is one state of a scene: a name and the steps applied when
// the state is activated. Step grammar (see app.ParseSceneStep):
//
//	on:<device>          off:<device>          toggle:<device>
//	brightness:<pct>:<device>                  (absolute; device must be Dimmable)
//	brightness_up:<step>:<device>              (relative; device must be Dimmable)
//	brightness_down:<step>:<device>
//
// Any of these may be prefixed with "after:<duration>:" to run that long after
// the state is activated, e.g. "after:10m:off:Porch".
type SceneStateConfig struct {
	Name    string
	Actions []string
}

// Scene drives Controllable devices directly, as an alternative to HomeKit
// scenes. Activation is fire-and-forget: a state's actions are applied once and
// not re-asserted on Sync.
//
// Delayed steps are scheduled when their state is activated. Activating any
// state (including the same one again) cancels the delayed steps still
// pending from the previous activation, so "Off" reliably stops a running
// "Evening" sequence. Close cancels everything (config reload, shutdown).
type Scene struct {
	name         string
	states       []sceneState
	currentState atomic.Int32 // index of the active state; 0 = off
	logger       *log.Logger

	// afterFunc schedules f to run after d and returns a cancel func.
	// Injectable for tests; defaults to time.AfterFunc.
	afterFunc func(d time.Duration, f func()) (cancel func())

	mu      sync.Mutex
	gen     uint64   // bumped by every Activate and by Close
	pending []func() // cancel funcs for the current activation's delayed steps
	closed  bool
}

type sceneState struct {
	name    string
	actions []sceneAction
}

type sceneAction struct {
	dev   Controllable
	verb  string        // see app.AllActionVerbs()
	level int           // brightness level/step; unused by non-brightness verbs
	delay time.Duration // 0 = apply on activation
}

// Scene is itself Controllable, so buttons (and other scenes) can drive it
// through the existing control machinery.
var _ Controllable = (*Scene)(nil)

// NewScene builds a Scene from config, resolving each action's device name via
// resolve. It returns an error if an action is malformed or references an
// unknown device.
func NewScene(config SceneConfig, resolve func(name string) (Controllable, bool), logger *log.Logger) (*Scene, error) {
	sc := &Scene{
		name:   config.Name,
		logger: logger,
		afterFunc: func(d time.Duration, f func()) func() {
			t := time.AfterFunc(d, f)
			return func() { t.Stop() }
		},
	}

	for _, stateConf := range config.States {
		st := sceneState{name: stateConf.Name}
		for _, actStr := range stateConf.Actions {
			act, err := app.ParseSceneStep(actStr)
			if err != nil {
				return nil, errors.Join(err, fmt.Errorf("scene %s: invalid action %q", config.Name, actStr))
			}
			dev, ok := resolve(act.Device)
			if !ok {
				return nil, fmt.Errorf("scene %s: action %q references unknown device %q", config.Name, actStr, act.Device)
			}
			st.actions = append(st.actions, sceneAction{dev: dev, verb: act.Verb, level: act.Level, delay: act.Delay})
		}
		sc.states = append(sc.states, st)
	}

	logger.Debug("scene created", "name", config.Name, "states", len(sc.states))
	return sc, nil
}

func (a sceneAction) apply() error {
	return applyVerb(a.dev, a.verb, a.level)
}

// Name returns the scene name.
func (sc *Scene) Name() string {
	return sc.name
}

// CurrentState returns the index of the currently active state (0 = off).
func (sc *Scene) CurrentState() int {
	return int(sc.currentState.Load())
}

// StateNames returns the names of all states, in order.
func (sc *Scene) StateNames() []string {
	names := make([]string, len(sc.states))
	for i, st := range sc.states {
		names[i] = st.name
	}
	return names
}

// Activate applies every immediate action in the given state, aggregating
// failures, schedules its delayed actions, and records it as the current state
// even if some actions fail. Delayed actions still pending from an earlier
// activation are cancelled first. Errors from delayed actions happen later and
// are logged, not returned.
func (sc *Scene) Activate(stateIndex int) error {
	if stateIndex < 0 || stateIndex >= len(sc.states) {
		return fmt.Errorf("scene %s: invalid state index %d", sc.name, stateIndex)
	}

	state := sc.states[stateIndex]
	sc.logger.Debug("activating scene state", "scene", sc.name, "state", state.name, "index", stateIndex)

	// Schedule under sc.mu, but apply outside it: an action reaches into a
	// driver and can block, and holding the lock across that would stall
	// the next Activate (and Close) for as long as the driver does.
	sc.mu.Lock()
	sc.cancelPendingLocked()
	gen := sc.gen
	if !sc.closed {
		for _, a := range state.actions {
			if a.delay <= 0 {
				continue
			}
			a := a
			sc.pending = append(sc.pending, sc.afterFunc(a.delay, func() { sc.runDelayed(gen, state.name, a) }))
		}
	}
	sc.mu.Unlock()

	var errs error
	for _, a := range state.actions {
		if a.delay > 0 {
			continue
		}
		if err := a.apply(); err != nil {
			errs = errors.Join(errs, err)
		}
	}

	sc.currentState.Store(int32(stateIndex))
	return errs
}

// runDelayed applies a delayed action unless its activation has since been
// superseded or the scene closed. The check and the apply are not atomic (the
// apply must not hold sc.mu, see Activate), so an Activate landing in that
// instant can still see this one last action run; it never runs twice and
// never runs after a later activation's own actions have been scheduled.
func (sc *Scene) runDelayed(gen uint64, stateName string, a sceneAction) {
	sc.mu.Lock()
	stale := sc.closed || sc.gen != gen
	sc.mu.Unlock()
	if stale {
		return
	}
	sc.logger.Debug("applying delayed scene step", "scene", sc.name, "state", stateName, "device", a.dev.Name(), "verb", a.verb, "delay", a.delay)
	if err := a.apply(); err != nil {
		sc.logger.Error("delayed scene step failed", "scene", sc.name, "state", stateName, "err", err)
	}
}

// cancelPendingLocked cancels every delayed action still pending and starts a
// new generation, so a timer that already fired but has not yet taken sc.mu
// sees itself as stale. Caller holds sc.mu.
func (sc *Scene) cancelPendingLocked() {
	for _, cancel := range sc.pending {
		cancel()
	}
	sc.pending = nil
	sc.gen++
}

// Close cancels all pending delayed actions; later activations still apply
// their immediate actions but schedule nothing.
func (sc *Scene) Close() {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	sc.closed = true
	sc.cancelPendingLocked()
}

// Toggle advances to the next state, wrapping from the last back to 0.
func (sc *Scene) Toggle() {
	if len(sc.states) == 0 {
		return
	}
	next := (int(sc.currentState.Load()) + 1) % len(sc.states)
	if err := sc.Activate(next); err != nil {
		sc.logger.Error("scene toggle failed", "scene", sc.name, "err", err)
	}
}

// SetValue activates the off state (false) or the first non-off state (true).
func (sc *Scene) SetValue(state bool) {
	target := 0
	if state {
		if len(sc.states) < 2 {
			sc.logger.Debug("scene SetValue(true) ignored: no non-off state", "scene", sc.name)
			return
		}
		target = 1
	}
	if err := sc.Activate(target); err != nil {
		sc.logger.Error("scene SetValue failed", "scene", sc.name, "state", state, "err", err)
	}
}

// GetUniqueId is provided for parity with other devices (e.g. future HomeKit or
// state-provider use); scenes have no HomeKit accessory yet.
func (sc *Scene) GetUniqueId() uint64 {
	hash := fnv.New64()
	hash.Write([]byte("Scene_" + sc.name))
	return hash.Sum64()
}
