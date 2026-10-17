package tui

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/hubertat/swkit/app"
)

// currentSceneItem returns the scene currently selected in the list, or false.
// Both the scene screen and the scene-state screen keep ce.cursor on the
// scene's list item, so this is the single source of truth for "which scene".
func (ce *ConfigEditor) currentSceneItem() (*app.SceneEditConfig, bool) {
	if ce.cursor < 0 || ce.cursor >= len(ce.items) {
		return nil, false
	}
	item := ce.items[ce.cursor]
	if item.itemType != configItemScene || item.index >= len(ce.config.Scenes) {
		return nil, false
	}
	return &ce.config.Scenes[item.index], true
}

// sceneTargetOptions returns the devices a step of scene may target: the
// scene's own device list.
func (ce *ConfigEditor) sceneTargetOptions(scene *app.SceneEditConfig) []string {
	if scene == nil {
		return nil
	}
	return scene.Devices
}

// firstSceneTarget returns the default target for a new step of scene: its
// first listed device, or "" when the list is empty.
func (ce *ConfigEditor) firstSceneTarget(scene *app.SceneEditConfig) string {
	if opts := ce.sceneTargetOptions(scene); len(opts) > 0 {
		return opts[0]
	}
	return ""
}

// sceneDeviceCandidate is one row of the scene device checklist.
type sceneDeviceCandidate struct {
	name string
	kind string // "light", "dimmable", "outlet", "color light", "scene" or "unknown"
}

// sceneDeviceCandidates returns the devices the scene at sceneIdx may list,
// in the same order as the config: lights, dimmable lights, outlets, color
// lights (from runtime state, as they are not part of the editable config)
// and the scenes defined before it (a scene may only target earlier scenes,
// never itself or a later one). Listed devices that are none of these (e.g.
// renamed or deleted since) are appended as "unknown" so they can be removed.
func (ce *ConfigEditor) sceneDeviceCandidates(sceneIdx int) []sceneDeviceCandidate {
	var out []sceneDeviceCandidate
	seen := make(map[string]bool)
	add := func(name, kind string) {
		if strings.TrimSpace(name) == "" || seen[name] {
			return
		}
		seen[name] = true
		out = append(out, sceneDeviceCandidate{name: name, kind: kind})
	}
	for _, l := range ce.config.Lights {
		add(l.Name, "light")
	}
	for _, dl := range ce.config.DimmableLights {
		add(dl.Name, "dimmable")
	}
	for _, o := range ce.config.Outlets {
		add(o.Name, "outlet")
	}
	for _, d := range ce.deviceStates {
		if d.Type == app.DeviceTypeColorLight {
			add(d.Name, "color light")
		}
	}
	for i := 0; i < sceneIdx && i < len(ce.config.Scenes); i++ {
		add(ce.config.Scenes[i].Name, "scene")
	}
	if sceneIdx >= 0 && sceneIdx < len(ce.config.Scenes) {
		for _, d := range ce.config.Scenes[sceneIdx].Devices {
			add(d, "unknown")
		}
	}
	return out
}

// currentSceneIndex returns the index in ce.config.Scenes of the scene
// selected in the list, or -1.
func (ce *ConfigEditor) currentSceneIndex() int {
	if _, ok := ce.currentSceneItem(); !ok {
		return -1
	}
	return ce.items[ce.cursor].index
}

// addSceneDevice adds name to the scene's device list. Like the web Scene
// Studio, a device joins switched off in state 0 ("off:<d>" appended to its
// steps) and left unchanged in the other states; while the checklist was
// opened on a brand new scene (no states yet), it instead builds the
// Studio's starting point: "Off" with every chosen device off and "On" with
// every chosen device on.
func (ce *ConfigEditor) addSceneDevice(scene *app.SceneEditConfig, name string) {
	if slices.Contains(scene.Devices, name) {
		return
	}
	scene.Devices = append(scene.Devices, name)
	if ce.sceneDevicesNewScene && len(scene.States) == 0 {
		scene.States = []app.SceneStateEditConfig{{Name: "Off"}, {Name: "On"}}
	}
	if len(scene.States) == 0 {
		scene.States = []app.SceneStateEditConfig{{Name: "Off"}}
	}
	scene.States[0].Actions = append(scene.States[0].Actions, app.Action{Verb: "off", Device: name}.String())
	if ce.sceneDevicesNewScene && len(scene.States) > 1 {
		scene.States[1].Actions = append(scene.States[1].Actions, app.Action{Verb: "on", Device: name}.String())
	}
	ce.markDirty()
}

// removeSceneDevice drops name from the scene's device list and strips every
// step that targets it from every state, delayed steps included. Steps that
// do not parse are left alone.
func (ce *ConfigEditor) removeSceneDevice(scene *app.SceneEditConfig, name string) {
	idx := slices.Index(scene.Devices, name)
	if idx < 0 {
		return
	}
	scene.Devices = slices.Delete(scene.Devices, idx, idx+1)
	for i := range scene.States {
		scene.States[i].Actions = slices.DeleteFunc(scene.States[i].Actions, func(a string) bool {
			step, err := app.ParseSceneStep(a)
			return err == nil && step.Device == name
		})
	}
	ce.markDirty()
}

// openSceneDevices opens the device checklist for the current scene.
func (ce *ConfigEditor) openSceneDevices(scene *app.SceneEditConfig) {
	ce.sceneDeviceCursor = 0
	ce.sceneDevicesNewScene = len(scene.States) == 0 && len(scene.Devices) == 0
	ce.mode = ConfigModeSceneDevices
}

// updateSceneDevices handles keys in the scene device checklist.
func (ce *ConfigEditor) updateSceneDevices(msg tea.Msg) tea.Cmd {
	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	scene, ok := ce.currentSceneItem()
	if !ok {
		ce.mode = ConfigModeList
		return nil
	}
	candidates := ce.sceneDeviceCandidates(ce.currentSceneIndex())

	switch keyMsg.String() {
	case "up", "k":
		if ce.sceneDeviceCursor > 0 {
			ce.sceneDeviceCursor--
		}
	case "down", "j":
		if ce.sceneDeviceCursor < len(candidates)-1 {
			ce.sceneDeviceCursor++
		}
	case " ", "space":
		if ce.sceneDeviceCursor < 0 || ce.sceneDeviceCursor >= len(candidates) {
			return nil
		}
		c := candidates[ce.sceneDeviceCursor]
		if slices.Contains(scene.Devices, c.name) {
			ce.removeSceneDevice(scene, c.name)
			ce.statusMsg = fmt.Sprintf("Removed %s and its steps", c.name)
			// An "unknown" row disappears once unlisted.
			if n := len(ce.sceneDeviceCandidates(ce.currentSceneIndex())); ce.sceneDeviceCursor >= n && n > 0 {
				ce.sceneDeviceCursor = n - 1
			}
		} else {
			ce.addSceneDevice(scene, c.name)
			ce.statusMsg = ""
		}
	case "enter", "esc":
		ce.fieldCursor = 1 // back onto the Devices row
		ce.mode = ConfigModeEditScene
	}
	return nil
}

// ---- Scene screen (name + device list + list of states) ----

// sceneStateFieldOffset is the field index of the first state row on the
// scene screen (after Name and Devices).
const sceneStateFieldOffset = 2

// updateEditScene handles keys while editing a scene (name + its states).
func (ce *ConfigEditor) updateEditScene(msg tea.Msg) tea.Cmd {
	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}

	scene, ok := ce.currentSceneItem()
	if !ok {
		ce.mode = ConfigModeList
		return nil
	}

	if ce.editing {
		return ce.handleSceneNameEditing(keyMsg, scene)
	}

	totalFields := sceneStateFieldOffset + len(scene.States) // Name + Devices + one row per state

	switch keyMsg.String() {
	case "up", "k":
		if ce.fieldCursor > 0 {
			ce.fieldCursor--
		}
	case "down", "j":
		if ce.fieldCursor < totalFields-1 {
			ce.fieldCursor++
		}
	case "enter":
		if ce.fieldCursor == 0 {
			return ce.startEditingSceneField(scene)
		}
		if ce.fieldCursor == 1 {
			ce.openSceneDevices(scene)
			return nil
		}
		// Drill into the selected state.
		ce.sceneStateIndex = ce.fieldCursor - sceneStateFieldOffset
		ce.fieldCursor = 0
		ce.sceneActionEditing = false
		ce.mode = ConfigModeEditSceneState
	case "a":
		scene.States = append(scene.States, app.SceneStateEditConfig{
			Name: fmt.Sprintf("state %d", len(scene.States)),
		})
		ce.dirty = true
		ce.fieldCursor = sceneStateFieldOffset + len(scene.States) - 1 // last state row
	case "d", "delete":
		stIdx := ce.fieldCursor - sceneStateFieldOffset
		if stIdx >= 0 && stIdx < len(scene.States) {
			scene.States = append(scene.States[:stIdx], scene.States[stIdx+1:]...)
			ce.dirty = true
			if ce.fieldCursor >= sceneStateFieldOffset+len(scene.States) && ce.fieldCursor > 0 {
				ce.fieldCursor--
			}
		}
	case "esc":
		ce.mode = ConfigModeList
	}

	return nil
}

// startEditingSceneField begins editing the scene Name field.
func (ce *ConfigEditor) startEditingSceneField(scene *app.SceneEditConfig) tea.Cmd {
	if ce.fieldCursor != 0 {
		return nil
	}
	ce.textInput.SetValue(scene.Name)
	ce.textInput.Focus()
	ce.editing = true
	return textinput.Blink
}

func (ce *ConfigEditor) handleSceneNameEditing(msg tea.KeyMsg, scene *app.SceneEditConfig) tea.Cmd {
	switch msg.Type {
	case tea.KeyEnter:
		value := strings.TrimSpace(ce.textInput.Value())
		if value != "" {
			scene.Name = value
			ce.dirty = true
			ce.rebuildItems()
		}
		ce.editing = false
		ce.textInput.Blur()
		return nil
	case tea.KeyEsc:
		ce.editing = false
		ce.textInput.Blur()
		return nil
	default:
		var cmd tea.Cmd
		ce.textInput, cmd = ce.textInput.Update(msg)
		return cmd
	}
}

// ---- Scene-state screen (name + list of actions) ----

// updateEditSceneState handles keys while editing one state of a scene.
func (ce *ConfigEditor) updateEditSceneState(msg tea.Msg) tea.Cmd {
	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}

	scene, ok := ce.currentSceneItem()
	if !ok || ce.sceneStateIndex < 0 || ce.sceneStateIndex >= len(scene.States) {
		ce.mode = ConfigModeEditScene
		return nil
	}
	state := &scene.States[ce.sceneStateIndex]

	if ce.editing {
		return ce.handleSceneStateNameEditing(keyMsg, state)
	}
	if ce.sceneActionEditing {
		return ce.updateSceneActionEdit(keyMsg, scene, state)
	}

	totalFields := 1 + len(state.Actions) // Name + one row per action

	switch keyMsg.String() {
	case "up", "k":
		if ce.fieldCursor > 0 {
			ce.fieldCursor--
		}
	case "down", "j":
		if ce.fieldCursor < totalFields-1 {
			ce.fieldCursor++
		}
	case "enter":
		if ce.fieldCursor == 0 {
			ce.textInput.SetValue(state.Name)
			ce.textInput.Focus()
			ce.editing = true
			return textinput.Blink
		}
		// Edit the selected action inline.
		ce.sceneActionField = 0
		ce.sceneActionEditing = true
	case "a":
		target := ce.firstSceneTarget(scene)
		if target == "" {
			ce.statusMsg = "Choose the scene's devices first (Devices row on the scene screen)"
			return nil
		}
		state.Actions = append(state.Actions, app.Action{Verb: "on", Device: target}.String())
		ce.dirty = true
		ce.fieldCursor = len(state.Actions) // last action row
	case "d", "delete":
		actIdx := ce.fieldCursor - 1
		if actIdx >= 0 && actIdx < len(state.Actions) {
			state.Actions = append(state.Actions[:actIdx], state.Actions[actIdx+1:]...)
			ce.dirty = true
			if ce.fieldCursor > len(state.Actions) {
				ce.fieldCursor--
			}
		}
	case "esc":
		ce.fieldCursor = sceneStateFieldOffset + ce.sceneStateIndex // return cursor onto this state row
		ce.mode = ConfigModeEditScene
	}

	return nil
}

func (ce *ConfigEditor) handleSceneStateNameEditing(msg tea.KeyMsg, state *app.SceneStateEditConfig) tea.Cmd {
	switch msg.Type {
	case tea.KeyEnter:
		value := strings.TrimSpace(ce.textInput.Value())
		if value != "" {
			state.Name = value
			ce.dirty = true
		}
		ce.editing = false
		ce.textInput.Blur()
		return nil
	case tea.KeyEsc:
		ce.editing = false
		ce.textInput.Blur()
		return nil
	default:
		var cmd tea.Cmd
		ce.textInput, cmd = ce.textInput.Update(msg)
		return cmd
	}
}

// updateSceneActionEdit handles the inline action editor (action/device/level).
func (ce *ConfigEditor) updateSceneActionEdit(msg tea.KeyMsg, scene *app.SceneEditConfig, state *app.SceneStateEditConfig) tea.Cmd {
	actIdx := ce.fieldCursor - 1
	if actIdx < 0 || actIdx >= len(state.Actions) {
		ce.sceneActionEditing = false
		return nil
	}

	switch msg.String() {
	case "up", "k":
		if ce.sceneActionField > 0 {
			ce.sceneActionField--
		}
	case "down", "j":
		if ce.sceneActionField < 2 {
			ce.sceneActionField++
		}
	case "left", "h":
		ce.cycleSceneActionField(scene, state, actIdx, -1)
		ce.dirty = true
	case "right", "l":
		ce.cycleSceneActionField(scene, state, actIdx, 1)
		ce.dirty = true
	case "+", "=":
		ce.adjustSceneActionLevel(state, actIdx, 1)
		ce.dirty = true
	case "-", "_":
		ce.adjustSceneActionLevel(state, actIdx, -1)
		ce.dirty = true
	case "enter", "esc":
		ce.sceneActionEditing = false
	}
	return nil
}

// adjustSceneActionLevel nudges the brightness step of the action at actIdx,
// a no-op for non-brightness verbs.
func (ce *ConfigEditor) adjustSceneActionLevel(state *app.SceneStateEditConfig, actIdx, direction int) {
	act, err := app.ParseSceneStep(state.Actions[actIdx])
	if err != nil {
		return
	}
	if app.IsBrightnessVerb(act.Verb) {
		act.Level = adjustBrightnessLevel(act.Level, direction)
		state.Actions[actIdx] = act.String()
	}
}

// cycleSceneActionField mutates the action string at actIdx by cycling the
// currently focused sub-field. The device cycles through the scene's own
// device list only.
func (ce *ConfigEditor) cycleSceneActionField(scene *app.SceneEditConfig, state *app.SceneStateEditConfig, actIdx, direction int) {
	// Parsed as a scene step so a delay set elsewhere (web Scene Studio or
	// config.json) survives editing the verb, device or level here.
	act, err := app.ParseSceneStep(state.Actions[actIdx])
	if err != nil {
		// Unparseable (e.g. hand-edited) — reset to a sane default.
		act = app.SceneStep{Action: app.Action{Verb: "on", Device: ce.firstSceneTarget(scene)}}
	}

	switch ce.sceneActionField {
	case 0: // action verb
		act.Verb = cycleOption(app.AllActionVerbs(), act.Verb, direction)
	case 1: // device
		if opts := ce.sceneTargetOptions(scene); len(opts) > 0 {
			act.Device = cycleOption(opts, act.Device, direction)
		}
	case 2: // brightness level/step (only meaningful for brightness-family verbs)
		if app.IsBrightnessVerb(act.Verb) {
			act.Level = adjustBrightnessLevel(act.Level, direction)
		}
	}

	state.Actions[actIdx] = act.String()
}

// ---- Views ----

func (ce *ConfigEditor) viewEditScene(theme Theme) string {
	scene, ok := ce.currentSceneItem()
	if !ok {
		return theme.Muted.Render("No item selected")
	}

	title := theme.BoxTitle.Render(IconScene + " Edit Scene")
	fields := []string{title, ""}
	fields = append(fields, ce.renderTextField(theme, "Name", scene.Name, 0))
	fields = append(fields, ce.renderSceneSettings(theme, scene)...)

	fields = append(fields, "")
	fields = append(fields, theme.BoxTitle.Render("States (index 0 = off)"))
	if len(scene.States) == 0 {
		if len(scene.Devices) == 0 {
			fields = append(fields, theme.Muted.Render("  No states. Choose the devices first: Off and On are created."))
		} else {
			fields = append(fields, theme.Muted.Render("  No states. Press 'a' to add one."))
		}
	}
	for i, st := range scene.States {
		fields = append(fields, ce.renderSceneStateRow(theme, st, sceneStateFieldOffset+i))
	}

	content := strings.Join(fields, "\n")
	result := theme.Box.Width(55).Render(content)
	result += ce.viewStatus(theme)
	return result
}

// renderSceneSettings shows the scene's device list (field 1; Enter opens
// the checklist) and its toggle timeout, which is read-only here (edited in
// the web Scene Studio; the TUI only keeps it intact).
func (ce *ConfigEditor) renderSceneSettings(theme Theme, scene *app.SceneEditConfig) []string {
	devices := theme.Muted.Render("[none - required, press Enter to choose]")
	if len(scene.Devices) > 0 {
		devices = theme.Primary.Render(strings.Join(scene.Devices, ", "))
	}
	prefix := "  "
	if ce.fieldCursor == 1 {
		prefix = "▶ "
	}
	timeout := theme.Muted.Render("off")
	if scene.ToggleTimeoutSeconds > 0 {
		timeout = theme.Primary.Render(app.FormatStepDelay(time.Duration(scene.ToggleTimeoutSeconds) * time.Second))
	}
	return []string{
		prefix + theme.Secondary.Render("Devices: ") + devices,
		"  " + theme.Secondary.Render("Back to off after: ") + timeout,
	}
}

// viewSceneDevices renders the scene device checklist.
func (ce *ConfigEditor) viewSceneDevices(theme Theme) string {
	scene, ok := ce.currentSceneItem()
	if !ok {
		return theme.Muted.Render("No item selected")
	}
	candidates := ce.sceneDeviceCandidates(ce.currentSceneIndex())

	lines := []string{theme.BoxTitle.Render(IconScene + " Scene Devices — " + theme.Primary.Render(scene.Name)), ""}
	if len(candidates) == 0 {
		lines = append(lines, theme.Muted.Render("  No devices available. Add a light or outlet first."))
	}
	for i, c := range candidates {
		prefix := "  "
		style := theme.ListItem
		if i == ce.sceneDeviceCursor {
			prefix = "▶ "
			style = theme.ListItemSelected
		}
		box := "[ ]"
		if slices.Contains(scene.Devices, c.name) {
			box = "[x]"
		}
		lines = append(lines, style.Render(prefix+box+" "+theme.Primary.Render(padRight(c.name, 22))+" "+theme.Muted.Render(c.kind)))
	}
	lines = append(lines, "", theme.Muted.Render("  Adding a device switches it off in state 0."),
		theme.Muted.Render("  Removing one deletes its steps from every state."))

	result := theme.Box.Width(55).Render(strings.Join(lines, "\n"))
	result += ce.viewStatus(theme)
	return result
}

func (ce *ConfigEditor) renderSceneStateRow(theme Theme, st app.SceneStateEditConfig, fieldIdx int) string {
	prefix := "  "
	if ce.fieldCursor == fieldIdx {
		prefix = "▶ "
	}
	name := theme.Primary.Render(st.Name)
	if st.Name == "" {
		name = theme.Muted.Render("[unnamed]")
	}
	count := theme.Muted.Render(fmt.Sprintf(" (%d actions)", len(st.Actions)))
	return prefix + theme.Secondary.Render("State: ") + name + count
}

func (ce *ConfigEditor) viewEditSceneState(theme Theme) string {
	scene, ok := ce.currentSceneItem()
	if !ok || ce.sceneStateIndex < 0 || ce.sceneStateIndex >= len(scene.States) {
		return theme.Muted.Render("No state selected")
	}
	state := scene.States[ce.sceneStateIndex]

	title := theme.BoxTitle.Render(IconScene + " Edit State — " + theme.Primary.Render(scene.Name))
	fields := []string{title, ""}
	fields = append(fields, ce.renderTextField(theme, "Name", state.Name, 0))

	fields = append(fields, "")
	fields = append(fields, theme.BoxTitle.Render("Actions"))
	if len(ce.sceneTargetOptions(scene)) == 0 {
		fields = append(fields, theme.Muted.Render("  This scene has no devices. Choose them on the scene screen."))
	} else if len(state.Actions) == 0 {
		fields = append(fields, theme.Muted.Render("  No actions. Press 'a' to add one."))
	}
	for i, act := range state.Actions {
		fields = append(fields, ce.renderSceneAction(theme, act, 1+i))
	}

	content := strings.Join(fields, "\n")
	result := theme.Box.Width(55).Render(content)
	result += ce.viewStatus(theme)
	return result
}

func (ce *ConfigEditor) renderSceneAction(theme Theme, actionStr string, fieldIdx int) string {
	if ce.sceneActionEditing && ce.fieldCursor == fieldIdx {
		return ce.renderSceneActionEditor(theme, actionStr)
	}

	prefix := "  "
	if ce.fieldCursor == fieldIdx {
		prefix = "▶ "
	}

	act, err := app.ParseSceneStep(actionStr)
	if err != nil {
		return prefix + theme.Muted.Render(actionStr+" [invalid]")
	}
	devStr := theme.Primary.Render(act.Device)
	if act.Device == "" {
		devStr = theme.Muted.Render("[none]")
	}
	line := prefix + "  " + theme.On.Render(act.Verb) + " " + theme.Muted.Render("→") + " " + devStr
	if app.IsBrightnessVerb(act.Verb) {
		line += theme.Secondary.Render(" " + brightnessLevelLabel(act.Verb, act.Level))
	}
	if act.Delay > 0 {
		line += theme.Muted.Render(" after " + app.FormatStepDelay(act.Delay))
	}
	return line
}

// adjustBrightnessLevel nudges a brightness step/target by direction*5,
// clamped to 0-100. Shared by the wizard and both inline editors.
func adjustBrightnessLevel(level, direction int) int {
	level += direction * 5
	if level < 0 {
		return 0
	}
	if level > 100 {
		return 100
	}
	return level
}

// brightnessLevelLabel renders a brightness verb's level: "@ N%" for the
// absolute verb, "+N%"/"-N%" for the relative ones.
func brightnessLevelLabel(verb string, level int) string {
	switch verb {
	case "brightness_up":
		return fmt.Sprintf("+%d%%", level)
	case "brightness_down":
		return fmt.Sprintf("-%d%%", level)
	default:
		return fmt.Sprintf("@ %d%%", level)
	}
}

func (ce *ConfigEditor) renderSceneActionEditor(theme Theme, actionStr string) string {
	act, err := app.ParseSceneStep(actionStr)
	if err != nil {
		act = app.SceneStep{Action: app.Action{Verb: "on"}}
	}

	arrowL := theme.Muted.Render("◀ ")
	arrowR := theme.Muted.Render(" ▶")
	var lines []string

	actionPrefix := "    "
	if ce.sceneActionField == 0 {
		actionPrefix = "  ▶ "
	}
	lines = append(lines, actionPrefix+theme.Secondary.Render("Action: ")+arrowL+theme.On.Render(act.Verb)+arrowR)

	devicePrefix := "    "
	if ce.sceneActionField == 1 {
		devicePrefix = "  ▶ "
	}
	devName := act.Device
	if devName == "" {
		devName = "[none]"
	}
	lines = append(lines, devicePrefix+theme.Secondary.Render("Device: ")+arrowL+theme.Primary.Render(devName)+arrowR)

	levelPrefix := "    "
	if ce.sceneActionField == 2 {
		levelPrefix = "  ▶ "
	}
	var levelVal string
	if app.IsBrightnessVerb(act.Verb) {
		levelVal = theme.Primary.Render(brightnessLevelLabel(act.Verb, act.Level))
	} else {
		levelVal = theme.Muted.Render("(brightness only)")
	}
	lines = append(lines, levelPrefix+theme.Secondary.Render("Level:  ")+arrowL+levelVal+arrowR)

	return strings.Join(lines, "\n")
}
