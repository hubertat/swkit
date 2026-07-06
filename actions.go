package swkit

import "fmt"

// applyVerb executes a single action verb against a resolved Controllable
// device. It is the one dispatcher shared by scenes and button controls, so the
// meaning of each verb is defined in exactly one place.
//
// Verbs: on, off, toggle, brightness, brightness_up, brightness_down. The
// brightness family requires the device to implement Dimmable; level is the
// target percentage for "brightness" and the step for the relative verbs.
func applyVerb(dev Controllable, verb string, level int) error {
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
	default:
		return fmt.Errorf("unknown action %q for device %s", verb, dev.Name())
	}
	return nil
}
