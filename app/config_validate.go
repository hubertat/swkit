package app

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/hubertat/swkit/drivers"
)

// This file holds the save-time rules for an EditableConfig. They are pure
// functions of the config and an AppState snapshot so every editor (the web
// config-edit API and the TUI config editor) applies exactly the same checks
// before persisting: a config that fails them would make the next reload
// reject it, and reload tears the running controller down before Setup gets
// the chance to complain.

// NormalizeEditableConfig trims whitespace from every user-facing string in
// cfg in place: device/scene/state names, io id strings, control-relation
// target names, and scene action strings. Mutating the config once, before
// both ValidateEditableConfig and SaveConfig see it, is what keeps validation
// (which rejects empty/duplicate/dangling names) and persistence (which
// writes whatever was validated) from disagreeing about a name that only
// differs by leading/trailing whitespace.
func NormalizeEditableConfig(cfg *EditableConfig) {
	for i := range cfg.Lights {
		cfg.Lights[i].Name = strings.TrimSpace(cfg.Lights[i].Name)
		cfg.Lights[i].DigitalOutName = strings.TrimSpace(cfg.Lights[i].DigitalOutName)
	}
	for i := range cfg.Outlets {
		cfg.Outlets[i].Name = strings.TrimSpace(cfg.Outlets[i].Name)
		cfg.Outlets[i].DigitalOutName = strings.TrimSpace(cfg.Outlets[i].DigitalOutName)
	}
	for i := range cfg.DimmableLights {
		cfg.DimmableLights[i].Name = strings.TrimSpace(cfg.DimmableLights[i].Name)
		cfg.DimmableLights[i].DigitalOutName = strings.TrimSpace(cfg.DimmableLights[i].DigitalOutName)
		cfg.DimmableLights[i].AnalogOutName = strings.TrimSpace(cfg.DimmableLights[i].AnalogOutName)
	}
	for i := range cfg.Buttons {
		cfg.Buttons[i].Name = strings.TrimSpace(cfg.Buttons[i].Name)
		cfg.Buttons[i].EventInputName = strings.TrimSpace(cfg.Buttons[i].EventInputName)
		for j := range cfg.Buttons[i].ControlDevices {
			cfg.Buttons[i].ControlDevices[j].DeviceName = strings.TrimSpace(cfg.Buttons[i].ControlDevices[j].DeviceName)
		}
	}
	for i := range cfg.Scenes {
		cfg.Scenes[i].Name = strings.TrimSpace(cfg.Scenes[i].Name)
		for j := range cfg.Scenes[i].Devices {
			cfg.Scenes[i].Devices[j] = strings.TrimSpace(cfg.Scenes[i].Devices[j])
		}
		for j := range cfg.Scenes[i].States {
			cfg.Scenes[i].States[j].Name = strings.TrimSpace(cfg.Scenes[i].States[j].Name)
			for k := range cfg.Scenes[i].States[j].Actions {
				cfg.Scenes[i].States[j].Actions[k] = strings.TrimSpace(cfg.Scenes[i].States[j].Actions[k])
			}
		}
	}
}

// ---- Validation ----

// canonicalEventInputId rewrites a d_in-typed io id to its push_event
// equivalent, mirroring SwKitConfigProvider.normalizeButtonEventInputIoType
// (the root package's config_provider.go) so duplicate-input detection treats
// "driver|d_in|x" and "driver|push_event|x" as the same input. Ids that don't
// parse, or aren't d_in, are returned unchanged.
func canonicalEventInputId(ioId string) string {
	driverName, ioType, ioName, err := drivers.ResolveIoIdString(ioId)
	if err != nil {
		return ioId
	}
	if ioType != drivers.IoTypeDigitalInput {
		return ioId
	}
	return drivers.GetIoIdString(driverName, drivers.IoTypePushEventEmitter, ioName)
}

// ValidateEditableConfig validates a full EditableConfig replacement against
// itself and the current runtime state (for configured driver names and color
// light target names, which live outside EditableConfig). It collects every
// violation rather than stopping at the first one, per the API contract.
func ValidateEditableConfig(cfg EditableConfig, state AppState) []string {
	var errs []string

	configuredDrivers := make(map[string]bool, len(state.Drivers))
	for _, d := range state.Drivers {
		configuredDrivers[d.Name] = true
	}

	// Valid control/scene-action targets: every device name in the posted
	// config's controllable lists (lights, dimmable lights, outlets, scenes),
	// plus color lights from current state (they're Controllable at runtime
	// but are not part of EditableConfig, so they'd otherwise be invisible
	// here - see controllableTargetTypes in server/schema.go for the same rule).
	// targetType additionally records each target's device type, used below
	// to reject brightness actions/relations aimed at a non-Dimmable device
	// (see dimmableTargetTypes) and to enforce scene-ordering (see sceneIndex).
	validTargets := make(map[string]bool)
	targetType := make(map[string]string)
	for _, l := range cfg.Lights {
		validTargets[l.Name] = true
		targetType[l.Name] = "light"
	}
	for _, dl := range cfg.DimmableLights {
		validTargets[dl.Name] = true
		targetType[dl.Name] = "dimmable_light"
	}
	for _, o := range cfg.Outlets {
		validTargets[o.Name] = true
		targetType[o.Name] = "outlet"
	}
	for _, s := range cfg.Scenes {
		validTargets[s.Name] = true
		targetType[s.Name] = "scene"
	}
	for _, d := range state.Devices {
		if d.Type == DeviceTypeColorLight {
			validTargets[d.Name] = true
			targetType[d.Name] = "color_light"
		}
	}

	// sceneIndex maps scene name -> its position in cfg.Scenes. SwKit.Setup
	// builds scenes sequentially and resolves each action's target via
	// resolveControllable, which only sees scenes already appended - so a
	// scene may target an earlier scene, never itself or a later one (see
	// swkit.go resolveControllable's doc comment). Checked below in the scene
	// action loop.
	sceneIndex := make(map[string]int, len(cfg.Scenes))
	for i, s := range cfg.Scenes {
		sceneIndex[s.Name] = i
	}

	// dimmableTargetTypes are the target types that implement Dimmable at
	// runtime (see swkit.go: DimmableLight is the only one - ColorLight and
	// Scene do not implement SetBrightness). Brightness-family actions
	// targeting anything else fail at Setup/apply time, so reject them here.
	dimmableTargetTypes := map[string]bool{"dimmable_light": true}

	// ---- Names: non-empty, unique within each list, unique globally ----
	nameCount := make(map[string]int)
	addName := func(list, name string) {
		trimmed := strings.TrimSpace(name)
		if trimmed == "" {
			errs = append(errs, fmt.Sprintf("%s: name must not be empty", list))
			return
		}
		// Control-device strings and scene action strings are colon-delimited
		// ("<event>:<verb>:<device>"); a colon in a device name is silently
		// accepted here but corrupts that grammar on the next reload (see
		// parseControlDeviceToEdit/ParseAction), so it must be rejected
		// up front instead.
		if strings.Contains(trimmed, ":") {
			errs = append(errs, fmt.Sprintf("%s: name %q must not contain ':' (colons delimit the control-relation/action grammar)", list, trimmed))
		}
		nameCount[trimmed]++
	}
	within := func(list string, names []string) {
		seen := make(map[string]bool, len(names))
		for _, n := range names {
			trimmed := strings.TrimSpace(n)
			if trimmed == "" {
				continue // already reported by addName
			}
			if seen[trimmed] {
				errs = append(errs, fmt.Sprintf("%s: duplicate name %q", list, trimmed))
			}
			seen[trimmed] = true
		}
	}

	lightNames := namesOf(cfg.Lights, func(l LightEditConfig) string { return l.Name })
	dimNames := namesOf(cfg.DimmableLights, func(d DimmableLightEditConfig) string { return d.Name })
	outletNames := namesOf(cfg.Outlets, func(o OutletEditConfig) string { return o.Name })
	buttonNames := namesOf(cfg.Buttons, func(b ButtonEditConfig) string { return b.Name })
	sceneNames := namesOf(cfg.Scenes, func(s SceneEditConfig) string { return s.Name })

	for _, n := range lightNames {
		addName("light", n)
	}
	for _, n := range dimNames {
		addName("dimmable light", n)
	}
	for _, n := range outletNames {
		addName("outlet", n)
	}
	for _, n := range buttonNames {
		addName("button", n)
	}
	for _, n := range sceneNames {
		addName("scene", n)
	}
	within("light", lightNames)
	within("dimmable light", dimNames)
	within("outlet", outletNames)
	within("button", buttonNames)
	within("scene", sceneNames)

	for name, count := range nameCount {
		if count > 1 {
			errs = append(errs, fmt.Sprintf("device name %q is used more than once (names must be unique across lights, dimmable lights, outlets, buttons and scenes)", name))
		}
	}

	// ---- IO ids: parse, type-check, driver membership, dedup ----
	type ioUse struct {
		field  string
		device string
		ioId   string
	}
	var digitalOutUses []ioUse
	var eventInputUses []ioUse

	checkIoField := func(device, field, ioId string, wantTypes []drivers.IoType, wantLabel string) {
		if strings.TrimSpace(ioId) == "" {
			errs = append(errs, fmt.Sprintf("%s: %s must not be empty", device, field))
			return
		}
		driverName, ioType, _, err := drivers.ResolveIoIdString(ioId)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %s %q is not a valid io id: %v", device, field, ioId, err))
			return
		}
		matches := false
		for _, want := range wantTypes {
			if ioType == want {
				matches = true
				break
			}
		}
		if !matches {
			errs = append(errs, fmt.Sprintf("%s: %s %q has io type %q, want %s", device, field, ioId, ioType.IdString(), wantLabel))
			return
		}
		if !configuredDrivers[driverName] {
			errs = append(errs, fmt.Sprintf("%s: %s %q references driver %q, which is not configured", device, field, ioId, driverName))
			return
		}
	}

	for _, l := range cfg.Lights {
		label := deviceLabel("light", l.Name)
		checkIoField(label, "DigitalOutName", l.DigitalOutName, []drivers.IoType{drivers.IoTypeDigitalOutput}, "d_out")
		if strings.TrimSpace(l.DigitalOutName) != "" {
			digitalOutUses = append(digitalOutUses, ioUse{"DigitalOutName", label, l.DigitalOutName})
		}
	}
	for _, o := range cfg.Outlets {
		label := deviceLabel("outlet", o.Name)
		checkIoField(label, "DigitalOutName", o.DigitalOutName, []drivers.IoType{drivers.IoTypeDigitalOutput}, "d_out")
		if strings.TrimSpace(o.DigitalOutName) != "" {
			digitalOutUses = append(digitalOutUses, ioUse{"DigitalOutName", label, o.DigitalOutName})
		}
	}
	for _, dl := range cfg.DimmableLights {
		label := deviceLabel("dimmable light", dl.Name)
		// DigitalOutName is optional: without it the analog output also
		// switches the light on/off (see drivers.LevelSwitchable).
		hasDigitalOut := strings.TrimSpace(dl.DigitalOutName) != ""
		if hasDigitalOut {
			checkIoField(label, "DigitalOutName", dl.DigitalOutName, []drivers.IoType{drivers.IoTypeDigitalOutput}, "d_out")
			digitalOutUses = append(digitalOutUses, ioUse{"DigitalOutName", label, dl.DigitalOutName})
		}
		checkIoField(label, "AnalogOutName", dl.AnalogOutName, []drivers.IoType{drivers.IoTypeAnalogOutput}, "a_out")
		if strings.TrimSpace(dl.AnalogOutName) != "" {
			digitalOutUses = append(digitalOutUses, ioUse{"AnalogOutName", label, dl.AnalogOutName})
		}
		if dl.DefaultSetpoint < 0 || dl.DefaultSetpoint > 100 {
			errs = append(errs, fmt.Sprintf("%s: DefaultSetpoint %d out of range (0-100)", label, dl.DefaultSetpoint))
		}
	}
	for _, b := range cfg.Buttons {
		label := deviceLabel("button", b.Name)
		checkIoField(label, "EventInputName", b.EventInputName,
			[]drivers.IoType{drivers.IoTypePushEventEmitter, drivers.IoTypeDigitalInput}, "push_event or d_in")
		if strings.TrimSpace(b.EventInputName) != "" {
			eventInputUses = append(eventInputUses, ioUse{"EventInputName", label, canonicalEventInputId(b.EventInputName)})
		}
	}

	// Duplicate assignment: same io id used by more than one device/field.
	reportDuplicates := func(uses []ioUse) {
		byId := make(map[string][]string)
		for _, u := range uses {
			byId[u.ioId] = append(byId[u.ioId], u.device+"."+u.field)
		}
		for ioId, users := range byId {
			if len(users) > 1 {
				errs = append(errs, fmt.Sprintf("io id %q is assigned to more than one field: %s", ioId, strings.Join(users, ", ")))
			}
		}
	}
	reportDuplicates(digitalOutUses)
	reportDuplicates(eventInputUses)

	// ---- Button control relations ----
	eventTypeSet := setOf(AllEventTypes())
	verbSet := setOf(AllActionVerbs())
	for _, b := range cfg.Buttons {
		label := deviceLabel("button", b.Name)
		for i, cd := range b.ControlDevices {
			relLabel := fmt.Sprintf("%s control relation #%d", label, i+1)
			if !eventTypeSet[cd.EventType] {
				errs = append(errs, fmt.Sprintf("%s: unknown event type %q", relLabel, cd.EventType))
			}
			if !verbSet[cd.Action] {
				errs = append(errs, fmt.Sprintf("%s: unknown action verb %q", relLabel, cd.Action))
			}
			if IsBrightnessVerb(cd.Action) && (cd.Level < 0 || cd.Level > 100) {
				errs = append(errs, fmt.Sprintf("%s: level %d out of range (0-100) for brightness action", relLabel, cd.Level))
			}
			if strings.TrimSpace(cd.DeviceName) == "" {
				errs = append(errs, fmt.Sprintf("%s: target device name must not be empty", relLabel))
			} else if !validTargets[cd.DeviceName] {
				errs = append(errs, fmt.Sprintf("%s: target device %q does not exist", relLabel, cd.DeviceName))
			} else if IsBrightnessVerb(cd.Action) && !dimmableTargetTypes[targetType[cd.DeviceName]] {
				errs = append(errs, fmt.Sprintf("%s: brightness action target %q does not support brightness (target type: %s)", relLabel, cd.DeviceName, targetType[cd.DeviceName]))
			}
		}
	}

	// sceneTargetError reports why name is not a valid target for a step (or
	// device-list entry) of the scene at position i, or nil. SwKit.Setup
	// builds scenes sequentially: a scene may only target a scene defined
	// earlier in the list (see sceneIndex above and swkit.go
	// resolveControllable). A forward reference or self-reference builds
	// fine here but fails at reload.
	sceneTargetError := func(i int, name string) error {
		if !validTargets[name] {
			return fmt.Errorf("target device %q does not exist", name)
		}
		if targetIdx, isScene := sceneIndex[name]; isScene && targetIdx >= i {
			if targetIdx == i {
				return fmt.Errorf("scene %q cannot target itself", name)
			}
			return fmt.Errorf("target scene %q is defined later than this scene; a scene may only target earlier scenes", name)
		}
		return nil
	}

	// ---- Scene actions ----
	for i, s := range cfg.Scenes {
		sceneLabel := deviceLabel("scene", s.Name)

		// Scene-level rules shared with the runtime (swkit.NewScene): the
		// toggle timeout range and the required device list.
		for _, err := range ValidateSceneConfig(s, func(name string) error { return sceneTargetError(i, name) }) {
			errs = append(errs, fmt.Sprintf("%s: %v", sceneLabel, err))
		}

		seenStates := make(map[string]bool, len(s.States))
		for _, st := range s.States {
			stName := strings.TrimSpace(st.Name)
			if stName == "" {
				errs = append(errs, fmt.Sprintf("%s: state name must not be empty", sceneLabel))
			} else if seenStates[stName] {
				errs = append(errs, fmt.Sprintf("%s: duplicate state name %q", sceneLabel, stName))
			} else {
				seenStates[stName] = true
			}

			for j, actionStr := range st.Actions {
				actLabel := fmt.Sprintf("%s state %q action #%d (%q)", sceneLabel, st.Name, j+1, actionStr)
				act, err := ParseSceneStep(actionStr)
				if err != nil {
					errs = append(errs, fmt.Sprintf("%s: %v", actLabel, err))
					continue
				}
				if IsBrightnessVerb(act.Verb) && (act.Level < 0 || act.Level > 100) {
					errs = append(errs, fmt.Sprintf("%s: level %d out of range (0-100) for brightness action", actLabel, act.Level))
				}
				if err := sceneTargetError(i, act.Device); err != nil {
					errs = append(errs, fmt.Sprintf("%s: %v", actLabel, err))
					if !validTargets[act.Device] {
						continue
					}
				}
				if IsBrightnessVerb(act.Verb) && !dimmableTargetTypes[targetType[act.Device]] {
					errs = append(errs, fmt.Sprintf("%s: brightness action target %q does not support brightness (target type: %s)", actLabel, act.Device, targetType[act.Device]))
				}
			}
		}
	}

	return errs
}

// deviceLabel formats a human-readable identifier for error messages: the
// device type and name, falling back to "(unnamed)" for an empty name so
// error messages stay readable even when the name-non-empty check also fired.
func deviceLabel(kind, name string) string {
	if strings.TrimSpace(name) == "" {
		return kind + " (unnamed)"
	}
	return kind + " " + strconv.Quote(name)
}

// namesOf extracts names from a slice via an accessor, used to keep the
// per-list uniqueness checks above short.
func namesOf[T any](items []T, get func(T) string) []string {
	names := make([]string, 0, len(items))
	for _, it := range items {
		names = append(names, get(it))
	}
	return names
}

// setOf builds a lookup set from a slice of strings.
func setOf(items []string) map[string]bool {
	set := make(map[string]bool, len(items))
	for _, it := range items {
		set[it] = true
	}
	return set
}
