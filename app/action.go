package app

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Action is a single operation applied to a target device: a verb, an optional
// numeric level, and the target device name. It is the shared unit used by both
// scenes and button control mappings.
//
// Grammar (see ParseAction / String):
//
//	on|off|toggle:<device>
//	brightness:<pct>:<device>            (absolute, 0-100)
//	brightness_up|brightness_down:<step>:<device>   (relative)
type Action struct {
	Verb   string
	Level  int // brightness percentage: target for "brightness", step for up/down
	Device string
}

// IsBrightnessVerb reports whether verb carries a numeric level and requires a
// Dimmable target.
func IsBrightnessVerb(verb string) bool {
	switch verb {
	case "brightness", "brightness_up", "brightness_down":
		return true
	default:
		return false
	}
}

// AllActionVerbs returns the available action verbs, in cycle order.
func AllActionVerbs() []string {
	return []string{"on", "off", "toggle", "brightness", "brightness_up", "brightness_down"}
}

// ParseAction parses an action string into an Action. Brightness-family verbs
// take the form <verb>:<level>:<device>; all other verbs take <verb>:<device>.
func ParseAction(s string) (Action, error) {
	if len(s) == 0 {
		return Action{}, errors.New("empty action")
	}

	parts := strings.Split(s, ":")
	act := Action{Verb: strings.ToLower(parts[0])}

	switch {
	case act.Verb == "on" || act.Verb == "off" || act.Verb == "toggle":
		if len(parts) != 2 {
			return Action{}, fmt.Errorf("action %q expects format <action>:<device>", act.Verb)
		}
		act.Device = parts[1]
	case IsBrightnessVerb(act.Verb):
		if len(parts) != 3 {
			return Action{}, fmt.Errorf("%s expects format %s:<level>:<device>", act.Verb, act.Verb)
		}
		level, err := strconv.Atoi(parts[1])
		if err != nil {
			return Action{}, fmt.Errorf("invalid level %q: %w", parts[1], err)
		}
		act.Level = level
		act.Device = parts[2]
	default:
		return Action{}, fmt.Errorf("unknown action %q", act.Verb)
	}

	return act, nil
}

// String renders the Action back to its config string form (inverse of
// ParseAction).
func (a Action) String() string {
	if IsBrightnessVerb(a.Verb) {
		return fmt.Sprintf("%s:%d:%s", a.Verb, a.Level, a.Device)
	}
	return a.Verb + ":" + a.Device
}
