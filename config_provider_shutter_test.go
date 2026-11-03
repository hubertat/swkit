package swkit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/hubertat/swkit/app"
)

func TestConfigProviderWindowShutterRoundTrip(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	sw := &SwKit{
		Name:        "t",
		Lights:      []LightConfig{{Name: "L1", DigitalOutName: "mock_driver|d_out|1"}},
		ColorLights: []ColorLightConfig{{Name: "Strip", DigitalOutName: "mock_driver|d_out|7", RgbwOutName: "mock_driver|rgbw_out|7"}},
		WindowShutters: []WindowShutterConfig{{
			Name: "Sh", OpenDigitalOutName: "mock_driver|d_out|10", CloseDigitalOutName: "mock_driver|d_out|11",
			OpenDurationSeconds: 20, CloseDurationSeconds: 18, ReverseDelayMilliseconds: 700, DisableHomekit: true,
		}},
	}
	p := NewConfigProvider(sw, configPath)
	cfg := p.GetEditableConfig()
	if len(cfg.WindowShutters) != 1 || cfg.WindowShutters[0].CloseDurationSeconds != 18 || cfg.WindowShutters[0].ReverseDelayMilliseconds != 700 || !cfg.WindowShutters[0].DisableHomekit {
		t.Fatalf("editable shutters: %+v", cfg.WindowShutters)
	}
	foundName := false
	for _, n := range cfg.OutputDeviceNames {
		if n == "Sh" {
			foundName = true
		}
	}
	if !foundName {
		t.Fatal("shutter must be a control target name")
	}
	if cfg.ColorLightOutputOwners["mock_driver|d_out|7"] != "Strip" || cfg.ColorLightOutputOwners["mock_driver|rgbw_out|7"] != "Strip" {
		t.Fatalf("color light owner metadata: %v", cfg.ColorLightOutputOwners)
	}

	rev := p.Revision()
	// Metadata never affects the revision.
	sw.ColorLights[0].Name = "Renamed"
	if p.Revision() != rev {
		t.Fatal("color light metadata must not change the revision")
	}

	// Saving an unrelated edit (a light rename) keeps shutters intact.
	cfg.Lights[0].Name = "L1b"
	if err := p.SaveConfigIfRevision(cfg, rev); err != nil {
		t.Fatalf("save: %v", err)
	}
	if p.Revision() == rev {
		t.Fatal("revision must change with content")
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var reread SwKit
	if err := json.Unmarshal(data, &reread); err != nil {
		t.Fatal(err)
	}
	if len(reread.WindowShutters) != 1 || reread.WindowShutters[0] != sw.WindowShutters[0] {
		t.Fatalf("persisted shutters: %+v", reread.WindowShutters)
	}
	if len(reread.ColorLights) != 1 {
		t.Fatal("color lights must be preserved")
	}

	// Removing all shutters persists their removal.
	cfg2 := p.GetEditableConfig()
	cfg2.WindowShutters = nil
	if err := p.SaveConfig(cfg2); err != nil {
		t.Fatal(err)
	}
	if len(sw.WindowShutters) != 0 {
		t.Fatal("shutters must be removable")
	}
	// Stale revision is rejected.
	if err := p.SaveConfigIfRevision(cfg2, rev); err != app.ErrConfigRevisionMismatch {
		t.Fatalf("stale revision: %v", err)
	}
}

func TestConfigProviderRevisionUnchangedWithoutShutters(t *testing.T) {
	sw := &SwKit{Lights: []LightConfig{{Name: "L1", DigitalOutName: "mock_driver|d_out|1"}}}
	p := NewConfigProvider(sw, filepath.Join(t.TempDir(), "c.json"))
	data, _ := json.Marshal(p.GetEditableConfig())
	var raw map[string]any
	_ = json.Unmarshal(data, &raw)
	if _, ok := raw["WindowShutters"]; ok {
		t.Fatal("configs without shutters must serialize as before (revision compatibility)")
	}
}
