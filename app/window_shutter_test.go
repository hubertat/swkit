package app

import (
	"slices"
	"strings"
	"testing"
)

func TestParseActionShutterVerbs(t *testing.T) {
	cases := map[string]Action{
		"open:Bedroom shutter":        {Verb: "open", Device: "Bedroom shutter"},
		"close:Bedroom shutter":       {Verb: "close", Device: "Bedroom shutter"},
		"stop:Bedroom shutter":        {Verb: "stop", Device: "Bedroom shutter"},
		"toggle:Bedroom shutter":      {Verb: "toggle", Device: "Bedroom shutter"},
		"position:40:Bedroom shutter": {Verb: "position", Level: 40, Device: "Bedroom shutter"},
		"position:0:Bedroom shutter":  {Verb: "position", Level: 0, Device: "Bedroom shutter"},
	}
	for in, want := range cases {
		got, err := ParseAction(in)
		if err != nil {
			t.Fatalf("ParseAction(%q): %v", in, err)
		}
		if got != want {
			t.Fatalf("ParseAction(%q) = %+v, want %+v", in, got, want)
		}
		if got.String() != in {
			t.Fatalf("round trip %q -> %q", in, got.String())
		}
		if IsBrightnessVerb(got.Verb) {
			t.Fatalf("%q must not be classified as brightness", in)
		}
	}
	for _, bad := range []string{"position:101:X", "position:-1:X", "position:X", "open:1:X", "stop"} {
		if _, err := ParseAction(bad); err == nil {
			t.Fatalf("ParseAction(%q): expected error", bad)
		}
	}
	step, err := ParseSceneStep("after:10s:stop:Bedroom shutter")
	if err != nil || step.Verb != "stop" || step.String() != "after:10s:stop:Bedroom shutter" {
		t.Fatalf("delayed shutter step: %+v %v", step, err)
	}
}

func TestActionVerbsForTargetType(t *testing.T) {
	shutter := ActionVerbsForTargetType(TargetTypeWindowShutter)
	if !slices.Equal(shutter, []string{"open", "close", "stop", "position", "toggle"}) {
		t.Fatalf("shutter verbs: %v", shutter)
	}
	for _, tt := range []string{TargetTypeLight, TargetTypeColorLight, TargetTypeOutlet, TargetTypeScene, TargetTypeDimmableLight} {
		verbs := ActionVerbsForTargetType(tt)
		for _, v := range verbs {
			if IsShutterVerb(v) {
				t.Fatalf("%s must not offer shutter verb %q", tt, v)
			}
		}
		if !slices.Contains(verbs, "on") || !slices.Contains(verbs, "toggle") {
			t.Fatalf("%s must offer on/toggle: %v", tt, verbs)
		}
		if (tt == TargetTypeDimmableLight) != slices.Contains(verbs, "brightness") {
			t.Fatalf("brightness only for dimmable lights (%s: %v)", tt, verbs)
		}
	}
	if VerbSupportedByTargetType("on", TargetTypeWindowShutter) || VerbSupportedByTargetType("off", TargetTypeWindowShutter) {
		t.Fatal("shutters reject on/off")
	}
	if !strings.Contains(UnsupportedVerbError("off", "S", TargetTypeWindowShutter).Error(), "open, close, stop") {
		t.Fatal("precise on/off message for shutters")
	}
	by := ActionVerbsByTargetType()
	if len(by) != 6 || !slices.Equal(by[TargetTypeWindowShutter], shutter) {
		t.Fatalf("by type: %v", by)
	}
}

func shutterEditConfig() EditableConfig {
	return EditableConfig{
		Lights:         []LightEditConfig{{Name: "L1", DigitalOutName: "mock_driver|d_out|1"}},
		DimmableLights: []DimmableLightEditConfig{{Name: "D1", AnalogOutName: "mock_driver|a_out|2"}},
		WindowShutters: []WindowShutterEditConfig{{
			Name: "Sh", OpenDigitalOutName: "mock_driver|d_out|10", CloseDigitalOutName: "mock_driver|d_out|11",
			OpenDurationSeconds: 20,
		}},
	}
}

func shutterState() AppState {
	return AppState{Drivers: []DriverState{{Name: "mock_driver"}}}
}

func hasErr(errs []string, sub string) bool {
	for _, e := range errs {
		if strings.Contains(e, sub) {
			return true
		}
	}
	return false
}

func TestValidateWindowShutterConfig(t *testing.T) {
	if errs := ValidateEditableConfig(shutterEditConfig(), shutterState()); len(errs) != 0 {
		t.Fatalf("valid config rejected: %v", errs)
	}
	type mut struct {
		name string
		f    func(*EditableConfig)
		want string // "" = valid
	}
	muts := []mut{
		{"open min", func(c *EditableConfig) { c.WindowShutters[0].OpenDurationSeconds = 1 }, ""},
		{"open max", func(c *EditableConfig) { c.WindowShutters[0].OpenDurationSeconds = 600 }, ""},
		{"open zero", func(c *EditableConfig) { c.WindowShutters[0].OpenDurationSeconds = 0 }, "OpenDurationSeconds 0 out of range"},
		{"open over", func(c *EditableConfig) { c.WindowShutters[0].OpenDurationSeconds = 601 }, "OpenDurationSeconds 601 out of range"},
		{"close fallback", func(c *EditableConfig) { c.WindowShutters[0].CloseDurationSeconds = 0 }, ""},
		{"close min", func(c *EditableConfig) { c.WindowShutters[0].CloseDurationSeconds = 1 }, ""},
		{"close max", func(c *EditableConfig) { c.WindowShutters[0].CloseDurationSeconds = 600 }, ""},
		{"close negative", func(c *EditableConfig) { c.WindowShutters[0].CloseDurationSeconds = -1 }, "CloseDurationSeconds -1 out of range"},
		{"close over", func(c *EditableConfig) { c.WindowShutters[0].CloseDurationSeconds = 601 }, "CloseDurationSeconds 601 out of range"},
		{"delay zero", func(c *EditableConfig) { c.WindowShutters[0].ReverseDelayMilliseconds = 0 }, ""},
		{"delay max", func(c *EditableConfig) { c.WindowShutters[0].ReverseDelayMilliseconds = 5000 }, ""},
		{"delay negative", func(c *EditableConfig) { c.WindowShutters[0].ReverseDelayMilliseconds = -1 }, "0 uses 500 ms"},
		{"delay over", func(c *EditableConfig) { c.WindowShutters[0].ReverseDelayMilliseconds = 5001 }, "ReverseDelayMilliseconds 5001"},
		{"missing output", func(c *EditableConfig) { c.WindowShutters[0].CloseDigitalOutName = "" }, "CloseDigitalOutName must not be empty"},
		{"same outputs", func(c *EditableConfig) { c.WindowShutters[0].CloseDigitalOutName = "mock_driver|d_out|10" }, "must be different outputs"},
		{"wrong type", func(c *EditableConfig) { c.WindowShutters[0].OpenDigitalOutName = "mock_driver|a_out|10" }, "want d_out"},
		{"unknown driver", func(c *EditableConfig) { c.WindowShutters[0].OpenDigitalOutName = "gpio|d_out|10" }, "not configured"},
		{"shared with light", func(c *EditableConfig) { c.WindowShutters[0].OpenDigitalOutName = "mock_driver|d_out|1" }, "assigned to more than one field"},
		{"shared with color light", func(c *EditableConfig) {
			c.ColorLightOutputOwners = map[string]string{"mock_driver|d_out|11": "Strip"}
		}, `already used by color light "Strip"`},
		{"duplicate name", func(c *EditableConfig) { c.WindowShutters[0].Name = "L1" }, "used more than once"},
		{"button on shutter", func(c *EditableConfig) {
			c.Buttons = []ButtonEditConfig{{Name: "B", EventInputName: "mock_driver|push_event|3",
				ControlDevices: []ControlDeviceEdit{{EventType: "single_press", Action: "on", DeviceName: "Sh"}}}}
		}, "not supported by window shutter"},
		{"button open on light", func(c *EditableConfig) {
			c.Buttons = []ButtonEditConfig{{Name: "B", EventInputName: "mock_driver|push_event|3",
				ControlDevices: []ControlDeviceEdit{{EventType: "single_press", Action: "open", DeviceName: "L1"}}}}
		}, "requires a window shutter"},
		{"button position ok", func(c *EditableConfig) {
			c.Buttons = []ButtonEditConfig{{Name: "B", EventInputName: "mock_driver|push_event|3",
				ControlDevices: []ControlDeviceEdit{{EventType: "single_press", Action: "position", Level: 40, DeviceName: "Sh"}}}}
		}, ""},
		{"button position range", func(c *EditableConfig) {
			c.Buttons = []ButtonEditConfig{{Name: "B", EventInputName: "mock_driver|push_event|3",
				ControlDevices: []ControlDeviceEdit{{EventType: "single_press", Action: "position", Level: 140, DeviceName: "Sh"}}}}
		}, "out of range (0-100) for position action"},
		{"scene off on shutter", func(c *EditableConfig) {
			c.Scenes = []SceneEditConfig{{Name: "S", Devices: []string{"Sh"}, States: []SceneStateEditConfig{{Name: "Off", Actions: []string{"off:Sh"}}}}}
		}, "not supported by window shutter"},
		{"scene brightness on shutter", func(c *EditableConfig) {
			c.Scenes = []SceneEditConfig{{Name: "S", Devices: []string{"Sh"}, States: []SceneStateEditConfig{{Name: "Off", Actions: []string{"brightness:20:Sh"}}}}}
		}, "does not support brightness"},
		{"scene stop on light", func(c *EditableConfig) {
			c.Scenes = []SceneEditConfig{{Name: "S", Devices: []string{"L1"}, States: []SceneStateEditConfig{{Name: "Off", Actions: []string{"stop:L1"}}}}}
		}, "requires a window shutter"},
		{"scene shutter ok", func(c *EditableConfig) {
			c.Scenes = []SceneEditConfig{{Name: "S", Devices: []string{"Sh"}, States: []SceneStateEditConfig{
				{Name: "Off", Actions: []string{"stop:Sh"}},
				{Name: "On", Actions: []string{"open:Sh", "after:10s:stop:Sh", "position:0:Sh"}},
			}}}
		}, ""},
	}
	for _, m := range muts {
		cfg := shutterEditConfig()
		m.f(&cfg)
		errs := ValidateEditableConfig(cfg, shutterState())
		if m.want == "" {
			if len(errs) != 0 {
				t.Errorf("%s: unexpected errors %v", m.name, errs)
			}
			continue
		}
		if !hasErr(errs, m.want) {
			t.Errorf("%s: want error containing %q, got %v", m.name, m.want, errs)
		}
	}
}

func TestEditableConfigCloneWindowShutters(t *testing.T) {
	cfg := shutterEditConfig()
	cfg.ColorLightOutputOwners = map[string]string{"x": "Strip"}
	cl := cfg.Clone()
	cl.WindowShutters[0].Name = "changed"
	cl.ColorLightOutputOwners["x"] = "other"
	if cfg.WindowShutters[0].Name != "Sh" || cfg.ColorLightOutputOwners["x"] != "Strip" {
		t.Fatal("clone must not alias shutters or owner metadata")
	}
}

func TestNormalizeWindowShutters(t *testing.T) {
	cfg := shutterEditConfig()
	cfg.WindowShutters[0].Name = "  Sh  "
	cfg.WindowShutters[0].OpenDigitalOutName = " mock_driver|d_out|10 "
	NormalizeEditableConfig(&cfg)
	if cfg.WindowShutters[0].Name != "Sh" || cfg.WindowShutters[0].OpenDigitalOutName != "mock_driver|d_out|10" {
		t.Fatalf("normalize: %+v", cfg.WindowShutters[0])
	}
}

func TestValidateWagoOutputAliases(t *testing.T) {
	state := AppState{
		Drivers: []DriverState{{Name: "wago"}},
		IoDebug: []IoPointDebugState{
			{DriverName: "wago", Index: 4, Name: "M2:DO1[4]", Type: "output"},
			{DriverName: "wago", Index: 5, Name: "M2:DO2[5]", Type: "output"},
		},
	}
	if got := CanonicalOutputId("wago|d_out|b:a", state); got != "wago|d_out|4" {
		t.Fatalf("canonical: %q", got)
	}
	cfg := EditableConfig{
		Lights: []LightEditConfig{{Name: "L1", DigitalOutName: "wago|d_out|4"}},
		WindowShutters: []WindowShutterEditConfig{{
			Name: "Sh", OpenDigitalOutName: "wago|d_out|2:1", CloseDigitalOutName: "wago|d_out|2:2", OpenDurationSeconds: 10,
		}},
	}
	if !hasErr(ValidateEditableConfig(cfg, state), "assigned to more than one field") {
		t.Fatal("an alias of a light's output must be rejected")
	}
	cfg.Lights = nil
	cfg.WindowShutters[0].CloseDigitalOutName = "wago|d_out|4" // alias of its own open output
	if !hasErr(ValidateEditableConfig(cfg, state), "assigned to more than one field") {
		t.Fatal("open and close aliasing one output must be rejected")
	}
	cfg.WindowShutters[0].CloseDigitalOutName = "wago|d_out|5"
	cfg.ColorLightOutputOwners = map[string]string{"wago|d_out|2:1": "Strip"}
	if !hasErr(ValidateEditableConfig(cfg, state), `already used by color light "Strip"`) {
		t.Fatal("color light alias must be rejected")
	}
}
