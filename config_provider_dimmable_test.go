package swkit

import (
	"path/filepath"
	"testing"
)

func TestConfigProviderDimmableLightRoundTrip(t *testing.T) {
	sw := &SwKit{
		Name: "t",
		DimmableLights: []DimmableLightConfig{{
			Name:            "Dim1",
			DigitalOutName:  "wago|d_out|0",
			AnalogOutName:   "wago|a_out|0",
			DefaultSetpoint: 80,
		}},
	}
	path := filepath.Join(t.TempDir(), "config.json")
	p := NewConfigProvider(sw, path)

	// GetEditableConfig should surface the dimmable light and list it as an output target.
	cfg := p.GetEditableConfig()
	if len(cfg.DimmableLights) != 1 {
		t.Fatalf("DimmableLights len = %d, want 1", len(cfg.DimmableLights))
	}
	dl := cfg.DimmableLights[0]
	if dl.Name != "Dim1" || dl.DigitalOutName != "wago|d_out|0" || dl.AnalogOutName != "wago|a_out|0" || dl.DefaultSetpoint != 80 {
		t.Errorf("unexpected dimmable edit config: %+v", dl)
	}
	found := false
	for _, n := range cfg.OutputDeviceNames {
		if n == "Dim1" {
			found = true
		}
	}
	if !found {
		t.Error("Dim1 not present in OutputDeviceNames")
	}

	// Edit and save, then confirm changes round-trip back into the SwKit config.
	cfg.DimmableLights[0].AnalogOutName = "wago|a_out|1"
	cfg.DimmableLights[0].DefaultSetpoint = 50
	if err := p.SaveConfig(cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	saved := sw.DimmableLights[0]
	if len(sw.DimmableLights) != 1 || saved.AnalogOutName != "wago|a_out|1" {
		t.Errorf("SaveConfig did not persist AnalogOutName edit: %+v", sw.DimmableLights)
	}
	if saved.DefaultSetpoint != 50 {
		t.Errorf("SaveConfig did not persist DefaultSetpoint: got %d, want 50", saved.DefaultSetpoint)
	}
}
