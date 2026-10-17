package app

import (
	"strings"
	"testing"
)

func TestValidateSceneConfigRequiresDevices(t *testing.T) {
	steps := []SceneStateEditConfig{
		{Name: "Off", Actions: []string{"off:Kitchen", "bogus", "off:Hall"}},
		{Name: "On", Actions: []string{"after:10m:off:Kitchen", "brightness:40:Desk & Lamp", "on:Hall"}},
	}
	cases := []struct {
		name string
		sc   SceneEditConfig
		want string
	}{
		{"nil, devices from steps", SceneEditConfig{Name: "Evening", States: steps},
			`Devices is required: list the devices this scene controls, e.g. "Devices": ["Kitchen", "Hall", "Desk & Lamp"]`},
		{"empty, devices from steps", SceneEditConfig{Name: "Evening", Devices: []string{}, States: steps},
			`Devices is required: list the devices this scene controls, e.g. "Devices": ["Kitchen", "Hall", "Desk & Lamp"]`},
		{"no steps, generic example", SceneEditConfig{Name: "Bare", States: []SceneStateEditConfig{{Name: "Off"}}},
			`Devices is required: list the devices this scene controls, e.g. "Devices": ["Kitchen", "Hall"]`},
		{"quotes escaped", SceneEditConfig{Name: "Q", States: []SceneStateEditConfig{{Name: "Off", Actions: []string{`off:Say "hi"`}}}},
			`e.g. "Devices": ["Say \"hi\""]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			errs := ValidateSceneConfig(tc.sc, nil)
			if len(errs) != 1 {
				t.Fatalf("errors = %v, want exactly the missing-Devices error", errs)
			}
			if !strings.Contains(errs[0].Error(), tc.want) {
				t.Errorf("error = %q, want it to contain %q", errs[0], tc.want)
			}
		})
	}

	// The timeout range is still checked without a device list.
	errs := ValidateSceneConfig(SceneEditConfig{Name: "T", ToggleTimeoutSeconds: -1, States: steps}, nil)
	if len(errs) != 2 || !strings.Contains(errs[0].Error(), "ToggleTimeoutSeconds -1 out of range") {
		t.Errorf("errors = %v, want the timeout error and the missing-Devices error", errs)
	}
}

func TestSceneStepDevices(t *testing.T) {
	got := SceneStepDevices(SceneEditConfig{States: []SceneStateEditConfig{
		{Actions: []string{"off:B", "off:A", "nonsense"}},
		{Actions: []string{"on:A", "after:1m:toggle:C", "brightness_up:10:B"}},
	}})
	if strings.Join(got, ",") != "B,A,C" {
		t.Errorf("SceneStepDevices = %v, want [B A C]", got)
	}
}
