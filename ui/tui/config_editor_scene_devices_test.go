package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/hubertat/swkit/app"
)

// openSceneState positions the editor inside the given scene's first state.
func openSceneState(t *testing.T, ce *ConfigEditor, sceneIdx int) {
	t.Helper()
	ce.rebuildItems()
	ce.cursor = -1
	for i, it := range ce.items {
		if it.itemType == configItemScene && it.index == sceneIdx {
			ce.cursor = i
		}
	}
	if ce.cursor < 0 {
		t.Fatal("scene item not found")
	}
	ce.mode = ConfigModeEditScene
	ce.fieldCursor = sceneStateFieldOffset
	ce.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if ce.mode != ConfigModeEditSceneState {
		t.Fatalf("mode = %v, want EditSceneState", ce.mode)
	}
}

func TestConfigEditorSceneRestrictsTargets(t *testing.T) {
	ce := NewConfigEditor(nil, DefaultTheme())
	ce.config = app.EditableConfig{
		Lights:            []app.LightEditConfig{{Name: "L1"}, {Name: "L2"}, {Name: "L3"}},
		OutputDeviceNames: []string{"L1", "L2", "L3"},
		Scenes: []app.SceneEditConfig{{
			Name:                 "Evening",
			Devices:              []string{"L2", "L3"},
			ToggleTimeoutSeconds: 1800,
			States:               []app.SceneStateEditConfig{{Name: "Off"}},
		}},
	}
	openSceneState(t, &ce, 0)

	// A new step defaults to the scene's first device, not L1.
	ce.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	state := &ce.config.Scenes[0].States[0]
	if len(state.Actions) != 1 || state.Actions[0] != "on:L2" {
		t.Fatalf("new step = %v, want [on:L2]", state.Actions)
	}

	// Cycling the device only visits L2 and L3.
	ce.Update(tea.KeyMsg{Type: tea.KeyEnter}) // inline editor
	ce.Update(tea.KeyMsg{Type: tea.KeyDown})  // device field
	seen := map[string]bool{}
	for i := 0; i < 4; i++ {
		ce.Update(tea.KeyMsg{Type: tea.KeyRight})
		act, err := app.ParseSceneStep(state.Actions[0])
		if err != nil {
			t.Fatal(err)
		}
		seen[act.Device] = true
	}
	if seen["L1"] || !seen["L2"] || !seen["L3"] {
		t.Errorf("device cycle visited %v, want only L2 and L3", seen)
	}

	// The scene-level fields survive edits untouched.
	sc := ce.config.Scenes[0]
	if strings.Join(sc.Devices, ",") != "L2,L3" || sc.ToggleTimeoutSeconds != 1800 {
		t.Errorf("scene settings changed: Devices=%v timeout=%d", sc.Devices, sc.ToggleTimeoutSeconds)
	}

	// The scene screen shows them.
	ce.mode = ConfigModeEditScene
	view := ce.viewEditScene(DefaultTheme())
	if !strings.Contains(view, "L2, L3") || !strings.Contains(view, "30m") {
		t.Errorf("scene view does not show device list and timeout:\n%s", view)
	}
}

// openSceneDevices positions the editor on the given scene's Devices row and
// opens the checklist with Enter.
func openSceneDevices(t *testing.T, ce *ConfigEditor, sceneIdx int) {
	t.Helper()
	ce.rebuildItems()
	ce.cursor = -1
	for i, it := range ce.items {
		if it.itemType == configItemScene && it.index == sceneIdx {
			ce.cursor = i
		}
	}
	if ce.cursor < 0 {
		t.Fatal("scene item not found")
	}
	ce.mode = ConfigModeEditScene
	ce.fieldCursor = 1
	ce.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if ce.mode != ConfigModeSceneDevices {
		t.Fatalf("mode = %v, want SceneDevices", ce.mode)
	}
}

// toggleSceneDevice moves the checklist cursor onto name and presses space.
func toggleSceneDevice(t *testing.T, ce *ConfigEditor, name string) {
	t.Helper()
	for i, c := range ce.sceneDeviceCandidates(ce.currentSceneIndex()) {
		if c.name == name {
			ce.sceneDeviceCursor = i
			ce.Update(tea.KeyMsg{Type: tea.KeySpace})
			return
		}
	}
	t.Fatalf("%q is not offered in the checklist", name)
}

func TestConfigEditorSceneDeviceCandidates(t *testing.T) {
	ce := NewConfigEditor(nil, DefaultTheme())
	ce.config = app.EditableConfig{
		Lights:         []app.LightEditConfig{{Name: "L1"}},
		DimmableLights: []app.DimmableLightEditConfig{{Name: "Dim"}},
		Outlets:        []app.OutletEditConfig{{Name: "Fan"}},
		Scenes: []app.SceneEditConfig{
			{Name: "Early", Devices: []string{"L1"}, States: []app.SceneStateEditConfig{{Name: "Off"}}},
			{Name: "Mid", Devices: []string{"L1", "Gone"}, States: []app.SceneStateEditConfig{{Name: "Off"}}},
			{Name: "Late", Devices: []string{"L1"}, States: []app.SceneStateEditConfig{{Name: "Off"}}},
		},
	}
	ce.SetDeviceStates([]app.DeviceState{
		{Name: "Mood", Type: app.DeviceTypeColorLight},
		{Name: "L1", Type: app.DeviceTypeLight},
	})

	var got []string
	for _, c := range ce.sceneDeviceCandidates(1) {
		got = append(got, c.name+"/"+c.kind)
	}
	// Earlier scenes only (never itself or a later one); a listed device
	// that no longer exists is still offered so it can be removed.
	want := "L1/light,Dim/dimmable,Fan/outlet,Mood/color light,Early/scene,Gone/unknown"
	if strings.Join(got, ",") != want {
		t.Errorf("candidates = %v, want %s", got, want)
	}

	openSceneDevices(t, &ce, 1)
	view := ce.viewSceneDevices(DefaultTheme())
	if !strings.Contains(view, "[x] ") || !strings.Contains(view, "[ ] ") || strings.Contains(view, "Late") {
		t.Errorf("checklist view:\n%s", view)
	}
}

func TestConfigEditorSceneDeviceAddAndRemove(t *testing.T) {
	ce := NewConfigEditor(nil, DefaultTheme())
	ce.config = app.EditableConfig{
		Lights: []app.LightEditConfig{{Name: "L1"}, {Name: "L2"}},
		Scenes: []app.SceneEditConfig{{
			Name:    "Evening",
			Devices: []string{"L1"},
			States: []app.SceneStateEditConfig{
				{Name: "Off", Actions: []string{"off:L1"}},
				{Name: "On", Actions: []string{"on:L1", "after:10m:off:L1", "not a step"}},
			},
		}},
	}
	openSceneDevices(t, &ce, 0)

	// Adding switches the device off in state 0 only, appended last.
	toggleSceneDevice(t, &ce, "L2")
	sc := &ce.config.Scenes[0]
	if got := strings.Join(sc.Devices, ","); got != "L1,L2" {
		t.Errorf("Devices = %q, want L1,L2", got)
	}
	if got := strings.Join(sc.States[0].Actions, ","); got != "off:L1,off:L2" {
		t.Errorf("state 0 = %q, want off:L1,off:L2", got)
	}
	if got := strings.Join(sc.States[1].Actions, ","); got != "on:L1,after:10m:off:L1,not a step" {
		t.Errorf("state 1 changed: %q", got)
	}
	if !ce.IsDirty() {
		t.Error("adding a device should mark the editor dirty")
	}

	// Removing strips the device's steps everywhere, delayed ones included,
	// and leaves unparseable steps alone.
	toggleSceneDevice(t, &ce, "L1")
	if got := strings.Join(sc.Devices, ","); got != "L2" {
		t.Errorf("Devices = %q, want L2", got)
	}
	if got := strings.Join(sc.States[0].Actions, ","); got != "off:L2" {
		t.Errorf("state 0 = %q, want off:L2", got)
	}
	if got := strings.Join(sc.States[1].Actions, ","); got != "not a step" {
		t.Errorf("state 1 = %q, want only the unparseable step", got)
	}

	// Enter returns to the scene screen on the Devices row.
	ce.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if ce.mode != ConfigModeEditScene || ce.fieldCursor != 1 {
		t.Errorf("mode/field = %v/%d, want EditScene/1", ce.mode, ce.fieldCursor)
	}
}

func TestConfigEditorNewSceneGetsOffAndOn(t *testing.T) {
	ce := NewConfigEditor(nil, DefaultTheme())
	ce.config = app.EditableConfig{
		Lights:  []app.LightEditConfig{{Name: "L1"}, {Name: "L2"}},
		Outlets: []app.OutletEditConfig{{Name: "Fan"}},
		Scenes:  []app.SceneEditConfig{{Name: "New"}},
	}

	ce.rebuildItems()
	ce.cursor = len(ce.items) - 1
	ce.mode = ConfigModeEditScene
	if view := ce.viewEditScene(DefaultTheme()); !strings.Contains(view, "[none - required") {
		t.Errorf("a scene without devices should say they are required:\n%s", view)
	}

	openSceneDevices(t, &ce, 0)
	toggleSceneDevice(t, &ce, "L1")
	toggleSceneDevice(t, &ce, "Fan")
	toggleSceneDevice(t, &ce, "L2")
	toggleSceneDevice(t, &ce, "L2") // and back out again
	ce.Update(tea.KeyMsg{Type: tea.KeyEsc})

	sc := ce.config.Scenes[0]
	if got := strings.Join(sc.Devices, ","); got != "L1,Fan" {
		t.Errorf("Devices = %q, want L1,Fan", got)
	}
	if len(sc.States) != 2 || sc.States[0].Name != "Off" || sc.States[1].Name != "On" {
		t.Fatalf("states = %+v, want Off and On", sc.States)
	}
	if got := strings.Join(sc.States[0].Actions, ","); got != "off:L1,off:Fan" {
		t.Errorf("Off = %q, want off:L1,off:Fan", got)
	}
	if got := strings.Join(sc.States[1].Actions, ","); got != "on:L1,on:Fan" {
		t.Errorf("On = %q, want on:L1,on:Fan", got)
	}

	// Reopening the checklist on the now-existing scene adds to Off only.
	openSceneDevices(t, &ce, 0)
	toggleSceneDevice(t, &ce, "L2")
	sc = ce.config.Scenes[0]
	if got := strings.Join(sc.States[1].Actions, ","); got != "on:L1,on:Fan" {
		t.Errorf("On after reopening = %q, want unchanged", got)
	}
	if got := strings.Join(sc.States[0].Actions, ","); got != "off:L1,off:Fan,off:L2" {
		t.Errorf("Off after reopening = %q", got)
	}
}

func TestConfigEditorSceneWithoutDevicesAddsNoStep(t *testing.T) {
	ce := NewConfigEditor(nil, DefaultTheme())
	ce.config = app.EditableConfig{
		Lights:            []app.LightEditConfig{{Name: "L1"}},
		OutputDeviceNames: []string{"L1"},
		Scenes:            []app.SceneEditConfig{{Name: "S", States: []app.SceneStateEditConfig{{Name: "Off"}}}},
	}
	openSceneState(t, &ce, 0)
	ce.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	if got := ce.config.Scenes[0].States[0].Actions; len(got) != 0 {
		t.Fatalf("steps = %v, want none until the scene has devices", got)
	}
	if !strings.Contains(ce.statusMsg, "devices first") {
		t.Errorf("status = %q, want a hint to choose devices", ce.statusMsg)
	}
}
