package rpixel_test

// Driver tests against the fake device. They run in the external test
// package because rpixeltest imports rpixel; export_test.go exposes the few
// hooks they need (fast timing, command generation), and internal_test.go
// holds the tests that need no device (transport matching, timing defaults).

import (
	"encoding/json"
	"errors"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hubertat/swkit/drivers"
	"github.com/hubertat/swkit/drivers/rpixel"
	"github.com/hubertat/swkit/drivers/rpixel/rpixeltest"
)

// idleMs is a poll interval long enough that no poll runs during a test,
// keeping command tests free of interleaved G traffic.
const idleMs = 600000

func newFake(t *testing.T) *rpixeltest.Device {
	t.Helper()
	fake, err := rpixeltest.New()
	if err != nil {
		t.Fatalf("rpixeltest.New: %v", err)
	}
	t.Cleanup(func() { fake.Close() })
	return fake
}

// start sets the driver up with fast test timing, for ios of every type on
// every device, and closes it at the end of the test.
func start(t *testing.T, d *rpixel.Driver) {
	t.Helper()
	var ios []string
	for _, dc := range d.Devices {
		ios = append(ios,
			"rpixel|d_out|"+dc.Name,
			"rpixel|a_out|"+dc.Name,
			"rpixel|rgbw_out|"+dc.Name,
		)
	}
	if err := rpixel.SetupForTest(d, ios); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	t.Cleanup(func() { d.Close() })
}

type views struct {
	dOut drivers.DigitalOutput
	aOut drivers.AnalogOutput
	rgbw drivers.RgbwOutput
}

func viewsOf(t *testing.T, d *rpixel.Driver, name string) views {
	t.Helper()
	dOut, err := d.GetDigitalOutput(name)
	if err != nil {
		t.Fatalf("GetDigitalOutput: %v", err)
	}
	aOut, err := d.GetAnalogOutput(name)
	if err != nil {
		t.Fatalf("GetAnalogOutput: %v", err)
	}
	rgbw, err := d.GetRgbwOutput(name)
	if err != nil {
		t.Fatalf("GetRgbwOutput: %v", err)
	}
	return views{dOut, aOut, rgbw}
}

// startOne starts a driver with one device named "ring" on fake.
func startOne(t *testing.T, fake *rpixeltest.Device, pollMs uint, mod func(*rpixel.DeviceConfig)) (*rpixel.Driver, views) {
	t.Helper()
	dc := rpixel.DeviceConfig{Name: "ring", Address: fake.Address()}
	if mod != nil {
		mod(&dc)
	}
	d := &rpixel.Driver{Devices: []rpixel.DeviceConfig{dc}, PollIntervalMs: pollMs}
	start(t, d)
	return d, viewsOf(t, d, "ring")
}

func eventually(t *testing.T, timeout time.Duration, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %s waiting for %s", timeout, what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func lastAnim(t *testing.T, fake *rpixeltest.Device) rpixeltest.AnimationRecord {
	t.Helper()
	rec, ok := fake.LastAnimation()
	if !ok {
		t.Fatal("device received no animation")
	}
	return rec
}

// wantAnim checks the device's last A, both parsed and byte for byte.
func wantAnim(t *testing.T, fake *rpixeltest.Device, want rpixel.Animation) {
	t.Helper()
	rec := lastAnim(t, fake)
	if !reflect.DeepEqual(rec.Animation, want) {
		t.Fatalf("last animation\n got %+v\nwant %+v", rec.Animation, want)
	}
	raw, err := rpixel.EncodeAnimation(rec.ReqId, want)
	if err != nil {
		t.Fatalf("EncodeAnimation: %v", err)
	}
	if string(raw) != string(rec.Raw) {
		t.Fatalf("last A bytes\n got %x\nwant %x", rec.Raw, raw)
	}
}

func fade(ms uint32, c rpixel.Colour, bri uint8) rpixel.Animation {
	return rpixel.Animation{
		Kind:            rpixel.KindFade,
		Target:          rpixel.TargetFullRing,
		StageDurationMs: ms,
		Stages:          []rpixel.Stage{{Colour: c, Brightness: bri}},
	}
}

func wipe(ms uint32, dir rpixel.Direction, c rpixel.Colour, bri uint8) rpixel.Animation {
	return rpixel.Animation{
		Kind:            rpixel.KindCircularWipe,
		Target:          rpixel.TargetFullRing,
		DirMode:         rpixel.DirModeFixed,
		Dir:             dir,
		StageDurationMs: ms,
		Stages:          []rpixel.Stage{{Colour: c, Brightness: bri}},
	}
}

var (
	white = rpixel.Colour{W: 255}
	off   = rpixel.Colour{}
)

func mustState(t *testing.T, v views) (bool, int, rpixel.Colour) {
	t.Helper()
	on, err := v.dOut.GetState()
	if err != nil {
		t.Fatalf("dOut.GetState: %v", err)
	}
	bri, err := v.aOut.GetState()
	if err != nil {
		t.Fatalf("aOut.GetState: %v", err)
	}
	r, g, b, w, err := v.rgbw.GetState()
	if err != nil {
		t.Fatalf("rgbw.GetState: %v", err)
	}
	return on, bri, rpixel.Colour{R: r, G: g, B: b, W: w}
}

func TestSetupReadsInitialState(t *testing.T) {
	fake := newFake(t)
	fake.SetState(rpixel.Colour{R: 10, G: 20, B: 30}, 77)
	d, v := startOne(t, fake, idleMs, nil)

	if !d.IsReady() || !v.dOut.IsHealthy() || !v.aOut.IsHealthy() || !v.rgbw.IsHealthy() {
		t.Fatal("driver should be ready and the device healthy right after Setup")
	}
	on, bri, c := mustState(t, v)
	if !on || bri != 77 || c != (rpixel.Colour{R: 10, G: 20, B: 30}) {
		t.Fatalf("state = on %v bri %d colour %+v", on, bri, c)
	}
	if min, max := v.aOut.GetMinMax(); min != 0 || max != 255 {
		t.Fatalf("GetMinMax = %d..%d", min, max)
	}
	if got := v.dOut.String(); got != "rpixel|d_out|ring" {
		t.Errorf("dOut.String() = %q", got)
	}
	if got := v.aOut.String(); got != "rpixel|a_out|ring" {
		t.Errorf("aOut.String() = %q", got)
	}
	if got := v.rgbw.String(); got != "rpixel|rgbw_out|ring" {
		t.Errorf("rgbw.String() = %q", got)
	}
}

func TestSetOnOffFade(t *testing.T) {
	fake := newFake(t)
	_, v := startOne(t, fake, idleMs, nil)

	if err := v.dOut.Set(false); err != nil {
		t.Fatalf("Set(false): %v", err)
	}
	wantAnim(t, fake, fade(500, off, 60))
	if on, _ := v.dOut.GetState(); on {
		t.Fatal("GetState should report off immediately after Set(false)")
	}
	if _, _, on := fake.State(); on {
		t.Fatal("device still on")
	}

	if err := v.dOut.Set(true); err != nil {
		t.Fatalf("Set(true): %v", err)
	}
	wantAnim(t, fake, fade(500, white, 60))
	if on, _ := v.dOut.GetState(); !on {
		t.Fatal("GetState should report on immediately after Set(true)")
	}
}

func TestSetOnOffWipe(t *testing.T) {
	fake := newFake(t)
	_, v := startOne(t, fake, idleMs, func(dc *rpixel.DeviceConfig) { dc.Effect = "wipe" })

	if err := v.dOut.Set(false); err != nil {
		t.Fatalf("Set(false): %v", err)
	}
	wantAnim(t, fake, wipe(2400, rpixel.DirReverse, off, 60))
	if err := v.dOut.Set(true); err != nil {
		t.Fatalf("Set(true): %v", err)
	}
	wantAnim(t, fake, wipe(2400, rpixel.DirForward, white, 60))

	// Brightness is a fade even with the wipe effect, and immediate.
	if err := v.aOut.Set(100); err != nil {
		t.Fatalf("aOut.Set: %v", err)
	}
	wantAnim(t, fake, fade(0, white, 100))
}

func TestTransitionMsOverride(t *testing.T) {
	fake := newFake(t)
	_, v := startOne(t, fake, idleMs, func(dc *rpixel.DeviceConfig) {
		dc.Effect = "Wipe"
		dc.TransitionMs = 1000
	})
	if err := v.dOut.Set(false); err != nil {
		t.Fatalf("Set(false): %v", err)
	}
	wantAnim(t, fake, wipe(1000, rpixel.DirReverse, off, 60))
}

func TestSetOnOffImmediate(t *testing.T) {
	fake := newFake(t)
	_, v := startOne(t, fake, idleMs, func(dc *rpixel.DeviceConfig) {
		dc.Effect = "immediate"
		dc.TransitionMs = 1234 // ignored
	})
	if err := v.dOut.Set(false); err != nil {
		t.Fatalf("Set(false): %v", err)
	}
	wantAnim(t, fake, fade(0, off, 60))
	if err := v.dOut.Set(true); err != nil {
		t.Fatalf("Set(true): %v", err)
	}
	wantAnim(t, fake, fade(0, white, 60))
}

func TestBrightnessSurvivesOnOff(t *testing.T) {
	fake := newFake(t)
	_, v := startOne(t, fake, idleMs, nil)

	if err := v.aOut.Set(128); err != nil {
		t.Fatalf("aOut.Set(128): %v", err)
	}
	wantAnim(t, fake, fade(500, white, 128))

	if err := v.dOut.Set(false); err != nil {
		t.Fatalf("Set(false): %v", err)
	}
	wantAnim(t, fake, fade(500, off, 128)) // off keeps the brightness
	if on, bri, _ := mustState(t, v); on || bri != 128 {
		t.Fatalf("after off: on %v bri %d, want off at 128", on, bri)
	}

	// Brightness while off only moves the brightness; the ring stays dark.
	if err := v.aOut.Set(90); err != nil {
		t.Fatalf("aOut.Set(90): %v", err)
	}
	wantAnim(t, fake, fade(500, off, 90))
	if on, bri, _ := mustState(t, v); on || bri != 90 {
		t.Fatalf("after brightness while off: on %v bri %d", on, bri)
	}

	if err := v.dOut.Set(true); err != nil {
		t.Fatalf("Set(true): %v", err)
	}
	wantAnim(t, fake, fade(500, white, 90))

	// Brightness 0 turns the ring off; the next "on" restores the last
	// non-zero brightness.
	if err := v.aOut.Set(0); err != nil {
		t.Fatalf("aOut.Set(0): %v", err)
	}
	if on, bri, _ := mustState(t, v); on || bri != 0 {
		t.Fatalf("after brightness 0: on %v bri %d", on, bri)
	}
	if err := v.dOut.Set(true); err != nil {
		t.Fatalf("Set(true): %v", err)
	}
	wantAnim(t, fake, fade(500, white, 90))

	// Out-of-range values are clamped.
	if err := v.aOut.Set(1000); err != nil {
		t.Fatalf("aOut.Set(1000): %v", err)
	}
	wantAnim(t, fake, fade(500, white, 255))
	if err := v.aOut.Set(-5); err != nil {
		t.Fatalf("aOut.Set(-5): %v", err)
	}
	wantAnim(t, fake, fade(500, white, 0))
}

func TestDefaultBrightness(t *testing.T) {
	fake := newFake(t)
	fake.SetState(off, 0) // dark, no brightness known
	_, v := startOne(t, fake, idleMs, func(dc *rpixel.DeviceConfig) { dc.DefaultBrightness = 200 })
	if err := v.dOut.Set(true); err != nil {
		t.Fatalf("Set(true): %v", err)
	}
	wantAnim(t, fake, fade(500, white, 200))
}

func TestSetColour(t *testing.T) {
	fake := newFake(t)
	_, v := startOne(t, fake, idleMs, nil)
	red := rpixel.Colour{R: 200, G: 10, B: 5}

	// On: the colour fades in at the current brightness.
	if err := v.rgbw.Set(red.R, red.G, red.B, red.W); err != nil {
		t.Fatalf("rgbw.Set: %v", err)
	}
	wantAnim(t, fake, fade(500, red, 60))
	if on, _, c := mustState(t, v); !on || c != red {
		t.Fatalf("after colour: on %v colour %+v", on, c)
	}

	// Off then on keeps the colour.
	if err := v.dOut.Set(false); err != nil {
		t.Fatalf("Set(false): %v", err)
	}
	if err := v.dOut.Set(true); err != nil {
		t.Fatalf("Set(true): %v", err)
	}
	wantAnim(t, fake, fade(500, red, 60))

	// While off, a colour is only remembered.
	if err := v.dOut.Set(false); err != nil {
		t.Fatalf("Set(false): %v", err)
	}
	sent := len(fake.Animations())
	blue := rpixel.Colour{B: 255, W: 3}
	if err := v.rgbw.Set(blue.R, blue.G, blue.B, blue.W); err != nil {
		t.Fatalf("rgbw.Set while off: %v", err)
	}
	if got := len(fake.Animations()); got != sent {
		t.Fatalf("colour while off sent %d animations, want none", got-sent)
	}
	if on, _, c := mustState(t, v); on || c != blue {
		t.Fatalf("after colour while off: on %v colour %+v, want off with blue remembered", on, c)
	}
	if err := v.dOut.Set(true); err != nil {
		t.Fatalf("Set(true): %v", err)
	}
	wantAnim(t, fake, fade(500, blue, 60))

	// Zero means off, and keeps the remembered colour.
	if err := v.rgbw.Set(0, 0, 0, 0); err != nil {
		t.Fatalf("rgbw.Set(0): %v", err)
	}
	wantAnim(t, fake, fade(500, off, 60))
	if on, _, c := mustState(t, v); on || c != blue {
		t.Fatalf("after zero colour: on %v colour %+v", on, c)
	}
	if err := v.dOut.Set(true); err != nil {
		t.Fatalf("Set(true): %v", err)
	}
	wantAnim(t, fake, fade(500, blue, 60))
}

func TestMixedStatusKeepsColourMemory(t *testing.T) {
	fake := newFake(t)
	green := rpixel.Colour{G: 180}
	fake.SetState(green, 60)
	d, v := startOne(t, fake, 50, nil)

	fake.SetMixed()
	eventually(t, 2*time.Second, func() bool { return rpixel.DisplayMixed(d, "ring") }, "mixed status to be polled")
	if _, _, c := mustState(t, v); c != green {
		t.Fatalf("mixed status: colour %+v, want remembered %+v", c, green)
	}
	if err := v.dOut.Set(true); err != nil {
		t.Fatalf("Set(true): %v", err)
	}
	wantAnim(t, fake, fade(500, green, 60))
}

func TestOutOfBandChangePickedUpByPoll(t *testing.T) {
	fake := newFake(t)
	_, v := startOne(t, fake, 50, nil)

	var mu sync.Mutex
	var calls []bool
	if err := v.dOut.SetOnStateUpdate(func(on bool) {
		mu.Lock()
		calls = append(calls, on)
		mu.Unlock()
	}); err != nil {
		t.Fatalf("SetOnStateUpdate: %v", err)
	}
	if err := v.dOut.SetOnStateUpdate(nil); err == nil {
		t.Fatal("SetOnStateUpdate(nil) should fail")
	}
	lastCall := func() (bool, int) {
		mu.Lock()
		defer mu.Unlock()
		if len(calls) == 0 {
			return false, 0
		}
		return calls[len(calls)-1], len(calls)
	}

	fake.Toggle() // HTTP /set/toggle: on -> off
	eventually(t, 2*time.Second, func() bool {
		on, n := lastCall()
		return n > 0 && !on
	}, "callback reporting off")
	if on, err := v.dOut.GetState(); err != nil || on {
		t.Fatalf("GetState = %v, %v; want off", on, err)
	}

	fake.Toggle() // off -> on
	eventually(t, 2*time.Second, func() bool {
		on, _ := lastCall()
		return on
	}, "callback reporting on")

	// A colour/brightness change without an on/off change is polled too.
	fake.SetState(rpixel.Colour{R: 1, G: 2, B: 3, W: 4}, 200)
	eventually(t, 2*time.Second, func() bool {
		bri, err := v.aOut.GetState()
		r, g, b, w, err2 := v.rgbw.GetState()
		return err == nil && err2 == nil && bri == 200 && r == 1 && g == 2 && b == 3 && w == 4
	}, "colour and brightness to be polled")

	// The on state did not change, so no extra callback fired.
	_, n := lastCall()
	time.Sleep(100 * time.Millisecond)
	if _, n2 := lastCall(); n2 != n {
		t.Fatalf("callback fired %d more times without an on/off change", n2-n)
	}
}

func TestCommandFiresCallback(t *testing.T) {
	fake := newFake(t)
	_, v := startOne(t, fake, idleMs, nil)
	got := make(chan bool, 4)
	_ = v.dOut.SetOnStateUpdate(func(on bool) { got <- on })

	if err := v.dOut.Set(false); err != nil {
		t.Fatalf("Set(false): %v", err)
	}
	select {
	case on := <-got:
		if on {
			t.Fatal("callback reported on after Set(false)")
		}
	case <-time.After(time.Second):
		t.Fatal("no callback after Set(false)")
	}
}

func TestRetryAfterLostPacket(t *testing.T) {
	fake := newFake(t)
	_, v := startOne(t, fake, idleMs, nil)

	fake.DropNext(rpixel.TypeAnimation, 1)
	if err := v.dOut.Set(false); err != nil {
		t.Fatalf("Set(false) with one lost packet: %v", err)
	}
	if n := fake.Received(rpixel.TypeAnimation); n != 2 {
		t.Fatalf("device received %d A frames, want 2 (original + retry)", n)
	}
	if n := len(fake.Animations()); n != 1 {
		t.Fatalf("device ran %d animations, want 1", n)
	}
	wantAnim(t, fake, fade(500, off, 60))
}

func TestSilentDevice(t *testing.T) {
	fake := newFake(t)
	_, v := startOne(t, fake, 50, nil)

	fake.SetSilent(true)
	begin := time.Now()
	err := v.dOut.Set(false)
	if err == nil {
		t.Fatal("Set on a silent device succeeded")
	}
	if !errors.Is(err, rpixel.ErrNoReply) {
		t.Fatalf("err = %v, want ErrNoReply", err)
	}
	if elapsed := time.Since(begin); elapsed > 2*time.Second {
		t.Fatalf("Set took %s to fail", elapsed)
	}
	// Nothing was confirmed, so the cached state is unchanged.
	eventually(t, 3*time.Second, func() bool { return !v.dOut.IsHealthy() }, "device to go unhealthy")
	if _, err := v.dOut.GetState(); err == nil {
		t.Fatal("GetState on an offline device should fail")
	}
	if _, err := v.aOut.GetState(); err == nil {
		t.Fatal("aOut.GetState on an offline device should fail")
	}

	fake.SetSilent(false)
	eventually(t, 3*time.Second, v.dOut.IsHealthy, "device to recover")
	if on, err := v.dOut.GetState(); err != nil || !on {
		t.Fatalf("after recovery GetState = %v, %v; want on", on, err)
	}
}

func TestUnreachableDeviceDoesNotFailSetup(t *testing.T) {
	fake := newFake(t)
	fake.SetSilent(true)
	begin := time.Now()
	d, v := startOne(t, fake, 50, nil)
	if elapsed := time.Since(begin); elapsed > 3*time.Second {
		t.Fatalf("Setup took %s with an unreachable device", elapsed)
	}
	if !d.IsReady() {
		t.Fatal("driver should be ready")
	}
	if v.dOut.IsHealthy() {
		t.Fatal("unreachable device reported healthy")
	}
	if _, err := v.dOut.GetState(); err == nil || !strings.Contains(err.Error(), "not seen") {
		t.Fatalf("GetState err = %v, want 'not seen'", err)
	}
	if got := d.Status(); got != "1 devices, 0 online" {
		t.Fatalf("Status() = %q", got)
	}
}

func TestDeviceIdMismatchIsUnhealthy(t *testing.T) {
	fake := newFake(t)
	_, v := startOne(t, fake, 50, func(dc *rpixel.DeviceConfig) { dc.DeviceId = "0011223344556677" })

	if v.dOut.IsHealthy() {
		t.Fatal("device with the wrong id reported healthy")
	}
	if _, err := v.dOut.GetState(); err == nil || !strings.Contains(err.Error(), "device id") {
		t.Fatalf("GetState err = %v, want a device id error", err)
	}

	// The address belongs to some other ring: it must not be driven.
	if err := v.dOut.Set(false); err == nil || !strings.Contains(err.Error(), "refusing") {
		t.Fatalf("Set on a mismatched device: err = %v, want a refusal", err)
	}
	if n := fake.Received(rpixel.TypeAnimation); n != 0 {
		t.Fatalf("mismatched device received %d A frames", n)
	}

	fake.SetDeviceId(rpixel.DeviceId{0x00, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77})
	eventually(t, 2*time.Second, v.dOut.IsHealthy, "device with the matching id to become healthy")
	if err := v.dOut.Set(false); err != nil {
		t.Fatalf("Set after verification: %v", err)
	}
}

func TestUnverifiedDeviceIdRefusesCommands(t *testing.T) {
	fake := newFake(t)
	fake.SetSilent(true) // never answers G, so the id is never verified
	_, v := startOne(t, fake, idleMs, func(dc *rpixel.DeviceConfig) { dc.DeviceId = rpixeltest.DefaultDeviceId.String() })
	fake.SetSilent(false)
	if err := v.dOut.Set(true); err == nil || !strings.Contains(err.Error(), "not verified") {
		t.Fatalf("Set before verification: err = %v", err)
	}
	if n := fake.Received(rpixel.TypeAnimation); n != 0 {
		t.Fatalf("unverified device received %d A frames", n)
	}
}

func TestDeviceIdMatchUppercase(t *testing.T) {
	fake := newFake(t)
	_, v := startOne(t, fake, idleMs, func(dc *rpixel.DeviceConfig) {
		dc.DeviceId = strings.ToUpper(rpixeltest.DefaultDeviceId.String())
	})
	if !v.dOut.IsHealthy() {
		t.Fatal("device with the configured id reported unhealthy")
	}
}

func TestProtocolVersionMismatchIsUnhealthy(t *testing.T) {
	fake := newFake(t)
	fake.SetProtocolVersion(2)
	_, v := startOne(t, fake, 50, nil)
	if v.dOut.IsHealthy() {
		t.Fatal("device speaking protocol 2 reported healthy")
	}
	if _, err := v.dOut.GetState(); err == nil || !strings.Contains(err.Error(), "protocol version") {
		t.Fatalf("GetState err = %v, want a protocol version error", err)
	}
	fake.SetProtocolVersion(1)
	eventually(t, 2*time.Second, v.dOut.IsHealthy, "device to become healthy on protocol 1")
}

func TestDiscoveryByDeviceId(t *testing.T) {
	fake := newFake(t)
	d := &rpixel.Driver{
		Devices: []rpixel.DeviceConfig{{
			Name:     "ring",
			DeviceId: rpixeltest.DefaultDeviceId.String(),
		}},
		PollIntervalMs: idleMs,
		// A unicast "broadcast" reaches the fake on loopback.
		BroadcastAddress: fake.Address(),
	}
	start(t, d)
	v := viewsOf(t, d, "ring")

	if !v.dOut.IsHealthy() {
		t.Fatal("device not discovered during Setup")
	}
	if fake.Received(rpixel.TypeStatusRequest) < 2 {
		t.Fatal("expected a broadcast G and a unicast G")
	}
	if err := v.dOut.Set(false); err != nil {
		t.Fatalf("Set(false): %v", err)
	}
	if _, _, on := fake.State(); on {
		t.Fatal("device still on")
	}
	ring := d.DriverDetails().(rpixel.Details).Rings[0]
	if ring.Resolved != fake.Address() || !ring.Online || ring.DeviceId != rpixeltest.DefaultDeviceId.String() {
		t.Fatalf("details = %+v", ring)
	}
}

func TestUndiscoveredDeviceCommandFails(t *testing.T) {
	fake := newFake(t)
	fake.SetSilent(true)
	d := &rpixel.Driver{
		Devices:          []rpixel.DeviceConfig{{Name: "ring", DeviceId: rpixeltest.DefaultDeviceId.String()}},
		PollIntervalMs:   idleMs,
		BroadcastAddress: fake.Address(),
	}
	start(t, d)
	v := viewsOf(t, d, "ring")
	if err := v.dOut.Set(true); err == nil || !strings.Contains(err.Error(), "not known") {
		t.Fatalf("Set on an undiscovered device: err = %v", err)
	}
}

// TestStaleStatusDropped drives the status handler directly: an S answering a G sent
// before the latest command, or arriving while a command is in flight, must
// not overwrite the command's optimistic state.
func TestStaleStatusDropped(t *testing.T) {
	fake, err := rpixeltest.New()
	if err != nil {
		t.Fatal(err)
	}
	defer fake.Close()
	d := &rpixel.Driver{Devices: []rpixel.DeviceConfig{{Name: "ring", Address: fake.Address()}}, PollIntervalMs: 600000}
	if err := rpixel.SetupForTest(d, nil); err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	dOut, _ := d.GetDigitalOutput("ring")

	onStatus := rpixel.Status{
		ProtocolVersion: rpixel.ProtocolVersion,
		DeviceId:        rpixeltest.DefaultDeviceId,
		Flags:           rpixel.StatusFlags(false, true, false),
		Brightness:      60,
		Colour:          white,
	}
	isOn := func() bool {
		on, err := dOut.GetState()
		if err != nil {
			t.Fatalf("GetState: %v", err)
		}
		return on
	}

	genBefore := rpixel.CmdGen(d, "ring")
	if err := dOut.Set(false); err != nil {
		t.Fatalf("Set(false): %v", err)
	}

	rpixel.ApplyStatus(d, "ring", onStatus, genBefore)
	if isOn() {
		t.Fatal("S for a G sent before the command was applied")
	}

	genNow := rpixel.CmdGen(d, "ring")
	rpixel.SetCmdInFlight(d, "ring", true)
	rpixel.ApplyStatus(d, "ring", onStatus, genNow)
	if isOn() {
		t.Fatal("S applied while a command was in flight")
	}

	rpixel.SetCmdInFlight(d, "ring", false)
	rpixel.ApplyStatus(d, "ring", onStatus, genNow)
	if !isOn() {
		t.Fatal("current S was not applied")
	}
}

// TestStaleStatusDroppedLive delays every S so a G is in flight when the
// command runs. The S answering that G still shows the ring on; it must not
// undo the optimistic "off".
func TestStaleStatusDroppedLive(t *testing.T) {
	fake := newFake(t)
	_, v := startOne(t, fake, 50, nil)
	var flippedOn sync.Once
	sawOn := make(chan struct{})
	fake.SetStatusDelay(120 * time.Millisecond)

	// Wait for a G to reach the device under the delay: its S, showing the
	// ring on, is now pending and will land after the command.
	gs := fake.Received(rpixel.TypeStatusRequest)
	eventually(t, time.Second, func() bool { return fake.Received(rpixel.TypeStatusRequest) > gs }, "a delayed G")
	if err := v.dOut.Set(false); err != nil {
		t.Fatalf("Set(false): %v", err)
	}
	_ = v.dOut.SetOnStateUpdate(func(on bool) {
		if on {
			flippedOn.Do(func() { close(sawOn) })
		}
	})
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if on, err := v.dOut.GetState(); err == nil && on {
			t.Fatal("a stale S flipped the ring back on")
		}
		time.Sleep(5 * time.Millisecond)
	}
	select {
	case <-sawOn:
		t.Fatal("callback reported on from a stale S")
	default:
	}
}

func TestCloseIsPromptAndLeaksNothing(t *testing.T) {
	fake := newFake(t)
	runtime.GC()
	baseline := runtime.NumGoroutine()

	d := &rpixel.Driver{
		Devices:        []rpixel.DeviceConfig{{Name: "ring", Address: fake.Address()}},
		PollIntervalMs: 20,
	}
	if err := rpixel.SetupForTest(d, []string{"rpixel|d_out|ring"}); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	dOut, _ := d.GetDigitalOutput("ring")

	// Leave a command stuck in its retries when Close runs.
	fake.SetSilent(true)
	errCh := make(chan error, 1)
	before := fake.Received(rpixel.TypeAnimation)
	go func() { errCh <- dOut.Set(false) }()
	eventually(t, time.Second, func() bool { return fake.Received(rpixel.TypeAnimation) > before }, "command to be sent")

	begin := time.Now()
	if err := d.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if elapsed := time.Since(begin); elapsed > 200*time.Millisecond {
		t.Fatalf("Close took %s", elapsed)
	}
	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("in-flight command succeeded across Close")
		}
	case <-time.After(time.Second):
		t.Fatal("in-flight command not aborted by Close")
	}
	if err := d.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}

	// Close sends nothing, and nothing is sent after it.
	sent := fake.ReceivedTotal()
	time.Sleep(100 * time.Millisecond)
	if got := fake.ReceivedTotal(); got != sent {
		t.Fatalf("device received %d packets after Close", got-sent)
	}

	if d.IsReady() {
		t.Fatal("driver still ready after Close")
	}
	if err := dOut.Set(true); err == nil {
		t.Fatal("Set after Close succeeded")
	}
	if _, err := dOut.GetState(); err == nil {
		t.Fatal("GetState after Close succeeded")
	}

	eventually(t, 2*time.Second, func() bool { return runtime.NumGoroutine() <= baseline }, "goroutines to settle")
}

func TestCloseWithoutSetup(t *testing.T) {
	d := &rpixel.Driver{}
	if err := d.Close(); err != nil {
		t.Fatalf("Close before Setup: %v", err)
	}
}

func TestSetupConfigErrors(t *testing.T) {
	addr := "127.0.0.1:9"
	cases := []struct {
		name    string
		devices []rpixel.DeviceConfig
		bcast   string
		ios     []string
		want    string
	}{
		{"empty name", []rpixel.DeviceConfig{{Address: addr}}, "", nil, "name is required"},
		{"colon in name", []rpixel.DeviceConfig{{Name: "a:b", Address: addr}}, "", nil, "may not contain"},
		{"pipe in name", []rpixel.DeviceConfig{{Name: "a|b", Address: addr}}, "", nil, "may not contain"},
		{"duplicate", []rpixel.DeviceConfig{{Name: "a", Address: addr}, {Name: "a", Address: addr}}, "", nil, "duplicate"},
		{"no address or id", []rpixel.DeviceConfig{{Name: "a"}}, "", nil, "Address or DeviceId"},
		{"short id", []rpixel.DeviceConfig{{Name: "a", DeviceId: "abc"}}, "", nil, "16 hex"},
		{"non-hex id", []rpixel.DeviceConfig{{Name: "a", DeviceId: "zz11223344556677"}}, "", nil, "16 hex"},
		{"bad effect", []rpixel.DeviceConfig{{Name: "a", Address: addr, Effect: "sparkle"}}, "", nil, "unknown effect"},
		{"bad port", []rpixel.DeviceConfig{{Name: "a", Address: "host:99999"}}, "", nil, "invalid port"},
		{"huge transition", []rpixel.DeviceConfig{{Name: "a", Address: addr, TransitionMs: 1 << 31}}, "", nil, "TransitionMs"},
		{"bad broadcast", []rpixel.DeviceConfig{{Name: "a", DeviceId: "0011223344556677"}}, "my-lan", nil, "IPv4"},
		{"unknown device io", []rpixel.DeviceConfig{{Name: "a", Address: addr}}, "", []string{"rpixel|d_out|b"}, "unknown device"},
		{"input io", []rpixel.DeviceConfig{{Name: "a", Address: addr}}, "", []string{"rpixel|d_in|a"}, "unsupported io type"},
		{"push io", []rpixel.DeviceConfig{{Name: "a", Address: addr}}, "", []string{"rpixel|push_event|a"}, "unsupported io type"},
		{"other driver", []rpixel.DeviceConfig{{Name: "a", Address: addr}}, "", []string{"gpio|d_out|a"}, "mismatch"},
		{"segment suffix", []rpixel.DeviceConfig{{Name: "a", Address: addr}}, "", []string{"rpixel|d_out|a:inner"}, "unknown device"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := &rpixel.Driver{Devices: c.devices, BroadcastAddress: c.bcast}
			err := rpixel.SetupForTest(d, c.ios)
			defer d.Close()
			if err == nil {
				t.Fatal("Setup succeeded")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want it to mention %q", err, c.want)
			}
			if d.IsReady() {
				t.Fatal("driver ready after a failed Setup")
			}
		})
	}
}

func TestSetupDoesNotRewriteConfig(t *testing.T) {
	fake := newFake(t)
	d := &rpixel.Driver{Devices: []rpixel.DeviceConfig{{Name: "ring", Address: fake.Address()}}}
	before, _ := json.Marshal(d)
	start(t, d)
	after, _ := json.Marshal(d)
	if string(before) != string(after) {
		t.Fatalf("Setup changed the config:\nbefore %s\nafter  %s", before, after)
	}
	if want := `{"Devices":[{"Name":"ring","Address":"` + fake.Address() + `"}]}`; string(after) != want {
		t.Fatalf("config JSON = %s, want %s", after, want)
	}
}

func TestUnsupportedGetters(t *testing.T) {
	fake := newFake(t)
	d, _ := startOne(t, fake, idleMs, nil)
	if _, err := d.GetDigitalInput("ring"); err == nil {
		t.Error("GetDigitalInput should fail")
	}
	if _, err := d.GetPushEventEmitter("ring"); err == nil {
		t.Error("GetPushEventEmitter should fail")
	}
	if _, err := d.GetDigitalOutput("nope"); err == nil {
		t.Error("GetDigitalOutput for an unknown device should fail")
	}
	if d.String() != "rpixel" {
		t.Errorf("String() = %q", d.String())
	}
}

func TestDebugSnapshotStatusAndDetails(t *testing.T) {
	up := newFake(t)
	down := newFake(t)
	down.SetSilent(true)
	d := &rpixel.Driver{
		Devices: []rpixel.DeviceConfig{
			{Name: "up", Address: up.Address()},
			{Name: "down", Address: down.Address()},
		},
		PollIntervalMs: idleMs,
	}
	start(t, d)

	snap := d.GetIoDebugSnapshot()
	if len(snap.Points) != 4 {
		t.Fatalf("snapshot has %d points, want 4", len(snap.Points))
	}
	p := snap.Points
	if p[0].Index != 0 || p[0].Name != "up" || p[0].Type != drivers.IoTypeDigitalOutput || !p[0].State || !p[0].Healthy {
		t.Errorf("point 0 = %+v", p[0])
	}
	if p[1].Index != 0 || p[1].Type != drivers.IoTypeAnalogOutput || p[1].Value != 60 || p[1].Min != 0 || p[1].Max != 255 {
		t.Errorf("point 1 = %+v", p[1])
	}
	if p[2].Index != 1 || p[2].Name != "down" || p[2].Healthy {
		t.Errorf("point 2 = %+v", p[2])
	}

	if got := d.Status(); got != "2 devices, 1 online" {
		t.Errorf("Status() = %q", got)
	}

	raw, err := json.Marshal(d.DriverDetails())
	if err != nil {
		t.Fatalf("marshal details: %v", err)
	}
	var det struct {
		Rings []map[string]interface{} `json:"rings"`
	}
	if err := json.Unmarshal(raw, &det); err != nil || len(det.Rings) != 2 {
		t.Fatalf("details %s: %v", raw, err)
	}
	if det.Rings[0]["name"] != "up" || det.Rings[0]["online"] != true || det.Rings[0]["last_seen"] == nil {
		t.Errorf("ring 0 = %v", det.Rings[0])
	}
	if det.Rings[1]["online"] != false || det.Rings[1]["problem"] == nil {
		t.Errorf("ring 1 = %v", det.Rings[1])
	}

	// Raw IO control from the debug UIs.
	if err := d.ToggleOutput(0); err != nil {
		t.Fatalf("ToggleOutput(0): %v", err)
	}
	if _, _, on := up.State(); on {
		t.Fatal("ToggleOutput did not turn the ring off")
	}
	if err := d.SetAnalogOutput(0, 42); err != nil {
		t.Fatalf("SetAnalogOutput: %v", err)
	}
	if _, bri, _ := up.State(); bri != 42 {
		t.Fatalf("brightness = %d, want 42", bri)
	}
	if err := d.ToggleOutput(1); err == nil {
		t.Error("ToggleOutput on an offline device should fail")
	}
	if err := d.ToggleOutput(2); err == nil {
		t.Error("ToggleOutput out of range should fail")
	}
	if err := d.SetAnalogOutput(-1, 1); err == nil {
		t.Error("SetAnalogOutput out of range should fail")
	}
}
