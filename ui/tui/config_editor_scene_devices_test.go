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
	ce.fieldCursor = 1
	ce.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if ce.mode != ConfigModeEditSceneState {
		t.Fatalf("mode = %v, want EditSceneState", ce.mode)
	}
}

func TestConfigEditorManagedSceneRestrictsTargets(t *testing.T) {
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

	// The scene screen shows them read-only.
	ce.mode = ConfigModeEditScene
	view := ce.viewEditScene(DefaultTheme())
	if !strings.Contains(view, "L2, L3") || !strings.Contains(view, "30m") {
		t.Errorf("scene view does not show device list and timeout:\n%s", view)
	}
}

func TestConfigEditorLegacySceneOffersAllTargets(t *testing.T) {
	ce := NewConfigEditor(nil, DefaultTheme())
	ce.config = app.EditableConfig{
		Lights:            []app.LightEditConfig{{Name: "L1"}, {Name: "L2"}},
		OutputDeviceNames: []string{"L1", "L2"},
		Scenes:            []app.SceneEditConfig{{Name: "Old", States: []app.SceneStateEditConfig{{Name: "off"}}}},
	}
	openSceneState(t, &ce, 0)
	ce.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	if got := ce.config.Scenes[0].States[0].Actions; len(got) != 1 || got[0] != "on:L1" {
		t.Fatalf("new step = %v, want [on:L1]", got)
	}
	if ce.config.Scenes[0].Devices != nil {
		t.Error("editing a legacy scene must not add a device list")
	}
	ce.mode = ConfigModeEditScene
	if view := ce.viewEditScene(DefaultTheme()); !strings.Contains(view, "legacy") {
		t.Errorf("legacy scene view should say legacy:\n%s", view)
	}
}
