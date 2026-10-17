package swkit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/log"
	"github.com/hubertat/swkit/app"
	drivers "github.com/hubertat/swkit/drivers"
)

// Devices nil (legacy) and [] (invalid managed) must stay distinct through
// SceneConfig -> SceneEditConfig -> JSON (API) -> SceneEditConfig ->
// SceneConfig -> config.json -> SceneConfig.
func TestConfigProviderSceneDevicesNilVsEmptyRoundTrip(t *testing.T) {
	sw := &SwKit{
		Name:   "t",
		Lights: []LightConfig{{Name: "L1", DigitalOutName: "wago|d_out|0"}},
		Scenes: []SceneConfig{
			{Name: "Legacy", States: []SceneStateConfig{{Name: "Off", Actions: []string{"off:L1"}}}},
			{Name: "Empty", Devices: []string{}, States: []SceneStateConfig{{Name: "Off"}}},
			{Name: "Managed", Devices: []string{"L1"}, ToggleTimeoutSeconds: 1800, States: []SceneStateConfig{{Name: "Off", Actions: []string{"off:L1"}}}},
		},
	}
	path := filepath.Join(t.TempDir(), "config.json")
	p := NewConfigProvider(sw, path)

	check := func(label string, devices [][]string, timeouts []int) {
		t.Helper()
		if devices[0] != nil {
			t.Errorf("%s: Legacy Devices = %#v, want nil", label, devices[0])
		}
		if devices[1] == nil || len(devices[1]) != 0 {
			t.Errorf("%s: Empty Devices = %#v, want non-nil []", label, devices[1])
		}
		if len(devices[2]) != 1 || devices[2][0] != "L1" {
			t.Errorf("%s: Managed Devices = %#v, want [L1]", label, devices[2])
		}
		if timeouts[0] != 0 || timeouts[2] != 1800 {
			t.Errorf("%s: timeouts = %v, want [0 _ 1800]", label, timeouts)
		}
	}
	editDevices := func(cfg app.EditableConfig) ([][]string, []int) {
		var d [][]string
		var to []int
		for _, s := range cfg.Scenes {
			d = append(d, s.Devices)
			to = append(to, s.ToggleTimeoutSeconds)
		}
		return d, to
	}
	swDevices := func(scenes []SceneConfig) ([][]string, []int) {
		var d [][]string
		var to []int
		for _, s := range scenes {
			d = append(d, s.Devices)
			to = append(to, s.ToggleTimeoutSeconds)
		}
		return d, to
	}

	cfg := p.GetEditableConfig()
	d, to := editDevices(cfg)
	check("GetEditableConfig", d, to)

	// Through JSON, as the config editor API does.
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(data), `"Devices"`) != 2 {
		t.Errorf("API JSON should carry Devices for Empty and Managed only: %s", data)
	}
	var decoded app.EditableConfig
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	d, to = editDevices(decoded)
	check("API JSON round trip", d, to)

	// The provider's copy must not alias the SwKit config.
	cfg.Scenes[2].Devices[0] = "mutated"
	if sw.Scenes[2].Devices[0] != "L1" {
		t.Error("GetEditableConfig aliased SceneConfig.Devices")
	}

	if err := p.SaveConfig(decoded); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	d, to = swDevices(sw.Scenes)
	check("SaveConfig", d, to)

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Scenes []map[string]json.RawMessage
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	if _, ok := file.Scenes[0]["Devices"]; ok {
		t.Errorf("legacy scene written with Devices: %s", raw)
	}
	if _, ok := file.Scenes[0]["ToggleTimeoutSeconds"]; ok {
		t.Errorf("zero ToggleTimeoutSeconds written: %s", raw)
	}
	if got := string(file.Scenes[1]["Devices"]); got != "[]" {
		t.Errorf("empty Devices written as %q, want []", got)
	}
	if got := string(file.Scenes[2]["ToggleTimeoutSeconds"]); got != "1800" {
		t.Errorf("ToggleTimeoutSeconds written as %q, want 1800", got)
	}

	var reloaded SwKit
	if err := json.Unmarshal(raw, &reloaded); err != nil {
		t.Fatal(err)
	}
	d, to = swDevices(reloaded.Scenes)
	check("config.json reload", d, to)
}

func TestStateProviderSceneNextAndToggleIndex(t *testing.T) {
	a := newTestLight("A", false)
	scene, err := NewScene(SceneConfig{
		Name:                 "Evening",
		Devices:              []string{"A"},
		ToggleTimeoutSeconds: 60,
		States: []SceneStateConfig{
			{Name: "Off", Actions: []string{"off:A"}},
			{Name: "On", Actions: []string{"on:A"}},
			{Name: "Late", Actions: []string{"on:A"}},
		},
	}, resolverFrom(a), log.New(nil))
	if err != nil {
		t.Fatal(err)
	}
	clk := newFakeClock()
	scene.now = clk.now

	sw := &SwKit{Name: "t"}
	sw.scenes = []*Scene{scene}
	sw.ioDrivers = map[string]drivers.IoDriver{}
	p := NewStateProvider(sw)

	sceneState := func() app.DeviceState {
		t.Helper()
		for _, d := range p.GetState().Devices {
			if d.Type == app.DeviceTypeScene {
				return d
			}
		}
		t.Fatal("no scene in state")
		return app.DeviceState{}
	}

	if ds := sceneState(); ds.SceneStateIndex != 0 || ds.SceneNextIndex != 1 {
		t.Errorf("initial index/next = %d/%d, want 0/1", ds.SceneStateIndex, ds.SceneNextIndex)
	}

	res := p.ToggleDevice(0)
	if res.Error != nil || res.SceneIndex == nil || *res.SceneIndex != 1 || !res.NewState {
		t.Fatalf("toggle result = %+v (SceneIndex %v), want SceneIndex 1, on", res, res.SceneIndex)
	}
	if ds := sceneState(); ds.SceneStateIndex != 1 || ds.SceneNextIndex != 2 {
		t.Errorf("after toggle index/next = %d/%d, want 1/2", ds.SceneStateIndex, ds.SceneNextIndex)
	}

	clk.advance(time.Minute) // timeout reached: next tap returns to Off
	if ds := sceneState(); ds.SceneStateIndex != 1 || ds.SceneNextIndex != 0 {
		t.Errorf("after timeout index/next = %d/%d, want 1/0", ds.SceneStateIndex, ds.SceneNextIndex)
	}
	res = p.ToggleDevice(0)
	if res.SceneIndex == nil || *res.SceneIndex != 0 || res.NewState {
		t.Fatalf("toggle after timeout = %+v, want SceneIndex 0, off", res)
	}
	if on, _ := a.GetState(); on {
		t.Error("toggle after timeout should have switched A off")
	}

	// Non-toggle actions never carry a scene index.
	if res := p.SetDevice(0, true); res.SceneIndex != nil {
		t.Errorf("SetDevice result carries SceneIndex %v", *res.SceneIndex)
	}
}
