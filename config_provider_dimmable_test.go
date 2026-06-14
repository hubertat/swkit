package swkit

import (
	"path/filepath"
	"testing"
)

func TestConfigProviderDimmableLightRoundTrip(t *testing.T) {
	sw := &SwKit{
		Name: "t",
		DimmableLights: []DimmableLightConfig{{
			Name:           "Dim1",
			DigitalOutName: "wago|d_out|0",
			AnalogOutName:  "wago|a_out|0",
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
	if dl.Name != "Dim1" || dl.DigitalOutName != "wago|d_out|0" || dl.AnalogOutName != "wago|a_out|0" {
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

	// Edit and save, then confirm it round-trips back into the SwKit config.
	cfg.DimmableLights[0].AnalogOutName = "wago|a_out|1"
	if err := p.SaveConfig(cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	if len(sw.DimmableLights) != 1 || sw.DimmableLights[0].AnalogOutName != "wago|a_out|1" {
		t.Errorf("SaveConfig did not persist edit: %+v", sw.DimmableLights)
	}
}
