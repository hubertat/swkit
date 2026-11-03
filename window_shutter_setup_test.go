package swkit

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/log"
	drivers "github.com/hubertat/swkit/drivers"
)

func shutterSwKit() *SwKit {
	return &SwKit{
		Name:       "t",
		FakeDriver: &drivers.MockIoDriver{},
		Lights:     []LightConfig{{Name: "L1", DigitalOutName: "mock_driver|d_out|1"}},
		WindowShutters: []WindowShutterConfig{{
			Name:                "Bedroom shutter",
			OpenDigitalOutName:  "mock_driver|d_out|10",
			CloseDigitalOutName: "mock_driver|d_out|11",
			OpenDurationSeconds: 20,
		}},
	}
}

func TestSetupRegistersWindowShutter(t *testing.T) {
	sw := shutterSwKit()
	if err := sw.Setup(context.Background(), log.New(nil)); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	defer sw.Close()

	if len(sw.windowShutters) != 1 {
		t.Fatalf("expected 1 shutter, got %d", len(sw.windowShutters))
	}
	ws := sw.windowShutters[0]
	if dev, ok := sw.resolveControllable("Bedroom shutter"); !ok || dev != Controllable(ws) {
		t.Fatal("shutter must be a resolvable controllable target")
	}
	foundDevice, foundHk := false, false
	for _, d := range sw.getDevices() {
		if d == Device(ws) {
			foundDevice = true
		}
	}
	for _, th := range sw.getHkThings() {
		if th == HkThing(ws) {
			foundHk = true
		}
	}
	if !foundDevice || !foundHk {
		t.Fatalf("shutter must be synced and exposed to HomeKit (device %v, hk %v)", foundDevice, foundHk)
	}
	// Startup: unknown position, both outputs inactive, no fault.
	s := ws.Snapshot()
	if s.PositionKnown || s.Motion != "stopped" || s.Faulty {
		t.Fatalf("startup snapshot: %+v", s)
	}
	// Sync works without HomeKit initialized.
	if err := ws.Sync(true); err != nil {
		t.Fatalf("sync: %v", err)
	}
	// The mock outputs are real: Open energizes the open output.
	if err := ws.Open(); err != nil {
		t.Fatalf("open: %v", err)
	}
	if on, _ := ws.openDout.GetState(); !on {
		t.Fatal("open output must be energized")
	}
}

// emitterDriver is the mock driver plus push event emitters, so buttons
// can be set up and their events fired from tests.
type emitterDriver struct {
	*drivers.MockIoDriver
	emitters map[string]*shutterTestEmitter
}

type shutterTestEmitter struct {
	id      string
	handler func(drivers.PushEvent)
}

func (e *shutterTestEmitter) Subscribe(_ drivers.PushEvent, h func(drivers.PushEvent)) error {
	e.handler = h
	return nil
}
func (e *shutterTestEmitter) String() string  { return "mock_driver|push_event|" + e.id }
func (e *shutterTestEmitter) IsHealthy() bool { return true }

func (d *emitterDriver) GetPushEventEmitter(id string) (drivers.PushEventEmitter, error) {
	if d.emitters == nil {
		d.emitters = map[string]*shutterTestEmitter{}
	}
	e := &shutterTestEmitter{id: id}
	d.emitters[id] = e
	return e, nil
}

func TestSetupWindowShutterButtonAndScene(t *testing.T) {
	sw := shutterSwKit()
	sw.FakeDriver = nil
	sw.Scenes = []SceneConfig{{
		Name:    "Night",
		Devices: []string{"Bedroom shutter"},
		States: []SceneStateConfig{
			{Name: "Off", Actions: []string{"stop:Bedroom shutter"}},
			{Name: "On", Actions: []string{"close:Bedroom shutter", "after:10s:stop:Bedroom shutter"}},
		},
	}}
	sw.Buttons = []ButtonConfig{{
		Name:           "B1",
		EventInputName: "mock_driver|push_event|5",
		ControlDevices: []string{"single_press:open:Bedroom shutter", "double_press:stop:Bedroom shutter", "long_press:position:40:Bedroom shutter"},
		DisableHomekit: true,
	}}
	d := &emitterDriver{MockIoDriver: &drivers.MockIoDriver{}}
	if err := setupWithDriver(sw, d); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	defer sw.Close()
	if len(sw.scenes) != 1 || len(sw.buttons) != 1 {
		t.Fatal("scene and button must build")
	}
	ws := sw.windowShutters[0]
	em := d.emitters["5"]
	em.handler(drivers.PushEventSinglePress)
	if s := ws.Snapshot(); s.Motion != "opening" {
		t.Fatalf("single press must open: %+v", s)
	}
	em.handler(drivers.PushEventDoublePress)
	if s := ws.Snapshot(); s.Motion != "stopped" {
		t.Fatalf("double press must stop: %+v", s)
	}
	// Position from unknown is rejected (logged), nothing moves.
	em.handler(drivers.PushEventLongPress)
	if s := ws.Snapshot(); s.Motion != "stopped" {
		t.Fatalf("intermediate position from unknown must not move: %+v", s)
	}
	// The scene closes the shutter (fire-and-forget) and Off stops it.
	if err := sw.scenes[0].Activate(1); err != nil {
		t.Fatalf("activate: %v", err)
	}
	if s := ws.Snapshot(); s.Motion != "closing" && !s.PendingReversal {
		t.Fatalf("scene must start closing: %+v", s)
	}
	if err := sw.scenes[0].Activate(0); err != nil {
		t.Fatalf("activate off: %v", err)
	}
	if s := ws.Snapshot(); s.Motion != "stopped" || s.PendingReversal {
		t.Fatalf("scene Off must stop: %+v", s)
	}
}

func TestSetupRejectsOnOffForShutter(t *testing.T) {
	sw := shutterSwKit()
	sw.Scenes = []SceneConfig{{
		Name: "Bad", Devices: []string{"Bedroom shutter"},
		States: []SceneStateConfig{{Name: "Off", Actions: []string{"off:Bedroom shutter"}}},
	}}
	err := sw.Setup(context.Background(), log.New(nil))
	if err == nil || !strings.Contains(err.Error(), "not supported by window shutter") {
		t.Fatalf("expected on/off rejection for a shutter, got %v", err)
	}

	sw = shutterSwKit()
	sw.Scenes = []SceneConfig{{
		Name: "Bad", Devices: []string{"L1"},
		States: []SceneStateConfig{{Name: "Off", Actions: []string{"open:L1"}}},
	}}
	err = sw.Setup(context.Background(), log.New(nil))
	if err == nil || !strings.Contains(err.Error(), "requires a window shutter") {
		t.Fatalf("expected shutter verb rejection for a light, got %v", err)
	}
}

func TestSetupRejectsSharedShutterOutput(t *testing.T) {
	sw := shutterSwKit()
	sw.WindowShutters[0].OpenDigitalOutName = "mock_driver|d_out|1" // owned by L1
	err := sw.Setup(context.Background(), log.New(nil))
	if err == nil || !strings.Contains(err.Error(), "already used by light L1") {
		t.Fatalf("expected shared output error, got %v", err)
	}
}

func TestSetupRejectsInvalidShutterDurations(t *testing.T) {
	sw := shutterSwKit()
	sw.WindowShutters[0].OpenDurationSeconds = 0
	if err := sw.Setup(context.Background(), log.New(nil)); err == nil || !strings.Contains(err.Error(), "OpenDurationSeconds") {
		t.Fatalf("expected duration error, got %v", err)
	}
}

// failingOutputDriver wraps the mock driver and fails GetDigitalOutput for
// one id, to exercise rollback after a shutter acquired its first output.
type failingOutputDriver struct {
	*drivers.MockIoDriver
	failId string
	closed int
}

func (f *failingOutputDriver) GetDigitalOutput(id string) (drivers.DigitalOutput, error) {
	if id == f.failId {
		return nil, errors.New("injected output failure")
	}
	return f.MockIoDriver.GetDigitalOutput(id)
}

func (f *failingOutputDriver) Close() error {
	f.closed++
	return f.MockIoDriver.Close()
}

func TestSetupRollbackOnSecondOutputFailure(t *testing.T) {
	sw := shutterSwKit()
	sw.WindowShutters = append(sw.WindowShutters, WindowShutterConfig{
		Name:                "Second",
		OpenDigitalOutName:  "mock_driver|d_out|20",
		CloseDigitalOutName: "mock_driver|d_out|21",
		OpenDurationSeconds: 10,
	})
	fd := &failingOutputDriver{MockIoDriver: &drivers.MockIoDriver{}, failId: "21"}
	sw.FakeDriver = nil
	err := setupWithDriver(sw, fd)
	if err == nil || !strings.Contains(err.Error(), "injected output failure") {
		t.Fatalf("expected injected failure, got %v", err)
	}
	if len(sw.windowShutters) != 1 {
		t.Fatalf("first shutter must have been built before the failure, got %d", len(sw.windowShutters))
	}
	if err := sw.windowShutters[0].Open(); err == nil {
		t.Fatal("rolled-back shutter must be shut down")
	}
	if fd.closed != 1 {
		t.Fatalf("set-up driver must be closed on rollback, closed %d times", fd.closed)
	}
}

func TestSetupRollbackOnLaterSceneFailure(t *testing.T) {
	sw := shutterSwKit()
	sw.Scenes = []SceneConfig{{
		Name:    "Broken",
		Devices: []string{"Nope"},
		States:  []SceneStateConfig{{Name: "Off", Actions: []string{"off:Nope"}}},
	}}
	if err := sw.Setup(context.Background(), log.New(nil)); err == nil {
		t.Fatal("expected scene failure")
	}
	if len(sw.windowShutters) != 1 {
		t.Fatal("shutter must be built before scenes")
	}
	if err := sw.windowShutters[0].Open(); err == nil {
		t.Fatal("rolled-back shutter must reject commands")
	}
}

func TestReloadStopsMovingShutterAndDiscardsPosition(t *testing.T) {
	old := shutterSwKit()
	if err := old.Setup(context.Background(), log.New(nil)); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	ws := old.windowShutters[0]
	if err := ws.Open(); err != nil {
		t.Fatalf("open: %v", err)
	}
	openOut := ws.openDout
	// Saving an unrelated change (a scene) reloads: the old instance closes.
	if err := old.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if on, _ := openOut.GetState(); on {
		t.Fatal("reload must stop the moving shutter")
	}
	if s := ws.Snapshot(); s.Motion != "stopped" {
		t.Fatalf("old instance must be stopped: %+v", s)
	}
	if err := ws.Open(); err == nil {
		t.Fatal("old instance must reject commands after reload")
	}

	replacement := shutterSwKit()
	replacement.Scenes = []SceneConfig{{
		Name: "Unrelated", Devices: []string{"L1"},
		States: []SceneStateConfig{{Name: "Off", Actions: []string{"off:L1"}}},
	}}
	if err := replacement.Setup(context.Background(), log.New(nil)); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	defer replacement.Close()
	s := replacement.windowShutters[0].Snapshot()
	if s.PositionKnown || s.Motion != "stopped" {
		t.Fatalf("replacement must start unknown and stopped: %+v", s)
	}
}

// setupWithDriver runs Setup with an arbitrary driver injected as the mock
// driver slot.
func setupWithDriver(sw *SwKit, d drivers.IoDriver) error {
	sw.testDriver = d
	return sw.Setup(context.Background(), log.New(nil))
}

func TestSetupRejectsAliasedShutterOutput(t *testing.T) {
	shared := &fakeOut{name: "w4"}
	d := &debugDriver{MockIoDriver: &drivers.MockIoDriver{}, name: "aliasdrv", outs: map[string]drivers.DigitalOutput{
		"4": shared, "2:1": shared, "5": &fakeOut{name: "w5"},
	}}
	sw := &SwKit{
		Name:   "t",
		Lights: []LightConfig{{Name: "L1", DigitalOutName: "aliasdrv|d_out|4"}},
		WindowShutters: []WindowShutterConfig{{
			Name: "Sh", OpenDigitalOutName: "aliasdrv|d_out|2:1", CloseDigitalOutName: "aliasdrv|d_out|5", OpenDurationSeconds: 10,
		}},
	}
	err := setupWithDriver(sw, d)
	if err == nil || !strings.Contains(err.Error(), "already used by light L1") {
		t.Fatalf("aliased output must be rejected: %v", err)
	}
	if shared.isOn() {
		t.Fatal("nothing may be energized")
	}
}
