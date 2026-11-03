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
//	open|close|stop:<device>             (window shutters)
//	position:<pct>:<device>              (window shutters, percent open 0-100)
//
// The grammar is shared by every target type; which verbs a given target
// accepts is decided by ActionVerbsForTargetType.
type Action struct {
	Verb   string
	Level  int // brightness percentage (target or step), or shutter position percentage
	Device string
}

// Target type names used by ActionVerbsForTargetType (they match DeviceType).
const (
	TargetTypeLight         = string(DeviceTypeLight)
	TargetTypeColorLight    = string(DeviceTypeColorLight)
	TargetTypeDimmableLight = string(DeviceTypeDimmableLight)
	TargetTypeOutlet        = string(DeviceTypeOutlet)
	TargetTypeScene         = string(DeviceTypeScene)
	TargetTypeWindowShutter = string(DeviceTypeWindowShutter)
)

// IsBrightnessVerb reports whether verb is a brightness-family verb, which
// carries a numeric level and requires a Dimmable target.
func IsBrightnessVerb(verb string) bool {
	switch verb {
	case "brightness", "brightness_up", "brightness_down":
		return true
	default:
		return false
	}
}

// IsShutterVerb reports whether verb is a window-shutter-only verb.
func IsShutterVerb(verb string) bool {
	switch verb {
	case "open", "close", "stop", "position":
		return true
	default:
		return false
	}
}

// HasLevel reports whether verb carries a numeric level (<verb>:<level>:<device>):
// the brightness family and the shutter "position" verb.
func HasLevel(verb string) bool {
	return IsBrightnessVerb(verb) || verb == "position"
}

// AllActionVerbs returns the complete grammar vocabulary, in cycle order. It
// is not proof that an action is supported by a target: use
// ActionVerbsForTargetType for that.
func AllActionVerbs() []string {
	return []string{"on", "off", "toggle", "brightness", "brightness_up", "brightness_down", "open", "close", "stop", "position"}
}

// ActionVerbsForTargetType returns the verbs a target of the given type
// accepts, in cycle order. Lights, color lights, outlets and scenes take
// on/off/toggle; dimmable lights additionally take the brightness verbs;
// window shutters take only open/close/stop/position/toggle. Unknown types
// get the conservative on/off/toggle set.
func ActionVerbsForTargetType(targetType string) []string {
	switch targetType {
	case TargetTypeDimmableLight:
		return []string{"on", "off", "toggle", "brightness", "brightness_up", "brightness_down"}
	case TargetTypeWindowShutter:
		return []string{"open", "close", "stop", "position", "toggle"}
	default:
		return []string{"on", "off", "toggle"}
	}
}

// ActionVerbsByTargetType returns ActionVerbsForTargetType for every known
// target type, for publishing in editor metadata.
func ActionVerbsByTargetType() map[string][]string {
	out := make(map[string][]string)
	for _, t := range []string{TargetTypeLight, TargetTypeColorLight, TargetTypeDimmableLight, TargetTypeOutlet, TargetTypeScene, TargetTypeWindowShutter} {
		out[t] = ActionVerbsForTargetType(t)
	}
	return out
}

// VerbSupportedByTargetType reports whether verb is accepted by a target of
// the given type.
func VerbSupportedByTargetType(verb, targetType string) bool {
	for _, v := range ActionVerbsForTargetType(targetType) {
		if v == verb {
			return true
		}
	}
	return false
}

// UnsupportedVerbError describes why verb is not accepted by a target type,
// with a precise message for the common mistakes.
func UnsupportedVerbError(verb, device, targetType string) error {
	switch {
	case targetType == TargetTypeWindowShutter && (verb == "on" || verb == "off"):
		return fmt.Errorf("action %q is not supported by window shutter %q: use open, close, stop, position or toggle", verb, device)
	case IsBrightnessVerb(verb):
		return fmt.Errorf("brightness action target %q does not support brightness (target type: %s)", device, targetType)
	case IsShutterVerb(verb):
		return fmt.Errorf("action %q requires a window shutter target; %q is a %s", verb, device, targetType)
	default:
		return fmt.Errorf("action %q is not supported by %q (target type: %s)", verb, device, targetType)
	}
}

// ParseAction parses an action string into an Action. Level verbs (see
// HasLevel) take the form <verb>:<level>:<device>; all other verbs take
// <verb>:<device>.
func ParseAction(s string) (Action, error) {
	if len(s) == 0 {
		return Action{}, errors.New("empty action")
	}

	parts := strings.Split(s, ":")
	act := Action{Verb: strings.ToLower(parts[0])}

	switch {
	case act.Verb == "on" || act.Verb == "off" || act.Verb == "toggle" ||
		act.Verb == "open" || act.Verb == "close" || act.Verb == "stop":
		if len(parts) != 2 {
			return Action{}, fmt.Errorf("action %q expects format <action>:<device>", act.Verb)
		}
		act.Device = parts[1]
	case HasLevel(act.Verb):
		if len(parts) != 3 {
			return Action{}, fmt.Errorf("%s expects format %s:<level>:<device>", act.Verb, act.Verb)
		}
		level, err := strconv.Atoi(parts[1])
		if err != nil {
			return Action{}, fmt.Errorf("invalid level %q: %w", parts[1], err)
		}
		if act.Verb == "position" && (level < 0 || level > 100) {
			return Action{}, fmt.Errorf("position %d out of range (0-100)", level)
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
	if HasLevel(a.Verb) {
		return fmt.Sprintf("%s:%d:%s", a.Verb, a.Level, a.Device)
	}
	return a.Verb + ":" + a.Device
}
