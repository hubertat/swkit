package app

import (
	"strings"
	"testing"
)

// The exhaustive rule coverage lives in server/config_edit_test.go, which
// drives these functions through POST /api/config/edit. These tests pin the
// exported entry points the TUI uses directly.

func validateFixture() (EditableConfig, AppState) {
	cfg := EditableConfig{
		Lights:  []LightEditConfig{{Name: "L1", DigitalOutName: "mock|d_out|1"}},
		Buttons: []ButtonEditConfig{{Name: "B1", EventInputName: "mock|push_event|1"}},
		Scenes: []SceneEditConfig{{
			Name:    "Evening",
			Devices: []string{"L1"},
			States:  []SceneStateEditConfig{{Name: "Off", Actions: []string{"off:L1"}}},
		}},
	}
	return cfg, AppState{Drivers: []DriverState{{Name: "mock"}}}
}

func TestValidateEditableConfigAcceptsValid(t *testing.T) {
	cfg, state := validateFixture()
	if errs := ValidateEditableConfig(cfg, state); len(errs) != 0 {
		t.Fatalf("unexpected problems: %v", errs)
	}
}

func TestValidateEditableConfigRejectsEmptySceneDevices(t *testing.T) {
	cfg, state := validateFixture()
	cfg.Scenes[0].Devices = []string{}
	cfg.Scenes[0].States[0].Actions = nil
	errs := ValidateEditableConfig(cfg, state)
	if len(errs) != 1 || !strings.HasPrefix(errs[0], `scene "Evening": Devices is required`) {
		t.Fatalf("problems = %v, want the missing Devices list", errs)
	}
}

func TestValidateEditableConfigUsesState(t *testing.T) {
	cfg, _ := validateFixture()
	// No configured drivers: every io id is dangling.
	errs := ValidateEditableConfig(cfg, AppState{})
	if len(errs) != 2 || !strings.Contains(strings.Join(errs, "\n"), `references driver "mock", which is not configured`) {
		t.Fatalf("problems = %v, want both io ids reported as unconfigured", errs)
	}

	// A color light exists only in state, and is still a valid target.
	cfg, state := validateFixture()
	cfg.Scenes[0].Devices = append(cfg.Scenes[0].Devices, "Mood")
	if errs := ValidateEditableConfig(cfg, state); len(errs) != 1 {
		t.Fatalf("problems = %v, want only the unknown Mood target", errs)
	}
	state.Devices = []DeviceState{{Name: "Mood", Type: DeviceTypeColorLight}}
	if errs := ValidateEditableConfig(cfg, state); len(errs) != 0 {
		t.Fatalf("problems = %v, want none once Mood is a color light", errs)
	}
}

func TestValidateEditableConfigCanonicalEventInput(t *testing.T) {
	cfg, state := validateFixture()
	cfg.Buttons = append(cfg.Buttons, ButtonEditConfig{Name: "B2", EventInputName: "mock|d_in|1"})
	errs := ValidateEditableConfig(cfg, state)
	if len(errs) != 1 || !strings.Contains(errs[0], "assigned to more than one field") {
		t.Fatalf("problems = %v, want d_in and push_event of the same input reported as a duplicate", errs)
	}
}

func TestNormalizeEditableConfigTrims(t *testing.T) {
	cfg := EditableConfig{
		Lights: []LightEditConfig{{Name: " L1 ", DigitalOutName: " mock|d_out|1\t"}},
		Scenes: []SceneEditConfig{{
			Name:    " S ",
			Devices: []string{" L1"},
			States:  []SceneStateEditConfig{{Name: " Off ", Actions: []string{" off:L1 "}}},
		}},
	}
	NormalizeEditableConfig(&cfg)
	sc := cfg.Scenes[0]
	if cfg.Lights[0].Name != "L1" || cfg.Lights[0].DigitalOutName != "mock|d_out|1" ||
		sc.Name != "S" || sc.Devices[0] != "L1" || sc.States[0].Name != "Off" || sc.States[0].Actions[0] != "off:L1" {
		t.Fatalf("not trimmed: %+v", cfg)
	}
}

func TestEditableConfigCloneIsDeep(t *testing.T) {
	cfg, _ := validateFixture()
	cfg.Buttons[0].ControlDevices = []ControlDeviceEdit{{EventType: "single_press", Action: "toggle", DeviceName: "L1"}}
	cp := cfg.Clone()
	cp.Lights[0].Name = "x"
	cp.Buttons[0].ControlDevices[0].DeviceName = "x"
	cp.Scenes[0].Devices[0] = "x"
	cp.Scenes[0].States[0].Actions[0] = "x"
	if cfg.Lights[0].Name != "L1" || cfg.Buttons[0].ControlDevices[0].DeviceName != "L1" ||
		cfg.Scenes[0].Devices[0] != "L1" || cfg.Scenes[0].States[0].Actions[0] != "off:L1" {
		t.Fatalf("Clone shares memory with the original: %+v", cfg)
	}
}
