package server

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hubertat/swkit/app"
)

// sceneDevicesConfig is a valid config with two lights (plus the fixture's
// color light "Mood") and the given scenes.
func sceneDevicesConfig(scenes ...app.SceneEditConfig) app.EditableConfig {
	return app.EditableConfig{
		Lights: []app.LightEditConfig{
			{Name: "Kitchen", DigitalOutName: "gpio|d_out|5"},
			{Name: "Hall", DigitalOutName: "gpio|d_out|6"},
		},
		Scenes: scenes,
	}
}

func sceneWithDevices(name string, devices []string, states ...app.SceneStateEditConfig) app.SceneEditConfig {
	return app.SceneEditConfig{Name: name, Devices: devices, States: states}
}

func offOn(devs ...string) []app.SceneStateEditConfig {
	var off, on []string
	for _, d := range devs {
		off = append(off, "off:"+d)
		on = append(on, "on:"+d)
	}
	return []app.SceneStateEditConfig{{Name: "Off", Actions: off}, {Name: "On", Actions: on}}
}

func postExpectOK(t *testing.T, cfg app.EditableConfig) *fakeSavingConfigProvider {
	t.Helper()
	provider := &fakeSavingConfigProvider{cfg: baseFixtureConfig()}
	ws := newConfigEditTestServer(t, baseFixtureState(), provider)
	body, err := json.Marshal(apiConfigEditPostBody{Config: cfg, Revision: provider.Revision()})
	if err != nil {
		t.Fatal(err)
	}
	code, resp := doConfigEditPost(t, ws, string(body))
	if code != 200 {
		t.Fatalf("status = %d, want 200, errors=%v", code, resp.Errors)
	}
	return provider
}

func TestConfigEdit_SceneDevicesValid(t *testing.T) {
	sc := sceneWithDevices("Evening", []string{" Kitchen ", "Hall", "Mood"}, offOn("Kitchen", "Hall", "Mood")...)
	sc.ToggleTimeoutSeconds = 1800
	// Delayed steps are allowed (state 0 included), and order is kept.
	sc.States[1].Actions = append([]string{"after:10m:off:Hall"}, sc.States[1].Actions...)
	sc.States[0].Actions = append(sc.States[0].Actions, "after:1m:on:Kitchen")
	p := postExpectOK(t, sceneDevicesConfig(sc))

	saved := p.saved.Scenes[0]
	if got := strings.Join(saved.Devices, ","); got != "Kitchen,Hall,Mood" {
		t.Errorf("saved Devices = %q, want trimmed Kitchen,Hall,Mood", got)
	}
	if saved.ToggleTimeoutSeconds != 1800 {
		t.Errorf("saved ToggleTimeoutSeconds = %d, want 1800", saved.ToggleTimeoutSeconds)
	}
	if saved.States[1].Actions[0] != "after:10m:off:Hall" {
		t.Errorf("step order changed: %v", saved.States[1].Actions)
	}
}

func TestConfigEdit_SceneWithoutDevicesRejected(t *testing.T) {
	sc := app.SceneEditConfig{Name: "Old", States: []app.SceneStateEditConfig{
		{Name: "off", Actions: []string{"off:Kitchen", "bogus", "off:Hall"}},
		{Name: "on", Actions: []string{"on:Hall", "on:Mood", "after:5m:off:Kitchen"}},
	}}
	errs := postAndExpectErrors(t, newValidationTestServer(t), sceneDevicesConfig(sc),
		`scene "Old": Devices is required: list the devices this scene controls, e.g. "Devices": ["Kitchen", "Hall", "Mood"]`)
	// The missing list is reported once, not once per step.
	joined := strings.Join(errs, "\n")
	if n := strings.Count(joined, "Devices is required"); n != 1 || strings.Contains(joined, "not in the scene's Devices list") {
		t.Errorf("missing Devices should be reported exactly once: %v", errs)
	}

	// A scene without steps gets a generic example.
	postAndExpectErrors(t, newValidationTestServer(t), sceneDevicesConfig(app.SceneEditConfig{Name: "Bare", States: []app.SceneStateEditConfig{{Name: "off"}}}),
		`scene "Bare": Devices is required: list the devices this scene controls, e.g. "Devices": ["Kitchen", "Hall"]`)
}

func TestConfigEdit_NullAndEmptyDevicesRejected(t *testing.T) {
	for _, devices := range []string{`null`, `[]`} {
		t.Run(devices, func(t *testing.T) {
			provider := &fakeSavingConfigProvider{cfg: baseFixtureConfig()}
			ws := newConfigEditTestServer(t, baseFixtureState(), provider)
			body := `{"revision":"` + provider.Revision() + `","config":{
				"Lights":[{"Name":"Kitchen","DigitalOutName":"gpio|d_out|5"}],
				"Scenes":[{"Name":"S","Devices":` + devices + `,"States":[{"Name":"off","Actions":["off:Kitchen"]}]}]}}`
			code, resp := doConfigEditPost(t, ws, body)
			if code != 400 {
				t.Fatalf("status = %d, want 400, errors=%v", code, resp.Errors)
			}
			want := `scene "S": Devices is required: list the devices this scene controls, e.g. "Devices": ["Kitchen"]`
			if !strings.Contains(strings.Join(resp.Errors, " | "), want) {
				t.Errorf("errors = %v, want %q", resp.Errors, want)
			}
			if provider.saveCalls != 0 {
				t.Errorf("SaveConfig called %d times, want 0", provider.saveCalls)
			}
		})
	}
}

func TestConfigEdit_SceneDevicesValidation(t *testing.T) {
	cases := []struct {
		name   string
		scenes []app.SceneEditConfig
		want   []string
	}{
		{
			name:   "empty list",
			scenes: []app.SceneEditConfig{sceneWithDevices("Evening", []string{}, app.SceneStateEditConfig{Name: "Off"})},
			want:   []string{`scene "Evening": Devices is required: list the devices this scene controls, e.g. "Devices": ["Kitchen", "Hall"]`},
		},
		{
			name:   "blank entry",
			scenes: []app.SceneEditConfig{sceneWithDevices("Evening", []string{"Kitchen", "  "}, offOn("Kitchen")...)},
			want:   []string{`scene "Evening": Devices entry #2: device name must not be empty`},
		},
		{
			name:   "unknown device",
			scenes: []app.SceneEditConfig{sceneWithDevices("Evening", []string{"Kitchen", "Ghost"}, offOn("Kitchen")...)},
			want:   []string{`scene "Evening": Devices entry "Ghost": target device "Ghost" does not exist`},
		},
		{
			name:   "duplicate",
			scenes: []app.SceneEditConfig{sceneWithDevices("Evening", []string{"Kitchen", "Kitchen"}, offOn("Kitchen")...)},
			want:   []string{`scene "Evening": Devices: duplicate device "Kitchen"`},
		},
		{
			name:   "self",
			scenes: []app.SceneEditConfig{sceneWithDevices("Evening", []string{"Kitchen", "Evening"}, offOn("Kitchen")...)},
			want:   []string{`Devices entry "Evening": scene "Evening" cannot target itself`},
		},
		{
			name: "later scene",
			scenes: []app.SceneEditConfig{
				sceneWithDevices("Evening", []string{"Kitchen", "Night"}, offOn("Kitchen")...),
				sceneWithDevices("Night", []string{"Hall"}, app.SceneStateEditConfig{Name: "off"}),
			},
			want: []string{`Devices entry "Night": target scene "Night" is defined later than this scene`},
		},
		{
			name:   "step outside list",
			scenes: []app.SceneEditConfig{sceneWithDevices("Evening", []string{"Kitchen"}, offOn("Kitchen", "Hall")...)},
			want: []string{
				`scene "Evening": state "Off" action #2 ("off:Hall"): device "Hall" is not in the scene's Devices list`,
				`state "On" action #2 ("on:Hall")`,
			},
		},
		{
			name:   "no states",
			scenes: []app.SceneEditConfig{sceneWithDevices("Evening", []string{"Kitchen"})},
			want:   []string{`scene "Evening": a scene needs at least one state (state 0, "Off")`},
		},
		{
			name:   "timeout negative",
			scenes: []app.SceneEditConfig{{Name: "Evening", Devices: []string{"Kitchen"}, ToggleTimeoutSeconds: -5, States: offOn("Kitchen")}},
			want:   []string{`scene "Evening": ToggleTimeoutSeconds -5 out of range (0-86400)`},
		},
		{
			name:   "timeout too long",
			scenes: []app.SceneEditConfig{{Name: "Evening", Devices: []string{"Kitchen"}, ToggleTimeoutSeconds: 86401, States: offOn("Kitchen")}},
			want:   []string{"ToggleTimeoutSeconds 86401 out of range (0-86400)"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			postAndExpectErrors(t, newValidationTestServer(t), sceneDevicesConfig(tc.scenes...), tc.want...)
		})
	}
}

func TestConfigEdit_SceneMayListEarlierScene(t *testing.T) {
	postExpectOK(t, sceneDevicesConfig(
		sceneWithDevices("Night", []string{"Hall"}, offOn("Hall")...),
		sceneWithDevices("Evening", []string{"Kitchen", "Night"}, offOn("Kitchen", "Night")...),
	))
}
