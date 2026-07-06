package swkit

import (
	"context"
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
			{Name: "Movie", States: []SceneStateConfig{
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
			{Name: "Inner", States: []SceneStateConfig{
				{Name: "off", Actions: []string{"off:L1"}},
				{Name: "on", Actions: []string{"on:L1"}},
			}},
			{Name: "Outer", States: []SceneStateConfig{
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
			{Name: "Outer", States: []SceneStateConfig{
				{Name: "on", Actions: []string{"on:Inner"}},
			}},
			{Name: "Inner", States: []SceneStateConfig{
				{Name: "on", Actions: []string{"on:L1"}},
			}},
		},
	}

	if err := sw.Setup(context.Background(), log.New(nil)); err == nil {
		t.Fatal("expected Setup to fail: Outer references later-defined Inner")
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
		Name: "S",
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
