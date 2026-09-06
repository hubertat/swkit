package swkit

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/hubertat/swkit/app"
)

func TestMarshalWithoutNilIndentOmitsNilFields(t *testing.T) {
	sw := SwKit{
		Name:    "test",
		Lights:  []LightConfig{},
		Buttons: nil,
		Gpio:    nil,
	}

	data, err := marshalWithoutNilIndent(sw)
	if err != nil {
		t.Fatalf("marshalWithoutNilIndent returned error: %v", err)
	}

	jsonOut := string(data)
	if strings.Contains(jsonOut, `"Buttons": null`) {
		t.Fatalf("expected nil slice to be omitted, got: %s", jsonOut)
	}
	if strings.Contains(jsonOut, `"Gpio": null`) {
		t.Fatalf("expected nil pointer to be omitted, got: %s", jsonOut)
	}
	if !strings.Contains(jsonOut, `"Lights": []`) {
		t.Fatalf("expected non-nil empty slice to be preserved as [], got: %s", jsonOut)
	}
}

func TestMarshalWithoutNilIndentKeepsFalseAndZeroValues(t *testing.T) {
	sw := SwKit{
		Name:        "test",
		HkDebug:     false,
		ColorLights: []ColorLightConfig{},
	}

	data, err := marshalWithoutNilIndent(sw)
	if err != nil {
		t.Fatalf("marshalWithoutNilIndent returned error: %v", err)
	}

	jsonOut := string(data)
	if !strings.Contains(jsonOut, `"HkDebug": false`) {
		t.Fatalf("expected false bool to be preserved, got: %s", jsonOut)
	}
}

func TestNormalizeButtonEventInputIoTypeMigratesDigitalInput(t *testing.T) {
	got := normalizeButtonEventInputIoType("shelly|d_in|dev:1")
	want := "shelly|push_event|dev:1"
	if got != want {
		t.Fatalf("unexpected migrated io id: got %q, want %q", got, want)
	}
}

func TestNormalizeButtonEventInputIoTypeKeepsPushEvent(t *testing.T) {
	input := "shelly|push_event|dev:1"
	got := normalizeButtonEventInputIoType(input)
	if got != input {
		t.Fatalf("expected push_event io id unchanged: got %q, want %q", got, input)
	}
}

func TestNormalizeButtonEventInputIoTypeKeepsInvalidValue(t *testing.T) {
	input := "invalid-value"
	got := normalizeButtonEventInputIoType(input)
	if got != input {
		t.Fatalf("expected invalid value unchanged: got %q, want %q", got, input)
	}
}

// TestSwKitConfigProviderSwapReflectsInEditableConfig proves that Swap
// actually retargets the provider: GetEditableConfig must reflect the
// swapped-in SwKit's config, not the one the provider was constructed with.
// This is what performReload relies on after a hot reload swaps the state
// provider - without it, saves after a reload would read/write through the
// stale, torn-down SwKit.
func TestSwKitConfigProviderSwapReflectsInEditableConfig(t *testing.T) {
	oldSw := &SwKit{
		Name:   "old",
		Lights: []LightConfig{{Name: "Old Light", DigitalOutName: "gpio|d_out|1"}},
	}
	provider := NewConfigProvider(oldSw, "unused-config-path.json")

	before := provider.GetEditableConfig()
	if len(before.Lights) != 1 || before.Lights[0].Name != "Old Light" {
		t.Fatalf("before swap: config = %+v, want one light named Old Light", before)
	}

	newSw := &SwKit{
		Name:   "new",
		Lights: []LightConfig{{Name: "New Light", DigitalOutName: "gpio|d_out|2"}},
	}
	provider.Swap(newSw)

	after := provider.GetEditableConfig()
	if len(after.Lights) != 1 || after.Lights[0].Name != "New Light" {
		t.Fatalf("after swap: config = %+v, want one light named New Light", after)
	}
}

// TestSwKitConfigProviderScenePreservesDisableHomekit proves that
// DisableHomekit round-trips through GetEditableConfig -> SaveConfig for
// scenes, the same as it does for every other device type. Scenes have no
// HomeKit accessory today, but a save from the web UI/TUI must not silently
// erase a flag that already exists in config.json - regressing the mapping
// in either direction (dropped on read, or dropped on write) would flip this
// back to false.
func TestSwKitConfigProviderScenePreservesDisableHomekit(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	sw := &SwKit{
		Name: "test",
		Scenes: []SceneConfig{
			{
				Name:           "Evening",
				DisableHomekit: true,
				States: []SceneStateConfig{
					{Name: "off"},
					{Name: "cozy", Actions: []string{"brightness:40:Hall"}},
				},
			},
		},
	}
	provider := NewConfigProvider(sw, configPath)

	edit := provider.GetEditableConfig()
	if len(edit.Scenes) != 1 || !edit.Scenes[0].DisableHomekit {
		t.Fatalf("GetEditableConfig: scenes = %+v, want one scene with DisableHomekit=true", edit.Scenes)
	}

	if err := provider.SaveConfig(edit); err != nil {
		t.Fatalf("SaveConfig returned error: %v", err)
	}

	if !sw.Scenes[0].DisableHomekit {
		t.Fatalf("SaveConfig: sw.Scenes[0].DisableHomekit = false, want true (flag was dropped on save)")
	}

	// Also confirm it survives the actual JSON round-trip written to disk.
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("reading saved config: %v", err)
	}
	var written SwKit
	if err := json.Unmarshal(data, &written); err != nil {
		t.Fatalf("unmarshal saved config: %v", err)
	}
	if len(written.Scenes) != 1 || !written.Scenes[0].DisableHomekit {
		t.Fatalf("saved config on disk: scenes = %+v, want one scene with DisableHomekit=true", written.Scenes)
	}
}

// ---- Revision / SaveConfigIfRevision (lost-update protection) ----

func newRevisionTestProvider(t *testing.T) *SwKitConfigProvider {
	t.Helper()
	configPath := filepath.Join(t.TempDir(), "config.json")
	sw := &SwKit{
		Name:   "test",
		Lights: []LightConfig{{Name: "Living Room", DigitalOutName: "gpio|d_out|5"}},
	}
	return NewConfigProvider(sw, configPath)
}

// TestSwKitConfigProviderRevisionStableAndContentAddressed proves Revision()
// is non-empty, stable across repeated calls against unchanged content, and
// changes when the persisted editable config changes.
func TestSwKitConfigProviderRevisionStableAndContentAddressed(t *testing.T) {
	provider := newRevisionTestProvider(t)

	rev1 := provider.Revision()
	if rev1 == "" {
		t.Fatal("Revision() is empty, want a non-empty content hash")
	}
	if rev2 := provider.Revision(); rev2 != rev1 {
		t.Errorf("Revision() = %q then %q, want stable across calls with no change", rev1, rev2)
	}

	edit := provider.GetEditableConfig()
	edit.Lights[0].Name = "Renamed"
	if err := provider.SaveConfig(edit); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	if rev3 := provider.Revision(); rev3 == rev1 {
		t.Errorf("Revision() unchanged after SaveConfig altered the config: %q", rev3)
	}
}

// TestSwKitConfigProviderRevisionExcludesOutputDeviceNames proves the design
// choice documented on editableConfigLocked: Revision must not change just
// because a ColorLight (surfaced only via the derived OutputDeviceNames
// field, never part of what SaveConfig persists) is renamed. Otherwise an
// in-flight light/button/scene edit session would be spuriously invalidated
// by an unrelated color-light rename elsewhere.
func TestSwKitConfigProviderRevisionExcludesOutputDeviceNames(t *testing.T) {
	sw := &SwKit{
		Name:        "test",
		Lights:      []LightConfig{{Name: "Living Room", DigitalOutName: "gpio|d_out|5"}},
		ColorLights: []ColorLightConfig{{Name: "Mood", DigitalOutName: "gpio|d_out|8", RgbwOutName: "gpio|rgbw_out|1"}},
	}
	provider := NewConfigProvider(sw, filepath.Join(t.TempDir(), "config.json"))

	before := provider.Revision()

	// Rename the color light directly on the underlying SwKit, exactly as a
	// concurrent, unrelated edit would - GetEditableConfig().OutputDeviceNames
	// would now differ, but the persisted editable config (Lights etc.) has
	// not changed.
	sw.ColorLights[0].Name = "Mood Renamed"

	after := provider.Revision()
	if after != before {
		t.Errorf("Revision() changed from a ColorLight rename alone: before=%q after=%q, want unchanged", before, after)
	}

	edit := provider.GetEditableConfig()
	found := false
	for _, n := range edit.OutputDeviceNames {
		if n == "Mood Renamed" {
			found = true
		}
	}
	if !found {
		t.Errorf("GetEditableConfig().OutputDeviceNames = %v, want it to still reflect the rename (only Revision ignores it)", edit.OutputDeviceNames)
	}
}

// TestSwKitConfigProviderSaveConfigIfRevisionMatching proves a save whose
// expectedRevision matches Revision() persists normally, exactly like
// SaveConfig.
func TestSwKitConfigProviderSaveConfigIfRevisionMatching(t *testing.T) {
	provider := newRevisionTestProvider(t)

	edit := provider.GetEditableConfig()
	edit.Lights[0].Name = "Renamed"

	if err := provider.SaveConfigIfRevision(edit, provider.Revision()); err != nil {
		t.Fatalf("SaveConfigIfRevision: %v", err)
	}

	got := provider.GetEditableConfig()
	if len(got.Lights) != 1 || got.Lights[0].Name != "Renamed" {
		t.Fatalf("config after save = %+v, want the renamed light persisted", got)
	}
}

// TestSwKitConfigProviderSaveConfigIfRevisionStaleRejected proves a save
// whose expectedRevision no longer matches (because the config changed in
// between, simulating a second editor session or an out-of-band reload) is
// rejected with app.ErrConfigRevisionMismatch and leaves the config - both
// in memory and on disk - completely untouched.
func TestSwKitConfigProviderSaveConfigIfRevisionStaleRejected(t *testing.T) {
	provider := newRevisionTestProvider(t)

	staleRevision := provider.Revision()

	// Someone else saves first.
	concurrentEdit := provider.GetEditableConfig()
	concurrentEdit.Lights[0].Name = "Changed By Someone Else"
	if err := provider.SaveConfig(concurrentEdit); err != nil {
		t.Fatalf("SaveConfig (simulating a concurrent save): %v", err)
	}

	// This session's save, built from the config it read before the
	// concurrent save above, now carries a stale revision.
	staleEdit := app.EditableConfig{
		Lights: []app.LightEditConfig{{Name: "Should Not Be Saved", DigitalOutName: "gpio|d_out|5"}},
	}
	err := provider.SaveConfigIfRevision(staleEdit, staleRevision)
	if !errors.Is(err, app.ErrConfigRevisionMismatch) {
		t.Fatalf("SaveConfigIfRevision error = %v, want app.ErrConfigRevisionMismatch", err)
	}

	got := provider.GetEditableConfig()
	if len(got.Lights) != 1 || got.Lights[0].Name != "Changed By Someone Else" {
		t.Fatalf("config after rejected stale save = %+v, want the concurrent save's content untouched", got)
	}

	// Also on disk: a rejected save must not rewrite config.json, and must
	// not leave a backup copy behind either (backupConfig runs first inside
	// saveConfigLocked, so a mismatch checked too late would still litter
	// the config directory with a spurious backup on every conflict).
	onDisk, err := os.ReadFile(provider.configPath)
	if err != nil {
		t.Fatalf("read config file: %v", err)
	}
	var written SwKit
	if err := json.Unmarshal(onDisk, &written); err != nil {
		t.Fatalf("unmarshal config file: %v", err)
	}
	if len(written.Lights) != 1 || written.Lights[0].Name != "Changed By Someone Else" {
		t.Fatalf("config file after rejected stale save: lights = %+v, want the concurrent save's content untouched", written.Lights)
	}
	entries, err := os.ReadDir(filepath.Dir(provider.configPath))
	if err != nil {
		t.Fatalf("read config dir: %v", err)
	}
	if len(entries) != 1 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("config dir contains %v, want only config.json (a rejected save must not write a backup)", names)
	}
}

// TestSwKitConfigProviderSaveConfigIfRevisionConcurrent races two savers that
// both hold the same (currently valid) revision. The compare-and-swap must
// admit exactly one: whichever loses sees the other's write reflected in the
// revision and must be rejected, never merged or silently applied on top.
// Run under -race, this also covers the check and the save happening under a
// single lock acquisition - a check that released the lock before saving
// would let both writers through.
func TestSwKitConfigProviderSaveConfigIfRevisionConcurrent(t *testing.T) {
	provider := newRevisionTestProvider(t)
	revision := provider.Revision()

	// Several savers rather than two: the window a non-atomic
	// check-then-save would leave open is narrow, and more contenders make
	// it far likelier that at least two of them pass the check before the
	// first write lands - which is exactly what this test must not tolerate.
	names := []string{"Saver A", "Saver B", "Saver C", "Saver D", "Saver E", "Saver F", "Saver G", "Saver H"}
	errs := make([]error, len(names))
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i, name := range names {
		wg.Add(1)
		go func(i int, name string) {
			defer wg.Done()
			cfg := app.EditableConfig{
				Lights: []app.LightEditConfig{{Name: name, DigitalOutName: "gpio|d_out|5"}},
			}
			<-start
			errs[i] = provider.SaveConfigIfRevision(cfg, revision)
		}(i, name)
	}
	close(start)
	wg.Wait()

	winners := 0
	for i, err := range errs {
		switch {
		case err == nil:
			winners++
		case errors.Is(err, app.ErrConfigRevisionMismatch):
		default:
			t.Fatalf("%s: unexpected error: %v", names[i], err)
		}
	}
	if winners != 1 {
		t.Fatalf("%d of %d concurrent saves succeeded with the same revision, want exactly 1 (errors: %v)", winners, len(names), errs)
	}

	// The surviving config must be one saver's write in full, not a blend,
	// and the revision must have moved off the one both of them used.
	got := provider.GetEditableConfig()
	if len(got.Lights) != 1 || !strings.HasPrefix(got.Lights[0].Name, "Saver ") {
		t.Errorf("config after the race = %+v, want exactly one saver's light", got.Lights)
	}
	if provider.Revision() == revision {
		t.Error("Revision() unchanged after the winning save, want it to have moved")
	}
}

// TestSwKitConfigProviderRevisionCoversEveryEditableChange pins down what the
// revision must be sensitive to: it is the guard against lost updates, so any
// change a save can make - including deep inside a scene's actions, a bare
// bool, or a pure reordering that changes no field value at all - has to move
// it, or that change can be silently overwritten by a stale save.
func TestSwKitConfigProviderRevisionCoversEveryEditableChange(t *testing.T) {
	baseSw := func() *SwKit {
		return &SwKit{
			Name: "test",
			Lights: []LightConfig{
				{Name: "Living Room", DigitalOutName: "gpio|d_out|5"},
				{Name: "Kitchen", DigitalOutName: "gpio|d_out|6"},
			},
			Buttons: []ButtonConfig{{
				Name:           "Btn",
				EventInputName: "gpio|push_event|7",
				ControlDevices: []string{"single_press:toggle:Living Room"},
			}},
			Scenes: []SceneConfig{{
				Name: "Evening",
				States: []SceneStateConfig{
					{Name: "On", Actions: []string{"on:Living Room"}},
				},
			}},
		}
	}

	tests := []struct {
		name   string
		mutate func(sw *SwKit)
	}{
		{"nested scene action", func(sw *SwKit) { sw.Scenes[0].States[0].Actions[0] = "off:Living Room" }},
		{"nested scene state name", func(sw *SwKit) { sw.Scenes[0].States[0].Name = "Renamed" }},
		{"added scene state", func(sw *SwKit) {
			sw.Scenes[0].States = append(sw.Scenes[0].States, SceneStateConfig{Name: "Off", Actions: []string{"off:Kitchen"}})
		}},
		{"scene DisableHomekit bool", func(sw *SwKit) { sw.Scenes[0].DisableHomekit = true }},
		{"light DisableHomekit bool", func(sw *SwKit) { sw.Lights[0].DisableHomekit = true }},
		{"button control relation", func(sw *SwKit) {
			sw.Buttons[0].ControlDevices = []string{"single_press:toggle:Kitchen"}
		}},
		{"light reordering only", func(sw *SwKit) { sw.Lights[0], sw.Lights[1] = sw.Lights[1], sw.Lights[0] }},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sw := baseSw()
			provider := NewConfigProvider(sw, filepath.Join(t.TempDir(), "config.json"))
			before := provider.Revision()
			tc.mutate(sw)
			if after := provider.Revision(); after == before {
				t.Errorf("Revision() unchanged by %s: %q - a stale save could silently overwrite it", tc.name, after)
			}
		})
	}
}
