package swkit

import (
	"errors"
	"fmt"
	"hash/fnv"
	"sync/atomic"

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

// SceneStateConfig is one state of a scene: a name and the actions applied when
// the state is activated. Action grammar:
//
//	on:<device>          off:<device>          toggle:<device>
//	brightness:<pct>:<device>   (device must support Dimmable)
type SceneStateConfig struct {
	Name    string
	Actions []string
}

// Scene drives Controllable devices directly, as an alternative to HomeKit
// scenes. Activation is fire-and-forget: a state's actions are applied once and
// not re-asserted on Sync.
type Scene struct {
	name         string
	states       []sceneState
	currentState atomic.Int32 // index of the active state; 0 = off
	logger       *log.Logger
}

type sceneState struct {
	name    string
	actions []sceneAction
}

type sceneAction struct {
	dev    Controllable
	action string // on | off | toggle | brightness
	level  int    // brightness percentage (0-100); unused otherwise
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
	}

	for _, stateConf := range config.States {
		st := sceneState{name: stateConf.Name}
		for _, actStr := range stateConf.Actions {
			action, level, devName, err := parseSceneAction(actStr)
			if err != nil {
				return nil, errors.Join(err, fmt.Errorf("scene %s: invalid action %q", config.Name, actStr))
			}
			dev, ok := resolve(devName)
			if !ok {
				return nil, fmt.Errorf("scene %s: action %q references unknown device %q", config.Name, actStr, devName)
			}
			st.actions = append(st.actions, sceneAction{dev: dev, action: action, level: level})
		}
		sc.states = append(sc.states, st)
	}

	logger.Debug("scene created", "name", config.Name, "states", len(sc.states))
	return sc, nil
}

// parseSceneAction parses a scene action string into its action, brightness
// level (0 when not applicable) and target device name. The grammar is defined
// once in app.ParseSceneAction and shared with the config editor.
func parseSceneAction(s string) (action string, level int, devName string, err error) {
	return app.ParseSceneAction(s)
}

func (a sceneAction) apply() error {
	switch a.action {
	case "on":
		a.dev.SetValue(true)
	case "off":
		a.dev.SetValue(false)
	case "toggle":
		a.dev.Toggle()
	case "brightness":
		d, ok := a.dev.(Dimmable)
		if !ok {
			return fmt.Errorf("device %s does not support brightness", a.dev.Name())
		}
		d.SetBrightness(a.level)
	default:
		return fmt.Errorf("unknown action %q for device %s", a.action, a.dev.Name())
	}
	return nil
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

// Activate applies every action in the given state, aggregating failures, and
// records it as the current state even if some actions fail.
func (sc *Scene) Activate(stateIndex int) error {
	if stateIndex < 0 || stateIndex >= len(sc.states) {
		return fmt.Errorf("scene %s: invalid state index %d", sc.name, stateIndex)
	}

	state := sc.states[stateIndex]
	sc.logger.Debug("activating scene state", "scene", sc.name, "state", state.name, "index", stateIndex)

	var errs error
	for _, a := range state.actions {
		if err := a.apply(); err != nil {
			errs = errors.Join(errs, err)
		}
	}

	sc.currentState.Store(int32(stateIndex))
	return errs
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
