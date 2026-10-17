package tui

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/hubertat/swkit/app"
)

// reloadCountingConfigProvider adds TriggerReload to the revision fake and,
// like SwKitConfigProvider.saveConfigLocked, triggers a reload after every
// successful save, so a test can see whether a save reached the provider and
// whether it would have reloaded the application.
type reloadCountingConfigProvider struct {
	fakeRevisionConfigProvider
	reloads int
}

func (f *reloadCountingConfigProvider) TriggerReload() { f.reloads++ }

func (f *reloadCountingConfigProvider) SaveConfigIfRevision(config app.EditableConfig, expectedRevision string) error {
	err := f.fakeRevisionConfigProvider.SaveConfigIfRevision(config, expectedRevision)
	if err == nil {
		f.TriggerReload()
	}
	return err
}

func sceneFixtureConfig() app.EditableConfig {
	return app.EditableConfig{
		Lights:            []app.LightEditConfig{{Name: "L1", DigitalOutName: "mock|d_out|1"}},
		OutputDeviceNames: []string{"L1"},
		Scenes: []app.SceneEditConfig{{
			Name:    "Evening",
			Devices: []string{"L1"},
			States: []app.SceneStateEditConfig{
				{Name: "Off", Actions: []string{"off:L1"}},
				{Name: "On", Actions: []string{"on:L1"}},
			},
		}},
	}
}

// pressSave sends Ctrl+S through the Model, runs the save command it returns
// and feeds the resulting ConfigSaveMsg back, as Bubble Tea would.
func pressSave(t *testing.T, m Model) (Model, ConfigSaveMsg) {
	t.Helper()
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	m = next.(Model)
	if cmd == nil {
		t.Fatal("Ctrl+S on a dirty editor returned no save command")
	}
	msg, ok := cmd().(ConfigSaveMsg)
	if !ok {
		t.Fatalf("save command produced %T, want ConfigSaveMsg", msg)
	}
	next, _ = m.Update(msg)
	return next.(Model), msg
}

// Removing a scene's last device in the TUI checklist leaves Devices empty,
// which the runtime rejects. Saving that must be refused before it reaches
// the provider: a save would trigger a reload, and reload tears the running
// controller down before Setup rejects the scene.
func TestConfigEditorSaveRefusesInvalidConfig(t *testing.T) {
	provider := &reloadCountingConfigProvider{fakeRevisionConfigProvider: fakeRevisionConfigProvider{cfg: sceneFixtureConfig()}}
	m := NewModelFromOptions(Options{Provider: &fakeStateProvider{state: mockDriverState()}, ConfigProvider: provider})
	defer m.Close()
	m.activeTab = TabConfig

	openSceneDevices(t, &m.configEditor, 0)
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeySpace}) // uncheck L1, the only device
	m = next.(Model)
	if got := m.configEditor.config.Scenes[0].Devices; len(got) != 0 {
		t.Fatalf("Devices = %v, want empty after removing the last device", got)
	}

	m, msg := pressSave(t, m)
	var verr *ConfigValidationError
	if !errors.As(msg.Error, &verr) {
		t.Fatalf("save error = %v, want a *ConfigValidationError", msg.Error)
	}
	if provider.saveCalls != 0 {
		t.Fatalf("invalid config reached the provider (%d save calls)", provider.saveCalls)
	}
	if provider.reloads != 0 {
		t.Fatalf("invalid config triggered %d reload(s)", provider.reloads)
	}
	if got := provider.cfg.Scenes[0].Devices; len(got) != 1 || got[0] != "L1" {
		t.Fatalf("stored config changed: Devices = %v", got)
	}
	if !m.configEditor.IsDirty() {
		t.Fatal("a refused save must keep the editor dirty")
	}
	if !strings.Contains(m.configEditor.statusMsg, "Save refused") {
		t.Errorf("status = %q, want a save-refused summary", m.configEditor.statusMsg)
	}
	view := m.configEditor.View(DefaultTheme(), 30)
	if !strings.Contains(view, "Not saved") || !strings.Contains(view, `scene "Evening": Devices is required`) {
		t.Errorf("view does not list the validation problem:\n%s", view)
	}

	// The problem list outlives the fading status line.
	next, _ = m.Update(configSaveClearMsg{})
	m = next.(Model)
	if view := m.configEditor.View(DefaultTheme(), 30); !strings.Contains(view, "Devices is required") {
		t.Errorf("problem list disappeared with the status line:\n%s", view)
	}

	// Fixing the config (re-adding the device) makes the save go through.
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeySpace})
	m = next.(Model)
	m, msg = pressSave(t, m)
	if msg.Error != nil {
		t.Fatalf("valid save failed: %v", msg.Error)
	}
	if provider.saveCalls != 1 || provider.reloads != 1 {
		t.Fatalf("save calls/reloads = %d/%d, want 1/1", provider.saveCalls, provider.reloads)
	}
	if got := provider.cfg.Scenes[0].Devices; len(got) != 1 || got[0] != "L1" {
		t.Fatalf("saved Devices = %v, want [L1]", got)
	}
	if m.configEditor.IsDirty() || len(m.configEditor.saveProblems) != 0 {
		t.Fatalf("after a good save: dirty=%v problems=%v", m.configEditor.IsDirty(), m.configEditor.saveProblems)
	}
}

// Save trims whitespace before validating and saving, as the web API does.
func TestConfigEditorSaveNormalizesBeforeSaving(t *testing.T) {
	provider := &fakeRevisionConfigProvider{cfg: newFixtureConfig("Kitchen")}
	ce := newSavingConfigEditor(provider)
	ce.config.Lights[0].Name = "  Kitchen 2 "
	ce.config.Lights[0].DigitalOutName = " mock|d_out|2"
	ce.markDirty()

	if err := runSave(t, &ce); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if got := provider.saved.Lights[0]; got.Name != "Kitchen 2" || got.DigitalOutName != "mock|d_out|2" {
		t.Fatalf("saved %+v, want trimmed values", got)
	}
}

// Without a state source the editor cannot validate, so it must not save.
func TestConfigEditorSaveWithoutStateSourceRefuses(t *testing.T) {
	provider := &fakeRevisionConfigProvider{cfg: newFixtureConfig("Kitchen")}
	ce := NewConfigEditor(provider, DefaultTheme())
	ce.config.Lights[0].DigitalOutName = "mock|d_out|2"
	ce.markDirty()

	if err := runSave(t, &ce); !errors.Is(err, errNoStateSource) {
		t.Fatalf("Save error = %v, want errNoStateSource", err)
	}
	if provider.saveCalls != 0 {
		t.Fatalf("unvalidated config reached the provider (%d save calls)", provider.saveCalls)
	}
}

// Many problems are listed readably: capped, each line bounded in width.
func TestConfigEditorSaveProblemsTruncated(t *testing.T) {
	ce := NewConfigEditor(nil, DefaultTheme())
	for i := 0; i < maxShownSaveProblems+3; i++ {
		ce.saveProblems = append(ce.saveProblems, fmt.Sprintf("problem %d %s", i, strings.Repeat("x", 200)))
	}
	status := ce.viewStatus(DefaultTheme())
	if !strings.Contains(status, "… and 3 more") {
		t.Errorf("status does not cap the list:\n%s", status)
	}
	if strings.Contains(status, fmt.Sprintf("problem %d", maxShownSaveProblems)) {
		t.Errorf("status shows more than %d problems:\n%s", maxShownSaveProblems, status)
	}
	for _, line := range strings.Split(status, "\n") {
		if w := lipgloss.Width(line); w > maxSaveProblemWidth+4 {
			t.Errorf("status line is %d cells wide: %q", w, line)
		}
	}
}

// The scene device checklist scrolls: with more devices than fit, the view
// stays within the height and the cursor row is always on screen.
func TestConfigEditorSceneDevicesScroll(t *testing.T) {
	const height = 15
	ce := NewConfigEditor(nil, DefaultTheme())
	var names []string
	for i := 1; i <= 40; i++ {
		name := fmt.Sprintf("Lamp %02d", i)
		names = append(names, name)
		ce.config.Lights = append(ce.config.Lights, app.LightEditConfig{Name: name})
	}
	ce.config.Scenes = []app.SceneEditConfig{{
		Name:    "Big",
		Devices: []string{"Lamp 01"},
		States:  []app.SceneStateEditConfig{{Name: "Off", Actions: []string{"off:Lamp 01"}}},
	}}
	openSceneDevices(t, &ce, 0)

	check := func(step int) string {
		t.Helper()
		view := ce.View(DefaultTheme(), height)
		if h := lipgloss.Height(view); h > height {
			t.Fatalf("step %d: view is %d rows, want <= %d:\n%s", step, h, height, view)
		}
		cursorName := names[ce.sceneDeviceCursor]
		found := false
		for _, line := range strings.Split(view, "\n") {
			if strings.Contains(line, "▶ ") && strings.Contains(line, cursorName) {
				found = true
			}
		}
		if !found {
			t.Fatalf("step %d: cursor row %q not visible:\n%s", step, cursorName, view)
		}
		return view
	}

	view := check(0)
	if !strings.Contains(view, "↓ ") || strings.Contains(view, "↑ ") {
		t.Errorf("at the top: want only a ↓ indicator:\n%s", view)
	}
	for i := 1; i < len(names); i++ {
		ce.Update(tea.KeyMsg{Type: tea.KeyDown})
		if ce.sceneDeviceCursor != i {
			t.Fatalf("cursor = %d after %d downs", ce.sceneDeviceCursor, i)
		}
		view = check(i)
		if i == len(names)/2 && (!strings.Contains(view, "↑ ") || !strings.Contains(view, "↓ ")) {
			t.Errorf("mid-list: want both indicators:\n%s", view)
		}
	}
	if !strings.Contains(view, "↑ ") || strings.Contains(view, "↓ ") {
		t.Errorf("at the bottom: want only an ↑ indicator:\n%s", view)
	}

	// Down at the last row stays put; the view still fits.
	ce.Update(tea.KeyMsg{Type: tea.KeyDown})
	if ce.sceneDeviceCursor != len(names)-1 {
		t.Fatalf("cursor moved past the last row: %d", ce.sceneDeviceCursor)
	}
	check(len(names))
}
