package swkit

import (
	"errors"
	"fmt"
	"hash/fnv"
	"sync"
	"time"

	"github.com/charmbracelet/log"
	"github.com/hubertat/swkit/app"
)

// SceneConfig configures a multi-state scene. A scene has N states; index 0 is
// "off" by convention. Toggle() advances to the next state and wraps around.
type SceneConfig struct {
	Name string
	// Devices is the scene's device list. nil (absent or null in JSON) means a
	// legacy scene: steps may target any valid device. A non-nil list makes
	// it a managed scene, where every step must target a listed device and
	// the list itself must be non-empty (see app.ValidateSceneConfig). The
	// tag keeps an explicit [] (which validation rejects) and drops nil.
	Devices []string `json:",omitzero"`
	// ToggleTimeoutSeconds, when > 0, makes Toggle return to state 0 instead
	// of advancing once the scene has been in a non-zero state at least this
	// long without another activation. 0 disables it.
	ToggleTimeoutSeconds int `json:",omitzero"`
	States               []SceneStateConfig
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
// Every activation (Activate, Toggle, SetValue, and so buttons, nested scenes
// and the APIs) first selects its target state under sc.mu: it validates or
// chooses the index, publishes it as the current state, records changedAt,
// cancels the previous activation's delayed steps, schedules its own, and
// takes a FIFO ticket. Toggle chooses its next state inside that same
// critical section, so concurrent toggles always pick distinct consecutive
// states. The current state is therefore published at selection time, before
// its immediate steps have run.
//
// Immediate steps are then applied in ticket (selection) order, one
// activation at a time, under applyMu: each activation waits on applyCond
// until its ticket is served. So devices always end up in the state matching
// the published index, however the callers' goroutines are scheduled.
//
// Toggle timeout: with ToggleTimeoutSeconds > 0, a Toggle made while the scene
// is in a non-zero state that was selected at least that long ago goes to
// state 0 instead of advancing. It is evaluated lazily at toggle time (no
// running timer) against the injectable clock sc.now.
//
// Delayed steps are scheduled when their state is activated, one timer per
// distinct delay; steps sharing a delay run in config order. Activating any
// state (including the same one again) cancels the delayed steps still
// pending from the previous activation, so "Off" reliably stops a running
// "Evening" sequence. Close cancels everything (config reload, shutdown).
//
// All step application, immediate and delayed, is serialized by applyMu. A
// delayed batch first waits until its own activation's immediate steps have
// been applied, then re-checks, before each step, that its activation is still
// current. So a delayed step can never land after a later activation's own
// steps: either it sees itself superseded and is skipped, or it was already
// running and the later activation waits for it, then applies on top.
//
// sc.mu is never held across a driver call; applyMu is (on purpose). Lock
// order is applyMu -> sc.mu, never the reverse. Close only takes sc.mu, so it
// never waits for a driver; activations queued behind a stuck driver call stay
// queued until it returns (see "Accepted Open Issues" #2 in CLAUDE.md).
type Scene struct {
	name          string
	states        []sceneState
	toggleTimeout time.Duration // 0 = disabled
	logger        *log.Logger

	// afterFunc schedules f to run after d and returns a cancel func.
	// Injectable for tests; defaults to time.AfterFunc.
	afterFunc func(d time.Duration, f func()) (cancel func())
	// now is the clock used for the toggle timeout. Injectable for tests;
	// defaults to time.Now.
	now func() time.Time

	mu         sync.Mutex
	current    int       // index of the selected (published) state; 0 = off
	changedAt  time.Time // when current was last selected; zero before any activation
	gen        uint64    // bumped by every activation and by Close
	pending    []func()  // cancel funcs for the current activation's delayed steps
	closed     bool
	nextTicket uint64 // next FIFO ticket handed out at selection time

	// applyMu serializes applying steps (see the type comment). It is held
	// across driver calls on purpose; sc.mu never is, so selecting, cancelling
	// and scheduling stay instant even while a step is stuck in a driver.
	applyMu sync.Mutex
	// applyCond (on applyMu) is broadcast whenever serving advances.
	applyCond *sync.Cond
	serving   uint64 // ticket whose immediate steps may be applied now
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
// unknown device, if ToggleTimeoutSeconds is out of range, or if a managed
// scene (non-nil Devices) breaks the device-list rules of
// app.ValidateSceneConfig.
func NewScene(config SceneConfig, resolve func(name string) (Controllable, bool), logger *log.Logger) (*Scene, error) {
	sc := &Scene{
		name:          config.Name,
		toggleTimeout: time.Duration(config.ToggleTimeoutSeconds) * time.Second,
		logger:        logger,
		afterFunc: func(d time.Duration, f func()) func() {
			t := time.AfterFunc(d, f)
			return func() { t.Stop() }
		},
		now: time.Now,
	}
	sc.applyCond = sync.NewCond(&sc.applyMu)

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

	if err := sceneConfigError(config, resolve); err != nil {
		return nil, err
	}

	logger.Debug("scene created", "name", config.Name, "states", len(sc.states), "managed", config.Devices != nil)
	return sc, nil
}

// sceneConfigError applies the shared scene-level rules (app.ValidateSceneConfig:
// toggle timeout range and, for a managed scene, its device list) and joins
// any violations into one error. A device-list entry must resolve like a step
// target, and may not name the scene itself.
func sceneConfigError(config SceneConfig, resolve func(name string) (Controllable, bool)) error {
	edit := app.SceneEditConfig{
		Name:                 config.Name,
		Devices:              config.Devices,
		ToggleTimeoutSeconds: config.ToggleTimeoutSeconds,
	}
	for _, st := range config.States {
		edit.States = append(edit.States, app.SceneStateEditConfig{Name: st.Name, Actions: st.Actions})
	}
	checkTarget := func(name string) error {
		if name == config.Name {
			return fmt.Errorf("scene %q cannot target itself", name)
		}
		if _, ok := resolve(name); !ok {
			return fmt.Errorf("unknown device %q (a scene may use devices and earlier scenes only)", name)
		}
		return nil
	}
	var errs error
	for _, err := range app.ValidateSceneConfig(edit, checkTarget) {
		errs = errors.Join(errs, fmt.Errorf("scene %s: %w", config.Name, err))
	}
	return errs
}

func (a sceneAction) apply() error {
	return applyVerb(a.dev, a.verb, a.level)
}

// Name returns the scene name.
func (sc *Scene) Name() string {
	return sc.name
}

// CurrentState returns the index of the currently selected state (0 = off).
// It is published when an activation selects it, before that activation's
// immediate steps have been applied.
func (sc *Scene) CurrentState() int {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	return sc.current
}

// ToggleInfo returns, as one consistent snapshot, the current state and the
// state a Toggle would select right now (honouring the toggle timeout). For a
// scene without states both are 0.
func (sc *Scene) ToggleInfo() (current, next int) {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	next = sc.nextToggleLocked(sc.now())
	if next < 0 {
		next = 0
	}
	return sc.current, next
}

// nextToggleLocked returns the state a Toggle at time now selects: state 0
// when the toggle timeout has expired in a non-zero state, otherwise the next
// state, wrapping around. It returns -1 for a scene without states. Caller
// holds sc.mu.
func (sc *Scene) nextToggleLocked(now time.Time) int {
	n := len(sc.states)
	if n == 0 {
		return -1
	}
	if sc.toggleTimeout > 0 && sc.current != 0 && now.Sub(sc.changedAt) >= sc.toggleTimeout {
		return 0
	}
	return (sc.current + 1) % n
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
	_, err := sc.activate(func(int, time.Time) (int, error) {
		if stateIndex < 0 || stateIndex >= len(sc.states) {
			return 0, fmt.Errorf("scene %s: invalid state index %d", sc.name, stateIndex)
		}
		return stateIndex, nil
	})
	return err
}

// ToggleState selects the next state (see nextToggleLocked: state 0 once the
// toggle timeout has expired, otherwise the next state, wrapping), activates
// it and returns its index. The choice and the selection are one critical
// section, so concurrent callers each get a distinct state. The index is
// returned even when some immediate steps fail (the state is still selected);
// the error aggregates those failures.
func (sc *Scene) ToggleState() (int, error) {
	return sc.activate(func(_ int, now time.Time) (int, error) {
		next := sc.nextToggleLocked(now)
		if next < 0 {
			return 0, fmt.Errorf("scene %s has no states", sc.name)
		}
		return next, nil
	})
}

// activate is the single activation path. pick chooses the state while sc.mu
// is held (it receives the current state and the selection time); see the
// Scene type comment for the selection / FIFO apply protocol. It returns the
// selected index, or an error without side effects if pick fails.
func (sc *Scene) activate(pick func(current int, now time.Time) (int, error)) (int, error) {
	// Select, publish, cancel and schedule under sc.mu, which is never held
	// across a driver call. Bumping the generation here, before waiting for
	// applyMu below, is what makes a delayed step that has not started yet
	// skip itself.
	sc.mu.Lock()
	now := sc.now()
	stateIndex, err := pick(sc.current, now)
	if err != nil {
		sc.mu.Unlock()
		return 0, err
	}
	state := sc.states[stateIndex]
	sc.current = stateIndex
	sc.changedAt = now
	sc.cancelPendingLocked()
	gen := sc.gen
	ticket := sc.nextTicket
	sc.nextTicket++
	if !sc.closed {
		for _, batch := range delayedBatches(state.actions) {
			batch := batch
			sc.pending = append(sc.pending, sc.afterFunc(batch[0].delay, func() { sc.runDelayed(gen, ticket, state.name, batch) }))
		}
	}
	sc.mu.Unlock()

	sc.logger.Debug("activating scene state", "scene", sc.name, "state", state.name, "index", stateIndex)

	// Apply in selection order: wait for every earlier activation's
	// immediate steps (and any delayed step already mid-apply), so this
	// state's steps land after them, never before.
	sc.applyMu.Lock()
	defer sc.applyMu.Unlock()
	for sc.serving != ticket {
		sc.applyCond.Wait()
	}
	defer func() {
		// Deferred so a panicking device cannot wedge every later
		// activation behind an unserved ticket.
		sc.serving++
		sc.applyCond.Broadcast()
	}()

	var errs error
	for _, a := range state.actions {
		if a.delay > 0 {
			continue
		}
		if err := a.apply(); err != nil {
			errs = errors.Join(errs, err)
		}
	}
	return stateIndex, errs
}

// delayedBatches groups a state's delayed actions by delay, in order of first
// appearance, keeping config order within each batch.
func delayedBatches(actions []sceneAction) [][]sceneAction {
	var batches [][]sceneAction
	index := make(map[time.Duration]int)
	for _, a := range actions {
		if a.delay <= 0 {
			continue
		}
		i, ok := index[a.delay]
		if !ok {
			i = len(batches)
			index[a.delay] = i
			batches = append(batches, nil)
		}
		batches[i] = append(batches[i], a)
	}
	return batches
}

// runDelayed applies a batch of delayed actions, in order, holding applyMu.
// It first waits until its own activation (ticket) has applied its immediate
// steps, so a delayed step never overtakes them. Before each action it
// re-checks that its activation is still current, so a later activation or
// Close stops the rest of the batch; and because every activation takes
// applyMu before applying, nothing from this batch can land after the later
// activation's own steps.
func (sc *Scene) runDelayed(gen, ticket uint64, stateName string, batch []sceneAction) {
	sc.applyMu.Lock()
	defer sc.applyMu.Unlock()
	for sc.serving <= ticket {
		sc.applyCond.Wait()
	}
	for _, a := range batch {
		if !sc.isCurrent(gen) {
			return
		}
		sc.logger.Debug("applying delayed scene step", "scene", sc.name, "state", stateName, "device", a.dev.Name(), "verb", a.verb, "delay", a.delay)
		if err := a.apply(); err != nil {
			sc.logger.Error("delayed scene step failed", "scene", sc.name, "state", stateName, "err", err)
		}
	}
}

// isCurrent reports whether gen is still the scene's active generation.
func (sc *Scene) isCurrent(gen uint64) bool {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	return !sc.closed && sc.gen == gen
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
// their immediate actions but schedule nothing. It only takes sc.mu, so it
// never waits for a driver call; a delayed batch already waiting for its turn
// wakes when its activation finishes and then skips itself as closed.
func (sc *Scene) Close() {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	sc.closed = true
	sc.cancelPendingLocked()
}

// Toggle advances to the next state, wrapping from the last back to 0, or
// returns to state 0 when the toggle timeout has expired (see ToggleState).
func (sc *Scene) Toggle() {
	if len(sc.states) == 0 {
		return
	}
	if _, err := sc.ToggleState(); err != nil {
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
