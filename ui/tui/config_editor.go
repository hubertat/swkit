package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/hubertat/swkit/app"
)

// ConfigMode represents the current mode of the config editor
type ConfigMode int

const (
	ConfigModeList ConfigMode = iota
	ConfigModeEditLight
	ConfigModeEditButton
)

// configListItemType identifies what kind of item is in the list
type configListItemType int

const (
	configItemLight configListItemType = iota
	configItemButton
)

// configListItem is an entry in the flattened config list
type configListItem struct {
	itemType configListItemType
	index    int    // index within the Lights or Buttons array
	name     string // display name
}

// ConfigSaveMsg is sent when config is saved
type ConfigSaveMsg struct {
	Error error
}

// ConfigEditor is the config editing TUI component
type ConfigEditor struct {
	provider app.ConfigProvider
	config   app.EditableConfig
	dirty    bool
	mode     ConfigMode

	// List mode
	cursor int
	items  []configListItem

	// Edit mode - common
	fieldCursor int
	editing     bool
	textInput   textinput.Model

	// Button ControlDevices sub-editor
	ctrlCursor      int
	ctrlEditing     bool
	ctrlFieldCursor int // 0=event, 1=action, 2=device

	// Status
	statusMsg string

	theme Theme
}

// NewConfigEditor creates a new config editor component
func NewConfigEditor(provider app.ConfigProvider, theme Theme) ConfigEditor {
	ti := textinput.New()
	ti.CharLimit = 80
	ti.Width = 40

	ce := ConfigEditor{
		provider:  provider,
		textInput: ti,
		theme:     theme,
	}

	if provider != nil {
		ce.config = provider.GetEditableConfig()
		ce.rebuildItems()
	}

	return ce
}

// IsDirty returns true if there are unsaved changes
func (ce *ConfigEditor) IsDirty() bool {
	return ce.dirty
}

// Reload reloads config from provider
func (ce *ConfigEditor) Reload() {
	if ce.provider == nil {
		return
	}
	ce.config = ce.provider.GetEditableConfig()
	ce.dirty = false
	ce.rebuildItems()
}

// Save persists the current config
func (ce *ConfigEditor) Save() tea.Cmd {
	if ce.provider == nil {
		return nil
	}
	config := ce.config
	return func() tea.Msg {
		err := ce.provider.SaveConfig(config)
		return ConfigSaveMsg{Error: err}
	}
}

// rebuildItems rebuilds the flattened list from config
func (ce *ConfigEditor) rebuildItems() {
	ce.items = nil
	for i, l := range ce.config.Lights {
		ce.items = append(ce.items, configListItem{
			itemType: configItemLight,
			index:    i,
			name:     l.Name,
		})
	}
	for i, b := range ce.config.Buttons {
		ce.items = append(ce.items, configListItem{
			itemType: configItemButton,
			index:    i,
			name:     b.Name,
		})
	}
}

// currentSection returns what section the cursor is in
func (ce *ConfigEditor) currentSection() configListItemType {
	if ce.cursor < len(ce.config.Lights) {
		return configItemLight
	}
	return configItemButton
}

// lightFieldCount returns the number of editable fields for a Light
func lightFieldCount() int { return 3 } // Name, DigitalOutName, DisableHomekit

// buttonBaseFieldCount returns the number of base fields for a Button (before ControlDevices)
func buttonBaseFieldCount() int { return 3 } // Name, EventInputName, DisableHomekit

// Update handles messages for the config editor
func (ce *ConfigEditor) Update(msg tea.Msg) tea.Cmd {
	switch ce.mode {
	case ConfigModeList:
		return ce.updateList(msg)
	case ConfigModeEditLight:
		return ce.updateEditLight(msg)
	case ConfigModeEditButton:
		return ce.updateEditButton(msg)
	}
	return nil
}

// updateList handles keys in list mode
func (ce *ConfigEditor) updateList(msg tea.Msg) tea.Cmd {
	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}

	switch keyMsg.String() {
	case "up", "k":
		if ce.cursor > 0 {
			ce.cursor--
		}
	case "down", "j":
		if ce.cursor < len(ce.items)-1 {
			ce.cursor++
		}
	case "enter":
		if ce.cursor >= 0 && ce.cursor < len(ce.items) {
			item := ce.items[ce.cursor]
			ce.fieldCursor = 0
			ce.editing = false
			ce.ctrlCursor = 0
			ce.ctrlEditing = false
			if item.itemType == configItemLight {
				ce.mode = ConfigModeEditLight
			} else {
				ce.mode = ConfigModeEditButton
			}
		}
	case "a":
		return ce.addItem()
	case "d", "delete":
		return ce.deleteItem()
	}

	return nil
}

// addItem adds a new Light or Button depending on cursor position
func (ce *ConfigEditor) addItem() tea.Cmd {
	section := ce.currentSection()
	if len(ce.items) == 0 {
		// Default to adding a light when list is empty
		section = configItemLight
	}

	if section == configItemLight {
		ce.config.Lights = append(ce.config.Lights, app.LightEditConfig{
			Name: "New Light",
		})
	} else {
		ce.config.Buttons = append(ce.config.Buttons, app.ButtonEditConfig{
			Name: "New Button",
		})
	}

	ce.dirty = true
	ce.rebuildItems()
	ce.cursor = len(ce.items) - 1
	return nil
}

// deleteItem removes the currently selected item
func (ce *ConfigEditor) deleteItem() tea.Cmd {
	if ce.cursor < 0 || ce.cursor >= len(ce.items) {
		return nil
	}

	item := ce.items[ce.cursor]
	if item.itemType == configItemLight {
		ce.config.Lights = append(ce.config.Lights[:item.index], ce.config.Lights[item.index+1:]...)
		// Also update OutputDeviceNames if we're removing a light
		ce.refreshOutputDeviceNames()
	} else {
		ce.config.Buttons = append(ce.config.Buttons[:item.index], ce.config.Buttons[item.index+1:]...)
	}

	ce.dirty = true
	ce.rebuildItems()
	if ce.cursor >= len(ce.items) && ce.cursor > 0 {
		ce.cursor--
	}
	return nil
}

// refreshOutputDeviceNames rebuilds the output device names list from current config
func (ce *ConfigEditor) refreshOutputDeviceNames() {
	ce.config.OutputDeviceNames = nil
	for _, l := range ce.config.Lights {
		ce.config.OutputDeviceNames = append(ce.config.OutputDeviceNames, l.Name)
	}
	// Keep any names from color lights and outlets that were loaded originally
	// (we don't edit those yet, but they're available as targets)
}

// updateEditLight handles keys in light edit mode
func (ce *ConfigEditor) updateEditLight(msg tea.Msg) tea.Cmd {
	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}

	if ce.cursor >= len(ce.items) {
		ce.mode = ConfigModeList
		return nil
	}
	item := ce.items[ce.cursor]
	light := &ce.config.Lights[item.index]

	// If text input is active, handle it
	if ce.editing {
		return ce.handleTextEditing(keyMsg, light, nil)
	}

	switch keyMsg.String() {
	case "up", "k":
		if ce.fieldCursor > 0 {
			ce.fieldCursor--
		}
	case "down", "j":
		if ce.fieldCursor < lightFieldCount()-1 {
			ce.fieldCursor++
		}
	case "enter":
		return ce.startEditingLightField(light)
	case " ":
		if ce.fieldCursor == 2 { // DisableHomekit
			light.DisableHomekit = !light.DisableHomekit
			ce.dirty = true
		}
	case "esc":
		ce.mode = ConfigModeList
	}

	return nil
}

// startEditingLightField begins editing the selected light field
func (ce *ConfigEditor) startEditingLightField(light *app.LightEditConfig) tea.Cmd {
	switch ce.fieldCursor {
	case 0: // Name
		ce.textInput.SetValue(light.Name)
		ce.textInput.Focus()
		ce.editing = true
		return textinput.Blink
	case 1: // DigitalOutName
		ce.textInput.SetValue(light.DigitalOutName)
		ce.textInput.Focus()
		ce.editing = true
		return textinput.Blink
	case 2: // DisableHomekit (toggle)
		light.DisableHomekit = !light.DisableHomekit
		ce.dirty = true
	}
	return nil
}

// handleTextEditing handles keystrokes when a text input is active
func (ce *ConfigEditor) handleTextEditing(msg tea.KeyMsg, light *app.LightEditConfig, button *app.ButtonEditConfig) tea.Cmd {
	switch msg.Type {
	case tea.KeyEnter:
		value := strings.TrimSpace(ce.textInput.Value())
		if ce.mode == ConfigModeEditLight && light != nil {
			switch ce.fieldCursor {
			case 0:
				if value != "" {
					light.Name = value
					ce.dirty = true
					ce.rebuildItems()
				}
			case 1:
				light.DigitalOutName = value
				ce.dirty = true
			}
		} else if ce.mode == ConfigModeEditButton && button != nil {
			switch ce.fieldCursor {
			case 0:
				if value != "" {
					button.Name = value
					ce.dirty = true
					ce.rebuildItems()
				}
			case 1:
				button.EventInputName = value
				ce.dirty = true
			}
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

// updateEditButton handles keys in button edit mode
func (ce *ConfigEditor) updateEditButton(msg tea.Msg) tea.Cmd {
	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}

	if ce.cursor >= len(ce.items) {
		ce.mode = ConfigModeList
		return nil
	}
	item := ce.items[ce.cursor]
	button := &ce.config.Buttons[item.index]

	// If text input is active, handle it
	if ce.editing {
		return ce.handleTextEditing(keyMsg, nil, button)
	}

	// If editing a control device inline
	if ce.ctrlEditing {
		return ce.updateCtrlDeviceEdit(keyMsg, button)
	}

	// Total fields: base fields + control device entries
	totalFields := buttonBaseFieldCount() + len(button.ControlDevices)

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
		if ce.fieldCursor < buttonBaseFieldCount() {
			return ce.startEditingButtonField(button)
		}
		// Editing a control device
		ctrlIdx := ce.fieldCursor - buttonBaseFieldCount()
		if ctrlIdx >= 0 && ctrlIdx < len(button.ControlDevices) {
			ce.ctrlCursor = ctrlIdx
			ce.ctrlFieldCursor = 0
			ce.ctrlEditing = true
		}
	case " ":
		if ce.fieldCursor == 2 { // DisableHomekit
			button.DisableHomekit = !button.DisableHomekit
			ce.dirty = true
		}
	case "a":
		// Add control device when cursor is in control devices section or at DisableHomekit
		if ce.fieldCursor >= 2 {
			newCtrl := app.ControlDeviceEdit{
				EventType: "single_press",
				Action:    "toggle",
			}
			if len(ce.config.OutputDeviceNames) > 0 {
				newCtrl.DeviceName = ce.config.OutputDeviceNames[0]
			}
			button.ControlDevices = append(button.ControlDevices, newCtrl)
			ce.dirty = true
			ce.fieldCursor = buttonBaseFieldCount() + len(button.ControlDevices) - 1
		}
	case "d", "delete":
		ctrlIdx := ce.fieldCursor - buttonBaseFieldCount()
		if ctrlIdx >= 0 && ctrlIdx < len(button.ControlDevices) {
			button.ControlDevices = append(button.ControlDevices[:ctrlIdx], button.ControlDevices[ctrlIdx+1:]...)
			ce.dirty = true
			if ce.fieldCursor >= buttonBaseFieldCount()+len(button.ControlDevices) && ce.fieldCursor > 0 {
				ce.fieldCursor--
			}
		}
	case "esc":
		ce.mode = ConfigModeList
	}

	return nil
}

// startEditingButtonField begins editing the selected button field
func (ce *ConfigEditor) startEditingButtonField(button *app.ButtonEditConfig) tea.Cmd {
	switch ce.fieldCursor {
	case 0: // Name
		ce.textInput.SetValue(button.Name)
		ce.textInput.Focus()
		ce.editing = true
		return textinput.Blink
	case 1: // EventInputName
		ce.textInput.SetValue(button.EventInputName)
		ce.textInput.Focus()
		ce.editing = true
		return textinput.Blink
	case 2: // DisableHomekit (toggle)
		button.DisableHomekit = !button.DisableHomekit
		ce.dirty = true
	}
	return nil
}

// updateCtrlDeviceEdit handles keys when editing a control device inline
func (ce *ConfigEditor) updateCtrlDeviceEdit(msg tea.KeyMsg, button *app.ButtonEditConfig) tea.Cmd {
	if ce.ctrlCursor < 0 || ce.ctrlCursor >= len(button.ControlDevices) {
		ce.ctrlEditing = false
		return nil
	}
	ctrl := &button.ControlDevices[ce.ctrlCursor]

	switch msg.String() {
	case "up", "k":
		if ce.ctrlFieldCursor > 0 {
			ce.ctrlFieldCursor--
		}
	case "down", "j":
		if ce.ctrlFieldCursor < 2 {
			ce.ctrlFieldCursor++
		}
	case "left", "h":
		ce.cycleCtrlField(ctrl, -1)
		ce.dirty = true
	case "right", "l":
		ce.cycleCtrlField(ctrl, 1)
		ce.dirty = true
	case "enter", "esc":
		ce.ctrlEditing = false
	}

	return nil
}

// cycleCtrlField cycles through options for the current control device field
func (ce *ConfigEditor) cycleCtrlField(ctrl *app.ControlDeviceEdit, direction int) {
	switch ce.ctrlFieldCursor {
	case 0: // EventType
		events := app.AllEventTypes()
		ctrl.EventType = cycleOption(events, ctrl.EventType, direction)
	case 1: // Action
		actions := app.AllActions()
		ctrl.Action = cycleOption(actions, ctrl.Action, direction)
	case 2: // DeviceName
		devices := ce.config.OutputDeviceNames
		if len(devices) > 0 {
			ctrl.DeviceName = cycleOption(devices, ctrl.DeviceName, direction)
		}
	}
}

// cycleOption cycles through a list of options
func cycleOption(options []string, current string, direction int) string {
	if len(options) == 0 {
		return current
	}
	idx := 0
	for i, o := range options {
		if o == current {
			idx = i
			break
		}
	}
	idx += direction
	if idx < 0 {
		idx = len(options) - 1
	} else if idx >= len(options) {
		idx = 0
	}
	return options[idx]
}

// View renders the config editor
func (ce *ConfigEditor) View(theme Theme) string {
	switch ce.mode {
	case ConfigModeList:
		return ce.viewList(theme)
	case ConfigModeEditLight:
		return ce.viewEditLight(theme)
	case ConfigModeEditButton:
		return ce.viewEditButton(theme)
	}
	return ""
}

// viewList renders the config item list
func (ce *ConfigEditor) viewList(theme Theme) string {
	if len(ce.items) == 0 {
		content := theme.Muted.Render("No lights or buttons configured") + "\n"
		content += theme.Muted.Render("Press 'a' to add a new item")
		return theme.Box.Render(content) + ce.viewStatus(theme)
	}

	var lines []string

	// Section: Lights
	if len(ce.config.Lights) > 0 {
		lines = append(lines, theme.BoxTitle.Render("Lights"))
		for i, l := range ce.config.Lights {
			prefix := "   "
			style := theme.ListItem
			if i == ce.cursor {
				prefix = " > "
				style = theme.ListItemSelected
			}

			hkText := ""
			if l.DisableHomekit {
				hkText = theme.Muted.Render(" (no HK)")
			}

			ioText := theme.Secondary.Render(l.DigitalOutName)
			if l.DigitalOutName == "" {
				ioText = theme.Muted.Render("[no IO]")
			}

			line := prefix + IconLight + " " + theme.Primary.Render(padRight(l.Name, 20)) + " " + ioText + hkText
			lines = append(lines, style.Render(line))
		}
	}

	// Separator
	if len(ce.config.Lights) > 0 && len(ce.config.Buttons) > 0 {
		lines = append(lines, "")
	}

	// Section: Buttons
	if len(ce.config.Buttons) > 0 {
		lines = append(lines, theme.BoxTitle.Render("Buttons"))
		for i, b := range ce.config.Buttons {
			listIdx := len(ce.config.Lights) + i
			prefix := "   "
			style := theme.ListItem
			if listIdx == ce.cursor {
				prefix = " > "
				style = theme.ListItemSelected
			}

			hkText := ""
			if b.DisableHomekit {
				hkText = theme.Muted.Render(" (no HK)")
			}

			ioText := theme.Secondary.Render(b.EventInputName)
			if b.EventInputName == "" {
				ioText = theme.Muted.Render("[no IO]")
			}

			ctrlCount := ""
			if len(b.ControlDevices) > 0 {
				ctrlCount = theme.Muted.Render(fmt.Sprintf(" [%d ctrl]", len(b.ControlDevices)))
			}

			line := prefix + IconButton + " " + theme.Primary.Render(padRight(b.Name, 20)) + " " + ioText + hkText + ctrlCount
			lines = append(lines, style.Render(line))
		}
	}

	content := strings.Join(lines, "\n")
	result := theme.Box.Render(content)
	result += ce.viewStatus(theme)
	return result
}

// viewEditLight renders the light edit form
func (ce *ConfigEditor) viewEditLight(theme Theme) string {
	if ce.cursor >= len(ce.items) {
		return theme.Muted.Render("No item selected")
	}

	item := ce.items[ce.cursor]
	light := ce.config.Lights[item.index]

	title := theme.BoxTitle.Render(IconLight + " Edit Light")

	fields := []string{title, ""}

	// Name field
	fields = append(fields, ce.renderTextField(theme, "Name", light.Name, 0))

	// DigitalOutName field
	fields = append(fields, ce.renderTextField(theme, "IO Output", light.DigitalOutName, 1))

	// DisableHomekit field
	fields = append(fields, ce.renderBoolField(theme, "Disable HomeKit", light.DisableHomekit, 2))

	content := strings.Join(fields, "\n")
	result := theme.Box.Width(50).Render(content)
	result += ce.viewStatus(theme)
	return result
}

// viewEditButton renders the button edit form
func (ce *ConfigEditor) viewEditButton(theme Theme) string {
	if ce.cursor >= len(ce.items) {
		return theme.Muted.Render("No item selected")
	}

	item := ce.items[ce.cursor]
	button := ce.config.Buttons[item.index]

	title := theme.BoxTitle.Render(IconButton + " Edit Button")

	fields := []string{title, ""}

	// Name field
	fields = append(fields, ce.renderTextField(theme, "Name", button.Name, 0))

	// EventInputName field
	fields = append(fields, ce.renderTextField(theme, "Event Input", button.EventInputName, 1))

	// DisableHomekit field
	fields = append(fields, ce.renderBoolField(theme, "Disable HomeKit", button.DisableHomekit, 2))

	// Control Devices section
	fields = append(fields, "")
	fields = append(fields, theme.BoxTitle.Render("Control Devices"))

	if len(button.ControlDevices) == 0 {
		fields = append(fields, theme.Muted.Render("  No control devices configured"))
	}

	for i, cd := range button.ControlDevices {
		fieldIdx := buttonBaseFieldCount() + i
		fields = append(fields, ce.renderControlDevice(theme, cd, fieldIdx, i))
	}

	content := strings.Join(fields, "\n")
	result := theme.Box.Width(55).Render(content)
	result += ce.viewStatus(theme)
	return result
}

// renderTextField renders a text form field
func (ce *ConfigEditor) renderTextField(theme Theme, label, value string, fieldIdx int) string {
	prefix := "  "
	if ce.fieldCursor == fieldIdx {
		prefix = "> "
	}

	labelStr := theme.Secondary.Render(padRight(label+":", 18))

	if ce.editing && ce.fieldCursor == fieldIdx {
		return prefix + labelStr + ce.textInput.View()
	}

	displayVal := value
	if displayVal == "" {
		displayVal = theme.Muted.Render("[empty]")
	} else {
		displayVal = theme.Primary.Render(displayVal)
	}

	return prefix + labelStr + displayVal
}

// renderBoolField renders a boolean toggle field
func (ce *ConfigEditor) renderBoolField(theme Theme, label string, value bool, fieldIdx int) string {
	prefix := "  "
	if ce.fieldCursor == fieldIdx {
		prefix = "> "
	}

	labelStr := theme.Secondary.Render(padRight(label+":", 18))

	valStr := theme.Success.Render("No")
	if value {
		valStr = theme.Error.Render("Yes")
	}

	return prefix + labelStr + valStr
}

// renderControlDevice renders a control device entry
func (ce *ConfigEditor) renderControlDevice(theme Theme, cd app.ControlDeviceEdit, fieldIdx int, ctrlIdx int) string {
	prefix := "  "
	if ce.fieldCursor == fieldIdx {
		prefix = "> "
	}

	// If this control device is being edited inline
	if ce.ctrlEditing && ce.ctrlCursor == ctrlIdx {
		return ce.renderControlDeviceEditor(theme, cd)
	}

	eventStr := theme.Secondary.Render(cd.EventType)
	actionStr := theme.On.Render(cd.Action)
	deviceStr := theme.Primary.Render(cd.DeviceName)

	if cd.DeviceName == "" {
		deviceStr = theme.Muted.Render("[none]")
	}

	return prefix + "  " + eventStr + " " + lipgloss.NewStyle().Foreground(ColorMuted).Render("→") +
		" " + actionStr + " " + lipgloss.NewStyle().Foreground(ColorMuted).Render("→") +
		" " + deviceStr
}

// renderControlDeviceEditor renders the inline control device editor with cycle selectors
func (ce *ConfigEditor) renderControlDeviceEditor(theme Theme, cd app.ControlDeviceEdit) string {
	var lines []string
	arrowL := theme.Muted.Render("◀ ")
	arrowR := theme.Muted.Render(" ▶")

	// Event type
	eventPrefix := "    "
	if ce.ctrlFieldCursor == 0 {
		eventPrefix = "  > "
	}
	lines = append(lines, eventPrefix+theme.Secondary.Render("Event:  ")+arrowL+theme.Primary.Render(cd.EventType)+arrowR)

	// Action
	actionPrefix := "    "
	if ce.ctrlFieldCursor == 1 {
		actionPrefix = "  > "
	}
	lines = append(lines, actionPrefix+theme.Secondary.Render("Action: ")+arrowL+theme.On.Render(cd.Action)+arrowR)

	// Device
	devicePrefix := "    "
	if ce.ctrlFieldCursor == 2 {
		devicePrefix = "  > "
	}
	devName := cd.DeviceName
	if devName == "" {
		devName = "[none]"
	}
	lines = append(lines, devicePrefix+theme.Secondary.Render("Device: ")+arrowL+theme.Primary.Render(devName)+arrowR)

	return strings.Join(lines, "\n")
}

// viewStatus renders the status bar below the form
func (ce *ConfigEditor) viewStatus(theme Theme) string {
	var parts []string
	if ce.dirty {
		parts = append(parts, theme.Error.Render("[unsaved changes]"))
	}
	if ce.statusMsg != "" {
		parts = append(parts, theme.Success.Render(ce.statusMsg))
	}
	if len(parts) > 0 {
		return "\n" + strings.Join(parts, "  ")
	}
	return ""
}

// ConfigHelpKeys returns context-appropriate help text
func (ce *ConfigEditor) ConfigHelpKeys(theme Theme) string {
	switch ce.mode {
	case ConfigModeList:
		parts := []string{
			theme.HelpKey.Render("enter") + " " + theme.HelpDesc.Render("edit"),
			theme.HelpKey.Render("a") + " " + theme.HelpDesc.Render("add"),
			theme.HelpKey.Render("d") + " " + theme.HelpDesc.Render("delete"),
		}
		if ce.dirty {
			parts = append(parts, theme.HelpKey.Render("ctrl+s")+" "+theme.HelpDesc.Render("save"))
		}
		return strings.Join(parts, "  ")
	case ConfigModeEditLight, ConfigModeEditButton:
		parts := []string{
			theme.HelpKey.Render("enter") + " " + theme.HelpDesc.Render("edit"),
			theme.HelpKey.Render("space") + " " + theme.HelpDesc.Render("toggle"),
			theme.HelpKey.Render("esc") + " " + theme.HelpDesc.Render("back"),
		}
		if ce.mode == ConfigModeEditButton {
			parts = append(parts,
				theme.HelpKey.Render("a")+" "+theme.HelpDesc.Render("add ctrl"),
				theme.HelpKey.Render("d")+" "+theme.HelpDesc.Render("del ctrl"),
			)
		}
		if ce.dirty {
			parts = append(parts, theme.HelpKey.Render("ctrl+s")+" "+theme.HelpDesc.Render("save"))
		}
		return strings.Join(parts, "  ")
	}
	return ""
}
