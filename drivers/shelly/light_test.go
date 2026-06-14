package shelly

import (
	"encoding/json"
	"testing"

	"github.com/hubertat/swkit/drivers/shelly/components"
	"github.com/hubertat/swkit/mqtt"
)

// captureMessenger records the last RpcRequest sent, for asserting RPC payloads.
type captureMessenger struct {
	last mqtt.RpcRequest
	sent int
}

func (cm *captureMessenger) SendRequest(topicRoot string, req mqtt.RpcRequest) error {
	cm.last = req
	cm.sent++
	return nil
}

func newLightStatusJSON(t *testing.T, output bool, brightness float64) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(map[string]interface{}{"output": output, "brightness": brightness})
	if err != nil {
		t.Fatalf("marshal light status: %v", err)
	}
	return b
}

func TestGetLightsParsesLightComponents(t *testing.T) {
	gs := GetStatus{
		Light0: newLightStatusJSON(t, true, 42),
		Light2: newLightStatusJSON(t, false, 0),
	}
	lights := gs.GetLights()
	if len(lights) != 2 {
		t.Fatalf("GetLights returned %d lights, want 2", len(lights))
	}
	if lights[0].ID != 0 || lights[0].Output == nil || !*lights[0].Output {
		t.Errorf("light 0 not parsed correctly: %+v", lights[0])
	}
	if lights[0].Brightness == nil || *lights[0].Brightness != 42 {
		t.Errorf("light 0 brightness not parsed: %+v", lights[0])
	}
	if lights[1].ID != 2 {
		t.Errorf("second light should have ID 2, got %d", lights[1].ID)
	}
}

func TestFillStatusAndGetLightState(t *testing.T) {
	cm := &captureMessenger{}
	dev, err := NewShellyDevice("shellydimmer-test", cm)
	if err != nil {
		t.Fatalf("NewShellyDevice: %v", err)
	}

	status := GetStatus{Light0: newLightStatusJSON(t, true, 73)}
	if err := dev.FillStatus(status); err != nil {
		t.Fatalf("FillStatus: %v", err)
	}

	if dev.LightCount() != 1 {
		t.Fatalf("LightCount = %d, want 1", dev.LightCount())
	}
	on, brightness, err := dev.GetLightState(0)
	if err != nil {
		t.Fatalf("GetLightState: %v", err)
	}
	if !on || brightness != 73 {
		t.Errorf("GetLightState = (%v, %v), want (true, 73)", on, brightness)
	}
}

func TestSetLightSendsPartialLightSet(t *testing.T) {
	cm := &captureMessenger{}
	dev, err := NewShellyDevice("shellydimmer-test", cm)
	if err != nil {
		t.Fatalf("NewShellyDevice: %v", err)
	}

	// brightness only — on must be absent from params
	brightness := 55.0
	if err := dev.SetLight(0, nil, &brightness); err != nil {
		t.Fatalf("SetLight: %v", err)
	}
	if cm.last.Method != "Light.Set" {
		t.Errorf("method = %q, want Light.Set", cm.last.Method)
	}
	if _, ok := cm.last.Params["on"]; ok {
		t.Error("on should be absent when only brightness is set")
	}
	if got := cm.last.Params["brightness"]; got != 55.0 {
		t.Errorf("brightness param = %v, want 55", got)
	}
	if cm.last.Params["id"] != 0 {
		t.Errorf("id param = %v, want 0", cm.last.Params["id"])
	}

	// on only — brightness must be absent
	on := false
	if err := dev.SetLight(1, &on, nil); err != nil {
		t.Fatalf("SetLight: %v", err)
	}
	if _, ok := cm.last.Params["brightness"]; ok {
		t.Error("brightness should be absent when only on is set")
	}
	if cm.last.Params["on"] != false {
		t.Errorf("on param = %v, want false", cm.last.Params["on"])
	}
}

func TestLightStatusUpdateMergesPartial(t *testing.T) {
	on := true
	bri := 80.0
	ls := components.LightStatus{Output: &on, Brightness: &bri}

	// partial update: only brightness changes, output must be preserved
	newBri := 20.0
	ls.Update(components.LightStatus{Brightness: &newBri})
	if ls.Output == nil || !*ls.Output {
		t.Error("output should be preserved through partial update")
	}
	if ls.Brightness == nil || *ls.Brightness != 20 {
		t.Errorf("brightness should be updated to 20, got %+v", ls.Brightness)
	}
}
