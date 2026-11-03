package tui

import (
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/hubertat/swkit/app"
)

func typeAndEnter(ce *ConfigEditor, field int, value string) {
	ce.fieldCursor = field
	ws, _ := ce.currentShutter()
	ce.startEditingShutterField(ws)
	ce.textInput.SetValue(value)
	ce.handleShutterTextEditing(tea.KeyMsg{Type: tea.KeyEnter}, ws)
}

func TestConfigEditorShutterCRUD(t *testing.T) {
	cfg := app.EditableConfig{
		Lights:  []app.LightEditConfig{{Name: "L1", DigitalOutName: "mock|d_out|1"}},
		Buttons: []app.ButtonEditConfig{{Name: "B", EventInputName: "mock|push_event|3"}},
	}
	prov := &fakeRevisionConfigProvider{cfg: cfg}
	ce := newSavingConfigEditor(prov)

	ce.addDeviceOfType(configItemWindowShutter)
	if ce.mode != ConfigModeEditShutter || len(ce.config.WindowShutters) != 1 {
		t.Fatalf("add: mode %v shutters %d", ce.mode, len(ce.config.WindowShutters))
	}
	ce.handleShutterTextEditing(tea.KeyMsg{Type: tea.KeyEsc}, &ce.config.WindowShutters[0])
	typeAndEnter(&ce, shutterFieldName, "Sh")
	typeAndEnter(&ce, shutterFieldOpen, "mock|d_out|10")
	typeAndEnter(&ce, shutterFieldClose, "mock|d_out|11")
	typeAndEnter(&ce, shutterFieldOpenDuration, "601") // rejected
	if ce.config.WindowShutters[0].OpenDurationSeconds != 30 || !strings.Contains(ce.statusMsg, "1-600") {
		t.Fatalf("out-of-range duration must be rejected: %+v %q", ce.config.WindowShutters[0], ce.statusMsg)
	}
	typeAndEnter(&ce, shutterFieldOpenDuration, "25")
	typeAndEnter(&ce, shutterFieldCloseDuration, "0")
	typeAndEnter(&ce, shutterFieldReverseDelay, "5001") // rejected
	typeAndEnter(&ce, shutterFieldReverseDelay, "700")
	ws := ce.config.WindowShutters[0]
	if ws.Name != "Sh" || ws.OpenDurationSeconds != 25 || ws.CloseDurationSeconds != 0 || ws.ReverseDelayMilliseconds != 700 {
		t.Fatalf("fields: %+v", ws)
	}
	if !slices.Contains(ce.config.OutputDeviceNames, "Sh") {
		t.Fatal("shutter must become a control target")
	}
	if !strings.Contains(ce.viewEditShutter(DefaultTheme()), "0 uses 500 ms") {
		t.Fatal("reversal delay help must be shown")
	}

	// References: a button relation and a scene, then rename and delete.
	ce.config.Buttons[0].ControlDevices = []app.ControlDeviceEdit{{EventType: "single_press", Action: "open", DeviceName: "Sh"}}
	ce.config.Scenes = []app.SceneEditConfig{{Name: "S", Devices: []string{"Sh"}, States: []app.SceneStateEditConfig{
		{Name: "Off", Actions: []string{"stop:Sh"}},
		{Name: "On", Actions: []string{"position:40:Sh", "after:10s:stop:Sh"}},
	}}}
	ce.rebuildItems()
	ce.setCursorToItem(configItemWindowShutter, 0)
	typeAndEnter(&ce, shutterFieldName, "Bedroom")
	if ce.config.Buttons[0].ControlDevices[0].DeviceName != "Bedroom" ||
		!slices.Equal(ce.config.Scenes[0].Devices, []string{"Bedroom"}) ||
		!slices.Equal(ce.config.Scenes[0].States[1].Actions, []string{"position:40:Bedroom", "after:10s:stop:Bedroom"}) {
		t.Fatalf("rename must propagate: %+v %+v", ce.config.Buttons[0], ce.config.Scenes[0])
	}

	// Save validates and persists the shutter.
	if err := runSave(t, &ce); err != nil {
		t.Fatalf("save: %v", err)
	}
	if prov.saved == nil || len(prov.saved.WindowShutters) != 1 || prov.saved.WindowShutters[0].Name != "Bedroom" {
		t.Fatalf("saved: %+v", prov.saved)
	}

	// Delete strips references.
	ce.rebuildItems()
	ce.setCursorToItem(configItemWindowShutter, 0)
	ce.mode = ConfigModeList
	ce.deleteItem()
	if len(ce.config.WindowShutters) != 0 || len(ce.config.Buttons[0].ControlDevices) != 0 ||
		len(ce.config.Scenes[0].Devices) != 0 || len(ce.config.Scenes[0].States[1].Actions) != 0 {
		t.Fatalf("delete must strip references: %+v", ce.config)
	}
}

func TestConfigEditorShutterSaveValidation(t *testing.T) {
	cfg := app.EditableConfig{WindowShutters: []app.WindowShutterEditConfig{{Name: "Sh", OpenDigitalOutName: "mock|d_out|1", CloseDigitalOutName: "mock|d_out|1", OpenDurationSeconds: 10}}}
	ce := newSavingConfigEditor(&fakeRevisionConfigProvider{cfg: cfg})
	ce.markDirty()
	err := runSave(t, &ce)
	if err == nil || !strings.Contains(err.Error(), "problem") {
		t.Fatalf("same outputs must be refused: %v", err)
	}
}
