package swkit

import (
	"path/filepath"
	"testing"

	"github.com/charmbracelet/log"
	"github.com/hubertat/swkit/app"
	drivers "github.com/hubertat/swkit/drivers"
)

func TestConfigProviderSceneRoundTrip(t *testing.T) {
	sw := &SwKit{
		Name:   "t",
		Lights: []LightConfig{{Name: "L1", DigitalOutName: "wago|d_out|0"}},
		Scenes: []SceneConfig{{
			Name: "Movie",
			States: []SceneStateConfig{
				{Name: "off", Actions: []string{"off:L1"}},
				{Name: "on", Actions: []string{"on:L1"}},
			},
		}},
	}
	path := filepath.Join(t.TempDir(), "config.json")
	p := NewConfigProvider(sw, path)

	cfg := p.GetEditableConfig()
	if len(cfg.Scenes) != 1 {
		t.Fatalf("Scenes len = %d, want 1", len(cfg.Scenes))
	}
	sc := cfg.Scenes[0]
	if sc.Name != "Movie" || len(sc.States) != 2 {
		t.Fatalf("unexpected scene edit config: %+v", sc)
	}
	if sc.States[1].Name != "on" || len(sc.States[1].Actions) != 1 || sc.States[1].Actions[0] != "on:L1" {
		t.Errorf("unexpected scene state: %+v", sc.States[1])
	}

	// Scene should be a valid control target.
	found := false
	for _, n := range cfg.OutputDeviceNames {
		if n == "Movie" {
			found = true
		}
	}
	if !found {
		t.Error("Movie not present in OutputDeviceNames")
	}

	// Edit and save, then confirm changes round-trip back into the SwKit config.
	cfg.Scenes[0].States = append(cfg.Scenes[0].States, app.SceneStateEditConfig{
		Name:    "dim",
		Actions: []string{"brightness:50:L1"},
	})
	if err := p.SaveConfig(cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	if len(sw.Scenes) != 1 || len(sw.Scenes[0].States) != 3 {
		t.Fatalf("SaveConfig did not persist new state: %+v", sw.Scenes)
	}
	got := sw.Scenes[0].States[2]
	if got.Name != "dim" || len(got.Actions) != 1 || got.Actions[0] != "brightness:50:L1" {
		t.Errorf("unexpected persisted state: %+v", got)
	}
}

func TestStateProviderSceneStateAndIndex(t *testing.T) {
	logger := log.New(nil)
	a := newTestLight("A", false)

	scene, err := NewScene(SceneConfig{
		Name: "Evening",
		States: []SceneStateConfig{
			{Name: "off", Actions: []string{"off:A"}},
			{Name: "on", Actions: []string{"on:A"}},
		},
	}, resolverFrom(a), logger)
	if err != nil {
		t.Fatalf("NewScene: %v", err)
	}

	sw := &SwKit{Name: "t"}
	sw.scenes = []*Scene{scene}
	sw.ioDrivers = map[string]drivers.IoDriver{}
	p := NewStateProvider(sw)

	// Scene appears in state with off as the initial state.
	state := p.GetState()
	var found *app.DeviceState
	for i := range state.Devices {
		if state.Devices[i].Type == app.DeviceTypeScene {
			found = &state.Devices[i]
			break
		}
	}
	if found == nil {
		t.Fatal("no scene device in state")
	}
	if found.Name != "Evening" || found.IsOn || found.SceneStateIndex != 0 {
		t.Errorf("unexpected initial scene state: %+v", found)
	}
	if len(found.SceneStateNames) != 2 || found.SceneStateNames[1] != "on" {
		t.Errorf("unexpected scene state names: %v", found.SceneStateNames)
	}

	// Scene is controllable by index (index 0: the only device).
	dev, name, dtype, err := p.getControllableByIndex(0)
	if err != nil {
		t.Fatalf("getControllableByIndex(0): %v", err)
	}
	if name != "Evening" || dtype != app.DeviceTypeScene {
		t.Errorf("index 0 = (%q,%s), want (Evening, scene)", name, dtype)
	}
	if _, ok := dev.(*Scene); !ok {
		t.Fatalf("index 0 is %T, want *Scene", dev)
	}

	// Toggling advances state; the provider reports the new on-state.
	res := p.ToggleDevice(0)
	if res.Error != nil {
		t.Fatalf("ToggleDevice: %v", res.Error)
	}
	if !res.NewState {
		t.Error("toggle should turn scene on (state 1)")
	}
	if on, _ := a.GetState(); !on {
		t.Error("scene toggle should turn underlying light A on")
	}
}
