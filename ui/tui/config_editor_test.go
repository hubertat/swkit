package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/hubertat/swkit/app"
)

func TestConfigEditorIoPickerDisplayNameUsesCustomName(t *testing.T) {
	ce := NewConfigEditor(nil, DefaultTheme())
	ce.SetIoDisplayNames(map[string]string{
		"wago|output|7": "Kitchen Light",
	})

	pt := app.IoPointDebugState{
		DriverName: "wago",
		Type:       "output",
		Index:      7,
		Name:       "M1:DO7",
	}

	got := ce.ioPickerDisplayName(pt)
	want := "Kitchen Light [M1:DO7]"
	if got != want {
		t.Fatalf("unexpected display name: got %q, want %q", got, want)
	}
}

func TestConfigEditorIoPickerDisplayNameFallsBackToHardwareName(t *testing.T) {
	ce := NewConfigEditor(nil, DefaultTheme())

	pt := app.IoPointDebugState{
		DriverName: "wago",
		Type:       "input",
		Index:      2,
		Name:       "M1:DI2",
	}

	got := ce.ioPickerDisplayName(pt)
	want := "M1:DI2"
	if got != want {
		t.Fatalf("unexpected fallback display name: got %q, want %q", got, want)
	}
}

func TestConfigEditorSetIoDisplayNamesCopiesInputMap(t *testing.T) {
	ce := NewConfigEditor(nil, DefaultTheme())
	original := map[string]string{
		"wago|output|1": "Initial Name",
	}

	ce.SetIoDisplayNames(original)
	original["wago|output|1"] = "Changed Externally"

	pt := app.IoPointDebugState{
		DriverName: "wago",
		Type:       "output",
		Index:      1,
		Name:       "M1:DO1",
	}

	got := ce.ioPickerDisplayName(pt)
	want := "Initial Name [M1:DO1]"
	if got != want {
		t.Fatalf("display name changed via external map mutation: got %q, want %q", got, want)
	}
}

func TestIoPointToIdShellyInputUsesNumericPort(t *testing.T) {
	pt := app.IoPointDebugState{
		DriverName: "shelly",
		Type:       "input",
		Name:       "shellyi4g3-e4b063d3f118:input1",
	}

	got := ioPointToId(pt)
	want := "shelly|d_in|shellyi4g3-e4b063d3f118:1"
	if got != want {
		t.Fatalf("unexpected io id: got %q, want %q", got, want)
	}
}

func TestIoPointToIdShellyOutputUsesNumericPort(t *testing.T) {
	pt := app.IoPointDebugState{
		DriverName: "shelly",
		Type:       "output",
		Name:       "shelly1pm-ABCDEF123456:switch0",
	}

	got := ioPointToId(pt)
	want := "shelly|d_out|shelly1pm-ABCDEF123456:0"
	if got != want {
		t.Fatalf("unexpected io id: got %q, want %q", got, want)
	}
}

func TestIoPointToIdWagoUsesGlobalIndex(t *testing.T) {
	pt := app.IoPointDebugState{
		DriverName: "wago",
		Type:       "input",
		Index:      5,
		Name:       "M2:DI1",
	}

	got := ioPointToId(pt)
	want := "wago|d_in|5"
	if got != want {
		t.Fatalf("unexpected io id: got %q, want %q", got, want)
	}
}

func TestIoPointToIdForCurrentFieldButtonUsesPushEvent(t *testing.T) {
	ce := NewConfigEditor(nil, DefaultTheme())
	ce.items = []configListItem{
		{itemType: configItemButton, index: 0},
	}
	ce.cursor = 0
	ce.ioPickerField = 1

	pt := app.IoPointDebugState{
		DriverName: "shelly",
		Type:       "input",
		Name:       "shellyi4g3-e4b063d3f118:input1",
	}

	got := ce.ioPointToIdForCurrentField(pt)
	want := "shelly|push_event|shellyi4g3-e4b063d3f118:1"
	if got != want {
		t.Fatalf("unexpected io id for button picker field: got %q, want %q", got, want)
	}
}

func TestIoPointToIdForCurrentFieldLightUsesDigitalOutput(t *testing.T) {
	ce := NewConfigEditor(nil, DefaultTheme())
	ce.items = []configListItem{
		{itemType: configItemLight, index: 0},
	}
	ce.cursor = 0
	ce.ioPickerField = 1

	pt := app.IoPointDebugState{
		DriverName: "shelly",
		Type:       "output",
		Name:       "shelly1pm-ABCDEF123456:switch0",
	}

	got := ce.ioPointToIdForCurrentField(pt)
	want := "shelly|d_out|shelly1pm-ABCDEF123456:0"
	if got != want {
		t.Fatalf("unexpected io id for light picker field: got %q, want %q", got, want)
	}
}

// newTestConfigEditor creates a ConfigEditor preloaded with sample config for clear tests
func newTestConfigEditor() ConfigEditor {
	ce := NewConfigEditor(nil, DefaultTheme())
	ce.config = app.EditableConfig{
		Lights: []app.LightEditConfig{
			{Name: "Kitchen", DigitalOutName: "gpio|d_out|5"},
			{Name: "Bedroom", DigitalOutName: "gpio|d_out|6"},
		},
		Buttons: []app.ButtonEditConfig{
			{
				Name:           "Wall Switch",
				EventInputName: "shelly|d_in|dev:0",
				ControlDevices: []app.ControlDeviceEdit{
					{EventType: "single_press", Action: "toggle", DeviceName: "Kitchen"},
				},
			},
			{
				Name:           "Door Switch",
				EventInputName: "shelly|d_in|dev:1",
				ControlDevices: []app.ControlDeviceEdit{
					{EventType: "single_press", Action: "on", DeviceName: "Bedroom"},
					{EventType: "double_press", Action: "off", DeviceName: "Bedroom"},
				},
			},
		},
		OutputDeviceNames: []string{"Kitchen", "Bedroom"},
	}
	ce.rebuildItems()
	return ce
}

func TestClearAllRemovesEverything(t *testing.T) {
	ce := newTestConfigEditor()
	ce.clearChoice = clearAll
	ce.executeClear()

	if len(ce.config.Lights) != 0 {
		t.Fatalf("expected 0 lights, got %d", len(ce.config.Lights))
	}
	if len(ce.config.Buttons) != 0 {
		t.Fatalf("expected 0 buttons, got %d", len(ce.config.Buttons))
	}
	if !ce.dirty {
		t.Fatal("expected dirty flag to be set")
	}
	if len(ce.items) != 0 {
		t.Fatalf("expected 0 items, got %d", len(ce.items))
	}
}

func TestClearLightsOnlyRemovesLights(t *testing.T) {
	ce := newTestConfigEditor()
	ce.clearChoice = clearLights
	ce.executeClear()

	if len(ce.config.Lights) != 0 {
		t.Fatalf("expected 0 lights, got %d", len(ce.config.Lights))
	}
	if len(ce.config.Buttons) != 2 {
		t.Fatalf("expected 2 buttons, got %d", len(ce.config.Buttons))
	}
	if !ce.dirty {
		t.Fatal("expected dirty flag to be set")
	}
}

func TestClearButtonsOnlyRemovesButtons(t *testing.T) {
	ce := newTestConfigEditor()
	ce.clearChoice = clearButtons
	ce.executeClear()

	if len(ce.config.Lights) != 2 {
		t.Fatalf("expected 2 lights, got %d", len(ce.config.Lights))
	}
	if len(ce.config.Buttons) != 0 {
		t.Fatalf("expected 0 buttons, got %d", len(ce.config.Buttons))
	}
	if !ce.dirty {
		t.Fatal("expected dirty flag to be set")
	}
}

func TestClearRelationsOnlyRemovesControlDevices(t *testing.T) {
	ce := newTestConfigEditor()
	ce.clearChoice = clearRelations
	ce.executeClear()

	if len(ce.config.Lights) != 2 {
		t.Fatalf("expected 2 lights, got %d", len(ce.config.Lights))
	}
	if len(ce.config.Buttons) != 2 {
		t.Fatalf("expected 2 buttons, got %d", len(ce.config.Buttons))
	}
	for i, b := range ce.config.Buttons {
		if len(b.ControlDevices) != 0 {
			t.Fatalf("button %d: expected 0 control devices, got %d", i, len(b.ControlDevices))
		}
	}
	if !ce.dirty {
		t.Fatal("expected dirty flag to be set")
	}
}

func TestClearSelectEscReturnsToList(t *testing.T) {
	ce := newTestConfigEditor()
	ce.mode = ConfigModeClearSelect
	ce.updateClearSelect(keyMsg("esc"))

	if ce.mode != ConfigModeList {
		t.Fatalf("expected ConfigModeList, got %d", ce.mode)
	}
}

func TestClearSelectEnterMovesToConfirm(t *testing.T) {
	ce := newTestConfigEditor()
	ce.mode = ConfigModeClearSelect
	ce.clearCursor = 2 // clearButtons
	ce.updateClearSelect(keyMsg("enter"))

	if ce.mode != ConfigModeClearConfirm {
		t.Fatalf("expected ConfigModeClearConfirm, got %d", ce.mode)
	}
	if ce.clearChoice != clearButtons {
		t.Fatalf("expected clearButtons choice, got %d", ce.clearChoice)
	}
}

func TestClearConfirmYesExecutesAndReturns(t *testing.T) {
	ce := newTestConfigEditor()
	ce.mode = ConfigModeClearConfirm
	ce.clearChoice = clearAll
	ce.updateClearConfirm(keyMsg("y"))

	if ce.mode != ConfigModeList {
		t.Fatalf("expected ConfigModeList, got %d", ce.mode)
	}
	if len(ce.config.Lights) != 0 || len(ce.config.Buttons) != 0 {
		t.Fatal("expected config to be cleared")
	}
}

func TestClearConfirmNoReturnsToSelect(t *testing.T) {
	ce := newTestConfigEditor()
	ce.mode = ConfigModeClearConfirm
	ce.clearChoice = clearAll
	ce.updateClearConfirm(keyMsg("n"))

	if ce.mode != ConfigModeClearSelect {
		t.Fatalf("expected ConfigModeClearSelect, got %d", ce.mode)
	}
	// Config should be untouched
	if len(ce.config.Lights) != 2 {
		t.Fatalf("expected 2 lights (untouched), got %d", len(ce.config.Lights))
	}
}

func TestClearKeyOnlyAvailableWithItems(t *testing.T) {
	ce := NewConfigEditor(nil, DefaultTheme())
	// Empty config - no items
	ce.mode = ConfigModeList
	ce.updateList(keyMsg("c"))

	if ce.mode != ConfigModeList {
		t.Fatalf("expected to stay in ConfigModeList with empty config, got %d", ce.mode)
	}
}

func TestClearOptionDescription(t *testing.T) {
	ce := newTestConfigEditor()

	ce.clearChoice = clearAll
	desc := ce.clearOptionDescription()
	if desc == "" {
		t.Fatal("expected non-empty description for clearAll")
	}

	ce.clearChoice = clearRelations
	desc = ce.clearOptionDescription()
	if desc == "" {
		t.Fatal("expected non-empty description for clearRelations")
	}
}

// keyMsg creates a tea.KeyMsg for testing
func keyMsg(key string) tea.KeyMsg {
	switch key {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEscape}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}
	}
}

// TestConfigEditorAddPositionsCursorOnNewItem guards against the panic where
// adding a light/dimmable light while buttons exist left the cursor on the last
// (button) item while the mode was EditLight/EditDimmableLight.
func TestConfigEditorAddPositionsCursorOnNewItem(t *testing.T) {
	ce := NewConfigEditor(nil, DefaultTheme())
	// Start with buttons present so the new light is NOT the last list item.
	ce.config.Buttons = []app.ButtonEditConfig{{Name: "B1"}, {Name: "B2"}, {Name: "B3"}}
	ce.config.Lights = []app.LightEditConfig{{Name: "L1"}}
	ce.rebuildItems()

	// Add a light: cursor must land on the new light item, mode EditLight.
	ce.addDeviceOfType(configItemLight)
	if ce.mode != ConfigModeEditLight {
		t.Fatalf("mode = %v, want ConfigModeEditLight", ce.mode)
	}
	item := ce.items[ce.cursor]
	if item.itemType != configItemLight || item.index != len(ce.config.Lights)-1 {
		t.Fatalf("cursor on %v idx %d, want new light idx %d", item.itemType, item.index, len(ce.config.Lights)-1)
	}
	// Rendering must not panic and must show the edit form.
	if out := ce.viewEditLight(DefaultTheme()); out == "" {
		t.Fatal("viewEditLight returned empty")
	}

	// Add a dimmable light: same guarantees.
	ce.mode = ConfigModeList
	ce.addDeviceOfType(configItemDimmableLight)
	if ce.mode != ConfigModeEditDimmableLight {
		t.Fatalf("mode = %v, want ConfigModeEditDimmableLight", ce.mode)
	}
	item = ce.items[ce.cursor]
	if item.itemType != configItemDimmableLight || item.index != len(ce.config.DimmableLights)-1 {
		t.Fatalf("cursor on %v idx %d, want new dimmable idx %d", item.itemType, item.index, len(ce.config.DimmableLights)-1)
	}
	if out := ce.viewEditDimmableLight(DefaultTheme()); out == "" {
		t.Fatal("viewEditDimmableLight returned empty")
	}
}
