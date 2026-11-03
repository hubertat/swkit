package swkit

import (
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/brutella/hap/characteristic"
	"github.com/charmbracelet/log"
	drivers "github.com/hubertat/swkit/drivers"
)

// shutterClock is a deterministic clock + timer queue for shutter tests.
// Advance fires timers in deadline order (including timers scheduled while
// firing) up to the advanced time.
type shutterClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*shutterTimer
}

type shutterTimer struct {
	at       time.Time
	f        func()
	canceled bool
	fired    bool
}

func newShutterClock() *shutterClock {
	return &shutterClock{now: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)}
}

func (fc *shutterClock) Now() time.Time {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	return fc.now
}

func (fc *shutterClock) AfterFunc(d time.Duration, f func()) func() {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	t := &shutterTimer{at: fc.now.Add(d), f: f}
	fc.timers = append(fc.timers, t)
	return func() {
		fc.mu.Lock()
		t.canceled = true
		fc.mu.Unlock()
	}
}

func (fc *shutterClock) Advance(d time.Duration) {
	target := fc.Now().Add(d)
	for {
		fc.mu.Lock()
		var next *shutterTimer
		for _, t := range fc.timers {
			if t.fired || t.canceled || t.at.After(target) {
				continue
			}
			if next == nil || t.at.Before(next.at) {
				next = t
			}
		}
		if next == nil {
			fc.now = target
			fc.mu.Unlock()
			return
		}
		next.fired = true
		if fc.now.Before(next.at) {
			fc.now = next.at
		}
		fc.mu.Unlock()
		next.f()
	}
}

// fakeOut is a DigitalOutput with injectable behavior. syncCb fires the
// registered callback inside Set (like Wago), otherwise the test fires it
// explicitly to emulate a driver's async notification.
type fakeOut struct {
	name string

	mu     sync.Mutex
	on     bool
	setErr error
	getErr error
	cb     func(bool)
	syncCb bool
	sets   []bool
}

func (f *fakeOut) GetState() (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.on, f.getErr
}

func (f *fakeOut) Set(v bool) error {
	f.mu.Lock()
	f.sets = append(f.sets, v)
	err := f.setErr
	if err == nil {
		f.on = v
	}
	cb := f.cb
	syncCb := f.syncCb
	f.mu.Unlock()
	if err != nil {
		return err
	}
	if syncCb && cb != nil {
		cb(v)
	}
	return nil
}

func (f *fakeOut) String() string { return "mock|d_out|" + f.name }

func (f *fakeOut) SetOnStateUpdate(cb func(bool)) error {
	f.mu.Lock()
	f.cb = cb
	f.mu.Unlock()
	return nil
}

func (f *fakeOut) IsHealthy() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.getErr == nil
}

// setOn simulates an external (driver-side) change without a Set call.
func (f *fakeOut) setOn(v bool) {
	f.mu.Lock()
	f.on = v
	cb := f.cb
	f.mu.Unlock()
	if cb != nil {
		cb(v)
	}
}

func (f *fakeOut) isOn() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.on
}

func (f *fakeOut) setCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sets)
}

func testLogger() *log.Logger {
	return log.New(io.Discard)
}

// shutterHarness wires a WindowShutter to a fake clock and queued callback
// dispatch, so tests can drain driver notifications deterministically.
type shutterHarness struct {
	ws    *WindowShutter
	fc    *shutterClock
	open  *fakeOut
	close *fakeOut

	queuedMu sync.Mutex
	queued   []func()
}

func newShutterHarness(t *testing.T, cfg WindowShutterConfig) *shutterHarness {
	t.Helper()
	h := &shutterHarness{
		fc:    newShutterClock(),
		open:  &fakeOut{name: "open"},
		close: &fakeOut{name: "close"},
	}
	h.ws = NewWindowShutter(cfg, h.open, h.close, testLogger())
	h.ws.now = h.fc.Now
	h.ws.afterFunc = h.fc.AfterFunc
	h.ws.dispatch = func(f func()) {
		h.queuedMu.Lock()
		h.queued = append(h.queued, f)
		h.queuedMu.Unlock()
	}
	return h
}

// drain runs all queued (asynchronous) callback notifications.
func (h *shutterHarness) drain() {
	h.queuedMu.Lock()
	work := h.queued
	h.queued = nil
	h.queuedMu.Unlock()
	for _, f := range work {
		f()
	}
}

// bothOff reports whether neither output is energized.
func (h *shutterHarness) bothOff() bool {
	return !h.open.isOn() && !h.close.isOn()
}

func (h *shutterHarness) snap() WindowShutterSnapshot {
	return h.ws.Snapshot()
}

func shutterConfig() WindowShutterConfig {
	return WindowShutterConfig{
		Name:                "Test shutter",
		OpenDigitalOutName:  "mock|d_out|open",
		CloseDigitalOutName: "mock|d_out|close",
		OpenDurationSeconds: 20,
	}
}

// calibrateOpen runs a full open from unknown so the position is known
// (100). The extra second also lets the reversal delay elapse, so commands
// issued right after calibration start immediately.
func (h *shutterHarness) calibrateOpen(t *testing.T) {
	t.Helper()
	if err := h.ws.Open(); err != nil {
		t.Fatalf("calibrate open: %v", err)
	}
	h.drain()
	h.fc.Advance(23 * time.Second) // 20s full + 2s (10%) overrun + settle
	if s := h.snap(); !s.PositionKnown || s.CurrentPosition != 100 {
		t.Fatalf("calibration failed: %+v", s)
	}
}

func TestShutterUnknownStartup(t *testing.T) {
	h := newShutterHarness(t, shutterConfig())
	s := h.snap()
	if s.Motion != "stopped" || s.PositionKnown || s.TargetPosition != -1 {
		t.Fatalf("bad initial snapshot: %+v", s)
	}
	if !h.bothOff() {
		t.Fatal("outputs must start inactive")
	}
	if h.open.setCount()+h.close.setCount() != 0 {
		t.Fatal("startup must not write outputs")
	}
}

func TestShutterFullOpenFromUnknown(t *testing.T) {
	h := newShutterHarness(t, shutterConfig())
	if err := h.ws.Open(); err != nil {
		t.Fatalf("open: %v", err)
	}
	if !h.open.isOn() || h.close.isOn() {
		t.Fatal("open output must be the only active one")
	}
	if s := h.snap(); s.Motion != "opening" || s.RemainingTravelMilliseconds != 22000 {
		t.Fatalf("bad moving snapshot: %+v", s)
	}
	// Halfway: position still unknown (run from unknown), but direction known.
	h.fc.Advance(10 * time.Second)
	if s := h.snap(); s.Motion != "opening" || s.PositionKnown {
		t.Fatalf("mid-run: %+v", s)
	}
	h.fc.Advance(12 * time.Second)
	s := h.snap()
	if s.Motion != "stopped" || !s.PositionKnown || s.CurrentPosition != 100 {
		t.Fatalf("after full run: %+v", s)
	}
	if !h.bothOff() {
		t.Fatal("outputs must be off after completion")
	}
}

func TestShutterFullCloseAsymmetricDuration(t *testing.T) {
	cfg := shutterConfig()
	cfg.CloseDurationSeconds = 10
	h := newShutterHarness(t, cfg)
	h.calibrateOpen(t)
	if err := h.ws.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if s := h.snap(); s.Motion != "closing" || s.CurrentPosition != 100 {
		t.Fatalf("close start: %+v", s)
	}
	if s := h.snap(); s.RemainingTravelMilliseconds != 11000 { // 10s + 1s overrun
		t.Fatalf("close duration must use CloseDurationSeconds: %+v", s)
	}
	h.fc.Advance(11 * time.Second)
	if s := h.snap(); s.CurrentPosition != 0 || !s.PositionKnown {
		t.Fatalf("close failed: %+v", s)
	}
}

func TestShutterCloseDurationFallback(t *testing.T) {
	cfg := shutterConfig()
	cfg.CloseDurationSeconds = 0 // uses open duration
	h := newShutterHarness(t, cfg)
	if h.ws.closeDuration != h.ws.openDuration {
		t.Fatal("zero CloseDurationSeconds must fall back to the open duration")
	}
	snap := h.snap()
	if snap.CloseDurationSeconds != 20 {
		t.Fatalf("snapshot must report effective close duration: %+v", snap)
	}
}

func TestShutterPartialTravel(t *testing.T) {
	h := newShutterHarness(t, shutterConfig())
	h.calibrateOpen(t)
	if err := h.ws.SetPosition(40); err != nil {
		t.Fatalf("set position: %v", err)
	}
	// Partial travel from 100 to 40: 12s, no overrun.
	if s := h.snap(); s.RemainingTravelMilliseconds != 12000 {
		t.Fatalf("partial travel time: %+v", s)
	}
	h.fc.Advance(6 * time.Second)
	if s := h.snap(); s.CurrentPosition != 70 || !s.PositionKnown {
		t.Fatalf("mid partial: %+v", s)
	}
	h.fc.Advance(6 * time.Second)
	if s := h.snap(); s.CurrentPosition != 40 || s.Motion != "stopped" {
		t.Fatalf("partial end: %+v", s)
	}
	if h.open.setCount() != 2 || h.close.setCount() != 2 {
		t.Fatalf("unexpected writes: open=%v close=%v", h.open.sets, h.close.sets)
	}
}

func TestShutterEndpointOverrunFromKnown(t *testing.T) {
	h := newShutterHarness(t, shutterConfig())
	h.calibrateOpen(t)
	if err := h.ws.SetPosition(50); err != nil {
		t.Fatalf("set position: %v", err)
	}
	h.fc.Advance(10 * time.Second) // stopped at 50
	if err := h.ws.Open(); err != nil {
		t.Fatalf("open: %v", err)
	}
	// Endpoint from 50 after a reversal: 500ms wait + 10s travel + 2s overrun.
	if s := h.snap(); s.Motion != "stopped" || !s.PendingReversal {
		t.Fatalf("reversal wait: %+v", s)
	}
	h.fc.Advance(500 * time.Millisecond)
	if s := h.snap(); s.RemainingTravelMilliseconds != 12000 {
		t.Fatalf("endpoint overrun from known: %+v", s)
	}
	h.fc.Advance(10 * time.Second)
	s := h.snap()
	if s.CurrentPosition != 100 || s.Motion != "opening" {
		t.Fatalf("during overrun: %+v", s)
	}
	h.fc.Advance(2 * time.Second)
	if s := h.snap(); s.Motion != "stopped" || s.CurrentPosition != 100 || !s.PositionKnown {
		t.Fatalf("after overrun: %+v", s)
	}
}

func TestShutterOverrunInterruption(t *testing.T) {
	h := newShutterHarness(t, shutterConfig())
	h.calibrateOpen(t)
	if err := h.ws.SetPosition(90); err != nil {
		t.Fatalf("set position: %v", err)
	}
	h.fc.Advance(18 * time.Second) // stopped at 10
	if err := h.ws.Open(); err != nil {
		t.Fatalf("open: %v", err)
	}
	// From 10: 2s travel + 2s overrun = 4s.
	h.fc.Advance(3 * time.Second) // 1s into the overrun
	if s := h.snap(); s.Motion != "opening" || s.CurrentPosition != 100 {
		t.Fatalf("overrun: %+v", s)
	}
	if err := h.ws.Stop(); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if s := h.snap(); s.Motion != "stopped" || s.CurrentPosition != 100 {
		t.Fatalf("interrupted overrun: %+v", s)
	}
	h.fc.Advance(2 * time.Second) // planned stop timer must be dead
	if s := h.snap(); s.Motion != "stopped" {
		t.Fatalf("planned stop must not refire: %+v", s)
	}
	if !h.bothOff() {
		t.Fatal("stop must deenergize both")
	}
}

func TestShutterNoOverrunForPartialTarget(t *testing.T) {
	h := newShutterHarness(t, shutterConfig())
	h.calibrateOpen(t)
	if err := h.ws.SetPosition(50); err != nil {
		t.Fatalf("set position: %v", err)
	}
	if s := h.snap(); s.RemainingTravelMilliseconds != 10000 {
		t.Fatalf("partial must not include overrun: %+v", s)
	}
	h.fc.Advance(10*time.Second + time.Millisecond)
	if s := h.snap(); s.Motion != "stopped" || s.CurrentPosition != 50 {
		t.Fatalf("partial completion: %+v", s)
	}
}

func TestShutterRepeatCommandsNoOp(t *testing.T) {
	h := newShutterHarness(t, shutterConfig())
	if err := h.ws.Open(); err != nil {
		t.Fatalf("open: %v", err)
	}
	openWrites := h.open.setCount()
	if err := h.ws.Open(); err != nil {
		t.Fatalf("repeat open: %v", err)
	}
	if err := h.ws.SetPosition(100); err != nil {
		t.Fatalf("repeat numeric: %v", err)
	}
	if h.open.setCount() != openWrites {
		t.Fatal("repeats must not rewrite outputs")
	}
	// ... and must not restart the timer: stop at the original deadline.
	if remaining := h.snap().RemainingTravelMilliseconds; remaining != 22000 {
		t.Fatalf("timer must be unchanged: %d", remaining)
	}

	h.drain()
	h.fc.Advance(22 * time.Second) // complete: known 100
	h.fc.Advance(600 * time.Millisecond)
	if err := h.ws.SetPosition(50); err != nil {
		t.Fatalf("set position: %v", err)
	}
	if err := h.ws.SetPosition(50); err != nil {
		t.Fatalf("repeat partial target while moving: %v", err)
	}
	if s := h.snap(); s.RemainingTravelMilliseconds != 10000 {
		t.Fatalf("retarget must not happen: %+v", s)
	}
}

func TestShutterRetargetDuringTravel(t *testing.T) {
	h := newShutterHarness(t, shutterConfig())
	h.calibrateOpen(t)
	if err := h.ws.SetPosition(50); err != nil {
		t.Fatalf("set position: %v", err)
	}
	h.fc.Advance(4 * time.Second) // 40% traveled, at 80
	if err := h.ws.Open(); err != nil {
		t.Fatalf("open during partial travel: %v", err)
	}
	// Retarget with a reversal: 500ms wait, then 20*(100-80)/100 + 2 = 6s.
	if s := h.snap(); !s.PendingReversal {
		t.Fatalf("retarget must wait for the reversal: %+v", s)
	}
	h.fc.Advance(500 * time.Millisecond)
	if s := h.snap(); s.RemainingTravelMilliseconds != 6000 || s.TargetPosition != 100 {
		t.Fatalf("retarget: %+v", s)
	}
	h.fc.Advance(6 * time.Second)
	if s := h.snap(); s.CurrentPosition != 100 || s.Motion != "stopped" {
		t.Fatalf("retarget completion: %+v", s)
	}
}

func TestShutterNumericEndpointRetarget(t *testing.T) {
	h := newShutterHarness(t, shutterConfig())
	h.calibrateOpen(t)
	if err := h.ws.SetPosition(30); err != nil {
		t.Fatalf("set position: %v", err)
	}
	h.fc.Advance(4 * time.Second) // closing from 100: at 80
	if err := h.ws.SetPosition(0); err != nil {
		t.Fatalf("set position 0: %v", err)
	}
	// Same-direction retarget to the endpoint: 20*(80-0)/100 + 2 = 18s.
	if s := h.snap(); s.TargetPosition != 0 || s.Motion != "closing" {
		t.Fatalf("numeric endpoint retarget: %+v", s)
	}
	if s := h.snap(); s.RemainingTravelMilliseconds != 18000 {
		t.Fatalf("retarget timing: %+v", s)
	}
}

func TestShutterIdleEndpointRecalibration(t *testing.T) {
	h := newShutterHarness(t, shutterConfig())
	h.calibrateOpen(t)
	if err := h.ws.Open(); err != nil {
		t.Fatalf("open at 100: %v", err)
	}
	// Just the overrun interval: 2s.
	if s := h.snap(); s.RemainingTravelMilliseconds != 2000 {
		t.Fatalf("recalibration must run only the overrun: %+v", s)
	}
	if !h.open.isOn() {
		t.Fatal("recalibration energizes the open output")
	}
	h.fc.Advance(2 * time.Second)
	if s := h.snap(); s.Motion != "stopped" || s.CurrentPosition != 100 {
		t.Fatalf("recalibration end: %+v", s)
	}

	// Numeric SetPosition(100) at a known 100 must not write or schedule.
	writes := h.open.setCount()
	if err := h.ws.SetPosition(100); err != nil {
		t.Fatalf("set position 100: %v", err)
	}
	if h.open.setCount() != writes {
		t.Fatal("numeric no-op must not write")
	}
	if s := h.snap(); s.Motion != "stopped" || s.RemainingTravelMilliseconds != 0 {
		t.Fatalf("numeric no-op must not schedule: %+v", s)
	}

	// Symmetric closed case.
	if err := h.ws.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	h.fc.Advance(23 * time.Second) // 500ms reversal wait + 22s close
	if s := h.snap(); s.CurrentPosition != 0 || !s.PositionKnown {
		t.Fatalf("closed: %+v", s)
	}
	if err := h.ws.Close(); err != nil {
		t.Fatalf("close at 0: %v", err)
	}
	if s := h.snap(); s.RemainingTravelMilliseconds != 2000 {
		t.Fatalf("closed recalibration: %+v", s)
	}
	h.fc.Advance(2 * time.Second)
	writes = h.close.setCount()
	if err := h.ws.SetPosition(0); err != nil {
		t.Fatalf("set position 0: %v", err)
	}
	if h.close.setCount() != writes {
		t.Fatal("numeric no-op at 0 must not write")
	}
}

func TestShutterNumericEndpointFromUnknown(t *testing.T) {
	h := newShutterHarness(t, shutterConfig())
	if err := h.ws.SetPosition(100); err != nil {
		t.Fatalf("set position 100 from unknown: %v", err)
	}
	if s := h.snap(); s.RemainingTravelMilliseconds != 22000 {
		t.Fatalf("unknown placeholder must not suppress endpoint run: %+v", s)
	}
	h.fc.Advance(22 * time.Second)
	if s := h.snap(); s.CurrentPosition != 100 || !s.PositionKnown {
		t.Fatalf("endpoint run from unknown: %+v", s)
	}

	if err := h.ws.SetPosition(0); err != nil {
		t.Fatalf("set position 0: %v", err)
	}
	h.fc.Advance(23 * time.Second) // reversal wait + full close
	if s := h.snap(); s.CurrentPosition != 0 {
		t.Fatalf("close endpoint: %+v", s)
	}
}

func TestShutterSetPositionUnknownIntermediate(t *testing.T) {
	h := newShutterHarness(t, shutterConfig())
	err := h.ws.SetPosition(40)
	if err == nil {
		t.Fatal("intermediate target from unknown position must fail")
	}
	if s := h.snap(); s.Motion != "stopped" || h.open.setCount() != 0 {
		t.Fatalf("failed command must not move: %+v", s)
	}
}

func TestShutterToggleCycle(t *testing.T) {
	h := newShutterHarness(t, shutterConfig())
	h.calibrateOpen(t) // known 100, last direction opening; delay settled
	// Reaching the endpoint left it stopped: toggle moves away (closing).
	if err := h.ws.ToggleMovement(); err != nil {
		t.Fatalf("toggle: %v", err)
	}
	if s := h.snap(); s.Motion != "closing" {
		t.Fatalf("toggle start: %+v", s)
	}
	// Toggle while moving stops (5s of the 20s full close: at 75).
	h.fc.Advance(5 * time.Second)
	if err := h.ws.ToggleMovement(); err != nil {
		t.Fatalf("toggle: %v", err)
	}
	if s := h.snap(); s.Motion != "stopped" || s.PendingReversal || s.CurrentPosition != 75 || !s.PositionKnown {
		t.Fatalf("toggle stop: %+v", s)
	}
	// Toggle while stopped reverses: opening, after the reversal delay.
	if err := h.ws.ToggleMovement(); err != nil {
		t.Fatalf("toggle: %v", err)
	}
	if s := h.snap(); !s.PendingReversal || s.Motion != "stopped" {
		t.Fatalf("reversal must wait: %+v", s)
	}
	if s := h.snap(); s.RemainingTravelMilliseconds != 500+5000+2000 {
		t.Fatalf("remaining travel includes the reversal wait and overrun: %+v", s)
	}
	h.fc.Advance(300 * time.Millisecond)
	if h.open.isOn() {
		t.Fatal("reversal delay must hold")
	}
	h.fc.Advance(200 * time.Millisecond)
	if s := h.snap(); s.Motion != "opening" {
		t.Fatalf("reversal start: %+v", s)
	}
	// Toggle stops again (no time passed: still at 75).
	if err := h.ws.ToggleMovement(); err != nil {
		t.Fatalf("toggle: %v", err)
	}
	if s := h.snap(); s.Motion != "stopped" || s.CurrentPosition != 75 {
		t.Fatalf("toggle stop mid-open: %+v", s)
	}
	// Repeat: the next toggle closes.
	if err := h.ws.ToggleMovement(); err != nil {
		t.Fatalf("toggle: %v", err)
	}
	h.fc.Advance(500 * time.Millisecond)
	if s := h.snap(); s.Motion != "closing" {
		t.Fatalf("cycle repeats: %+v", s)
	}
}

func TestShutterToggleCancelsPendingStart(t *testing.T) {
	h := newShutterHarness(t, shutterConfig())
	h.calibrateOpen(t)
	if err := h.ws.ToggleMovement(); err != nil { // closing
		t.Fatalf("toggle: %v", err)
	}
	h.fc.Advance(5 * time.Second)                 // at 75
	if err := h.ws.ToggleMovement(); err != nil { // stop
		t.Fatalf("toggle: %v", err)
	}
	if err := h.ws.ToggleMovement(); err != nil { // pending opening
		t.Fatalf("toggle: %v", err)
	}
	if s := h.snap(); s.Motion != "stopped" || !s.PendingReversal {
		t.Fatalf("pending start expected: %+v", s)
	}
	// A toggle during the pending start cancels it and leaves it stopped.
	if err := h.ws.ToggleMovement(); err != nil {
		t.Fatalf("toggle cancel: %v", err)
	}
	if s := h.snap(); s.Motion != "stopped" || s.PendingReversal || s.RemainingTravelMilliseconds != 0 {
		t.Fatalf("cancel: %+v", s)
	}
	h.fc.Advance(time.Second)
	if h.open.isOn() {
		t.Fatal("canceled start must never energize")
	}
	// The remembered direction is still closing (the last that moved), so
	// the next toggle starts the canceled direction: opening.
	if err := h.ws.ToggleMovement(); err != nil {
		t.Fatalf("toggle: %v", err)
	}
	if s := h.snap(); s.Motion != "opening" {
		t.Fatalf("next toggle must start the canceled direction: %+v", s)
	}
}

func TestShutterToggleDefaultOpenWhenNeverMoved(t *testing.T) {
	h := newShutterHarness(t, shutterConfig())
	// No movement has ever occurred: the first toggle starts opening.
	if err := h.ws.ToggleMovement(); err != nil {
		t.Fatalf("toggle: %v", err)
	}
	if s := h.snap(); s.Motion != "opening" {
		t.Fatalf("first toggle must open: %+v", s)
	}
	if err := h.ws.ToggleMovement(); err != nil { // stop (position stays unknown)
		t.Fatalf("toggle stop: %v", err)
	}
	if s := h.snap(); s.Motion != "stopped" {
		t.Fatalf("toggle while moving stops: %+v", s)
	}
	// The remembered direction (opening) makes the next toggle close, even
	// though the position is unknown.
	if err := h.ws.ToggleMovement(); err != nil {
		t.Fatalf("toggle: %v", err)
	}
	h.fc.Advance(500 * time.Millisecond)
	if s := h.snap(); s.Motion != "closing" {
		t.Fatalf("direction must come from the remembered movement: %+v", s)
	}
}

func TestShutterCancelFirstPendingStartKeepsOpenDefault(t *testing.T) {
	h := newShutterHarness(t, shutterConfig())
	// Force a pending first start: a recent (adopted) deenergization of the
	// close output makes the very first Open wait for the reversal delay,
	// without any commanded movement having happened.
	h.ws.mu.Lock()
	h.ws.energizedEver = true
	h.ws.lastActiveDir = motionClosing
	h.ws.lastDeenergized = h.fc.Now()
	h.ws.mu.Unlock()
	if err := h.ws.ToggleMovement(); err != nil {
		t.Fatalf("toggle: %v", err)
	}
	if s := h.snap(); !s.PendingReversal {
		t.Fatalf("expected pending start: %+v", s)
	}
	if err := h.ws.ToggleMovement(); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	h.fc.Advance(time.Second)
	if err := h.ws.ToggleMovement(); err != nil {
		t.Fatalf("toggle: %v", err)
	}
	if s := h.snap(); s.Motion != "opening" {
		t.Fatalf("canceling a first pending start keeps the Open default: %+v", s)
	}
}

func TestShutterReversalDelayObservance(t *testing.T) {
	h := newShutterHarness(t, shutterConfig())
	if err := h.ws.Open(); err != nil {
		t.Fatalf("open: %v", err)
	}
	h.fc.Advance(6 * time.Second) // at 30%
	// Close must deenergize open and wait before energizing close.
	if err := h.ws.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if h.open.isOn() {
		t.Fatal("old direction must be deenergized before reversal")
	}
	if h.close.isOn() {
		t.Fatal("reversal must wait the delay")
	}
	h.fc.Advance(499 * time.Millisecond)
	if h.close.isOn() {
		t.Fatal("reversal delay must be observed")
	}
	h.fc.Advance(1 * time.Millisecond)
	if !h.close.isOn() {
		t.Fatal("reversal delay elapsed")
	}
	if s := h.snap(); s.Motion != "closing" || s.PositionKnown {
		// The run started from an unknown position, so it stays unknown.
		t.Fatalf("reversal: %+v", s)
	}
	if s := h.snap(); s.RemainingTravelMilliseconds != 22000 {
		t.Fatalf("closing from unknown is a full endpoint run: %+v", s)
	}
}

func TestShutterReversalDelayAfterStop(t *testing.T) {
	h := newShutterHarness(t, shutterConfig())
	if err := h.ws.Open(); err != nil {
		t.Fatalf("open: %v", err)
	}
	h.fc.Advance(6 * time.Second)
	if err := h.ws.Stop(); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if err := h.ws.Close(); err != nil {
		t.Fatalf("close immediately after stop: %v", err)
	}
	if h.close.isOn() {
		t.Fatal("Stop followed by an opposite command must observe the delay")
	}
	h.fc.Advance(500 * time.Millisecond)
	if !h.close.isOn() {
		t.Fatal("delay elapsed")
	}
}

func TestShutterNoReversalDelaySameDirection(t *testing.T) {
	h := newShutterHarness(t, shutterConfig())
	h.calibrateOpen(t)
	if err := h.ws.SetPosition(50); err != nil {
		t.Fatalf("set position: %v", err)
	}
	h.fc.Advance(4 * time.Second) // closing, at 80
	if err := h.ws.Stop(); err != nil {
		t.Fatalf("stop: %v", err)
	}
	// SetPosition(0) continues the last direction (closing): no wait.
	if err := h.ws.SetPosition(0); err != nil {
		t.Fatalf("set position 0: %v", err)
	}
	if !h.close.isOn() {
		t.Fatal("same-direction restart must not wait")
	}
}

func TestShutterStopFailure(t *testing.T) {
	h := newShutterHarness(t, shutterConfig())
	if err := h.ws.Open(); err != nil {
		t.Fatalf("open: %v", err)
	}
	h.fc.Advance(6 * time.Second)
	h.open.mu.Lock()
	h.open.setErr = fmt.Errorf("write failed")
	h.open.mu.Unlock()
	err := h.ws.Stop()
	if err == nil {
		t.Fatal("stop with failed write must error")
	}
	if !h.open.isOn() {
		t.Fatal("write failed: output must keep its old state")
	}
	if s := h.snap(); s.PositionKnown || s.Motion != "stopped" || !s.Faulty {
		t.Fatalf("failed stop: %+v", s)
	}
	// An opposite direction must not start after a failed deactivation.
	if err := h.ws.Close(); err == nil {
		t.Fatal("close after failed deactivation must error")
	}
	if h.close.setCount() != 0 {
		t.Fatal("no close output write after failed deactivation")
	}
}

func TestShutterFailedActivationWrite(t *testing.T) {
	h := newShutterHarness(t, shutterConfig())
	h.open.mu.Lock()
	h.open.setErr = fmt.Errorf("write failed")
	h.open.mu.Unlock()
	if err := h.ws.Open(); err == nil {
		t.Fatal("open with failing write must error")
	}
	if s := h.snap(); s.Motion != "stopped" || !s.Faulty {
		t.Fatalf("failed activation: %+v", s)
	}
	// The failed write's expectation must be rolled back: a poll observing
	// the output off must not adopt phantom movement.
	h.fc.Advance(4 * time.Second)
	if err := h.ws.Sync(false); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if s := h.snap(); s.Motion != "stopped" {
		t.Fatalf("no phantom movement: %+v", s)
	}
}

func TestShutterCommandsAfterShutdown(t *testing.T) {
	h := newShutterHarness(t, shutterConfig())
	if err := h.ws.Open(); err != nil {
		t.Fatalf("open: %v", err)
	}
	h.fc.Advance(2 * time.Second)
	if err := h.ws.Shutdown(); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if !h.bothOff() {
		t.Fatal("shutdown must stop outputs")
	}
	for name, cmd := range map[string]func() error{
		"open":         func() error { return h.ws.Open() },
		"close":        func() error { return h.ws.Close() },
		"stop":         func() error { return h.ws.Stop() },
		"toggle":       func() error { return h.ws.ToggleMovement() },
		"set position": func() error { return h.ws.SetPosition(50) },
	} {
		if err := cmd(); err == nil {
			t.Fatalf("%s after shutdown must fail", name)
		}
	}
	// Idempotent.
	if err := h.ws.Shutdown(); err != nil {
		t.Fatalf("second shutdown: %v", err)
	}
	// Stale timers must not fire.
	h.fc.Advance(60 * time.Second)
	if s := h.snap(); s.Motion != "stopped" {
		t.Fatalf("stale timer after shutdown: %+v", s)
	}
	// Callbacks after shutdown are ignored.
	h.open.setOn(true)
	h.drain()
	if s := h.snap(); s.Motion != "stopped" {
		t.Fatalf("callback after shutdown: %+v", s)
	}
}

func TestShutterCloseUsableAfterStop(t *testing.T) {
	h := newShutterHarness(t, shutterConfig())
	if err := h.ws.Open(); err != nil {
		t.Fatalf("open: %v", err)
	}
	h.fc.Advance(6 * time.Second)
	if err := h.ws.Stop(); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if err := h.ws.Close(); err != nil {
		t.Fatalf("movement Close must remain usable: %v", err)
	}
	h.fc.Advance(500 * time.Millisecond) // reversal delay
	if !h.close.isOn() {
		t.Fatal("close must energize after the reversal delay")
	}
}

func TestShutterPollAdoptsContradictionOnlyAfterDeadline(t *testing.T) {
	h := newShutterHarness(t, shutterConfig())
	// Emulate a write that never reaches the relay: Set succeeds logically
	// but the output stays off.
	if err := h.ws.Open(); err != nil {
		t.Fatalf("open: %v", err)
	}
	h.open.mu.Lock()
	h.open.on = false // the relay never changed
	h.open.mu.Unlock()

	// Before the deadline: contradictory readback is ignored, commanded
	// estimate continues.
	for i := 0; i < 3; i++ {
		h.fc.Advance(500 * time.Millisecond)
		if err := h.ws.Sync(false); err != nil {
			t.Fatalf("sync: %v", err)
		}
		if s := h.snap(); s.Motion != "opening" {
			t.Fatalf("contradiction before deadline must be ignored: %+v", s)
		}
	}
	// Repeated polls must not extend the deadline: at 3s the mismatch is real.
	h.fc.Advance(2 * time.Second)
	if err := h.ws.Sync(false); err != nil {
		t.Fatalf("sync: %v", err)
	}
	s := h.snap()
	if s.Motion != "stopped" {
		t.Fatalf("mismatch after deadline must be adopted as a stop: %+v", s)
	}
	// A poll cannot reconstruct the transition time: position unknown.
	if s.PositionKnown {
		t.Fatalf("poll-adopted stop must not retain position: %+v", s)
	}
}

func TestShutterCallbackConfirmsWrite(t *testing.T) {
	h := newShutterHarness(t, shutterConfig())
	if err := h.ws.Open(); err != nil {
		t.Fatalf("open: %v", err)
	}
	// The driver reports the state change (async callback).
	h.drain()
	h.fc.Advance(2 * time.Second)
	if err := h.ws.Sync(false); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if s := h.snap(); s.Motion != "opening" {
		t.Fatalf("confirmation must continue the commanded estimate: %+v", s)
	}
}

func TestShutterSyncCallbackDuringWrite(t *testing.T) {
	h := newShutterHarness(t, shutterConfig())
	// Wago-style driver: fires the callback synchronously from Set.
	h.open.mu.Lock()
	h.open.syncCb = true
	h.open.mu.Unlock()
	if err := h.ws.Open(); err != nil {
		t.Fatalf("open: %v", err)
	}
	if s := h.snap(); s.Motion != "opening" {
		t.Fatalf("sync callback must not break the command: %+v", s)
	}
	// The queued confirmation runs after the command releases cmdMu.
	h.drain()
	// The expectation is confirmed: a contradictory cached readback right
	// after must be ignored inside the confirmation window.
	h.open.mu.Lock()
	h.open.on = false // cached off (stale)
	h.open.mu.Unlock()
	h.fc.Advance(100 * time.Millisecond)
	if err := h.ws.Sync(false); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if s := h.snap(); s.Motion != "opening" {
		t.Fatalf("confirmed write must not be re-adopted as a stop: %+v", s)
	}
}

func TestShutterOnReadbackAfterStop(t *testing.T) {
	h := newShutterHarness(t, shutterConfig())
	h.calibrateOpen(t)
	if err := h.ws.SetPosition(50); err != nil {
		t.Fatalf("set position: %v", err)
	}
	h.drain()
	h.fc.Advance(4 * time.Second) // at 80
	if err := h.ws.Stop(); err != nil {
		t.Fatalf("stop: %v", err)
	}
	// The driver still reports the close output on (cached): the movement
	// was closing, so that is the output Stop just deenergized.
	h.close.mu.Lock()
	h.close.on = true
	h.close.mu.Unlock()
	h.fc.Advance(100 * time.Millisecond)
	if err := h.ws.Sync(false); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if s := h.snap(); s.Motion != "stopped" || s.CurrentPosition != 80 {
		t.Fatalf("cached On after stop must not restart movement: %+v", s)
	}
	// If the driver still reports it after the deadline, it is adopted as
	// external movement.
	h.fc.Advance(3 * time.Second)
	if err := h.ws.Sync(false); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if s := h.snap(); s.Motion != "closing" {
		t.Fatalf("persistent On after deadline must be adopted: %+v", s)
	}
	// External movement is capped at one full duration after observation.
	if s := h.snap(); s.RemainingTravelMilliseconds != 20000 {
		t.Fatalf("external cap: %+v", s)
	}
}

func TestShutterExternalMovementAdoptionCallback(t *testing.T) {
	h := newShutterHarness(t, shutterConfig())
	h.calibrateOpen(t)
	if err := h.ws.SetPosition(50); err != nil {
		t.Fatalf("set position: %v", err)
	}
	h.fc.Advance(10 * time.Second) // stopped at 50
	h.fc.Advance(4 * time.Second)  // past the write's confirmation window
	// Someone (another controller) energizes the close output.
	h.close.setOn(true)
	h.drain()
	s := h.snap()
	if s.Motion != "closing" || !s.PositionKnown || s.CurrentPosition != 50 {
		t.Fatalf("callback adoption from a known stop: %+v", s)
	}
	// Integrate from the transition time.
	h.fc.Advance(5 * time.Second)
	if s := h.snap(); s.CurrentPosition != 25 {
		t.Fatalf("adopted estimate: %+v", s)
	}
	// Cap: one full close duration after observation.
	h.fc.Advance(15 * time.Second)
	s = h.snap()
	if s.Motion != "stopped" || s.PositionKnown {
		t.Fatalf("external cap must stop and lose position: %+v", s)
	}
	if !h.bothOff() {
		t.Fatal("cap must stop both outputs")
	}
}

func TestShutterExternalMovementAdoptionPoll(t *testing.T) {
	h := newShutterHarness(t, shutterConfig())
	h.calibrateOpen(t)
	// External change with no callback notification: only polling sees it.
	h.close.mu.Lock()
	h.close.on = true
	h.close.cb = nil
	h.close.mu.Unlock()
	h.fc.Advance(1 * time.Second)
	if err := h.ws.Sync(false); err != nil {
		t.Fatalf("sync: %v", err)
	}
	s := h.snap()
	if s.Motion != "closing" {
		t.Fatalf("poll adoption: %+v", s)
	}
	if s.PositionKnown {
		t.Fatal("poll adoption cannot reconstruct the transition time")
	}
	if s.RemainingTravelMilliseconds != 20000 {
		t.Fatalf("external cap: %+v", s)
	}
}

func TestShutterExternalStopAdoptionCallback(t *testing.T) {
	h := newShutterHarness(t, shutterConfig())
	h.calibrateOpen(t)
	if err := h.ws.SetPosition(50); err != nil {
		t.Fatalf("set position: %v", err)
	}
	h.fc.Advance(5 * time.Second) // closing, at 75
	// External actor stops the motor (the close output is the active one).
	h.close.setOn(false)
	h.drain()
	s := h.snap()
	if s.Motion != "stopped" || !s.PositionKnown || s.CurrentPosition != 75 {
		t.Fatalf("external stop must retain a trustworthy estimate: %+v", s)
	}
	// Commanded timers are cancelled: no phantom completion later.
	h.fc.Advance(20 * time.Second)
	if s := h.snap(); s.Motion != "stopped" || s.CurrentPosition != 75 {
		t.Fatalf("timers must be cancelled: %+v", s)
	}
}

func TestShutterBothOutputsActiveInvalid(t *testing.T) {
	h := newShutterHarness(t, shutterConfig())
	if err := h.ws.Open(); err != nil {
		t.Fatalf("open: %v", err)
	}
	h.drain()
	// External fault: both outputs energized.
	h.close.setOn(true)
	h.drain()
	s := h.snap()
	if s.Motion != "stopped" || !s.Faulty {
		t.Fatalf("both active is a fault: %+v", s)
	}
	if !h.bothOff() {
		t.Fatal("both-active handling must stop both outputs")
	}
	if s.PositionKnown {
		t.Fatal("both-active must clear position validity")
	}
}

func TestShutterStaleCallbackDiscarded(t *testing.T) {
	h := newShutterHarness(t, shutterConfig())
	if err := h.ws.Open(); err != nil {
		t.Fatalf("open: %v", err)
	}
	h.drain() // confirmation processed; write generation unchanged since
	// A callback is captured (queued) but not processed yet...
	h.close.setOn(true)
	// ... a newer write (the Stop below) happens before the queue drains:
	h.fc.Advance(6 * time.Second)
	if err := h.ws.Stop(); err != nil {
		t.Fatalf("stop: %v", err)
	}
	// Drain: the stale close=true callback must be discarded (its captured
	// generation predates the newer write).
	h.drain()
	if s := h.snap(); s.Motion != "stopped" {
		t.Fatalf("stale callback must be discarded: %+v", s)
	}
}

func TestShutterSyncWorksWithoutHomekit(t *testing.T) {
	cfg := shutterConfig()
	cfg.DisableHomekit = true
	h := newShutterHarness(t, cfg)
	if h.ws.InitHk() != nil {
		t.Fatal("homekit disabled")
	}
	h.calibrateOpen(t)
	if err := h.ws.SetPosition(50); err != nil {
		t.Fatalf("set position: %v", err)
	}
	h.drain()
	h.fc.Advance(4 * time.Second)
	if err := h.ws.Sync(false); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if s := h.snap(); s.CurrentPosition != 80 || !s.PositionKnown {
		t.Fatalf("estimates must advance without homekit: %+v", s)
	}
}

func TestShutterHkCharacteristics(t *testing.T) {
	h := newShutterHarness(t, shutterConfig())
	a := h.ws.InitHk()
	if a == nil {
		t.Fatal("accessory expected")
	}
	if h.ws.GetUniqueId() == 0 {
		t.Fatal("unique id")
	}
	// Remote target write routes through SetPosition with a nonzero status
	// on failure and succeeds for endpoints.
	hk := h.ws.hk
	if _, code := hk.WindowCovering.TargetPosition.C.SetValueRequest(100, &http.Request{}); code != 0 {
		t.Fatalf("target write: code %d", code)
	}
	if s := h.snap(); s.Motion != "opening" {
		t.Fatalf("target write must dispatch: %+v", s)
	}
	// Intermediate target from unknown must fail with a nonzero code.
	if _, code := hk.WindowCovering.TargetPosition.C.SetValueRequest(40, &http.Request{}); code == 0 {
		t.Fatal("intermediate from unknown must be rejected")
	}
	// Unknown position publishes the placeholder.
	if v := hk.WindowCovering.CurrentPosition.Value(); v != hkUnknownPositionPercent {
		t.Fatalf("placeholder: %d", v)
	}
	if v := hk.WindowCovering.PositionState.Value(); v != characteristic.PositionStateIncreasing {
		t.Fatalf("position state during movement: %d", v)
	}

	h.drain()
	h.fc.Advance(22 * time.Second)
	h.fc.Advance(600 * time.Millisecond)
	if v := hk.WindowCovering.PositionState.Value(); v != characteristic.PositionStateStopped {
		t.Fatalf("stopped: %d", v)
	}
	if v := hk.WindowCovering.CurrentPosition.Value(); v != 100 {
		t.Fatalf("current: %d", v)
	}

	// HoldPosition stops movement.
	if err := h.ws.SetPosition(50); err != nil {
		t.Fatalf("set position: %v", err)
	}
	h.drain()
	if s := h.snap(); s.Motion != "closing" {
		t.Fatalf("hold position prerequisite: %+v", s)
	}
	if _, code := h.ws.hold.C.SetValueRequest(true, &http.Request{}); code != 0 {
		t.Fatalf("hold position: code %d", code)
	}
	if s := h.snap(); s.Motion != "stopped" {
		t.Fatalf("hold position must stop: %+v", s)
	}
}

func TestShutterStopPublishesPositionAsTarget(t *testing.T) {
	h := newShutterHarness(t, shutterConfig())
	h.ws.InitHk()
	h.calibrateOpen(t)
	if err := h.ws.SetPosition(50); err != nil {
		t.Fatalf("set position: %v", err)
	}
	h.drain()
	h.fc.Advance(2 * time.Second) // at 90
	if err := h.ws.Stop(); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if v := h.ws.hk.WindowCovering.TargetPosition.Value(); v != 90 {
		t.Fatalf("stop must publish the stopped position as the target: %d", v)
	}
	// A later request for the abandoned endpoint is a changed value and
	// restarts movement (via the remote-write callback, honoring the
	// reversal delay).
	if _, code := h.ws.hk.WindowCovering.TargetPosition.C.SetValueRequest(100, &http.Request{}); code != 0 {
		t.Fatalf("target write: code %d", code)
	}
	h.fc.Advance(600 * time.Millisecond)
	if s := h.snap(); s.Motion != "opening" {
		t.Fatalf("abandoned target must restart movement: %+v", s)
	}
}

func TestShutterConcurrentCommands(t *testing.T) {
	h := newShutterHarness(t, shutterConfig())
	h.ws.dispatch = func(f func()) { go f() } // real async dispatch
	var wg sync.WaitGroup
	stop := make(chan struct{})
	var commands atomic.Int64
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				switch commands.Add(1) % 5 {
				case 0:
					_ = h.ws.Open()
				case 1:
					_ = h.ws.Close()
				case 2:
					_ = h.ws.Stop()
				case 3:
					_ = h.ws.ToggleMovement()
				case 4:
					_ = h.ws.SetPosition(50)
				}
				_ = h.ws.Snapshot()
				_ = h.ws.Sync(false)
			}
		}()
	}
	h.fc.Advance(2 * time.Second)
	close(stop)
	wg.Wait()
	// Whatever the interleaving, both outputs must never be on at once.
	if h.open.isOn() && h.close.isOn() {
		t.Fatal("both outputs active after concurrent commands")
	}
}

var _ drivers.DigitalOutput = (*fakeOut)(nil)

func TestShutterHkSameValueAndRejection(t *testing.T) {
	h := newShutterHarness(t, shutterConfig())
	h.ws.InitHk()
	tp := h.ws.hk.WindowCovering.TargetPosition
	// Rejected intermediate target from unknown: nonzero status and the
	// value is not stored (placeholder stays).
	if _, code := tp.C.SetValueRequest(40, &http.Request{}); code == 0 {
		t.Fatal("intermediate target from unknown must be rejected")
	}
	if v := tp.Value(); v != hkUnknownPositionPercent {
		t.Fatalf("rejected value must not be stored: %d", v)
	}
	if _, code := tp.C.SetValueRequest(100, &http.Request{}); code != 0 {
		t.Fatalf("endpoint target: %d", code)
	}
	writes := h.open.setCount()
	remaining := h.snap().RemainingTravelMilliseconds
	h.fc.Advance(time.Second)
	// Same value: HAP ignores it before the callback; the run is unchanged.
	if _, code := tp.C.SetValueRequest(100, &http.Request{}); code != 0 {
		t.Fatalf("same value: %d", code)
	}
	if h.open.setCount() != writes || h.snap().RemainingTravelMilliseconds != remaining-1000 {
		t.Fatal("same-value write must not restart movement")
	}
}

func TestShutterFaultOnlyForIoErrors(t *testing.T) {
	h := newShutterHarness(t, shutterConfig())
	h.ws.InitHk()
	if err := h.ws.Sync(true); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if v := h.ws.fault.Value(); v != characteristic.StatusFaultNoFault {
		t.Fatal("unknown position alone must not set StatusFault")
	}
	h.open.mu.Lock()
	h.open.getErr = fmt.Errorf("stale")
	h.open.mu.Unlock()
	if err := h.ws.Sync(true); err == nil {
		t.Fatal("unreadable output must report an error")
	}
	if v := h.ws.fault.Value(); v != characteristic.StatusFaultGeneralFault {
		t.Fatal("IO error must set StatusFault")
	}
	h.open.mu.Lock()
	h.open.getErr = nil
	h.open.mu.Unlock()
	if err := h.ws.Sync(true); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if v := h.ws.fault.Value(); v != characteristic.StatusFaultNoFault {
		t.Fatal("fault must clear once outputs are readable")
	}
}

func TestShutterStartupSwitchesOffEnergizedOutput(t *testing.T) {
	fc := newShutterClock()
	open, closeOut := &fakeOut{name: "open"}, &fakeOut{name: "close", on: true} // close relay left on
	ws := NewWindowShutter(shutterConfig(), open, closeOut, testLogger())
	ws.now, ws.afterFunc, ws.dispatch = fc.Now, fc.AfterFunc, func(f func()) {}
	if closeOut.isOn() {
		t.Fatal("an output found on at startup must be switched off")
	}
	if err := ws.Open(); err != nil {
		t.Fatalf("open: %v", err)
	}
	if open.isOn() && closeOut.isOn() {
		t.Fatal("both outputs energized")
	}
	if open.isOn() {
		t.Fatal("the first movement after a startup deactivation must wait for the reversal delay")
	}
	fc.Advance(500 * time.Millisecond)
	if !open.isOn() || closeOut.isOn() {
		t.Fatal("open must start after the delay, alone")
	}
}

func TestShutterStartupUnswitchableOutputBlocksOpposite(t *testing.T) {
	open := &fakeOut{name: "open"}
	closeOut := &fakeOut{name: "close", on: true, setErr: fmt.Errorf("unreachable")}
	ws := NewWindowShutter(shutterConfig(), open, closeOut, testLogger())
	ws.dispatch = func(f func()) {}
	if err := ws.Open(); err == nil {
		t.Fatal("open must be refused while the close output may still be on")
	}
	if open.isOn() {
		t.Fatal("open output must not be energized")
	}
	if !ws.Snapshot().Faulty {
		t.Fatal("startup deactivation failure is a fault")
	}
	// Once the close output is observed off, opening works.
	closeOut.mu.Lock()
	closeOut.on, closeOut.setErr = false, nil
	closeOut.mu.Unlock()
	if err := ws.Sync(true); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if err := ws.Open(); err != nil {
		t.Fatalf("open after confirmed off: %v", err)
	}
}

func TestShutterExternalStopObservesReversalDelay(t *testing.T) {
	h := newShutterHarness(t, shutterConfig())
	h.calibrateOpen(t)
	if err := h.ws.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	h.fc.Advance(5 * time.Second)
	h.close.setOn(false) // stopped by someone else
	h.drain()
	if s := h.snap(); s.Motion != "stopped" {
		t.Fatalf("external stop: %+v", s)
	}
	if err := h.ws.Open(); err != nil {
		t.Fatalf("open: %v", err)
	}
	if h.open.isOn() {
		t.Fatal("opposite direction right after an external stop must wait for the reversal delay")
	}
	h.fc.Advance(500 * time.Millisecond)
	if !h.open.isOn() {
		t.Fatal("open must start after the delay")
	}
}

func TestShutterRejectedPositionKeepsPendingReversal(t *testing.T) {
	h := newShutterHarness(t, shutterConfig())
	if err := h.ws.Open(); err != nil { // from unknown
		t.Fatalf("open: %v", err)
	}
	if err := h.ws.Close(); err != nil { // reversal: pending
		t.Fatalf("close: %v", err)
	}
	if !h.snap().PendingReversal {
		t.Fatal("close must be pending")
	}
	if err := h.ws.SetPosition(50); err == nil {
		t.Fatal("intermediate target with unknown position must be rejected")
	}
	if s := h.snap(); !s.PendingReversal || s.TargetPosition != -1 && s.Motion != "stopped" {
		t.Fatalf("rejected request must not cancel the accepted close: %+v", s)
	}
	h.fc.Advance(500 * time.Millisecond)
	if !h.close.isOn() {
		t.Fatal("the accepted close must still start")
	}
}
