package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/hubertat/swkit/app"
)

// TestConfigEditorSceneFlow drives the editor through add-scene -> name ->
// add-state -> add-action -> edit-action entirely via key messages, then
// asserts the resulting EditableConfig.
func TestConfigEditorSceneFlow(t *testing.T) {
	ce := NewConfigEditor(nil, DefaultTheme())
	ce.config = app.EditableConfig{
		Lights:            []app.LightEditConfig{{Name: "L1"}},
		OutputDeviceNames: []string{"L1"},
	}
	ce.rebuildItems()

	enter := tea.KeyMsg{Type: tea.KeyEnter}
	right := tea.KeyMsg{Type: tea.KeyRight}
	runes := func(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

	// List: 'a' opens the add-type selector.
	ce.Update(runes("a"))
	if ce.mode != ConfigModeAddSelector {
		t.Fatalf("expected AddSelector mode, got %v", ce.mode)
	}
	// Navigate to the Scene entry (last option) and confirm.
	for i := 0; i < len(addableDeviceTypes)-1; i++ {
		ce.Update(runes("j"))
	}
	ce.Update(enter)
	if ce.mode != ConfigModeEditScene {
		t.Fatalf("expected EditScene mode, got %v", ce.mode)
	}
	if len(ce.config.Scenes) != 1 {
		t.Fatalf("expected 1 scene, got %d", len(ce.config.Scenes))
	}

	// Name field is focused for editing; type a name and confirm.
	ce.Update(runes("Movie"))
	ce.Update(enter)
	if ce.config.Scenes[0].Name != "Movie" {
		t.Fatalf("scene name = %q, want Movie", ce.config.Scenes[0].Name)
	}

	// Add a state, then drill into it.
	ce.Update(runes("a"))
	if len(ce.config.Scenes[0].States) != 1 {
		t.Fatalf("expected 1 state, got %d", len(ce.config.Scenes[0].States))
	}
	ce.Update(enter) // cursor is on the new state row
	if ce.mode != ConfigModeEditSceneState {
		t.Fatalf("expected EditSceneState mode, got %v", ce.mode)
	}

	// Add an action (defaults to on:L1).
	ce.Update(runes("a"))
	state := ce.config.Scenes[0].States[0]
	if len(state.Actions) != 1 || state.Actions[0] != "on:L1" {
		t.Fatalf("expected [on:L1], got %v", state.Actions)
	}

	// Edit the action: enter inline editor, cycle the verb on -> off, confirm.
	ce.Update(enter)
	if !ce.sceneActionEditing {
		t.Fatal("expected sceneActionEditing to be active")
	}
	ce.Update(right) // action field: on -> off
	ce.Update(enter) // finish inline edit
	if got := ce.config.Scenes[0].States[0].Actions[0]; got != "off:L1" {
		t.Errorf("action after cycle = %q, want off:L1", got)
	}

	if !ce.IsDirty() {
		t.Error("editor should be dirty after edits")
	}
}

// TestConfigEditorSceneBrightnessLevel checks the brightness level sub-field
// cycles only for the brightness verb.
func TestConfigEditorSceneBrightnessLevel(t *testing.T) {
	ce := NewConfigEditor(nil, DefaultTheme())
	ce.config = app.EditableConfig{
		DimmableLights:    []app.DimmableLightEditConfig{{Name: "Dim"}},
		OutputDeviceNames: []string{"Dim"},
		Scenes: []app.SceneEditConfig{{
			Name:   "S",
			States: []app.SceneStateEditConfig{{Name: "on", Actions: []string{"brightness:50:Dim"}}},
		}},
	}
	ce.rebuildItems()

	// Position on the scene and drill into its state and action.
	ce.cursor = len(ce.items) - 1
	ce.mode = ConfigModeEditScene
	ce.fieldCursor = 1 // the single state row
	ce.Update(tea.KeyMsg{Type: tea.KeyEnter})
	ce.fieldCursor = 1 // the single action row
	ce.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !ce.sceneActionEditing {
		t.Fatal("expected action editing active")
	}

	// Move to the level field (action=0, device=1, level=2) and increase.
	ce.Update(tea.KeyMsg{Type: tea.KeyDown})
	ce.Update(tea.KeyMsg{Type: tea.KeyDown})
	ce.Update(tea.KeyMsg{Type: tea.KeyRight}) // 50 -> 55
	if got := ce.config.Scenes[0].States[0].Actions[0]; got != "brightness:55:Dim" {
		t.Errorf("level after increase = %q, want brightness:55:Dim", got)
	}
}

// TestConfigEditorSceneRelativeBrightnessVerb checks that the action verb cycles
// to the relative brightness verbs and their step still cycles.
func TestConfigEditorSceneRelativeBrightnessVerb(t *testing.T) {
	ce := NewConfigEditor(nil, DefaultTheme())
	ce.config = app.EditableConfig{
		DimmableLights:    []app.DimmableLightEditConfig{{Name: "Dim"}},
		OutputDeviceNames: []string{"Dim"},
		Scenes: []app.SceneEditConfig{{
			Name:   "S",
			States: []app.SceneStateEditConfig{{Name: "on", Actions: []string{"brightness:50:Dim"}}},
		}},
	}
	ce.rebuildItems()

	ce.cursor = len(ce.items) - 1
	ce.mode = ConfigModeEditScene
	ce.fieldCursor = 1
	ce.Update(tea.KeyMsg{Type: tea.KeyEnter}) // into state
	ce.fieldCursor = 1
	ce.Update(tea.KeyMsg{Type: tea.KeyEnter}) // into action edit

	// On the action verb field, cycle right: brightness -> brightness_up.
	ce.Update(tea.KeyMsg{Type: tea.KeyRight})
	if got := ce.config.Scenes[0].States[0].Actions[0]; got != "brightness_up:50:Dim" {
		t.Fatalf("verb after cycle = %q, want brightness_up:50:Dim", got)
	}

	// Level still cycles for the relative verb.
	ce.Update(tea.KeyMsg{Type: tea.KeyDown})
	ce.Update(tea.KeyMsg{Type: tea.KeyDown})
	ce.Update(tea.KeyMsg{Type: tea.KeyRight}) // 50 -> 55
	if got := ce.config.Scenes[0].States[0].Actions[0]; got != "brightness_up:55:Dim" {
		t.Errorf("step after increase = %q, want brightness_up:55:Dim", got)
	}
}

// TestConfigEditorSceneStepPlusMinusKeys checks +/- adjust the brightness step
// regardless of which sub-field is focused.
func TestConfigEditorSceneStepPlusMinusKeys(t *testing.T) {
	ce := NewConfigEditor(nil, DefaultTheme())
	ce.config = app.EditableConfig{
		DimmableLights:    []app.DimmableLightEditConfig{{Name: "Dim"}},
		OutputDeviceNames: []string{"Dim"},
		Scenes: []app.SceneEditConfig{{
			Name:   "S",
			States: []app.SceneStateEditConfig{{Name: "on", Actions: []string{"brightness_up:50:Dim"}}},
		}},
	}
	ce.rebuildItems()

	ce.cursor = len(ce.items) - 1
	ce.mode = ConfigModeEditScene
	ce.fieldCursor = 1
	ce.Update(tea.KeyMsg{Type: tea.KeyEnter}) // into state
	ce.fieldCursor = 1
	ce.Update(tea.KeyMsg{Type: tea.KeyEnter}) // into action edit

	// '+' brightens by 5 even while the verb field (0) is focused.
	ce.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'+'}})
	if got := ce.config.Scenes[0].States[0].Actions[0]; got != "brightness_up:55:Dim" {
		t.Errorf("after '+' = %q, want brightness_up:55:Dim", got)
	}
	// '-' dims by 5.
	ce.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'-'}})
	if got := ce.config.Scenes[0].States[0].Actions[0]; got != "brightness_up:50:Dim" {
		t.Errorf("after '-' = %q, want brightness_up:50:Dim", got)
	}
}
