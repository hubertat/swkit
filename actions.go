package swkit

import (
	"fmt"

	"github.com/hubertat/swkit/app"
)

// Shutter is the window shutter capability: error-returning movement and
// position commands. Call sites discover it via type assertion; the legacy
// Controllable SetValue/Toggle wrappers only log failures.
type Shutter interface {
	Command(cmd ShutterCommand, pct int) (WindowShutterSnapshot, error)
}

var _ Shutter = (*WindowShutter)(nil)
var _ Controllable = (*WindowShutter)(nil)

// applyVerb executes a single action verb against a resolved Controllable
// device. It is the one dispatcher shared by scenes and button controls, so the
// meaning of each verb is defined in exactly one place.
//
// Verbs: on, off, toggle, brightness, brightness_up, brightness_down, open,
// close, stop, position. The brightness family requires the device to
// implement Dimmable; level is the target percentage for "brightness" and the
// step for the relative verbs. open/close/stop/position require a Shutter
// (level is the target percent open for "position"); a shutter rejects
// on/off, and its toggle is the movement toggle.
func applyVerb(dev Controllable, verb string, level int) error {
	if sh, ok := dev.(Shutter); ok {
		var cmd ShutterCommand
		switch verb {
		case "open":
			cmd = ShutterCommandOpen
		case "close":
			cmd = ShutterCommandClose
		case "stop":
			cmd = ShutterCommandStop
		case "toggle":
			cmd = ShutterCommandToggle
		case "position":
			cmd = ShutterCommandPosition
		default:
			return fmt.Errorf("action %q is not supported by window shutter %s", verb, dev.Name())
		}
		_, err := sh.Command(cmd, level)
		return err
	}

	switch verb {
	case "on":
		dev.SetValue(true)
	case "off":
		dev.SetValue(false)
	case "toggle":
		dev.Toggle()
	case "brightness":
		d, ok := dev.(Dimmable)
		if !ok {
			return fmt.Errorf("device %s does not support brightness", dev.Name())
		}
		d.SetBrightness(level)
	case "brightness_up", "brightness_down":
		d, ok := dev.(Dimmable)
		if !ok {
			return fmt.Errorf("device %s does not support brightness", dev.Name())
		}
		cur, err := d.GetBrightness()
		if err != nil {
			return fmt.Errorf("device %s: read brightness: %w", dev.Name(), err)
		}
		if verb == "brightness_down" {
			level = -level
		}
		d.SetBrightness(cur + level) // SetBrightness clamps to 0-100
	case "open", "close", "stop", "position":
		return fmt.Errorf("action %q requires a window shutter; %s is not one", verb, dev.Name())
	default:
		return fmt.Errorf("unknown action %q for device %s", verb, dev.Name())
	}
	return nil
}

// targetTypeOf returns the app target type name of a resolved controllable,
// used to validate action verbs at runtime construction.
func targetTypeOf(dev Controllable) string {
	switch dev.(type) {
	case *Light:
		return "light"
	case *ColorLight:
		return "color_light"
	case *DimmableLight:
		return "dimmable_light"
	case *Outlet:
		return "outlet"
	case *Scene:
		return "scene"
	case *WindowShutter:
		return "window_shutter"
	default:
		return ""
	}
}

// shutterVerbError rejects, at runtime construction, verb/target pairs that
// involve a window shutter and cannot work: shutter-only verbs on other
// devices, and verbs other than open/close/stop/position/toggle on a
// shutter. Other mismatches (e.g. brightness on a plain light) keep their
// existing behaviour: rejected at save time, reported at apply time.
func shutterVerbError(verb string, dev Controllable) error {
	tt := targetTypeOf(dev)
	if tt == "" {
		return nil
	}
	isShutter := tt == "window_shutter"
	if (isShutter || app.IsShutterVerb(verb)) && !app.VerbSupportedByTargetType(verb, tt) {
		return app.UnsupportedVerbError(verb, dev.Name(), tt)
	}
	return nil
}
