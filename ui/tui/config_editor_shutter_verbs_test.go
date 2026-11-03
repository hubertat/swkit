package tui

import (
	"slices"
	"testing"

	"github.com/hubertat/swkit/app"
)

func shutterVerbFixture() app.EditableConfig {
	return app.EditableConfig{
		Lights:                 []app.LightEditConfig{{Name: "L1", DigitalOutName: "mock|d_out|1"}},
		DimmableLights:         []app.DimmableLightEditConfig{{Name: "D1", AnalogOutName: "mock|a_out|2"}},
		WindowShutters:         []app.WindowShutterEditConfig{{Name: "Sh", OpenDigitalOutName: "mock|d_out|10", CloseDigitalOutName: "mock|d_out|11", OpenDurationSeconds: 20}},
		Scenes:                 []app.SceneEditConfig{{Name: "S", Devices: []string{"L1"}, States: []app.SceneStateEditConfig{{Name: "Off", Actions: []string{"off:L1"}}}}},
		ColorLightOutputOwners: map[string]string{"mock|d_out|7": "Strip"},
		OutputDeviceNames:      []string{"L1", "D1", "Sh", "S", "Strip"},
	}
}

func TestConfigEditorVerbsPerTarget(t *testing.T) {
	ce := newSavingConfigEditor(&fakeRevisionConfigProvider{cfg: shutterVerbFixture()})
	if got := ce.verbsForTarget("Sh"); !slices.Equal(got, []string{"open", "close", "stop", "position", "toggle"}) {
		t.Fatalf("shutter verbs: %v", got)
	}
	for _, name := range []string{"L1", "S", "Strip", "D1"} {
		for _, v := range ce.verbsForTarget(name) {
			if app.IsShutterVerb(v) {
				t.Fatalf("%s must not offer %q", name, v)
			}
		}
	}
	if ce.targetTypeOf("Strip") != app.TargetTypeColorLight {
		t.Fatal("color lights come from the owner metadata")
	}
	// Cycling a light's verb never reaches shutter verbs.
	ctrl := &app.ControlDeviceEdit{EventType: "single_press", Action: "toggle", DeviceName: "L1"}
	ce.ctrlFieldCursor = 1
	for i := 0; i < 10; i++ {
		ce.cycleCtrlField(ctrl, 1)
		if app.IsShutterVerb(ctrl.Action) {
			t.Fatalf("light cycled to %q", ctrl.Action)
		}
	}
	// Retargeting to a shutter revalidates an on/off verb.
	ctrl.Action = "on"
	ce.ctrlFieldCursor = 2
	for ctrl.DeviceName != "Sh" {
		ce.cycleCtrlField(ctrl, 1)
	}
	if ctrl.Action != "toggle" {
		t.Fatalf("on must be coerced for a shutter target, got %q", ctrl.Action)
	}
}

func TestConfigEditorSceneShutterDefaults(t *testing.T) {
	ce := newSavingConfigEditor(&fakeRevisionConfigProvider{cfg: shutterVerbFixture()})
	// Existing scene: adding a shutter appends Stop to state 0 only.
	scene := &app.SceneEditConfig{Name: "X", Devices: []string{"L1"}, States: []app.SceneStateEditConfig{
		{Name: "Off", Actions: []string{"off:L1"}},
		{Name: "On", Actions: []string{"on:L1"}},
	}}
	ce.addSceneDevice(scene, "Sh")
	if !slices.Equal(scene.States[0].Actions, []string{"off:L1", "stop:Sh"}) || !slices.Equal(scene.States[1].Actions, []string{"on:L1"}) {
		t.Fatalf("existing scene: %+v", scene.States)
	}
	// New scene: Stop in Off, Open in On.
	fresh := &app.SceneEditConfig{Name: "Y"}
	ce.sceneDevicesNewScene = true
	ce.addSceneDevice(fresh, "Sh")
	ce.addSceneDevice(fresh, "L1")
	if !slices.Equal(fresh.States[0].Actions, []string{"stop:Sh", "off:L1"}) || !slices.Equal(fresh.States[1].Actions, []string{"open:Sh", "on:L1"}) {
		t.Fatalf("new scene: %+v", fresh.States)
	}
}
