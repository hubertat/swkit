package drivers

import (
	"testing"

	"github.com/hubertat/swkit/drivers/shelly"
	"github.com/hubertat/swkit/mqtt"
)

func TestParseShellyIoId(t *testing.T) {
	cases := []struct {
		in        string
		device    string
		component string
		ioNo      int
		wantErr   bool
	}{
		{"shellyplus1-abc:0", "shellyplus1-abc", "", 0, false},
		{"shellyplus1-abc:2", "shellyplus1-abc", "", 2, false},
		{"shellydimmer-xyz:light:0", "shellydimmer-xyz", "light", 0, false},
		{"shellydimmer-xyz:LIGHT:1", "shellydimmer-xyz", "light", 1, false},
		{"", "", "", 0, true},
		{"only-device", "", "", 0, true},
		{"dev:notanumber", "", "", 0, true},
		{"a:b:c:d", "", "", 0, true},
	}
	for _, c := range cases {
		dev, comp, no, err := parseShellyIoId(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("parseShellyIoId(%q): expected error, got none", c.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseShellyIoId(%q): unexpected error: %v", c.in, err)
			continue
		}
		if dev != c.device || comp != c.component || no != c.ioNo {
			t.Errorf("parseShellyIoId(%q) = (%q,%q,%d), want (%q,%q,%d)", c.in, dev, comp, no, c.device, c.component, c.ioNo)
		}
	}
}

// lightTestMessenger is a no-op messenger for binding a device in tests.
type lightTestMessenger struct{ last mqtt.RpcRequest }

func (m *lightTestMessenger) SendRequest(topicRoot string, req mqtt.RpcRequest) error {
	m.last = req
	return nil
}

func newBoundLightDevice(t *testing.T) (*shelly.ShellyDevice, *lightTestMessenger) {
	t.Helper()
	m := &lightTestMessenger{}
	dev, err := shelly.NewShellyDevice("shellydimmer-test", m)
	if err != nil {
		t.Fatalf("NewShellyDevice: %v", err)
	}
	status := shelly.GetStatus{Light0: []byte(`{"output":true,"brightness":40}`)}
	if err := dev.FillStatus(status); err != nil {
		t.Fatalf("FillStatus: %v", err)
	}
	return dev, m
}

func TestShellyOutputLightKindRouting(t *testing.T) {
	dev, m := newBoundLightDevice(t)

	out := &ShellyOutput{deviceId: "shellydimmer-test", switchNo: 0, kind: outputKindLight, dev: dev}

	// GetState reads the light output state
	on, err := out.GetState()
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if !on {
		t.Error("expected light output on")
	}

	// Set routes through Light.Set with on param
	if err := out.Set(false); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if m.last.Method != "Light.Set" {
		t.Errorf("method = %q, want Light.Set", m.last.Method)
	}
	if m.last.Params["on"] != false {
		t.Errorf("on param = %v, want false", m.last.Params["on"])
	}

	if out.getStringId() != "shellydimmer-test:light:0" {
		t.Errorf("getStringId = %q, want shellydimmer-test:light:0", out.getStringId())
	}
}

func TestShellyAnalogOutputBrightness(t *testing.T) {
	dev, m := newBoundLightDevice(t)

	aout := &ShellyAnalogOutput{deviceId: "shellydimmer-test", lightNo: 0, dev: dev}

	if min, max := aout.GetMinMax(); min != 0 || max != 100 {
		t.Errorf("GetMinMax = (%d,%d), want (0,100)", min, max)
	}

	got, err := aout.GetState()
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if got != 40 {
		t.Errorf("GetState = %d, want 40", got)
	}

	if err := aout.Set(70); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if m.last.Method != "Light.Set" {
		t.Errorf("method = %q, want Light.Set", m.last.Method)
	}
	if got := m.last.Params["brightness"]; got != 70.0 {
		t.Errorf("brightness param = %v, want 70", got)
	}
	if _, ok := m.last.Params["on"]; ok {
		t.Error("on should be absent when only brightness is set")
	}

	if aout.getStringId() != "shellydimmer-test:light:0" {
		t.Errorf("getStringId = %q, want shellydimmer-test:light:0", aout.getStringId())
	}
}
