package swkit

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/charmbracelet/log"
	drivers "github.com/hubertat/swkit/drivers"
)

// TestSetupBuildsAndResolvesScenes exercises the real SwKit.Setup wiring: scenes
// are built from config after physical devices and resolve their device names.
func TestSetupBuildsAndResolvesScenes(t *testing.T) {
	sw := &SwKit{
		Name:       "t",
		FakeDriver: &drivers.MockIoDriver{},
		Lights: []LightConfig{
			{Name: "L1", DigitalOutName: "mock_driver|d_out|1"},
			{Name: "L2", DigitalOutName: "mock_driver|d_out|2"},
		},
		Scenes: []SceneConfig{
			{Name: "Movie", Devices: []string{"L1", "L2"}, States: []SceneStateConfig{
				{Name: "off", Actions: []string{"off:L1", "off:L2"}},
				{Name: "on", Actions: []string{"on:L1", "off:L2"}},
			}},
		},
	}

	if err := sw.Setup(context.Background(), log.New(nil)); err != nil {
		t.Fatalf("Setup: %v", err)
	}

	if len(sw.scenes) != 1 {
		t.Fatalf("expected 1 scene, got %d", len(sw.scenes))
	}

	// Scene is resolvable as a Controllable (so buttons could target it).
	dev, ok := sw.resolveControllable("Movie")
	if !ok {
		t.Fatal("scene Movie not resolvable")
	}
	scene, ok := dev.(*Scene)
	if !ok {
		t.Fatalf("resolved Movie is %T, want *Scene", dev)
	}

	// Activating state 1 drives the underlying lights.
	if err := scene.Activate(1); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	if on, _ := sw.lights[0].GetState(); !on {
		t.Error("L1 should be on after activating scene state 1")
	}
	if on, _ := sw.lights[1].GetState(); on {
		t.Error("L2 should be off after activating scene state 1")
	}
}

// TestSetupSceneReferencesEarlierScene verifies a scene may reference a scene
// defined before it.
func TestSetupSceneReferencesEarlierScene(t *testing.T) {
	sw := &SwKit{
		Name:       "t",
		FakeDriver: &drivers.MockIoDriver{},
		Lights:     []LightConfig{{Name: "L1", DigitalOutName: "mock_driver|d_out|1"}},
		Scenes: []SceneConfig{
			{Name: "Inner", Devices: []string{"L1"}, States: []SceneStateConfig{
				{Name: "off", Actions: []string{"off:L1"}},
				{Name: "on", Actions: []string{"on:L1"}},
			}},
			{Name: "Outer", Devices: []string{"Inner"}, States: []SceneStateConfig{
				{Name: "off", Actions: []string{"off:Inner"}},
				{Name: "on", Actions: []string{"on:Inner"}},
			}},
		},
	}

	if err := sw.Setup(context.Background(), log.New(nil)); err != nil {
		t.Fatalf("Setup: %v", err)
	}

	outer, ok := sw.resolveControllable("Outer")
	if !ok {
		t.Fatal("Outer not resolvable")
	}
	outer.SetValue(true)
	if on, _ := sw.lights[0].GetState(); !on {
		t.Error("Outer -> Inner -> L1 should be on")
	}
}

// TestSetupSceneReferencesLaterSceneFails documents that a scene cannot
// reference a scene defined after it.
func TestSetupSceneReferencesLaterSceneFails(t *testing.T) {
	sw := &SwKit{
		Name:       "t",
		FakeDriver: &drivers.MockIoDriver{},
		Lights:     []LightConfig{{Name: "L1", DigitalOutName: "mock_driver|d_out|1"}},
		Scenes: []SceneConfig{
			{Name: "Outer", Devices: []string{"Inner"}, States: []SceneStateConfig{
				{Name: "on", Actions: []string{"on:Inner"}},
			}},
			{Name: "Inner", Devices: []string{"L1"}, States: []SceneStateConfig{
				{Name: "on", Actions: []string{"on:L1"}},
			}},
		},
	}

	if err := sw.Setup(context.Background(), log.New(nil)); err == nil {
		t.Fatal("expected Setup to fail: Outer references later-defined Inner")
	}
}

// TestSetupRejectsSceneWithoutDevices: a config.json scene without a Devices
// list (the old format) refuses to start, with an error that says how to fix
// it, suggesting the devices the scene's steps already use.
func TestSetupRejectsSceneWithoutDevices(t *testing.T) {
	raw := `{
		"Name": "t",
		"Lights": [
			{"Name": "Kitchen", "DigitalOutName": "mock_driver|d_out|1"},
			{"Name": "Hall", "DigitalOutName": "mock_driver|d_out|2"}
		],
		"Scenes": [{"Name": "Evening", "States": [
			{"Name": "Off", "Actions": ["off:Kitchen", "off:Hall"]},
			{"Name": "On", "Actions": ["on:Kitchen", "after:10m:off:Kitchen"]}
		]}]
	}`
	var sw SwKit
	if err := json.Unmarshal([]byte(raw), &sw); err != nil {
		t.Fatal(err)
	}
	sw.FakeDriver = &drivers.MockIoDriver{}

	err := sw.Setup(context.Background(), log.New(nil))
	if err == nil {
		t.Fatal("expected Setup to fail for a scene without Devices")
	}
	want := `scene "Evening": Devices is required: list the devices this scene controls, e.g. "Devices": ["Kitchen", "Hall"]`
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("Setup error = %q, want it to contain %q", err, want)
	}
}

// fakeEmitter is a test PushEventEmitter that lets the test fire events.
type fakeEmitter struct {
	handler func(drivers.PushEvent)
}

func (f *fakeEmitter) Subscribe(eventTypes drivers.PushEvent, handler func(drivers.PushEvent)) error {
	f.handler = handler
	return nil
}
func (f *fakeEmitter) String() string  { return "fake_emitter" }
func (f *fakeEmitter) IsHealthy() bool { return true }
func (f *fakeEmitter) emit(e drivers.PushEvent) {
	if f.handler != nil {
		f.handler(e)
	}
}

// TestButtonDrivesScene verifies a Button can toggle a Scene through the shared
// Controllable machinery.
func TestButtonDrivesScene(t *testing.T) {
	logger := log.New(nil)
	a := newTestLight("A", false)

	scene, err := NewScene(SceneConfig{
		Name:    "S",
		Devices: []string{"A"},
		States: []SceneStateConfig{
			{Name: "off", Actions: []string{"off:A"}},
			{Name: "on", Actions: []string{"on:A"}},
		},
	}, resolverFrom(a), logger)
	if err != nil {
		t.Fatalf("NewScene: %v", err)
	}

	emitter := &fakeEmitter{}
	ctrl := []ControlDevice{{dev: scene, e: drivers.PushEventSinglePress, verb: "toggle"}}
	NewButton(ButtonConfig{Name: "B", DisableHomekit: true}, emitter, ctrl, logger)

	emitter.emit(drivers.PushEventSinglePress) // 0 -> 1
	if on, _ := a.GetState(); !on {
		t.Error("single press should toggle scene to state 1 (A on)")
	}

	emitter.emit(drivers.PushEventSinglePress) // 1 -> 0
	if on, _ := a.GetState(); on {
		t.Error("second press should toggle scene back to state 0 (A off)")
	}
}

// TestSetupDimmableLightDigitalOutOptional exercises SwKit.Setup with one
// dimmable light wired both ways: digital + analog, and analog only.
func TestSetupDimmableLightDigitalOutOptional(t *testing.T) {
	sw := &SwKit{
		Name:       "t",
		FakeDriver: &drivers.MockIoDriver{},
		DimmableLights: []DimmableLightConfig{
			{Name: "Both", DigitalOutName: "mock_driver|d_out|1", AnalogOutName: "mock_driver|a_out|1"},
			{Name: "AnalogOnly", AnalogOutName: "mock_driver|a_out|2"},
		},
	}

	if err := sw.Setup(context.Background(), log.New(nil)); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if len(sw.dimmableLights) != 2 {
		t.Fatalf("expected 2 dimmable lights, got %d", len(sw.dimmableLights))
	}
	if sw.dimmableLights[0].onOut == nil {
		t.Error("Both: digital output should be wired")
	}
	if sw.dimmableLights[1].onOut != nil {
		t.Error("AnalogOnly: digital output should be nil")
	}
	for _, id := range sw.getAllIoIds() {
		if id == "" {
			t.Error("getAllIoIds must not include an empty id for an omitted DigitalOutName")
		}
	}
}
