package app

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// AllSceneActions returns the available scene action verbs.
func AllSceneActions() []string {
	return []string{"on", "off", "toggle", "brightness"}
}

// ParseSceneAction parses a scene action string into its verb, brightness level
// (0 when not applicable) and target device name. Grammar:
//
//	on|off|toggle:<device>
//	brightness:<pct>:<device>
func ParseSceneAction(s string) (action string, level int, device string, err error) {
	if len(s) == 0 {
		err = errors.New("empty scene action")
		return
	}

	parts := strings.Split(s, ":")
	action = strings.ToLower(parts[0])

	switch action {
	case "on", "off", "toggle":
		if len(parts) != 2 {
			err = fmt.Errorf("action %q expects format <action>:<device>", action)
			return
		}
		device = parts[1]
	case "brightness":
		if len(parts) != 3 {
			err = errors.New("brightness expects format brightness:<pct>:<device>")
			return
		}
		level, err = strconv.Atoi(parts[1])
		if err != nil {
			err = fmt.Errorf("invalid brightness level %q: %w", parts[1], err)
			return
		}
		device = parts[2]
	default:
		err = fmt.Errorf("unknown scene action %q", action)
	}

	return
}

// FormatSceneAction renders a scene action string from its parts.
func FormatSceneAction(action string, level int, device string) string {
	if action == "brightness" {
		return fmt.Sprintf("brightness:%d:%s", level, device)
	}
	return action + ":" + device
}
