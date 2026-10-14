package drivers

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/hubertat/swkit/drivers/shelly"
	"github.com/hubertat/swkit/logging"
)

// The Shelly Plus 0-10V Dimmer exposes light:0 plus input:0 and input:1 and
// no switch component. These tests wire it the way a config would:
//
//	shelly|d_out|<dev>:light:0  on/off
//	shelly|a_out|<dev>:light:0  brightness
//	shelly|d_in|<dev>:0, :1     push events
const plus10vId = "shellyplus010v-test"

func newPlus10vIO(t *testing.T) (*ShellyIO, *shelly.ShellyDevice) {
	t.Helper()
	dev, err := shelly.NewShellyDevice(plus10vId, &lightTestMessenger{})
	if err != nil {
		t.Fatalf("NewShellyDevice: %v", err)
	}
	status := shelly.GetStatus{
		Light0: []byte(`{"id":0,"source":"init","output":false,"brightness":50}`),
		Input0: []byte(`{"id":0,"state":null}`),
		Input1: []byte(`{"id":1,"state":null}`),
	}
	if err := dev.FillStatus(status); err != nil {
		t.Fatalf("FillStatus: %v", err)
	}

	logger := logging.NewLogger(logging.PrefixShelly)
	she := &ShellyIO{
		logger:            logger,
		devices:           []*shelly.ShellyDevice{dev},
		outputLastChanged: map[string]time.Time{},
		inputLastEvent:    map[string]time.Time{},
		prevSwitchStates:  map[string]bool{},
		outputs:           []*ShellyOutput{{deviceId: plus10vId, switchNo: 0, kind: outputKindLight}},
		analogOutputs:     []*ShellyAnalogOutput{{deviceId: plus10vId, lightNo: 0}},
		inputs: []*ShellyInput{
			{deviceId: plus10vId, inputNo: 0, logger: logger},
			{deviceId: plus10vId, inputNo: 1, logger: logger},
		},
	}
	if err := she.matchDevices(); err != nil {
		t.Fatalf("matchDevices: %v", err)
	}
	return she, dev
}

func TestShellyPlus10vResolvesIos(t *testing.T) {
	she, _ := newPlus10vIO(t)

	out, err := she.GetDigitalOutput(plus10vId + ":light:0")
	if err != nil {
		t.Fatalf("GetDigitalOutput: %v", err)
	}
	if on, err := out.GetState(); err != nil || on {
		t.Errorf("d_out GetState = (%v, %v), want (false, nil)", on, err)
	}

	aout, err := she.GetAnalogOutput(plus10vId + ":light:0")
	if err != nil {
		t.Fatalf("GetAnalogOutput: %v", err)
	}
	if v, err := aout.GetState(); err != nil || v != 50 {
		t.Errorf("a_out GetState = (%v, %v), want (50, nil)", v, err)
	}

	for _, id := range []string{plus10vId + ":0", plus10vId + ":1"} {
		if _, err := she.GetPushEventEmitter(id); err != nil {
			t.Errorf("GetPushEventEmitter(%s): %v", id, err)
		}
	}
}

// A physical input switching the light must reach the d_out's state callback;
// Light skips polling between force syncs once the callback is registered.
func TestShellyPlus10vLightStateCallback(t *testing.T) {
	she, dev := newPlus10vIO(t)

	out, _ := she.GetDigitalOutput(plus10vId + ":light:0")
	got := make(chan bool, 1)
	if err := out.SetOnStateUpdate(func(s bool) { got <- s }); err != nil {
		t.Fatalf("SetOnStateUpdate: %v", err)
	}

	before := she.captureOutputStates(plus10vId)
	if err := dev.UpdateFromStatus(shelly.GetStatus{Light0: []byte(`{"id":0,"source":"button","output":true}`)}); err != nil {
		t.Fatalf("UpdateFromStatus: %v", err)
	}
	she.notifyStateChanges(before, "test", "NotifyStatus")

	select {
	case s := <-got:
		if !s {
			t.Error("callback state = false, want true")
		}
	default:
		t.Fatal("state callback not fired for light output")
	}
	if she.outputLastChanged[outputStateKey(plus10vId, outputKindLight, 0)].IsZero() {
		t.Error("outputLastChanged not recorded for light")
	}
}

func TestShellyPlus10vInputEvents(t *testing.T) {
	she, _ := newPlus10vIO(t)

	fired := map[int]PushEvent{}
	for _, no := range []int{0, 1} {
		no := no
		em, _ := she.GetPushEventEmitter(getShellyIoId(plus10vId, no))
		if err := em.Subscribe(PushEventSinglePress|PushEventLongPress, func(e PushEvent) { fired[no] = e }); err != nil {
			t.Fatalf("Subscribe input %d: %v", no, err)
		}
	}

	// Wire format of a Gen2 NotifyEvent's params.
	var raw shelly.RawEvents
	if err := json.Unmarshal([]byte(`{"ts":1.0,"events":[{"component":"input:1","id":1,"event":"long_push","ts":1.0}]}`), &raw); err != nil {
		t.Fatalf("unmarshal events: %v", err)
	}
	evs, err := raw.GetEvents()
	if err != nil {
		t.Fatalf("GetEvents: %v", err)
	}
	she.dispatchEvents(plus10vId, evs)

	if _, ok := fired[0]; ok {
		t.Error("input 0 fired for an input:1 event")
	}
	if fired[1] != PushEventLongPress {
		t.Errorf("input 1 fired %v, want long press", fired[1])
	}
}

// The light's on/off must be offered as a digital output, numbered after all
// switches, so the config editors can pick it for DigitalOutName.
func TestShellyPlus10vSnapshotOffersLightOutput(t *testing.T) {
	she, _ := newPlus10vIO(t)

	var dOut, aOut *IoPointState
	snap := she.GetIoDebugSnapshot()
	for i, pt := range snap.Points {
		switch pt.Type {
		case IoTypeDigitalOutput:
			dOut = &snap.Points[i]
		case IoTypeAnalogOutput:
			aOut = &snap.Points[i]
		}
	}
	if dOut == nil || dOut.Name != plus10vId+":light0" || dOut.Index != 0 {
		t.Fatalf("digital output point = %+v, want %s:light0 at index 0", dOut, plus10vId)
	}
	if aOut == nil || aOut.Name != plus10vId+":light0" || aOut.Value != 50 {
		t.Fatalf("analog output point = %+v, want %s:light0 at 50", aOut, plus10vId)
	}

	m := &lightTestMessenger{}
	dev, _ := shelly.NewShellyDevice(plus10vId, m)
	if err := dev.FillStatus(shelly.GetStatus{Light0: []byte(`{"output":false,"brightness":50}`)}); err != nil {
		t.Fatalf("FillStatus: %v", err)
	}
	she.devices = []*shelly.ShellyDevice{dev}
	if err := she.ToggleOutput(dOut.Index); err != nil {
		t.Fatalf("ToggleOutput: %v", err)
	}
	if m.last.Method != "Light.Set" || m.last.Params["on"] != true {
		t.Errorf("toggle sent %s %v, want Light.Set on=true", m.last.Method, m.last.Params)
	}
	if _, ok := m.last.Params["brightness"]; ok {
		t.Error("toggle should not change brightness")
	}
}

// Analog-only dimmable lights switch the 0-10V through its brightness output.
func TestShellyAnalogOutputLevelSwitchesPower(t *testing.T) {
	m := &lightTestMessenger{}
	dev, _ := shelly.NewShellyDevice(plus10vId, m)
	if err := dev.FillStatus(shelly.GetStatus{Light0: []byte(`{"output":false,"brightness":50}`)}); err != nil {
		t.Fatalf("FillStatus: %v", err)
	}
	aout := &ShellyAnalogOutput{deviceId: plus10vId, lightNo: 0, dev: dev}

	if v, _ := aout.GetState(); v != 50 {
		t.Errorf("brightness-only GetState while off = %d, want 50", v)
	}

	aout.SetLevelSwitchesPower(true)
	if v, _ := aout.GetState(); v != 0 {
		t.Errorf("GetState while off = %d, want 0", v)
	}

	if err := aout.Set(60); err != nil {
		t.Fatalf("Set(60): %v", err)
	}
	if m.last.Params["on"] != true || m.last.Params["brightness"] != 60.0 {
		t.Errorf("Set(60) params = %v, want on=true brightness=60", m.last.Params)
	}

	if err := aout.Set(0); err != nil {
		t.Fatalf("Set(0): %v", err)
	}
	if m.last.Params["on"] != false {
		t.Errorf("Set(0) on = %v, want false", m.last.Params["on"])
	}
	if _, ok := m.last.Params["brightness"]; ok {
		t.Error("Set(0) should switch off without touching brightness")
	}

	if err := dev.UpdateFromStatus(shelly.GetStatus{Light0: []byte(`{"output":true,"brightness":0.2}`)}); err != nil {
		t.Fatalf("UpdateFromStatus: %v", err)
	}
	if v, _ := aout.GetState(); v != 1 {
		t.Errorf("GetState while on at 0.2%% = %d, want 1 (never the off level)", v)
	}
}
