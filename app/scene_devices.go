package app

import (
	"fmt"
	"strings"
	"time"
)

// MaxSceneToggleTimeoutSeconds caps SceneEditConfig.ToggleTimeoutSeconds (and
// SceneConfig's): the same 24h ceiling as a delayed step.
const MaxSceneToggleTimeoutSeconds = int(MaxSceneStepDelay / time.Second)

// ValidateSceneConfig checks the scene-level rules shared by the runtime
// (swkit.NewScene) and the config editor's save validation, so the two can
// never disagree about which scenes load:
//
//   - ToggleTimeoutSeconds is within 0..MaxSceneToggleTimeoutSeconds (every
//     scene).
//   - Devices == nil is a legacy scene: nothing else is checked here.
//   - Devices != nil is a managed scene: the list is non-empty, its names are
//     non-empty, unique and valid scene targets (checkTarget, which applies
//     the same rules as a step target: an existing controllable device or a
//     scene defined earlier, never the scene itself or a later one), the
//     scene has at least one state (state 0, "Off"), and every step in every
//     state targets a listed device. State 0 is constrained only by that last
//     rule.
//
// Steps that do not parse are skipped: parsing and per-step target checks are
// the caller's job, since each caller already reports those in its own way.
// Messages carry no scene prefix; callers add one ("scene \"X\": ...").
// Names are compared as given (the config editor trims them beforehand).
func ValidateSceneConfig(sc SceneEditConfig, checkTarget func(name string) error) []error {
	var errs []error

	if sc.ToggleTimeoutSeconds < 0 || sc.ToggleTimeoutSeconds > MaxSceneToggleTimeoutSeconds {
		errs = append(errs, fmt.Errorf("ToggleTimeoutSeconds %d out of range (0-%d)", sc.ToggleTimeoutSeconds, MaxSceneToggleTimeoutSeconds))
	}

	if sc.Devices == nil {
		return errs
	}

	if len(sc.Devices) == 0 {
		errs = append(errs, fmt.Errorf("Devices must not be empty: list at least one device, or remove Devices to make this a legacy scene"))
	}

	listed := make(map[string]bool, len(sc.Devices))
	for i, d := range sc.Devices {
		if strings.TrimSpace(d) == "" {
			errs = append(errs, fmt.Errorf("Devices entry #%d: device name must not be empty", i+1))
			continue
		}
		if listed[d] {
			errs = append(errs, fmt.Errorf("Devices: duplicate device %q", d))
			continue
		}
		listed[d] = true
		if checkTarget != nil {
			if err := checkTarget(d); err != nil {
				errs = append(errs, fmt.Errorf("Devices entry %q: %w", d, err))
			}
		}
	}

	if len(sc.States) == 0 {
		errs = append(errs, fmt.Errorf("a scene with a device list needs at least one state (state 0, \"Off\")"))
	}

	for _, st := range sc.States {
		for j, actionStr := range st.Actions {
			step, err := ParseSceneStep(actionStr)
			if err != nil {
				continue
			}
			if !listed[step.Device] {
				errs = append(errs, fmt.Errorf("state %q action #%d (%q): device %q is not in the scene's Devices list", st.Name, j+1, actionStr, step.Device))
			}
		}
	}

	return errs
}
