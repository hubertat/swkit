package tui

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/hubertat/swkit/app"
)

// configPageStep is how many items a page up/down jump moves the list cursor.
const configPageStep = 10

// ConfigMode represents the current mode of the config editor
type ConfigMode int

const (
	ConfigModeList ConfigMode = iota
	ConfigModeEditLight
	ConfigModeEditDimmableLight
	ConfigModeEditOutlet
	ConfigModeEditButton
	ConfigModeEditScene        // edit a scene: name + list of states
	ConfigModeEditSceneState   // edit one state of a scene: name + list of actions
	ConfigModeAddSelector      // inline device type picker when pressing 'a'
	ConfigModeIoPicker         // inline IO point browser for IO field assignment
	ConfigModeCtrlWizardDevice // wizard step 1: pick target output device
	ConfigModeCtrlWizardEvent  // wizard step 2: pick button + event
	ConfigModeClearSelect      // clear step 1: choose what to clear
	ConfigModeClearConfirm     // clear step 2: confirm the clear action
)

// clearOption defines a type of clear operation
type clearOption int

const (
	clearAll       clearOption = iota // clear all devices and relations
	clearLights                       // clear only lights
	clearOutlets                      // clear only outlets
	clearButtons                      // clear only buttons
	clearRelations                    // clear all control relations from buttons
)

var clearOptions = []struct {
	label  string
	option clearOption
}{
	{label: "All devices and relations", option: clearAll},
	{label: IconLight + " Lights only", option: clearLights},
	{label: IconOutlet + " Outlets only", option: clearOutlets},
	{label: IconButton + " Buttons only", option: clearButtons},
	{label: "Control relations only", option: clearRelations},
}

// configListItemType identifies what kind of item is in the list
type configListItemType int

const (
	configItemLight configListItemType = iota
	configItemDimmableLight
	configItemOutlet
	configItemButton
	configItemScene
)

// configListItem is an entry in the flattened config list
type configListItem struct {
	itemType configListItemType
	index    int    // index within the Lights or Buttons array
	name     string // display name
}

// addableDeviceType defines a device type that can be added via the type selector
type addableDeviceType struct {
	label    string
	itemType configListItemType
}

var addableDeviceTypes = []addableDeviceType{
	{label: IconLight + " Light", itemType: configItemLight},
	{label: IconLight + " Dimmable Light", itemType: configItemDimmableLight},
	{label: IconOutlet + " Outlet", itemType: configItemOutlet},
	{label: IconButton + " Button", itemType: configItemButton},
	{label: IconScene + " Scene", itemType: configItemScene},
}

// ConfigSaveMsg is sent when config is saved
type ConfigSaveMsg struct {
	Error error
}

// ConfigEditor is the config editing TUI component
type ConfigEditor struct {
	provider app.ConfigProvider
	config   app.EditableConfig
	// revision is the provider's Revision() paired with config at the
	// moment it was loaded (NewConfigEditor or Reload). Save passes it to
	// SaveConfigIfRevision so a concurrent/out-of-band change to the config
	// underneath this editor is detected instead of silently clobbered.
	revision string
	// saveConflict is set when the last Save failed with
	// app.ErrConfigRevisionMismatch: the provider's config moved on since
	// this editor loaded it, so a plain retry with the same revision would
	// only fail again. tui.go uses this to change what a repeated
	// Ctrl+R/Ctrl+S press does - see the ConfigSaveMsg handler and
	// discardAndReload.
	saveConflict bool
	dirty        bool
	mode         ConfigMode

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

	// Scene sub-editor (scene -> states -> actions)
	sceneStateIndex    int  // which state of the current scene is being edited
	sceneActionEditing bool // inline action editor active
	sceneActionField   int  // 0=action, 1=device, 2=level

	// Type selector state (ConfigModeAddSelector)
	addTypeCursor int

	// IO picker state (ConfigModeIoPicker)
	ioPoints         []app.IoPointDebugState
	ioDisplayNames   map[string]string // session-only names keyed by "driver|type|index"
	ioPickerFilter   string            // "input" or "output"
	ioPickerCursor   int
	ioPickerField    int // field index to populate (currently always 1)
	modeBeforePicker ConfigMode

	// Control-by-relation wizard (ConfigModeCtrlWizardDevice / ConfigModeCtrlWizardEvent)
	wizardTargetDeviceName string
	wizardTargetAction     string // "toggle", "on", "off", "brightness_up", etc.
	wizardTargetLevel      int    // brightness step/target for brightness verbs
	wizardDeviceCursor     int
	wizardEventCursor      int
	deviceStates           []app.DeviceState // updated by SetDeviceStates()

	// Clear dialog state
	clearCursor int         // cursor for clear option selection
	clearChoice clearOption // selected clear option for confirmation

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
		// Revision is read before the config snapshot so the revision can
		// only ever be as-old-or-older than the snapshot it is paired with
		// (a save landing in between just makes the eventual Save fail its
		// conflict check and ask the user to reload, rather than pairing a
		// snapshot with an already-newer revision and risking a silent
		// overwrite) - same reasoning as server/config_edit.go's
		// handleConfigEditGet.
		ce.revision = provider.Revision()
		ce.config = provider.GetEditableConfig()
		ce.rebuildItems()
	}

	return ce
}

// SetIoPoints updates the IO points available for the IO picker
func (ce *ConfigEditor) SetIoPoints(pts []app.IoPointDebugState) {
	ce.ioPoints = pts
}

// SetIoDisplayNames updates custom display names for IO points.
func (ce *ConfigEditor) SetIoDisplayNames(names map[string]string) {
	if len(names) == 0 {
		ce.ioDisplayNames = nil
		return
	}
	ce.ioDisplayNames = make(map[string]string, len(names))
	for k, v := range names {
		ce.ioDisplayNames[k] = v
	}
}

// SetDeviceStates updates device states used by the control-by-relation wizard.
func (ce *ConfigEditor) SetDeviceStates(devices []app.DeviceState) {
	ce.deviceStates = devices
}

// wizardOutputDevices returns devices that can be controlled (lights, color lights, outlets).
func (ce *ConfigEditor) wizardOutputDevices() []app.DeviceState {
	var result []app.DeviceState
	for _, d := range ce.deviceStates {
		if d.Type == app.DeviceTypeLight || d.Type == app.DeviceTypeColorLight || d.Type == app.DeviceTypeDimmableLight || d.Type == app.DeviceTypeOutlet {
			result = append(result, d)
		}
	}
	return result
}

// wizardEventList returns buttons sorted by LastEventTime (newest first, zero-time at bottom).
func (ce *ConfigEditor) wizardEventList() []app.DeviceState {
	var buttons []app.DeviceState
	for _, d := range ce.deviceStates {
		if d.Type == app.DeviceTypeButton {
			buttons = append(buttons, d)
		}
	}
	sort.SliceStable(buttons, func(i, j int) bool {
		ai := buttons[i].LastEventTime
		aj := buttons[j].LastEventTime
		if ai.IsZero() {
			return false
		}
		if aj.IsZero() {
			return true
		}
		return ai.After(aj)
	})
	return buttons
}

// IsDirty returns true if there are unsaved changes
func (ce *ConfigEditor) IsDirty() bool {
	return ce.dirty
}

// markDirty marks the editor dirty for a new edit. If a save previously
// failed with a revision conflict (saveConflict), that flag is cleared: the
// user chose to keep editing rather than respond to the conflict, so the
// next Ctrl+S/Ctrl+R should attempt an ordinary save again (see tui.go)
// instead of immediately reopening the discard-confirmation prompt, which
// would be a non sequitur right after unrelated new edits. If the same
// stale revision is still the problem, SaveConfigIfRevision simply reports
// the conflict again and saveConflict gets re-armed from there.
func (ce *ConfigEditor) markDirty() {
	ce.dirty = true
	ce.saveConflict = false
}

// Reload refreshes ce.config and ce.revision from the provider. This is
// distinct from TriggerReload: TriggerReload asks the *application* to
// reload the whole SwKit instance (drivers included) from the config file
// on disk, which happens asynchronously off in main.go's reload loop and is
// invisible to this editor; Reload only refreshes this editor's own
// in-memory snapshot of whatever the provider currently holds, so the
// revision it tracks matches what it displays again and a subsequent Save
// stops being rejected as stale.
//
// If the editor is dirty (unsaved edits in progress), Reload is a
// deliberate no-op: overwriting ce.config here would silently discard those
// edits with no way to get them back. Losing a stale revision - the save
// just fails once more with ErrConfigRevisionMismatch and says so - is far
// less surprising than losing typed-in edits, so callers that truly want to
// discard dirty edits must go through discardAndReload instead, which makes
// that intent explicit.
func (ce *ConfigEditor) Reload() {
	if ce.provider == nil || ce.dirty {
		return
	}
	ce.revision = ce.provider.Revision()
	ce.config = ce.provider.GetEditableConfig()
	ce.saveConflict = false
	ce.rebuildItems()
}

// discardAndReload discards any in-progress edits and refreshes from the
// provider. Unlike Reload, it does not refuse a dirty editor - it is only
// ever called (see tui.go) after the user has already been told a save hit
// a revision conflict and has pressed the reload key again anyway, which is
// treated as their explicit confirmation that they want the latest config
// rather than their unsaved edits.
func (ce *ConfigEditor) discardAndReload() {
	ce.dirty = false
	ce.Reload()
}

// HasSaveConflict reports whether the last Save failed because the
// provider's config changed underneath this editor (see saveConflict).
func (ce *ConfigEditor) HasSaveConflict() bool {
	return ce.saveConflict
}

// Save persists the current config, failing with app.ErrConfigRevisionMismatch
// (via SaveConfigIfRevision) if the provider's config changed since this
// editor loaded it - see the revision field.
func (ce *ConfigEditor) Save() tea.Cmd {
	if ce.provider == nil {
		return nil
	}
	config := ce.config
	revision := ce.revision
	return func() tea.Msg {
		err := ce.provider.SaveConfigIfRevision(config, revision)
		return ConfigSaveMsg{Error: err}
	}
}

// TriggerReload sends a reload signal through the config provider if it supports it.
func (ce *ConfigEditor) TriggerReload() {
	if ce.provider == nil {
		return
	}
	if tr, ok := ce.provider.(interface{ TriggerReload() }); ok {
		tr.TriggerReload()
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
	for i, dl := range ce.config.DimmableLights {
		ce.items = append(ce.items, configListItem{
			itemType: configItemDimmableLight,
			index:    i,
			name:     dl.Name,
		})
	}
	for i, o := range ce.config.Outlets {
		ce.items = append(ce.items, configListItem{
			itemType: configItemOutlet,
			index:    i,
			name:     o.Name,
		})
	}
	for i, b := range ce.config.Buttons {
		ce.items = append(ce.items, configListItem{
			itemType: configItemButton,
			index:    i,
			name:     b.Name,
		})
	}
	for i, s := range ce.config.Scenes {
		ce.items = append(ce.items, configListItem{
			itemType: configItemScene,
			index:    i,
			name:     s.Name,
		})
	}
}

// lightFieldCount returns the number of editable fields for a Light
func lightFieldCount() int { return 3 } // Name, DigitalOutName, DisableHomekit

// dimmableLightFieldCount returns the number of editable fields for a DimmableLight
func dimmableLightFieldCount() int { return 5 } // Name, DigitalOutName, AnalogOutName, DefaultSetpoint, DisableHomekit

// outletFieldCount returns the number of editable fields for an Outlet
func outletFieldCount() int { return 3 } // Name, DigitalOutName, DisableHomekit

// buttonBaseFieldCount returns the number of base fields for a Button (before ControlDevices)
func buttonBaseFieldCount() int { return 3 } // Name, EventInputName, DisableHomekit

// Update handles messages for the config editor
func (ce *ConfigEditor) Update(msg tea.Msg) tea.Cmd {
	switch ce.mode {
	case ConfigModeList:
		return ce.updateList(msg)
	case ConfigModeEditLight:
		return ce.updateEditLight(msg)
	case ConfigModeEditDimmableLight:
		return ce.updateEditDimmableLight(msg)
	case ConfigModeEditOutlet:
		return ce.updateEditOutlet(msg)
	case ConfigModeEditButton:
		return ce.updateEditButton(msg)
	case ConfigModeEditScene:
		return ce.updateEditScene(msg)
	case ConfigModeEditSceneState:
		return ce.updateEditSceneState(msg)
	case ConfigModeAddSelector:
		keyMsg, ok := msg.(tea.KeyMsg)
		if !ok {
			return nil
		}
		return ce.updateAddSelector(keyMsg)
	case ConfigModeIoPicker:
		keyMsg, ok := msg.(tea.KeyMsg)
		if !ok {
			return nil
		}
		return ce.updateIoPicker(keyMsg)
	case ConfigModeCtrlWizardDevice:
		keyMsg, ok := msg.(tea.KeyMsg)
		if !ok {
			return nil
		}
		return ce.updateCtrlWizardDevice(keyMsg)
	case ConfigModeCtrlWizardEvent:
		keyMsg, ok := msg.(tea.KeyMsg)
		if !ok {
			return nil
		}
		return ce.updateCtrlWizardEvent(keyMsg)
	case ConfigModeClearSelect:
		keyMsg, ok := msg.(tea.KeyMsg)
		if !ok {
			return nil
		}
		return ce.updateClearSelect(keyMsg)
	case ConfigModeClearConfirm:
		keyMsg, ok := msg.(tea.KeyMsg)
		if !ok {
			return nil
		}
		return ce.updateClearConfirm(keyMsg)
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
	case "pgup", "ctrl+u", "ctrl+b":
		ce.cursor -= configPageStep
		if ce.cursor < 0 {
			ce.cursor = 0
		}
	case "pgdown", "ctrl+d", "ctrl+f":
		ce.cursor += configPageStep
		if ce.cursor > len(ce.items)-1 {
			ce.cursor = len(ce.items) - 1
		}
		if ce.cursor < 0 {
			ce.cursor = 0
		}
	case "enter":
		if ce.cursor >= 0 && ce.cursor < len(ce.items) {
			item := ce.items[ce.cursor]
			ce.fieldCursor = 0
			ce.editing = false
			ce.ctrlCursor = 0
			ce.ctrlEditing = false
			switch item.itemType {
			case configItemLight:
				ce.mode = ConfigModeEditLight
			case configItemDimmableLight:
				ce.mode = ConfigModeEditDimmableLight
			case configItemOutlet:
				ce.mode = ConfigModeEditOutlet
			case configItemButton:
				ce.mode = ConfigModeEditButton
			case configItemScene:
				ce.sceneActionEditing = false
				ce.mode = ConfigModeEditScene
			}
		}
	case "a":
		ce.addTypeCursor = 0
		ce.mode = ConfigModeAddSelector
	case "w":
		devices := ce.wizardOutputDevices()
		if len(devices) == 0 {
			ce.statusMsg = "No controllable devices configured"
			return nil
		}
		ce.wizardTargetAction = "toggle"
		ce.wizardTargetLevel = 0
		ce.wizardDeviceCursor = 0
		// Pre-position cursor if selected list item is a controllable output device
		if ce.cursor < len(ce.items) {
			item := ce.items[ce.cursor]
			if item.itemType == configItemLight || item.itemType == configItemDimmableLight || item.itemType == configItemOutlet {
				for i, d := range devices {
					if d.Name == item.name {
						ce.wizardDeviceCursor = i
						break
					}
				}
			}
		}
		ce.mode = ConfigModeCtrlWizardDevice
	case "d", "delete":
		return ce.deleteItem()
	case "c":
		if len(ce.items) > 0 {
			ce.clearCursor = 0
			ce.mode = ConfigModeClearSelect
		}
	}

	return nil
}

// setCursorToItem positions the list cursor on the item matching (itemType, index).
// The new item is not necessarily last in the flattened list (lights and dimmable
// lights are rendered before buttons), so the cursor must be looked up rather than
// assumed to be len(items)-1.
func (ce *ConfigEditor) setCursorToItem(itemType configListItemType, index int) {
	for i, it := range ce.items {
		if it.itemType == itemType && it.index == index {
			ce.cursor = i
			return
		}
	}
	if len(ce.items) > 0 {
		ce.cursor = len(ce.items) - 1
	} else {
		ce.cursor = 0
	}
}

// addDeviceOfType adds a new device of the given type and opens the edit view
// with the Name field focused so the user can enter a name immediately.
func (ce *ConfigEditor) addDeviceOfType(itemType configListItemType) tea.Cmd {
	ce.markDirty()
	ce.fieldCursor = 0
	switch itemType {
	case configItemLight:
		ce.config.Lights = append(ce.config.Lights, app.LightEditConfig{})
		ce.rebuildItems()
		ce.setCursorToItem(configItemLight, len(ce.config.Lights)-1)
		ce.mode = ConfigModeEditLight
		return ce.startEditingLightField(&ce.config.Lights[len(ce.config.Lights)-1])
	case configItemDimmableLight:
		ce.config.DimmableLights = append(ce.config.DimmableLights, app.DimmableLightEditConfig{})
		ce.rebuildItems()
		ce.setCursorToItem(configItemDimmableLight, len(ce.config.DimmableLights)-1)
		ce.mode = ConfigModeEditDimmableLight
		return ce.startEditingDimmableLightField(&ce.config.DimmableLights[len(ce.config.DimmableLights)-1])
	case configItemOutlet:
		ce.config.Outlets = append(ce.config.Outlets, app.OutletEditConfig{})
		ce.rebuildItems()
		ce.setCursorToItem(configItemOutlet, len(ce.config.Outlets)-1)
		ce.mode = ConfigModeEditOutlet
		return ce.startEditingOutletField(&ce.config.Outlets[len(ce.config.Outlets)-1])
	case configItemButton:
		ce.config.Buttons = append(ce.config.Buttons, app.ButtonEditConfig{})
		ce.rebuildItems()
		ce.setCursorToItem(configItemButton, len(ce.config.Buttons)-1)
		ce.mode = ConfigModeEditButton
		return ce.startEditingButtonField(&ce.config.Buttons[len(ce.config.Buttons)-1])
	case configItemScene:
		ce.config.Scenes = append(ce.config.Scenes, app.SceneEditConfig{})
		ce.rebuildItems()
		ce.setCursorToItem(configItemScene, len(ce.config.Scenes)-1)
		ce.sceneActionEditing = false
		ce.mode = ConfigModeEditScene
		return ce.startEditingSceneField(&ce.config.Scenes[len(ce.config.Scenes)-1])
	}
	return nil
}

// updateAddSelector handles keys in the add-type selector mode
func (ce *ConfigEditor) updateAddSelector(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "up", "k":
		if ce.addTypeCursor > 0 {
			ce.addTypeCursor--
		}
	case "down", "j":
		if ce.addTypeCursor < len(addableDeviceTypes)-1 {
			ce.addTypeCursor++
		}
	case "enter":
		return ce.addDeviceOfType(addableDeviceTypes[ce.addTypeCursor].itemType)
	case "esc":
		ce.mode = ConfigModeList
	}
	return nil
}

// deleteItem removes the currently selected item
func (ce *ConfigEditor) deleteItem() tea.Cmd {
	if ce.cursor < 0 || ce.cursor >= len(ce.items) {
		return nil
	}

	item := ce.items[ce.cursor]
	switch item.itemType {
	case configItemLight:
		ce.config.Lights = append(ce.config.Lights[:item.index], ce.config.Lights[item.index+1:]...)
		ce.refreshOutputDeviceNames()
	case configItemDimmableLight:
		ce.config.DimmableLights = append(ce.config.DimmableLights[:item.index], ce.config.DimmableLights[item.index+1:]...)
		ce.refreshOutputDeviceNames()
	case configItemOutlet:
		ce.config.Outlets = append(ce.config.Outlets[:item.index], ce.config.Outlets[item.index+1:]...)
		ce.refreshOutputDeviceNames()
	case configItemScene:
		ce.config.Scenes = append(ce.config.Scenes[:item.index], ce.config.Scenes[item.index+1:]...)
		ce.refreshOutputDeviceNames()
	default:
		ce.config.Buttons = append(ce.config.Buttons[:item.index], ce.config.Buttons[item.index+1:]...)
	}

	ce.markDirty()
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
	for _, dl := range ce.config.DimmableLights {
		ce.config.OutputDeviceNames = append(ce.config.OutputDeviceNames, dl.Name)
	}
	for _, o := range ce.config.Outlets {
		ce.config.OutputDeviceNames = append(ce.config.OutputDeviceNames, o.Name)
	}
	// Scenes are Controllable too, so they are valid control/action targets.
	for _, s := range ce.config.Scenes {
		ce.config.OutputDeviceNames = append(ce.config.OutputDeviceNames, s.Name)
	}
}

// updateEditLight handles keys in light edit mode
func (ce *ConfigEditor) updateEditLight(msg tea.Msg) tea.Cmd {
	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}

	if ce.cursor < 0 || ce.cursor >= len(ce.items) {
		ce.mode = ConfigModeList
		return nil
	}
	item := ce.items[ce.cursor]
	if item.itemType != configItemLight || item.index >= len(ce.config.Lights) {
		ce.mode = ConfigModeList
		return nil
	}
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
	case "p":
		if ce.fieldCursor == 1 { // DigitalOutName field
			ce.openIoPicker("output", 1)
		}
	case " ":
		if ce.fieldCursor == 2 { // DisableHomekit
			light.DisableHomekit = !light.DisableHomekit
			ce.markDirty()
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
		ce.markDirty()
	}
	return nil
}

// updateEditDimmableLight handles keys in dimmable light edit mode
func (ce *ConfigEditor) updateEditDimmableLight(msg tea.Msg) tea.Cmd {
	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}

	if ce.cursor < 0 || ce.cursor >= len(ce.items) {
		ce.mode = ConfigModeList
		return nil
	}
	item := ce.items[ce.cursor]
	if item.itemType != configItemDimmableLight || item.index >= len(ce.config.DimmableLights) {
		ce.mode = ConfigModeList
		return nil
	}
	dl := &ce.config.DimmableLights[item.index]

	// If text input is active, handle it
	if ce.editing {
		return ce.handleDimmableTextEditing(keyMsg, dl)
	}

	switch keyMsg.String() {
	case "up", "k":
		if ce.fieldCursor > 0 {
			ce.fieldCursor--
		}
	case "down", "j":
		if ce.fieldCursor < dimmableLightFieldCount()-1 {
			ce.fieldCursor++
		}
	case "enter":
		return ce.startEditingDimmableLightField(dl)
	case "p":
		switch ce.fieldCursor {
		case 1: // DigitalOutName field
			ce.openIoPicker("output", 1)
		case 2: // AnalogOutName field
			ce.openIoPicker("analog_output", 2)
		}
	case " ":
		if ce.fieldCursor == 4 { // DisableHomekit
			dl.DisableHomekit = !dl.DisableHomekit
			ce.markDirty()
		}
	case "esc":
		ce.mode = ConfigModeList
	}

	return nil
}

// startEditingDimmableLightField begins editing the selected dimmable light field
func (ce *ConfigEditor) startEditingDimmableLightField(dl *app.DimmableLightEditConfig) tea.Cmd {
	switch ce.fieldCursor {
	case 0: // Name
		ce.textInput.SetValue(dl.Name)
		ce.textInput.Focus()
		ce.editing = true
		return textinput.Blink
	case 1: // DigitalOutName
		ce.textInput.SetValue(dl.DigitalOutName)
		ce.textInput.Focus()
		ce.editing = true
		return textinput.Blink
	case 2: // AnalogOutName
		ce.textInput.SetValue(dl.AnalogOutName)
		ce.textInput.Focus()
		ce.editing = true
		return textinput.Blink
	case 3: // DefaultSetpoint
		ce.textInput.SetValue(strconv.Itoa(dl.DefaultSetpoint))
		ce.textInput.Focus()
		ce.editing = true
		return textinput.Blink
	case 4: // DisableHomekit (toggle)
		dl.DisableHomekit = !dl.DisableHomekit
		ce.markDirty()
	}
	return nil
}

// handleDimmableTextEditing handles keystrokes when editing a dimmable light text field
func (ce *ConfigEditor) handleDimmableTextEditing(msg tea.KeyMsg, dl *app.DimmableLightEditConfig) tea.Cmd {
	switch msg.Type {
	case tea.KeyEnter:
		value := strings.TrimSpace(ce.textInput.Value())
		switch ce.fieldCursor {
		case 0:
			if value != "" {
				dl.Name = value
				ce.markDirty()
				ce.rebuildItems()
			}
		case 1:
			dl.DigitalOutName = value
			ce.markDirty()
		case 2:
			dl.AnalogOutName = value
			ce.markDirty()
		case 3:
			if v, err := strconv.Atoi(value); err == nil {
				if v < 0 {
					v = 0
				} else if v > 100 {
					v = 100
				}
				dl.DefaultSetpoint = v
				ce.markDirty()
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

// updateEditOutlet handles keys in outlet edit mode
func (ce *ConfigEditor) updateEditOutlet(msg tea.Msg) tea.Cmd {
	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}

	if ce.cursor < 0 || ce.cursor >= len(ce.items) {
		ce.mode = ConfigModeList
		return nil
	}
	item := ce.items[ce.cursor]
	if item.itemType != configItemOutlet || item.index >= len(ce.config.Outlets) {
		ce.mode = ConfigModeList
		return nil
	}
	outlet := &ce.config.Outlets[item.index]

	if ce.editing {
		return ce.handleOutletTextEditing(keyMsg, outlet)
	}

	switch keyMsg.String() {
	case "up", "k":
		if ce.fieldCursor > 0 {
			ce.fieldCursor--
		}
	case "down", "j":
		if ce.fieldCursor < outletFieldCount()-1 {
			ce.fieldCursor++
		}
	case "enter":
		return ce.startEditingOutletField(outlet)
	case "p":
		if ce.fieldCursor == 1 { // DigitalOutName field
			ce.openIoPicker("output", 1)
		}
	case " ":
		if ce.fieldCursor == 2 { // DisableHomekit
			outlet.DisableHomekit = !outlet.DisableHomekit
			ce.markDirty()
		}
	case "esc":
		ce.mode = ConfigModeList
	}

	return nil
}

// startEditingOutletField begins editing the selected outlet field
func (ce *ConfigEditor) startEditingOutletField(outlet *app.OutletEditConfig) tea.Cmd {
	switch ce.fieldCursor {
	case 0: // Name
		ce.textInput.SetValue(outlet.Name)
		ce.textInput.Focus()
		ce.editing = true
		return textinput.Blink
	case 1: // DigitalOutName
		ce.textInput.SetValue(outlet.DigitalOutName)
		ce.textInput.Focus()
		ce.editing = true
		return textinput.Blink
	case 2: // DisableHomekit (toggle)
		outlet.DisableHomekit = !outlet.DisableHomekit
		ce.markDirty()
	}
	return nil
}

// handleOutletTextEditing handles keystrokes when editing an outlet text field
func (ce *ConfigEditor) handleOutletTextEditing(msg tea.KeyMsg, outlet *app.OutletEditConfig) tea.Cmd {
	switch msg.Type {
	case tea.KeyEnter:
		value := strings.TrimSpace(ce.textInput.Value())
		switch ce.fieldCursor {
		case 0:
			if value != "" {
				outlet.Name = value
				ce.markDirty()
				ce.rebuildItems()
			}
		case 1:
			outlet.DigitalOutName = value
			ce.markDirty()
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
					ce.markDirty()
					ce.rebuildItems()
				}
			case 1:
				light.DigitalOutName = value
				ce.markDirty()
			}
		} else if ce.mode == ConfigModeEditButton && button != nil {
			switch ce.fieldCursor {
			case 0:
				if value != "" {
					button.Name = value
					ce.markDirty()
					ce.rebuildItems()
				}
			case 1:
				button.EventInputName = value
				ce.markDirty()
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

	if ce.cursor < 0 || ce.cursor >= len(ce.items) {
		ce.mode = ConfigModeList
		return nil
	}
	item := ce.items[ce.cursor]
	if item.itemType != configItemButton || item.index >= len(ce.config.Buttons) {
		ce.mode = ConfigModeList
		return nil
	}
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
	case "p":
		if ce.fieldCursor == 1 { // EventInputName field
			ce.openIoPicker("input", 1)
		}
	case " ":
		if ce.fieldCursor == 2 { // DisableHomekit
			button.DisableHomekit = !button.DisableHomekit
			ce.markDirty()
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
			ce.markDirty()
			ce.fieldCursor = buttonBaseFieldCount() + len(button.ControlDevices) - 1
		}
	case "d", "delete":
		ctrlIdx := ce.fieldCursor - buttonBaseFieldCount()
		if ctrlIdx >= 0 && ctrlIdx < len(button.ControlDevices) {
			button.ControlDevices = append(button.ControlDevices[:ctrlIdx], button.ControlDevices[ctrlIdx+1:]...)
			ce.markDirty()
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
		ce.markDirty()
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
		if ce.ctrlFieldCursor < 3 {
			ce.ctrlFieldCursor++
		}
	case "left", "h":
		ce.cycleCtrlField(ctrl, -1)
		ce.markDirty()
	case "right", "l":
		ce.cycleCtrlField(ctrl, 1)
		ce.markDirty()
	case "+", "=":
		if app.IsBrightnessVerb(ctrl.Action) {
			ctrl.Level = adjustBrightnessLevel(ctrl.Level, 1)
			ce.markDirty()
		}
	case "-", "_":
		if app.IsBrightnessVerb(ctrl.Action) {
			ctrl.Level = adjustBrightnessLevel(ctrl.Level, -1)
			ce.markDirty()
		}
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
		ctrl.Action = cycleOption(app.AllActionVerbs(), ctrl.Action, direction)
	case 2: // DeviceName
		devices := ce.config.OutputDeviceNames
		if len(devices) > 0 {
			ctrl.DeviceName = cycleOption(devices, ctrl.DeviceName, direction)
		}
	case 3: // Level/step (only meaningful for brightness-family verbs)
		if app.IsBrightnessVerb(ctrl.Action) {
			ctrl.Level = adjustBrightnessLevel(ctrl.Level, direction)
		}
	}
}

// openIoPicker transitions to IO picker mode for the given filter type and field index
func (ce *ConfigEditor) openIoPicker(filterType string, fieldIdx int) {
	ce.ioPickerFilter = filterType
	ce.ioPickerField = fieldIdx
	ce.ioPickerCursor = 0
	ce.modeBeforePicker = ce.mode
	ce.mode = ConfigModeIoPicker
}

// filteredIoPoints returns IO points matching the current picker filter,
// sorted by most recent activity first (LastEvent or LastChanged), with
// inactive points retaining their original order at the bottom.
func (ce *ConfigEditor) filteredIoPoints() []app.IoPointDebugState {
	var pts []app.IoPointDebugState
	for _, pt := range ce.ioPoints {
		if pt.Type == ce.ioPickerFilter {
			pts = append(pts, pt)
		}
	}
	sort.SliceStable(pts, func(i, j int) bool {
		ai := pts[i].LastEvent
		if pts[i].LastChanged.After(ai) {
			ai = pts[i].LastChanged
		}
		aj := pts[j].LastEvent
		if pts[j].LastChanged.After(aj) {
			aj = pts[j].LastChanged
		}
		if ai.IsZero() {
			return false
		}
		if aj.IsZero() {
			return true
		}
		return ai.After(aj)
	})
	return pts
}

// ioPointDisplayKey returns the key format used for session IO names.
func ioPointDisplayKey(pt app.IoPointDebugState) string {
	return pt.DriverName + "|" + pt.Type + "|" + strconv.Itoa(pt.Index)
}

// ioPickerDisplayName returns the label shown in picker rows for a point.
func (ce *ConfigEditor) ioPickerDisplayName(pt app.IoPointDebugState) string {
	custom := strings.TrimSpace(ce.ioDisplayNames[ioPointDisplayKey(pt)])
	if custom == "" {
		return pt.Name
	}
	return custom + " [" + pt.Name + "]"
}

// applyIoSelection writes the selected IO ID into the appropriate config field
func (ce *ConfigEditor) applyIoSelection(ioId string) {
	if ce.cursor < 0 || ce.cursor >= len(ce.items) {
		return
	}
	item := ce.items[ce.cursor]
	switch item.itemType {
	case configItemLight:
		if ce.ioPickerField == 1 {
			ce.config.Lights[item.index].DigitalOutName = ioId
			ce.markDirty()
		}
	case configItemDimmableLight:
		switch ce.ioPickerField {
		case 1:
			ce.config.DimmableLights[item.index].DigitalOutName = ioId
			ce.markDirty()
		case 2:
			ce.config.DimmableLights[item.index].AnalogOutName = ioId
			ce.markDirty()
		}
	case configItemOutlet:
		if ce.ioPickerField == 1 {
			ce.config.Outlets[item.index].DigitalOutName = ioId
			ce.markDirty()
		}
	case configItemButton:
		if ce.ioPickerField == 1 {
			ce.config.Buttons[item.index].EventInputName = ioId
			ce.markDirty()
		}
	}
}

func (ce *ConfigEditor) ioPointToIdForCurrentField(pt app.IoPointDebugState) string {
	if ce.cursor >= 0 && ce.cursor < len(ce.items) {
		item := ce.items[ce.cursor]
		switch item.itemType {
		case configItemLight:
			if ce.ioPickerField == 1 {
				return app.IoPointToIdWithType(pt, "d_out")
			}
		case configItemDimmableLight:
			switch ce.ioPickerField {
			case 1:
				return app.IoPointToIdWithType(pt, "d_out")
			case 2:
				return app.IoPointToIdWithType(pt, "a_out")
			}
		case configItemOutlet:
			if ce.ioPickerField == 1 {
				return app.IoPointToIdWithType(pt, "d_out")
			}
		case configItemButton:
			if ce.ioPickerField == 1 {
				return app.IoPointToIdWithType(pt, "push_event")
			}
		}
	}
	return app.IoPointToId(pt)
}

// updateIoPicker handles keys in IO picker mode
func (ce *ConfigEditor) updateIoPicker(msg tea.KeyMsg) tea.Cmd {
	pts := ce.filteredIoPoints()
	switch msg.String() {
	case "up", "k":
		if ce.ioPickerCursor > 0 {
			ce.ioPickerCursor--
		}
	case "down", "j":
		if ce.ioPickerCursor < len(pts)-1 {
			ce.ioPickerCursor++
		}
	case "enter":
		if len(pts) > 0 && ce.ioPickerCursor < len(pts) {
			ce.applyIoSelection(ce.ioPointToIdForCurrentField(pts[ce.ioPickerCursor]))
		}
		ce.mode = ce.modeBeforePicker
	case "t":
		// Toggle the selected output for physical identification (digital outputs
		// flip on/off; analog outputs toggle between 0 and full scale). Inputs
		// cannot be driven, so this is a no-op for the input filter.
		if len(pts) > 0 && ce.ioPickerCursor < len(pts) {
			pt := pts[ce.ioPickerCursor]
			if pt.Type == "output" || pt.Type == "analog_output" {
				return func() tea.Msg {
					return configIoToggleMsg{
						DriverName: pt.DriverName,
						Index:      pt.Index,
						IoType:     pt.Type,
						CurValue:   pt.Value,
						Max:        pt.Max,
					}
				}
			}
		}
	case "esc":
		ce.mode = ce.modeBeforePicker
	}
	return nil
}

// updateCtrlWizardDevice handles keys in wizard step 1 (target device selection)
func (ce *ConfigEditor) updateCtrlWizardDevice(msg tea.KeyMsg) tea.Cmd {
	devices := ce.wizardOutputDevices()
	switch msg.String() {
	case "up", "k":
		if ce.wizardDeviceCursor > 0 {
			ce.wizardDeviceCursor--
		}
	case "down", "j":
		if ce.wizardDeviceCursor < len(devices)-1 {
			ce.wizardDeviceCursor++
		}
	case "t":
		if ce.wizardDeviceCursor < len(devices) {
			name := devices[ce.wizardDeviceCursor].Name
			return func() tea.Msg {
				return configWizardToggleMsg{DeviceName: name}
			}
		}
	case "enter":
		if ce.wizardDeviceCursor < len(devices) {
			ce.wizardTargetDeviceName = devices[ce.wizardDeviceCursor].Name
			ce.wizardEventCursor = 0
			ce.mode = ConfigModeCtrlWizardEvent
		}
	case "esc":
		ce.mode = ConfigModeList
	}
	return nil
}

// updateCtrlWizardEvent handles keys in wizard step 2 (button + event selection)
func (ce *ConfigEditor) updateCtrlWizardEvent(msg tea.KeyMsg) tea.Cmd {
	events := ce.wizardEventList()
	switch msg.String() {
	case "up", "k":
		if ce.wizardEventCursor > 0 {
			ce.wizardEventCursor--
		}
	case "down", "j":
		if ce.wizardEventCursor < len(events)-1 {
			ce.wizardEventCursor++
		}
	case "left", "h":
		ce.wizardTargetAction = cycleOption(app.AllActionVerbs(), ce.wizardTargetAction, -1)
	case "right", "l":
		ce.wizardTargetAction = cycleOption(app.AllActionVerbs(), ce.wizardTargetAction, 1)
	case "+", "=":
		if app.IsBrightnessVerb(ce.wizardTargetAction) {
			ce.wizardTargetLevel = adjustBrightnessLevel(ce.wizardTargetLevel, 1)
		}
	case "-", "_":
		if app.IsBrightnessVerb(ce.wizardTargetAction) {
			ce.wizardTargetLevel = adjustBrightnessLevel(ce.wizardTargetLevel, -1)
		}
	case "enter":
		return ce.confirmWizard(events)
	case "esc":
		ce.mode = ConfigModeCtrlWizardDevice
	}
	return nil
}

// confirmWizard finalizes the wizard and appends a ControlDeviceEdit to the selected button.
func (ce *ConfigEditor) confirmWizard(events []app.DeviceState) tea.Cmd {
	if len(events) == 0 || ce.wizardEventCursor >= len(events) {
		ce.statusMsg = "No button selected"
		ce.mode = ConfigModeList
		return nil
	}

	selected := events[ce.wizardEventCursor]
	eventType := selected.LastEventType
	if eventType == "" {
		eventType = "single_press"
	}

	// Find button by name in config
	buttonIdx := -1
	for i, b := range ce.config.Buttons {
		if b.Name == selected.Name {
			buttonIdx = i
			break
		}
	}
	if buttonIdx == -1 {
		ce.statusMsg = "Button not found: " + selected.Name
		ce.mode = ConfigModeList
		return nil
	}

	newCtrl := app.ControlDeviceEdit{
		EventType:  eventType,
		Action:     ce.wizardTargetAction,
		Level:      ce.wizardTargetLevel,
		DeviceName: ce.wizardTargetDeviceName,
	}
	ce.config.Buttons[buttonIdx].ControlDevices = append(ce.config.Buttons[buttonIdx].ControlDevices, newCtrl)
	ce.markDirty()
	ce.statusMsg = fmt.Sprintf("Added: %s %s → %s → %s", selected.Name, eventType, ce.wizardActionLabel(), ce.wizardTargetDeviceName)
	ce.mode = ConfigModeList
	return nil
}

// updateClearSelect handles keys in clear step 1 (choose what to clear)
func (ce *ConfigEditor) updateClearSelect(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "up", "k":
		if ce.clearCursor > 0 {
			ce.clearCursor--
		}
	case "down", "j":
		if ce.clearCursor < len(clearOptions)-1 {
			ce.clearCursor++
		}
	case "enter":
		ce.clearChoice = clearOptions[ce.clearCursor].option
		ce.mode = ConfigModeClearConfirm
	case "esc":
		ce.mode = ConfigModeList
	}
	return nil
}

// updateClearConfirm handles keys in clear step 2 (confirm action)
func (ce *ConfigEditor) updateClearConfirm(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "y":
		ce.executeClear()
		ce.mode = ConfigModeList
	case "n", "esc":
		ce.mode = ConfigModeClearSelect
	}
	return nil
}

// executeClear performs the selected clear operation
func (ce *ConfigEditor) executeClear() {
	switch ce.clearChoice {
	case clearAll:
		cleared := len(ce.config.Lights) + len(ce.config.DimmableLights) + len(ce.config.Outlets) + len(ce.config.Buttons)
		ce.config.Lights = nil
		ce.config.DimmableLights = nil
		ce.config.Outlets = nil
		ce.config.Buttons = nil
		ce.statusMsg = fmt.Sprintf("Cleared all %d devices", cleared)
	case clearLights:
		cleared := len(ce.config.Lights)
		ce.config.Lights = nil
		ce.statusMsg = fmt.Sprintf("Cleared %d lights", cleared)
	case clearOutlets:
		cleared := len(ce.config.Outlets)
		ce.config.Outlets = nil
		ce.statusMsg = fmt.Sprintf("Cleared %d outlets", cleared)
	case clearButtons:
		cleared := len(ce.config.Buttons)
		ce.config.Buttons = nil
		ce.statusMsg = fmt.Sprintf("Cleared %d buttons", cleared)
	case clearRelations:
		cleared := 0
		for i := range ce.config.Buttons {
			cleared += len(ce.config.Buttons[i].ControlDevices)
			ce.config.Buttons[i].ControlDevices = nil
		}
		ce.statusMsg = fmt.Sprintf("Cleared %d control relations", cleared)
	}
	ce.markDirty()
	ce.refreshOutputDeviceNames()
	ce.rebuildItems()
	ce.cursor = 0
}

// clearOptionDescription returns a description of what the clear option will remove
func (ce *ConfigEditor) clearOptionDescription() string {
	switch ce.clearChoice {
	case clearAll:
		total := len(ce.config.Lights) + len(ce.config.DimmableLights) + len(ce.config.Outlets) + len(ce.config.Buttons)
		relCount := 0
		for _, b := range ce.config.Buttons {
			relCount += len(b.ControlDevices)
		}
		return fmt.Sprintf("%d devices (%d lights, %d dimmable, %d outlets, %d buttons) and %d control relations",
			total, len(ce.config.Lights), len(ce.config.DimmableLights), len(ce.config.Outlets), len(ce.config.Buttons), relCount)
	case clearLights:
		return fmt.Sprintf("%d lights", len(ce.config.Lights))
	case clearOutlets:
		return fmt.Sprintf("%d outlets", len(ce.config.Outlets))
	case clearButtons:
		relCount := 0
		for _, b := range ce.config.Buttons {
			relCount += len(b.ControlDevices)
		}
		return fmt.Sprintf("%d buttons with %d control relations", len(ce.config.Buttons), relCount)
	case clearRelations:
		relCount := 0
		for _, b := range ce.config.Buttons {
			relCount += len(b.ControlDevices)
		}
		return fmt.Sprintf("%d control relations across %d buttons", relCount, len(ce.config.Buttons))
	}
	return ""
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

// View renders the config editor. height is the number of lines available for
// the content (negative when the terminal size is not yet known); it is used to
// keep the device list scrollable rather than overflowing the screen.
func (ce *ConfigEditor) View(theme Theme, height int) string {
	switch ce.mode {
	case ConfigModeList:
		return ce.viewList(theme, height)
	case ConfigModeEditLight:
		return ce.viewEditLight(theme)
	case ConfigModeEditDimmableLight:
		return ce.viewEditDimmableLight(theme)
	case ConfigModeEditOutlet:
		return ce.viewEditOutlet(theme)
	case ConfigModeEditButton:
		return ce.viewEditButton(theme)
	case ConfigModeEditScene:
		return ce.viewEditScene(theme)
	case ConfigModeEditSceneState:
		return ce.viewEditSceneState(theme)
	case ConfigModeAddSelector:
		return ce.viewAddSelector(theme)
	case ConfigModeIoPicker:
		return ce.viewIoPicker(theme)
	case ConfigModeCtrlWizardDevice:
		return ce.viewCtrlWizardDevice(theme)
	case ConfigModeCtrlWizardEvent:
		return ce.viewCtrlWizardEvent(theme)
	case ConfigModeClearSelect:
		return ce.viewClearSelect(theme)
	case ConfigModeClearConfirm:
		return ce.viewClearConfirm(theme)
	}
	return ""
}

// viewList renders the config item list
func (ce *ConfigEditor) viewList(theme Theme, height int) string {
	if len(ce.items) == 0 {
		content := theme.Muted.Render("No devices configured") + "\n"
		content += theme.Muted.Render("Press 'a' to add a new item")
		return theme.Box.Render(content) + ce.viewStatus(theme)
	}

	var lines []string
	focusLine := 0 // line index of the currently selected item

	// Section: Lights
	if len(ce.config.Lights) > 0 {
		lines = append(lines, theme.BoxTitle.Render("Lights"))
		for i, l := range ce.config.Lights {
			prefix := "   "
			style := theme.ListItem
			if i == ce.cursor {
				prefix = " ▶ "
				style = theme.ListItemSelected
				focusLine = len(lines)
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

	// Section: Dimmable Lights
	if len(ce.config.DimmableLights) > 0 {
		if len(ce.config.Lights) > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, theme.BoxTitle.Render("Dimmable Lights"))
		for i, dl := range ce.config.DimmableLights {
			listIdx := len(ce.config.Lights) + i
			prefix := "   "
			style := theme.ListItem
			if listIdx == ce.cursor {
				prefix = " ▶ "
				style = theme.ListItemSelected
				focusLine = len(lines)
			}

			hkText := ""
			if dl.DisableHomekit {
				hkText = theme.Muted.Render(" (no HK)")
			}

			ioText := theme.Secondary.Render(dl.DigitalOutName + " + " + dl.AnalogOutName)
			if dl.DigitalOutName == "" && dl.AnalogOutName == "" {
				ioText = theme.Muted.Render("[no IO]")
			}

			line := prefix + IconLight + " " + theme.Primary.Render(padRight(dl.Name, 20)) + " " + ioText + hkText
			lines = append(lines, style.Render(line))
		}
	}

	// Section: Outlets
	if len(ce.config.Outlets) > 0 {
		if len(ce.config.Lights) > 0 || len(ce.config.DimmableLights) > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, theme.BoxTitle.Render("Outlets"))
		for i, o := range ce.config.Outlets {
			listIdx := len(ce.config.Lights) + len(ce.config.DimmableLights) + i
			prefix := "   "
			style := theme.ListItem
			if listIdx == ce.cursor {
				prefix = " ▶ "
				style = theme.ListItemSelected
				focusLine = len(lines)
			}

			hkText := ""
			if o.DisableHomekit {
				hkText = theme.Muted.Render(" (no HK)")
			}

			ioText := theme.Secondary.Render(o.DigitalOutName)
			if o.DigitalOutName == "" {
				ioText = theme.Muted.Render("[no IO]")
			}

			line := prefix + IconOutlet + " " + theme.Primary.Render(padRight(o.Name, 20)) + " " + ioText + hkText
			lines = append(lines, style.Render(line))
		}
	}

	// Separator
	if (len(ce.config.Lights) > 0 || len(ce.config.DimmableLights) > 0 || len(ce.config.Outlets) > 0) && len(ce.config.Buttons) > 0 {
		lines = append(lines, "")
	}

	// Section: Buttons
	if len(ce.config.Buttons) > 0 {
		lines = append(lines, theme.BoxTitle.Render("Buttons"))
		for i, b := range ce.config.Buttons {
			listIdx := len(ce.config.Lights) + len(ce.config.DimmableLights) + len(ce.config.Outlets) + i
			prefix := "   "
			style := theme.ListItem
			if listIdx == ce.cursor {
				prefix = " ▶ "
				style = theme.ListItemSelected
				focusLine = len(lines)
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

	// Section: Scenes
	if len(ce.config.Scenes) > 0 {
		if len(ce.config.Lights) > 0 || len(ce.config.DimmableLights) > 0 || len(ce.config.Outlets) > 0 || len(ce.config.Buttons) > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, theme.BoxTitle.Render("Scenes"))
		base := len(ce.config.Lights) + len(ce.config.DimmableLights) + len(ce.config.Outlets) + len(ce.config.Buttons)
		for i, s := range ce.config.Scenes {
			listIdx := base + i
			prefix := "   "
			style := theme.ListItem
			if listIdx == ce.cursor {
				prefix = " ▶ "
				style = theme.ListItemSelected
				focusLine = len(lines)
			}

			stateCount := theme.Muted.Render(fmt.Sprintf(" [%d states]", len(s.States)))
			line := prefix + IconScene + " " + theme.Primary.Render(padRight(s.Name, 20)) + stateCount
			lines = append(lines, style.Render(line))
		}
	}

	status := ce.viewStatus(theme)

	// Window the list to fit, reserving rows for the box border and the
	// status line(s) below it so the whole view stays on screen.
	rows := height
	if rows >= 0 {
		rows -= 2 // box border
		rows -= lipgloss.Height(status) - 1
		if rows < 1 {
			rows = 1
		}
	}

	content := windowLines(theme, lines, focusLine, rows)
	result := theme.Box.Render(content)
	result += status
	return result
}

// viewEditLight renders the light edit form
func (ce *ConfigEditor) viewEditLight(theme Theme) string {
	if ce.cursor < 0 || ce.cursor >= len(ce.items) {
		return theme.Muted.Render("No item selected")
	}

	item := ce.items[ce.cursor]
	if item.itemType != configItemLight || item.index >= len(ce.config.Lights) {
		return theme.Muted.Render("No item selected")
	}
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

// viewEditDimmableLight renders the dimmable light edit form
func (ce *ConfigEditor) viewEditDimmableLight(theme Theme) string {
	if ce.cursor < 0 || ce.cursor >= len(ce.items) {
		return theme.Muted.Render("No item selected")
	}

	item := ce.items[ce.cursor]
	if item.itemType != configItemDimmableLight || item.index >= len(ce.config.DimmableLights) {
		return theme.Muted.Render("No item selected")
	}
	dl := ce.config.DimmableLights[item.index]

	title := theme.BoxTitle.Render(IconLight + " Edit Dimmable Light")

	fields := []string{title, ""}
	fields = append(fields, ce.renderTextField(theme, "Name", dl.Name, 0))
	fields = append(fields, ce.renderTextField(theme, "IO Output (on/off)", dl.DigitalOutName, 1))
	fields = append(fields, ce.renderTextField(theme, "IO Analog (bright)", dl.AnalogOutName, 2))
	fields = append(fields, ce.renderTextField(theme, "Default Brightness (0-100)", strconv.Itoa(dl.DefaultSetpoint), 3))
	fields = append(fields, ce.renderBoolField(theme, "Disable HomeKit", dl.DisableHomekit, 4))

	content := strings.Join(fields, "\n")
	result := theme.Box.Width(50).Render(content)
	result += ce.viewStatus(theme)
	return result
}

// viewEditOutlet renders the outlet edit form
func (ce *ConfigEditor) viewEditOutlet(theme Theme) string {
	if ce.cursor < 0 || ce.cursor >= len(ce.items) {
		return theme.Muted.Render("No item selected")
	}

	item := ce.items[ce.cursor]
	if item.itemType != configItemOutlet || item.index >= len(ce.config.Outlets) {
		return theme.Muted.Render("No item selected")
	}
	outlet := ce.config.Outlets[item.index]

	title := theme.BoxTitle.Render(IconOutlet + " Edit Outlet")

	fields := []string{title, ""}
	fields = append(fields, ce.renderTextField(theme, "Name", outlet.Name, 0))
	fields = append(fields, ce.renderTextField(theme, "IO Output", outlet.DigitalOutName, 1))
	fields = append(fields, ce.renderBoolField(theme, "Disable HomeKit", outlet.DisableHomekit, 2))

	content := strings.Join(fields, "\n")
	result := theme.Box.Width(50).Render(content)
	result += ce.viewStatus(theme)
	return result
}

// viewEditButton renders the button edit form
func (ce *ConfigEditor) viewEditButton(theme Theme) string {
	if ce.cursor < 0 || ce.cursor >= len(ce.items) {
		return theme.Muted.Render("No item selected")
	}

	item := ce.items[ce.cursor]
	if item.itemType != configItemButton || item.index >= len(ce.config.Buttons) {
		return theme.Muted.Render("No item selected")
	}
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

	if len(ce.config.OutputDeviceNames) == 0 {
		fields = append(fields, theme.Muted.Render("  No controllable devices available."))
		fields = append(fields, theme.Muted.Render("  Configure lights or outlets first."))
	} else if len(button.ControlDevices) == 0 {
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
		prefix = "▶ "
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
		prefix = "▶ "
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
		prefix = "▶ "
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

	line := prefix + "  " + eventStr + " " + lipgloss.NewStyle().Foreground(ColorMuted).Render("→") +
		" " + actionStr + " " + lipgloss.NewStyle().Foreground(ColorMuted).Render("→") +
		" " + deviceStr
	if app.IsBrightnessVerb(cd.Action) {
		line += theme.Secondary.Render(" " + brightnessLevelLabel(cd.Action, cd.Level))
	}
	return line
}

// renderControlDeviceEditor renders the inline control device editor with cycle selectors
func (ce *ConfigEditor) renderControlDeviceEditor(theme Theme, cd app.ControlDeviceEdit) string {
	var lines []string
	arrowL := theme.Muted.Render("◀ ")
	arrowR := theme.Muted.Render(" ▶")

	// Event type
	eventPrefix := "    "
	if ce.ctrlFieldCursor == 0 {
		eventPrefix = "  ▶ "
	}
	lines = append(lines, eventPrefix+theme.Secondary.Render("Event:  ")+arrowL+theme.Primary.Render(cd.EventType)+arrowR)

	// Action
	actionPrefix := "    "
	if ce.ctrlFieldCursor == 1 {
		actionPrefix = "  ▶ "
	}
	lines = append(lines, actionPrefix+theme.Secondary.Render("Action: ")+arrowL+theme.On.Render(cd.Action)+arrowR)

	// Device
	devicePrefix := "    "
	if ce.ctrlFieldCursor == 2 {
		devicePrefix = "  ▶ "
	}
	devName := cd.DeviceName
	if devName == "" {
		devName = "[none]"
	}
	lines = append(lines, devicePrefix+theme.Secondary.Render("Device: ")+arrowL+theme.Primary.Render(devName)+arrowR)

	// Level/step (only meaningful for brightness-family verbs)
	levelPrefix := "    "
	if ce.ctrlFieldCursor == 3 {
		levelPrefix = "  ▶ "
	}
	var levelVal string
	if app.IsBrightnessVerb(cd.Action) {
		levelVal = theme.Primary.Render(brightnessLevelLabel(cd.Action, cd.Level))
	} else {
		levelVal = theme.Muted.Render("(brightness only)")
	}
	lines = append(lines, levelPrefix+theme.Secondary.Render("Level:  ")+arrowL+levelVal+arrowR)

	return strings.Join(lines, "\n")
}

// viewAddSelector renders the device type picker
func (ce *ConfigEditor) viewAddSelector(theme Theme) string {
	title := theme.BoxTitle.Render("Add device")
	var lines []string
	lines = append(lines, title, "")

	for i, t := range addableDeviceTypes {
		prefix := "  "
		style := theme.ListItem
		if i == ce.addTypeCursor {
			prefix = "▶ "
			style = theme.ListItemSelected
		}
		lines = append(lines, style.Render(prefix+t.label))
	}

	content := strings.Join(lines, "\n")
	result := theme.Box.Width(30).Render(content)
	result += "\n" + theme.Help.Render(
		theme.HelpKey.Render("enter")+" "+theme.HelpDesc.Render("confirm")+"  "+
			theme.HelpKey.Render("esc")+" "+theme.HelpDesc.Render("cancel"),
	)
	return result
}

// viewIoPicker renders the IO point picker
func (ce *ConfigEditor) viewIoPicker(theme Theme) string {
	pts := ce.filteredIoPoints()

	titleLabel := "Select IO Inputs"
	switch ce.ioPickerFilter {
	case "output":
		titleLabel = "Select IO Outputs"
	case "analog_output":
		titleLabel = "Select Analog Outputs"
	}
	title := theme.BoxTitle.Render(titleLabel)

	var lines []string
	lines = append(lines, title, "")

	if len(pts) == 0 {
		lines = append(lines, theme.Muted.Render("No IO points available — check IO Debug tab"))
	}

	nameWidth := 8
	for _, pt := range pts {
		displayName := ce.ioPickerDisplayName(pt)
		if len(displayName) > nameWidth {
			nameWidth = len(displayName)
		}
	}

	now := time.Now()
	const ioPickerRecentThreshold = 10 * time.Minute

	for i, pt := range pts {
		prefix := "  "
		style := theme.ListItem
		if i == ce.ioPickerCursor {
			prefix = "▶ "
			style = theme.ListItemSelected
		}

		stateIcon := theme.Off.Render(IconOff)
		if pt.State {
			stateIcon = theme.On.Render(IconOn)
		}

		healthIcon := theme.Healthy.Render(IconHealthy)
		if !pt.Healthy {
			healthIcon = theme.Faulty.Render(IconFaulty)
		}

		// Activity time: show how long ago this IO was last active (within 10 min)
		actTime := pt.LastEvent
		if pt.LastChanged.After(actTime) {
			actTime = pt.LastChanged
		}
		activityText := ""
		if !actTime.IsZero() {
			ago := now.Sub(actTime)
			if ago < ioPickerRecentThreshold {
				var agoStr string
				if ago < time.Minute {
					agoStr = fmt.Sprintf("%ds ago", int(ago.Seconds()))
				} else {
					agoStr = fmt.Sprintf("%dm%ds ago", int(ago.Minutes()), int(ago.Seconds())%60)
				}
				actStyle := theme.Secondary
				if ago < 5*time.Second {
					actStyle = theme.Event
				} else if ago < 30*time.Second {
					actStyle = theme.On
				}
				activityText = "  " + actStyle.Render(agoStr)
			}
		}

		ioId := theme.Muted.Render(ce.ioPointToIdForCurrentField(pt))
		displayName := ce.ioPickerDisplayName(pt)
		line := prefix + theme.Primary.Render(padRight(displayName, nameWidth)) + " " + stateIcon + " " + healthIcon + activityText + "  " + ioId
		lines = append(lines, style.Render(line))
	}

	content := strings.Join(lines, "\n")
	result := theme.Box.Render(content)
	help := theme.HelpKey.Render("enter") + " " + theme.HelpDesc.Render("select") + "  "
	if ce.ioPickerFilter == "output" || ce.ioPickerFilter == "analog_output" {
		help += theme.HelpKey.Render("t") + " " + theme.HelpDesc.Render("toggle (identify)") + "  "
	}
	help += theme.HelpKey.Render("esc") + " " + theme.HelpDesc.Render("cancel")
	result += "\n" + theme.Help.Render(help)
	return result
}

// viewCtrlWizardDevice renders wizard step 1: target device selection
func (ce *ConfigEditor) viewCtrlWizardDevice(theme Theme) string {
	devices := ce.wizardOutputDevices()
	title := theme.BoxTitle.Render("Add Control by Relation — Step 1/2: Target Device")

	var lines []string
	lines = append(lines, title, "")

	if len(devices) == 0 {
		lines = append(lines, theme.Muted.Render("No controllable devices configured"))
	}

	for i, d := range devices {
		prefix := "  "
		style := theme.ListItem
		if i == ce.wizardDeviceCursor {
			prefix = "▶ "
			style = theme.ListItemSelected
		}

		icon := deviceIcon(d.Type)

		stateIcon := theme.Off.Render(IconOff)
		if d.IsOn {
			stateIcon = theme.On.Render(IconOn)
		}

		faultyText := ""
		if d.IsFaulty {
			faultyText = " " + theme.Faulty.Render(IconFaulty)
		}

		line := prefix + icon + " " + theme.Primary.Render(padRight(d.Name, 22)) + " " + stateIcon + faultyText
		lines = append(lines, style.Render(line))
	}

	content := strings.Join(lines, "\n")
	result := theme.Box.Render(content)
	result += "\n" + theme.Help.Render(
		theme.HelpKey.Render("↑↓")+" "+theme.HelpDesc.Render("select")+"  "+
			theme.HelpKey.Render("t")+" "+theme.HelpDesc.Render("toggle/identify")+"  "+
			theme.HelpKey.Render("enter")+" "+theme.HelpDesc.Render("next")+"  "+
			theme.HelpKey.Render("esc")+" "+theme.HelpDesc.Render("cancel"),
	)
	return result
}

// wizardActionLabel describes the wizard's target action, including the
// brightness step for brightness verbs (e.g. "brightness_up +10%").
func (ce *ConfigEditor) wizardActionLabel() string {
	if app.IsBrightnessVerb(ce.wizardTargetAction) {
		return ce.wizardTargetAction + " " + brightnessLevelLabel(ce.wizardTargetAction, ce.wizardTargetLevel)
	}
	return ce.wizardTargetAction
}

// viewCtrlWizardEvent renders wizard step 2: button + event selection
func (ce *ConfigEditor) viewCtrlWizardEvent(theme Theme) string {
	events := ce.wizardEventList()

	titleContent := theme.BoxTitle.Render("Add Control — Step 2/2: Button Event") +
		"  " + theme.Muted.Render("[→ "+ce.wizardActionLabel()+" → "+ce.wizardTargetDeviceName+"]")

	var lines []string
	lines = append(lines, titleContent)
	lines = append(lines, theme.Muted.Render("  Press a button to detect, or select:"))
	lines = append(lines, "")

	if len(events) == 0 {
		lines = append(lines, theme.Muted.Render("No buttons configured"))
	}

	now := time.Now()
	nameWidth := 8
	for _, e := range events {
		if len(e.Name) > nameWidth {
			nameWidth = len(e.Name)
		}
	}

	for i, e := range events {
		prefix := "  "
		style := theme.ListItem
		if i == ce.wizardEventCursor {
			prefix = "▶ "
			style = theme.ListItemSelected
		}

		eventText := theme.Muted.Render("(no events)")
		ageText := ""

		if !e.LastEventTime.IsZero() {
			ago := now.Sub(e.LastEventTime)
			var agoStr string
			if ago < time.Minute {
				agoStr = fmt.Sprintf("%ds ago", int(ago.Seconds()))
			} else {
				agoStr = fmt.Sprintf("%dm%ds ago", int(ago.Minutes()), int(ago.Seconds())%60)
			}

			eventStyle := theme.On
			if ago < 2*time.Second {
				eventStyle = theme.Event
			}
			eventText = eventStyle.Render(e.LastEventType)
			ageText = "  " + theme.Secondary.Render(agoStr)
		}

		line := prefix + IconButton + " " + theme.Primary.Render(padRight(e.Name, nameWidth)) + "  " + eventText + ageText
		lines = append(lines, style.Render(line))
	}

	stepHelp := ""
	if app.IsBrightnessVerb(ce.wizardTargetAction) {
		stepHelp = theme.HelpKey.Render("+/-") + " " + theme.HelpDesc.Render("step") + "  "
	}

	content := strings.Join(lines, "\n")
	result := theme.Box.Render(content)
	result += "\n" + theme.Help.Render(
		theme.HelpKey.Render("↑↓")+" "+theme.HelpDesc.Render("select")+"  "+
			theme.HelpKey.Render("←→")+" "+theme.HelpDesc.Render("action: "+ce.wizardActionLabel())+"  "+
			stepHelp+
			theme.HelpKey.Render("enter")+" "+theme.HelpDesc.Render("confirm")+"  "+
			theme.HelpKey.Render("esc")+" "+theme.HelpDesc.Render("back"),
	)
	return result
}

// viewClearSelect renders clear step 1: choose what to clear
func (ce *ConfigEditor) viewClearSelect(theme Theme) string {
	title := theme.BoxTitle.Render("Clear Config")
	subtitle := theme.Muted.Render("Select what to clear:")

	var lines []string
	lines = append(lines, title, subtitle, "")

	for i, opt := range clearOptions {
		prefix := "  "
		style := theme.ListItem
		if i == ce.clearCursor {
			prefix = "▶ "
			style = theme.ListItemSelected
		}
		lines = append(lines, style.Render(prefix+opt.label))
	}

	content := strings.Join(lines, "\n")
	result := theme.Box.Width(40).Render(content)
	result += "\n" + theme.Help.Render(
		theme.HelpKey.Render("↑↓")+" "+theme.HelpDesc.Render("select")+"  "+
			theme.HelpKey.Render("enter")+" "+theme.HelpDesc.Render("next")+"  "+
			theme.HelpKey.Render("esc")+" "+theme.HelpDesc.Render("cancel"),
	)
	return result
}

// viewClearConfirm renders clear step 2: confirm the action
func (ce *ConfigEditor) viewClearConfirm(theme Theme) string {
	title := theme.BoxTitle.Render("Confirm Clear")

	optionLabel := ""
	for _, opt := range clearOptions {
		if opt.option == ce.clearChoice {
			optionLabel = opt.label
			break
		}
	}

	description := ce.clearOptionDescription()

	var lines []string
	lines = append(lines, title, "")
	lines = append(lines, theme.Secondary.Render("Action: ")+theme.Error.Render(optionLabel))
	lines = append(lines, theme.Secondary.Render("Will remove: ")+theme.Primary.Render(description))
	lines = append(lines, "")
	lines = append(lines, theme.Error.Render("Are you sure? (y/n)"))

	content := strings.Join(lines, "\n")
	result := theme.Box.Width(50).Render(content)
	result += "\n" + theme.Help.Render(
		theme.HelpKey.Render("y")+" "+theme.HelpDesc.Render("confirm")+"  "+
			theme.HelpKey.Render("n/esc")+" "+theme.HelpDesc.Render("back"),
	)
	return result
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
			theme.HelpKey.Render("a") + " " + theme.HelpDesc.Render("add device"),
			theme.HelpKey.Render("w") + " " + theme.HelpDesc.Render("add by relation"),
			theme.HelpKey.Render("d") + " " + theme.HelpDesc.Render("delete"),
			theme.HelpKey.Render("c") + " " + theme.HelpDesc.Render("clear"),
		}
		if len(ce.items) > configPageStep {
			parts = append(parts, theme.HelpKey.Render("ctrl+u/d")+" "+theme.HelpDesc.Render("page"))
		}
		if ce.dirty {
			parts = append(parts, theme.HelpKey.Render("ctrl+r")+" "+theme.HelpDesc.Render("save"))
		}
		return strings.Join(parts, "  ")
	case ConfigModeAddSelector:
		return theme.HelpKey.Render("enter") + " " + theme.HelpDesc.Render("confirm") + "  " +
			theme.HelpKey.Render("esc") + " " + theme.HelpDesc.Render("cancel")
	case ConfigModeEditLight, ConfigModeEditDimmableLight, ConfigModeEditOutlet, ConfigModeEditButton:
		parts := []string{
			theme.HelpKey.Render("enter") + " " + theme.HelpDesc.Render("edit"),
			theme.HelpKey.Render("space") + " " + theme.HelpDesc.Render("toggle"),
			theme.HelpKey.Render("esc") + " " + theme.HelpDesc.Render("back"),
		}
		pickable := ce.fieldCursor == 1 ||
			(ce.mode == ConfigModeEditDimmableLight && ce.fieldCursor == 2)
		if pickable {
			parts = append(parts, theme.HelpKey.Render("p")+" "+theme.HelpDesc.Render("pick IO"))
		}
		if ce.mode == ConfigModeEditButton {
			parts = append(parts,
				theme.HelpKey.Render("a")+" "+theme.HelpDesc.Render("add ctrl"),
				theme.HelpKey.Render("d")+" "+theme.HelpDesc.Render("del ctrl"),
			)
		}
		if ce.dirty {
			parts = append(parts, theme.HelpKey.Render("ctrl+r")+" "+theme.HelpDesc.Render("save"))
		}
		return strings.Join(parts, "  ")
	case ConfigModeEditScene:
		parts := []string{
			theme.HelpKey.Render("enter") + " " + theme.HelpDesc.Render("edit/open"),
			theme.HelpKey.Render("a") + " " + theme.HelpDesc.Render("add state"),
			theme.HelpKey.Render("d") + " " + theme.HelpDesc.Render("del state"),
			theme.HelpKey.Render("esc") + " " + theme.HelpDesc.Render("back"),
		}
		if ce.dirty {
			parts = append(parts, theme.HelpKey.Render("ctrl+r")+" "+theme.HelpDesc.Render("save"))
		}
		return strings.Join(parts, "  ")
	case ConfigModeEditSceneState:
		var parts []string
		if ce.sceneActionEditing {
			parts = []string{
				theme.HelpKey.Render("↑↓") + " " + theme.HelpDesc.Render("field"),
				theme.HelpKey.Render("←→") + " " + theme.HelpDesc.Render("change"),
				theme.HelpKey.Render("enter") + " " + theme.HelpDesc.Render("done"),
			}
		} else {
			parts = []string{
				theme.HelpKey.Render("enter") + " " + theme.HelpDesc.Render("edit"),
				theme.HelpKey.Render("a") + " " + theme.HelpDesc.Render("add action"),
				theme.HelpKey.Render("d") + " " + theme.HelpDesc.Render("del action"),
				theme.HelpKey.Render("esc") + " " + theme.HelpDesc.Render("back"),
			}
		}
		if ce.dirty {
			parts = append(parts, theme.HelpKey.Render("ctrl+r")+" "+theme.HelpDesc.Render("save"))
		}
		return strings.Join(parts, "  ")
	case ConfigModeIoPicker:
		s := theme.HelpKey.Render("enter") + " " + theme.HelpDesc.Render("select") + "  "
		if ce.ioPickerFilter == "output" || ce.ioPickerFilter == "analog_output" {
			s += theme.HelpKey.Render("t") + " " + theme.HelpDesc.Render("toggle (identify)") + "  "
		}
		s += theme.HelpKey.Render("esc") + " " + theme.HelpDesc.Render("cancel")
		return s
	case ConfigModeCtrlWizardDevice:
		return theme.HelpKey.Render("↑↓") + " " + theme.HelpDesc.Render("select") + "  " +
			theme.HelpKey.Render("t") + " " + theme.HelpDesc.Render("toggle") + "  " +
			theme.HelpKey.Render("enter") + " " + theme.HelpDesc.Render("next") + "  " +
			theme.HelpKey.Render("esc") + " " + theme.HelpDesc.Render("cancel")
	case ConfigModeCtrlWizardEvent:
		stepHelp := ""
		if app.IsBrightnessVerb(ce.wizardTargetAction) {
			stepHelp = theme.HelpKey.Render("+/-") + " " + theme.HelpDesc.Render("step") + "  "
		}
		return theme.HelpKey.Render("↑↓") + " " + theme.HelpDesc.Render("select") + "  " +
			theme.HelpKey.Render("←→") + " " + theme.HelpDesc.Render("action: "+ce.wizardActionLabel()) + "  " +
			stepHelp +
			theme.HelpKey.Render("enter") + " " + theme.HelpDesc.Render("confirm") + "  " +
			theme.HelpKey.Render("esc") + " " + theme.HelpDesc.Render("back")
	case ConfigModeClearSelect:
		return theme.HelpKey.Render("↑↓") + " " + theme.HelpDesc.Render("select") + "  " +
			theme.HelpKey.Render("enter") + " " + theme.HelpDesc.Render("next") + "  " +
			theme.HelpKey.Render("esc") + " " + theme.HelpDesc.Render("cancel")
	case ConfigModeClearConfirm:
		return theme.HelpKey.Render("y") + " " + theme.HelpDesc.Render("confirm") + "  " +
			theme.HelpKey.Render("n/esc") + " " + theme.HelpDesc.Render("back")
	}
	return ""
}
