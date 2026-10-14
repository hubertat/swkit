package app

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrSceneNotFound is returned when a scene, or a state within it, that a
// caller asked to activate does not exist.
var ErrSceneNotFound = errors.New("scene not found")

// MaxSceneStepDelay caps how far after activation a scene step may run. It
// keeps an accidental "after:9999h" from parking a timer for a year, and is
// long enough for any realistic "turn off later" use.
const MaxSceneStepDelay = 24 * time.Hour

// sceneStepDelayPrefix introduces an optional delay in front of an Action.
const sceneStepDelayPrefix = "after"

// SceneStep is one entry in a scene state: an Action and how long after the
// state is activated it runs. Steps are scene-only; button control mappings
// use plain Actions and never carry a delay.
//
// Grammar (see ParseSceneStep / String):
//
//	<action>                    runs immediately, e.g. "on:Kitchen"
//	after:<duration>:<action>   runs <duration> later, e.g. "after:10m:off:Kitchen"
//
// <duration> uses Go duration syntax ("500ms", "5s", "1m30s", "2h") and must be
// between 0 and MaxSceneStepDelay. A zero delay is written without the prefix.
type SceneStep struct {
	Action
	Delay time.Duration
}

// ParseSceneStep parses a scene state action string, with or without a delay.
func ParseSceneStep(s string) (SceneStep, error) {
	rest := s
	var delay time.Duration
	if prefix, after, ok := strings.Cut(s, ":"); ok && strings.EqualFold(prefix, sceneStepDelayPrefix) {
		durStr, actionStr, ok := strings.Cut(after, ":")
		if !ok {
			return SceneStep{}, fmt.Errorf("delayed step %q expects format after:<duration>:<action>", s)
		}
		d, err := time.ParseDuration(durStr)
		if err != nil {
			return SceneStep{}, fmt.Errorf("invalid delay %q: %w", durStr, err)
		}
		if d < 0 {
			return SceneStep{}, fmt.Errorf("delay %q must not be negative", durStr)
		}
		if d%time.Millisecond != 0 {
			return SceneStep{}, fmt.Errorf("delay %q is finer than a millisecond", durStr)
		}
		if d > MaxSceneStepDelay {
			return SceneStep{}, fmt.Errorf("delay %q exceeds the maximum of %s", durStr, FormatStepDelay(MaxSceneStepDelay))
		}
		delay = d
		rest = actionStr
	}
	act, err := ParseAction(rest)
	if err != nil {
		return SceneStep{}, err
	}
	return SceneStep{Action: act, Delay: delay}, nil
}

// String renders the step back to its config form (inverse of ParseSceneStep).
func (s SceneStep) String() string {
	if s.Delay <= 0 {
		return s.Action.String()
	}
	return sceneStepDelayPrefix + ":" + FormatStepDelay(s.Delay) + ":" + s.Action.String()
}

// FormatStepDelay renders a delay compactly, the way a person would write it:
// "90s" becomes "1m30s", "1h0m0s" becomes "1h". The result always parses back
// with time.ParseDuration to the same value.
func FormatStepDelay(d time.Duration) string {
	if d <= 0 {
		return "0s"
	}
	var b strings.Builder
	if h := d / time.Hour; h > 0 {
		fmt.Fprintf(&b, "%dh", h)
		d -= h * time.Hour
	}
	if m := d / time.Minute; m > 0 {
		fmt.Fprintf(&b, "%dm", m)
		d -= m * time.Minute
	}
	if s := d / time.Second; s > 0 {
		fmt.Fprintf(&b, "%ds", s)
		d -= s * time.Second
	}
	if d > 0 {
		fmt.Fprintf(&b, "%dms", d/time.Millisecond)
	}
	return b.String()
}
