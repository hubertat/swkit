package tui

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/hubertat/swkit/app"
)

// fakeRevisionConfigProvider is a minimal app.ConfigProvider for exercising
// ConfigEditor's revision-checked save path without SwKitConfigProvider (and
// without a real SwKit instance). Revision is always recomputed fresh from
// cfg (never cached), so mutating cfg directly - as a test simulating an
// out-of-band/concurrent change would do - is picked up exactly like the
// real provider picks up a concurrent save. Mirrors
// server/config_edit_test.go's fakeSavingConfigProvider.
type fakeRevisionConfigProvider struct {
	cfg       app.EditableConfig
	saved     *app.EditableConfig
	saveCalls int
}

// GetEditableConfig returns an independent copy of f.cfg (via a JSON
// round-trip), mirroring the real SwKitConfigProvider: it rebuilds
// EditableConfig fresh from p.sw on every call rather than aliasing any
// slice a caller might hold, so a caller mutating its own snapshot (as
// ConfigEditor does while the user edits) can never reach back and mutate
// what this fake considers "persisted".
func (f *fakeRevisionConfigProvider) GetEditableConfig() app.EditableConfig {
	return cloneEditableConfig(f.cfg)
}

func cloneEditableConfig(cfg app.EditableConfig) app.EditableConfig {
	data, err := json.Marshal(cfg)
	if err != nil {
		return cfg
	}
	var out app.EditableConfig
	if err := json.Unmarshal(data, &out); err != nil {
		return cfg
	}
	return out
}

func (f *fakeRevisionConfigProvider) Revision() string { return revisionOf(f.cfg) }

func revisionOf(cfg app.EditableConfig) string {
	data, err := json.Marshal(cfg)
	if err != nil {
		return "unknown"
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])[:16]
}

func (f *fakeRevisionConfigProvider) SaveConfig(config app.EditableConfig) error {
	f.saveCalls++
	cp := cloneEditableConfig(config)
	f.saved = &cp
	f.cfg = cloneEditableConfig(config)
	return nil
}

func (f *fakeRevisionConfigProvider) SaveConfigIfRevision(config app.EditableConfig, expectedRevision string) error {
	if expectedRevision != f.Revision() {
		return app.ErrConfigRevisionMismatch
	}
	return f.SaveConfig(config)
}

func newFixtureConfig(lightName string) app.EditableConfig {
	return app.EditableConfig{
		Lights: []app.LightEditConfig{
			{Name: lightName, DigitalOutName: "mock|d_out|1"},
		},
	}
}

// runSave synchronously executes the tea.Cmd returned by ConfigEditor.Save
// and returns the resulting ConfigSaveMsg's error.
func runSave(t *testing.T, ce *ConfigEditor) error {
	t.Helper()
	cmd := ce.Save()
	if cmd == nil {
		t.Fatal("Save() returned a nil command")
	}
	msg, ok := cmd().(ConfigSaveMsg)
	if !ok {
		t.Fatalf("Save() command produced unexpected message type %T", cmd())
	}
	return msg.Error
}

// (a) A save against an unchanged config succeeds and passes the expected
// revision through to SaveConfigIfRevision.
func TestConfigEditorSaveUnchangedRevisionSucceeds(t *testing.T) {
	provider := &fakeRevisionConfigProvider{cfg: newFixtureConfig("Kitchen")}
	ce := NewConfigEditor(provider, DefaultTheme())

	ce.config.Lights[0].DigitalOutName = "mock|d_out|2"
	ce.dirty = true

	if err := runSave(t, &ce); err != nil {
		t.Fatalf("Save: unexpected error: %v", err)
	}
	if provider.saveCalls != 1 {
		t.Fatalf("expected 1 save call, got %d", provider.saveCalls)
	}
	if provider.saved == nil || provider.saved.Lights[0].DigitalOutName != "mock|d_out|2" {
		t.Fatalf("saved config not as edited: %+v", provider.saved)
	}
}

// (b) A save whose revision no longer matches fails with
// app.ErrConfigRevisionMismatch and does not overwrite the stored config,
// leaving the editor dirty.
func TestConfigEditorSaveStaleRevisionFailsWithoutOverwriting(t *testing.T) {
	provider := &fakeRevisionConfigProvider{cfg: newFixtureConfig("Kitchen")}
	ce := NewConfigEditor(provider, DefaultTheme())

	// Simulate an out-of-band change (e.g. a web-UI save) landing after
	// this editor loaded its snapshot but before it saves.
	provider.cfg = newFixtureConfig("Kitchen (renamed elsewhere)")

	ce.config.Lights[0].DigitalOutName = "mock|d_out|2"
	ce.dirty = true

	err := runSave(t, &ce)
	if err == nil {
		t.Fatal("Save: expected an error, got nil")
	}
	if !isRevisionMismatch(err) {
		t.Fatalf("Save: expected app.ErrConfigRevisionMismatch, got %v", err)
	}
	if provider.saveCalls != 0 {
		t.Fatalf("expected SaveConfigIfRevision to leave the stored config untouched, but SaveConfig was invoked (calls=%d)", provider.saveCalls)
	}
	if provider.cfg.Lights[0].Name != "Kitchen (renamed elsewhere)" {
		t.Fatalf("stored config was overwritten: %+v", provider.cfg)
	}

	// The handler in tui.go is responsible for keeping dirty set on error;
	// Save itself must not have touched it.
	if !ce.dirty {
		t.Fatal("expected editor to remain dirty after a failed save")
	}
}

// (c) Reloading refreshes both config and revision so a subsequent save
// succeeds.
func TestConfigEditorReloadRefreshesConfigAndRevisionThenSaveSucceeds(t *testing.T) {
	provider := &fakeRevisionConfigProvider{cfg: newFixtureConfig("Kitchen")}
	ce := NewConfigEditor(provider, DefaultTheme())

	// Out-of-band change lands, invalidating ce's original revision.
	provider.cfg = newFixtureConfig("Kitchen (renamed elsewhere)")
	staleRevision := ce.revision

	ce.Reload()

	if ce.revision == staleRevision {
		t.Fatal("Reload did not refresh the revision")
	}
	if ce.revision != provider.Revision() {
		t.Fatalf("Reload's revision %q does not match provider's current revision %q", ce.revision, provider.Revision())
	}
	if ce.config.Lights[0].Name != "Kitchen (renamed elsewhere)" {
		t.Fatalf("Reload did not refresh config: %+v", ce.config)
	}
	if ce.dirty {
		t.Fatal("Reload should not leave a clean editor dirty")
	}

	// A fresh edit against the refreshed snapshot must now save cleanly.
	ce.config.Lights[0].DigitalOutName = "mock|d_out|3"
	ce.dirty = true
	if err := runSave(t, &ce); err != nil {
		t.Fatalf("Save after Reload: unexpected error: %v", err)
	}
}

// (d) Reloading while the editor is dirty must not discard in-progress
// edits: Reload is a deliberate no-op in that case (see its doc comment in
// config_editor.go for the reasoning - losing a stale revision is far less
// surprising than silently losing typed-in edits).
func TestConfigEditorReloadWhileDirtyDoesNotDiscardEdits(t *testing.T) {
	provider := &fakeRevisionConfigProvider{cfg: newFixtureConfig("Kitchen")}
	ce := NewConfigEditor(provider, DefaultTheme())

	ce.config.Lights[0].Name = "Kitchen (being edited)"
	ce.dirty = true
	dirtyRevision := ce.revision

	// Out-of-band change lands while the user is still editing.
	provider.cfg = newFixtureConfig("Kitchen (renamed elsewhere)")

	ce.Reload()

	if !ce.dirty {
		t.Fatal("Reload must not clear dirty while edits are in progress")
	}
	if ce.config.Lights[0].Name != "Kitchen (being edited)" {
		t.Fatalf("Reload discarded in-progress edits: %+v", ce.config)
	}
	if ce.revision != dirtyRevision {
		t.Fatal("Reload must not change the tracked revision while dirty")
	}

	// discardAndReload is the explicit, opt-in escape hatch: it does
	// discard the edits and pick up the latest config.
	ce.discardAndReload()

	if ce.dirty {
		t.Fatal("discardAndReload should leave the editor clean")
	}
	if ce.config.Lights[0].Name != "Kitchen (renamed elsewhere)" {
		t.Fatalf("discardAndReload did not pick up the latest config: %+v", ce.config)
	}
	if ce.revision != provider.Revision() {
		t.Fatal("discardAndReload did not refresh the revision")
	}
}

func isRevisionMismatch(err error) bool {
	return errors.Is(err, app.ErrConfigRevisionMismatch)
}

// TestConfigDiscardConfirm_OpensPromptCancelsAndOnlyConfirmDiscards pins the
// full Ctrl+S-hits-a-conflict flow at the Model level (see tui.go's
// configDiscardConfirm handling): a conflict must never be resolved by a
// bare second keypress - it takes an explicit confirmation, defaulting to
// "no", with only "y" actually discarding.
func TestConfigDiscardConfirm_OpensPromptCancelsAndOnlyConfirmDiscards(t *testing.T) {
	provider := &fakeRevisionConfigProvider{cfg: newFixtureConfig("Kitchen")}
	m := NewModelFromOptions(Options{Provider: &fakeStateProvider{}, ConfigProvider: provider})

	// Put the editor into "dirty with a known save conflict" - the state
	// left behind by a ConfigSaveMsg carrying app.ErrConfigRevisionMismatch
	// (see the ConfigSaveMsg handler in tui.go). An out-of-band change has
	// also landed on the provider, standing in for whatever caused the
	// conflict.
	m.configEditor.config.Lights[0].Name = "Kitchen (being edited)"
	m.configEditor.dirty = true
	m.configEditor.saveConflict = true
	provider.cfg = newFixtureConfig("Kitchen (renamed elsewhere)")

	// Ctrl+S while a conflict is flagged must only open the confirmation -
	// never act immediately.
	newModel, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	m = newModel.(Model)
	if cmd != nil {
		t.Fatal("Ctrl+S while in conflict should not itself return a save/discard command - it should only arm the confirmation prompt")
	}
	if !m.configDiscardConfirm {
		t.Fatal("Ctrl+S while in conflict did not open the discard-confirmation prompt")
	}
	if !m.configEditor.dirty {
		t.Fatal("opening the confirmation prompt must not itself clear dirty")
	}
	if m.configEditor.config.Lights[0].Name != "Kitchen (being edited)" {
		t.Fatalf("opening the confirmation prompt must not touch the in-progress edits: %+v", m.configEditor.config)
	}

	// Cancelling (Esc) must leave everything exactly as it was.
	newModel, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = newModel.(Model)
	if m.configDiscardConfirm {
		t.Fatal("Esc did not close the confirmation prompt")
	}
	if !m.configEditor.dirty {
		t.Fatal("cancelling must leave the editor dirty")
	}
	if m.configEditor.config.Lights[0].Name != "Kitchen (being edited)" {
		t.Fatalf("cancelling discarded the in-progress edits: %+v", m.configEditor.config)
	}

	// The reflex-press scenario this test exists to prevent: pressing the
	// save key again (as if retrying a failed save) must reopen the prompt,
	// not silently discard.
	newModel, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	m = newModel.(Model)
	if !m.configDiscardConfirm {
		t.Fatal("a second Ctrl+S press did not reopen the confirmation prompt")
	}
	if !m.configEditor.dirty || m.configEditor.config.Lights[0].Name != "Kitchen (being edited)" {
		t.Fatal("a second Ctrl+S press must not discard the edits by itself")
	}

	// Enter (the default, non-destructive answer per the "[y/N]" prompt)
	// must also cancel rather than discard.
	newModel, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = newModel.(Model)
	if m.configDiscardConfirm {
		t.Fatal("Enter did not close the confirmation prompt")
	}
	if !m.configEditor.dirty || m.configEditor.config.Lights[0].Name != "Kitchen (being edited)" {
		t.Fatal("Enter (the default answer) must not discard the edits")
	}

	// Only an explicit "y" actually discards and reloads.
	newModel, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	m = newModel.(Model)
	newModel, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	m = newModel.(Model)

	if m.configDiscardConfirm {
		t.Fatal("confirming did not close the prompt")
	}
	if m.configEditor.dirty {
		t.Fatal("confirming discard should leave the editor clean")
	}
	if m.configEditor.config.Lights[0].Name != "Kitchen (renamed elsewhere)" {
		t.Fatalf("confirming discard did not load the latest config: %+v", m.configEditor.config)
	}
}

// TestConfigEditorEditAfterConflictClearsSaveConflict pins the decision that
// saveConflict is cleared as soon as the user makes a new edit: continuing
// to work is not, by itself, a request to see the discard-confirmation
// prompt again. If the same stale revision is still the problem, the next
// Save simply reports the conflict again (re-arming saveConflict from
// there).
func TestConfigEditorEditAfterConflictClearsSaveConflict(t *testing.T) {
	provider := &fakeRevisionConfigProvider{cfg: newFixtureConfig("Kitchen")}
	ce := NewConfigEditor(provider, DefaultTheme())

	ce.dirty = true
	ce.saveConflict = true

	ce.markDirty()

	if ce.saveConflict {
		t.Fatal("markDirty (a new edit) should clear a previously flagged save conflict")
	}
	if !ce.dirty {
		t.Fatal("markDirty must still leave the editor dirty")
	}
}
