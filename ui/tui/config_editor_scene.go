package tui

import (
	"fmt"
	"strings"

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

// firstTargetDevice returns a sensible default action target.
func (ce *ConfigEditor) firstTargetDevice() string {
	if len(ce.config.OutputDeviceNames) > 0 {
		return ce.config.OutputDeviceNames[0]
	}
	return ""
}

// ---- Scene screen (name + list of states) ----

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

	totalFields := 1 + len(scene.States) // Name + one row per state

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
		// Drill into the selected state.
		ce.sceneStateIndex = ce.fieldCursor - 1
		ce.fieldCursor = 0
		ce.sceneActionEditing = false
		ce.mode = ConfigModeEditSceneState
	case "a":
		scene.States = append(scene.States, app.SceneStateEditConfig{
			Name: fmt.Sprintf("state %d", len(scene.States)),
		})
		ce.dirty = true
		ce.fieldCursor = len(scene.States) // last state row
	case "d", "delete":
		stIdx := ce.fieldCursor - 1
		if stIdx >= 0 && stIdx < len(scene.States) {
			scene.States = append(scene.States[:stIdx], scene.States[stIdx+1:]...)
			ce.dirty = true
			if ce.fieldCursor > len(scene.States) {
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
		return ce.updateSceneActionEdit(keyMsg, state)
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
		state.Actions = append(state.Actions, app.Action{Verb: "on", Device: ce.firstTargetDevice()}.String())
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
		ce.fieldCursor = 1 + ce.sceneStateIndex // return cursor onto this state row
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
func (ce *ConfigEditor) updateSceneActionEdit(msg tea.KeyMsg, state *app.SceneStateEditConfig) tea.Cmd {
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
		ce.cycleSceneActionField(state, actIdx, -1)
		ce.dirty = true
	case "right", "l":
		ce.cycleSceneActionField(state, actIdx, 1)
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
	act, err := app.ParseAction(state.Actions[actIdx])
	if err != nil {
		return
	}
	if app.IsBrightnessVerb(act.Verb) {
		act.Level = adjustBrightnessLevel(act.Level, direction)
		state.Actions[actIdx] = act.String()
	}
}

// cycleSceneActionField mutates the action string at actIdx by cycling the
// currently focused sub-field.
func (ce *ConfigEditor) cycleSceneActionField(state *app.SceneStateEditConfig, actIdx, direction int) {
	act, err := app.ParseAction(state.Actions[actIdx])
	if err != nil {
		// Unparseable (e.g. legacy/hand-edited) — reset to a sane default.
		act = app.Action{Verb: "on", Device: ce.firstTargetDevice()}
	}

	switch ce.sceneActionField {
	case 0: // action verb
		act.Verb = cycleOption(app.AllActionVerbs(), act.Verb, direction)
	case 1: // device
		if len(ce.config.OutputDeviceNames) > 0 {
			act.Device = cycleOption(ce.config.OutputDeviceNames, act.Device, direction)
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

	fields = append(fields, "")
	fields = append(fields, theme.BoxTitle.Render("States (index 0 = off)"))
	if len(scene.States) == 0 {
		fields = append(fields, theme.Muted.Render("  No states. Press 'a' to add one."))
	}
	for i, st := range scene.States {
		fields = append(fields, ce.renderSceneStateRow(theme, st, 1+i))
	}

	content := strings.Join(fields, "\n")
	result := theme.Box.Width(55).Render(content)
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
	if len(ce.config.OutputDeviceNames) == 0 {
		fields = append(fields, theme.Muted.Render("  No target devices available."))
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

	act, err := app.ParseAction(actionStr)
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
	act, err := app.ParseAction(actionStr)
	if err != nil {
		act = app.Action{Verb: "on"}
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
