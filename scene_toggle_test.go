package swkit

import (
	"fmt"
	"math/rand/v2"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/log"
)

// fakeClock is a manually advanced clock for the toggle timeout.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{t: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// lockedSceneTimers is a goroutine-safe fake afterFunc: it records the
// scheduled callbacks so a test can fire them from any goroutine.
type lockedSceneTimers struct {
	mu sync.Mutex
	fs []func()
}

func (lt *lockedSceneTimers) install(sc *Scene) {
	sc.afterFunc = func(_ time.Duration, f func()) func() {
		lt.mu.Lock()
		defer lt.mu.Unlock()
		lt.fs = append(lt.fs, f)
		return func() {}
	}
}

// waitFor waits until at least n callbacks are scheduled and returns the
// i-th one.
func (lt *lockedSceneTimers) waitFor(t *testing.T, i int) func() {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		lt.mu.Lock()
		if len(lt.fs) > i {
			f := lt.fs[i]
			lt.mu.Unlock()
			return f
		}
		lt.mu.Unlock()
		if time.Now().After(deadline) {
			t.Fatalf("timer #%d was never scheduled", i)
		}
		time.Sleep(time.Millisecond)
	}
}

// newTimeoutScene builds an n-state scene ("s0".."s<n-1>", each switching A)
// with the given toggle timeout and a fake clock.
func newTimeoutScene(t *testing.T, n int, timeoutSeconds int) (*Scene, *fakeClock) {
	t.Helper()
	a := newTestLight("A", false)
	var states []SceneStateConfig
	for i := 0; i < n; i++ {
		verb := "on"
		if i == 0 {
			verb = "off"
		}
		states = append(states, SceneStateConfig{Name: fmt.Sprintf("s%d", i), Actions: []string{verb + ":A"}})
	}
	sc, err := NewScene(SceneConfig{Name: "T", Devices: []string{"A"}, ToggleTimeoutSeconds: timeoutSeconds, States: states}, resolverFrom(a), log.New(nil))
	if err != nil {
		t.Fatalf("NewScene: %v", err)
	}
	clk := newFakeClock()
	sc.now = clk.now
	return sc, clk
}

func toggleTo(t *testing.T, sc *Scene, want int) {
	t.Helper()
	got, err := sc.ToggleState()
	if err != nil {
		t.Fatalf("ToggleState: %v", err)
	}
	if got != want {
		t.Fatalf("ToggleState selected %d, want %d", got, want)
	}
	if cur := sc.CurrentState(); cur != want {
		t.Fatalf("CurrentState = %d after toggle, want %d", cur, want)
	}
}

func TestSceneToggleTimeoutBoundaries(t *testing.T) {
	const timeout = 30 * time.Second
	cases := []struct {
		name    string
		elapsed time.Duration
		want    int
	}{
		{"just before timeout advances", timeout - time.Nanosecond, 2},
		{"exactly at timeout returns to 0", timeout, 0},
		{"after timeout returns to 0", timeout + time.Hour, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sc, clk := newTimeoutScene(t, 4, int(timeout/time.Second))
			toggleTo(t, sc, 1)
			clk.advance(tc.elapsed)
			if cur, next := sc.ToggleInfo(); cur != 1 || next != tc.want {
				t.Errorf("ToggleInfo = (%d, %d), want (1, %d)", cur, next, tc.want)
			}
			toggleTo(t, sc, tc.want)
		})
	}
}

func TestSceneToggleTimeoutFromOffAdvances(t *testing.T) {
	sc, clk := newTimeoutScene(t, 3, 10)
	// Never activated: state 0, changedAt zero (infinitely long ago).
	if cur, next := sc.ToggleInfo(); cur != 0 || next != 1 {
		t.Errorf("fresh ToggleInfo = (%d, %d), want (0, 1)", cur, next)
	}
	toggleTo(t, sc, 1)
	toggleTo(t, sc, 2)
	toggleTo(t, sc, 0)
	clk.advance(time.Hour) // timeout long expired, but we are in state 0
	toggleTo(t, sc, 1)
}

func TestSceneToggleTimeoutZeroWraps(t *testing.T) {
	sc, clk := newTimeoutScene(t, 3, 0)
	for _, want := range []int{1, 2, 0, 1} {
		clk.advance(48 * time.Hour)
		toggleTo(t, sc, want)
	}
}

func TestSceneToggleTimeoutResetByActivate(t *testing.T) {
	sc, clk := newTimeoutScene(t, 4, 30)
	toggleTo(t, sc, 1)
	clk.advance(20 * time.Second)
	if err := sc.Activate(1); err != nil { // re-selecting resets the timer
		t.Fatal(err)
	}
	clk.advance(20 * time.Second) // 40s since the toggle, 20s since Activate
	toggleTo(t, sc, 2)
	clk.advance(29 * time.Second)
	toggleTo(t, sc, 3) // each toggle resets it too
	clk.advance(30 * time.Second)
	toggleTo(t, sc, 0)
}

func TestSceneToggleTimeoutResetBySetValue(t *testing.T) {
	sc, clk := newTimeoutScene(t, 4, 30)
	sc.SetValue(true) // state 1
	clk.advance(25 * time.Second)
	sc.SetValue(true) // state 1 again: timer restarts
	clk.advance(25 * time.Second)
	toggleTo(t, sc, 2)

	clk.advance(time.Minute)
	sc.SetValue(false) // state 0
	if cur, next := sc.ToggleInfo(); cur != 0 || next != 1 {
		t.Errorf("after SetValue(false) ToggleInfo = (%d, %d), want (0, 1)", cur, next)
	}
}

// NewScene rejects a scene without states, but the runtime still guards
// against one; build a valid scene and drop its states to exercise that.
func TestSceneToggleInfoEmptyScene(t *testing.T) {
	sc, err := NewScene(SceneConfig{Name: "E", Devices: []string{"A"}, States: []SceneStateConfig{{Name: "Off"}}}, resolverFrom(newTestLight("A", false)), log.New(nil))
	if err != nil {
		t.Fatal(err)
	}
	sc.states = nil
	if cur, next := sc.ToggleInfo(); cur != 0 || next != 0 {
		t.Errorf("ToggleInfo = (%d, %d), want (0, 0)", cur, next)
	}
	if _, err := sc.ToggleState(); err == nil {
		t.Error("ToggleState on a scene without states should fail")
	}
	sc.Toggle() // must not panic
}

// jitterDevice is a Dimmable whose writes take a random, short time, to
// shuffle goroutine interleavings in the concurrency tests.
type jitterDevice struct {
	name string
	mu   sync.Mutex
	on   bool
	bri  int
}

func (d *jitterDevice) jitter() { time.Sleep(time.Duration(rand.IntN(200)) * time.Microsecond) }
func (d *jitterDevice) Name() string {
	return d.name
}
func (d *jitterDevice) SetValue(v bool) {
	d.jitter()
	d.mu.Lock()
	d.on = v
	d.mu.Unlock()
}
func (d *jitterDevice) Toggle() {
	d.jitter()
	d.mu.Lock()
	d.on = !d.on
	d.mu.Unlock()
}
func (d *jitterDevice) SetBrightness(p int) {
	d.jitter()
	d.mu.Lock()
	d.bri = p
	d.mu.Unlock()
}
func (d *jitterDevice) GetBrightness() (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.bri, nil
}

// Concurrent toggles must each select a distinct consecutive state (no two
// toggles may read the same "current" and pick the same next), and the
// devices must end up in the state matching the published index.
func TestSceneConcurrentTogglesDistinctAndConsistent(t *testing.T) {
	const nStates = 4
	const nToggles = 24

	a := &jitterDevice{name: "A"}
	b := &jitterDevice{name: "B"}
	var states []SceneStateConfig
	for i := 0; i < nStates; i++ {
		// Two devices and two steps each, so a torn interleaving would
		// leave A and B disagreeing.
		states = append(states, SceneStateConfig{
			Name:    fmt.Sprintf("s%d", i),
			Actions: []string{fmt.Sprintf("brightness:%d:A", 10*i), fmt.Sprintf("brightness:%d:B", 10*i)},
		})
	}
	sc, err := NewScene(SceneConfig{Name: "C", Devices: []string{"A", "B"}, States: states}, resolverFrom(a, b), log.New(nil))
	if err != nil {
		t.Fatal(err)
	}

	stop := make(chan struct{})
	var readers sync.WaitGroup
	readers.Add(1)
	go func() {
		// Snapshot readers run alongside: next must always belong to cur.
		defer readers.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			if cur, next := sc.ToggleInfo(); next != (cur+1)%nStates {
				t.Errorf("inconsistent ToggleInfo snapshot (%d, %d)", cur, next)
				return
			}
		}
	}()

	results := make(chan int, nToggles)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < nToggles; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			idx, err := sc.ToggleState()
			if err != nil {
				t.Errorf("ToggleState: %v", err)
			}
			results <- idx
		}()
	}
	close(start)
	wg.Wait()
	close(stop)
	readers.Wait()
	close(results)

	// Toggle k (1-based, in selection order) selects k % nStates, so the
	// multiset of selections is fixed whatever the interleaving.
	counts := make(map[int]int)
	for idx := range results {
		counts[idx]++
	}
	want := make(map[int]int)
	for k := 1; k <= nToggles; k++ {
		want[k%nStates]++
	}
	if fmt.Sprint(counts) != fmt.Sprint(want) {
		t.Errorf("selected states %v, want %v (two toggles picked the same next state)", counts, want)
	}

	final := sc.CurrentState()
	if final != nToggles%nStates {
		t.Errorf("CurrentState = %d, want %d", final, nToggles%nStates)
	}
	for _, d := range []*jitterDevice{a, b} {
		if bri, _ := d.GetBrightness(); bri != 10*final {
			t.Errorf("device %s brightness = %d, want %d (state %d's steps must land last)", d.name, bri, 10*final, final)
		}
	}
}

// The index is published at selection time, and a later selection's steps
// wait for an earlier activation stuck in a driver, then land on top of it.
func TestSceneSelectionPublishedBeforeApplyAndFIFO(t *testing.T) {
	a := newRecordingDevice("A")
	sc, err := NewScene(SceneConfig{
		Name:    "F",
		Devices: []string{"A"},
		States: []SceneStateConfig{
			{Name: "off", Actions: []string{"off:A"}},
			{Name: "one", Actions: []string{"brightness:10:A"}},
			{Name: "two", Actions: []string{"brightness:20:A"}},
		},
	}, resolverFrom(a), log.New(nil))
	if err != nil {
		t.Fatal(err)
	}

	a.blockOn = "brightness:10"
	first := make(chan int)
	go func() { idx, _ := sc.ToggleState(); first <- idx }()
	<-a.entered // state 1's step is stuck in the driver

	if cur := sc.CurrentState(); cur != 1 {
		t.Fatalf("CurrentState = %d while state 1 is applying, want 1 (published at selection)", cur)
	}

	second := make(chan int)
	go func() { idx, _ := sc.ToggleState(); second <- idx }()
	// The second toggle selects state 2 straight away, without waiting.
	deadline := time.Now().Add(2 * time.Second)
	for sc.CurrentState() != 2 {
		if time.Now().After(deadline) {
			t.Fatal("second toggle did not select state 2 while the first was stuck")
		}
		time.Sleep(time.Millisecond)
	}
	if _, calls := a.state(); len(calls) != 0 {
		t.Fatalf("calls before release = %v, want none (second toggle must wait its turn)", calls)
	}

	close(a.release)
	if got := <-first; got != 1 {
		t.Errorf("first toggle selected %d, want 1", got)
	}
	if got := <-second; got != 2 {
		t.Errorf("second toggle selected %d, want 2", got)
	}
	_, calls := a.state()
	if want := []string{"brightness:10", "brightness:20"}; fmt.Sprint(calls) != fmt.Sprint(want) {
		t.Errorf("calls = %v, want %v (steps in selection order)", calls, want)
	}
}

// A delayed step of an activation still queued behind another one must not
// run before its own activation's immediate steps.
func TestSceneDelayedStepWaitsForOwnImmediateSteps(t *testing.T) {
	a := newRecordingDevice("A")
	sc, err := NewScene(SceneConfig{
		Name:    "D",
		Devices: []string{"A"},
		States: []SceneStateConfig{
			{Name: "off", Actions: []string{"off:A"}},
			{Name: "one", Actions: []string{"brightness:10:A"}},
			{Name: "two", Actions: []string{"brightness:20:A", "after:1m:brightness:80:A"}},
		},
	}, resolverFrom(a), log.New(nil))
	if err != nil {
		t.Fatal(err)
	}
	lt := &lockedSceneTimers{}
	lt.install(sc)

	a.blockOn = "brightness:10"
	firstDone := make(chan struct{})
	go func() { sc.Activate(1); close(firstDone) }()
	<-a.entered

	secondDone := make(chan struct{})
	go func() { sc.Activate(2); close(secondDone) }()
	fire := lt.waitFor(t, 0) // state 2 selected and its delayed step scheduled
	delayedDone := make(chan struct{})
	go func() { fire(); close(delayedDone) }() // fires "early"
	time.Sleep(20 * time.Millisecond)

	close(a.release)
	<-firstDone
	<-secondDone
	<-delayedDone

	_, calls := a.state()
	if want := []string{"brightness:10", "brightness:20", "brightness:80"}; fmt.Sprint(calls) != fmt.Sprint(want) {
		t.Errorf("calls = %v, want %v", calls, want)
	}
}

// Close takes only sc.mu: it must return while an activation is stuck in a
// driver and others are queued, and the queued delayed step skips itself.
func TestSceneCloseDoesNotWaitForStuckActivation(t *testing.T) {
	a := newRecordingDevice("A")
	sc, err := NewScene(SceneConfig{
		Name:    "X",
		Devices: []string{"A"},
		States: []SceneStateConfig{
			{Name: "off", Actions: []string{"off:A"}},
			{Name: "on", Actions: []string{"on:A", "after:1m:brightness:80:A"}},
		},
	}, resolverFrom(a), log.New(nil))
	if err != nil {
		t.Fatal(err)
	}
	lt := &lockedSceneTimers{}
	lt.install(sc)

	a.blockOn = "on"
	actDone := make(chan struct{})
	go func() { sc.Activate(1); close(actDone) }()
	<-a.entered
	fire := lt.waitFor(t, 0)
	delayedDone := make(chan struct{})
	go func() { fire(); close(delayedDone) }()
	queuedDone := make(chan struct{})
	go func() { sc.Toggle(); close(queuedDone) }() // queued behind the stuck one

	closed := make(chan struct{})
	go func() { sc.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("Close blocked behind a stuck activation")
	}

	close(a.release)
	<-actDone
	<-queuedDone
	<-delayedDone
	_, calls := a.state()
	for _, c := range calls {
		if c == "brightness:80" {
			t.Errorf("delayed step ran after Close; calls %v", calls)
		}
	}
}

func TestSceneConfigErrors(t *testing.T) {
	a := newTestLight("A", false)
	b := newTestLight("B", false)
	resolve := resolverFrom(a, b)
	states := []SceneStateConfig{
		{Name: "Off", Actions: []string{"off:A"}},
		{Name: "On", Actions: []string{"on:A"}},
	}
	cases := []struct {
		name string
		conf SceneConfig
		want string // "" = must succeed
	}{
		{"missing list", SceneConfig{Name: "S", States: states}, `Devices is required: list the devices this scene controls, e.g. "Devices": ["A"]`},
		{"ok", SceneConfig{Name: "S", Devices: []string{"A", "B"}, ToggleTimeoutSeconds: 1800, States: states}, ""},
		{"state 0 unconstrained but listed", SceneConfig{Name: "S", Devices: []string{"A"}, States: []SceneStateConfig{{Name: "Off", Actions: []string{"after:5m:off:A"}}}}, ""},
		{"empty list", SceneConfig{Name: "S", Devices: []string{}, States: []SceneStateConfig{{Name: "Off"}}}, `Devices is required: list the devices this scene controls, e.g. "Devices": ["Kitchen", "Hall"]`},
		{"unknown device", SceneConfig{Name: "S", Devices: []string{"A", "Ghost"}, States: states}, `unknown device "Ghost"`},
		{"duplicate device", SceneConfig{Name: "S", Devices: []string{"A", "A"}, States: states}, `duplicate device "A"`},
		{"blank device", SceneConfig{Name: "S", Devices: []string{"A", " "}, States: states}, "device name must not be empty"},
		{"self", SceneConfig{Name: "S", Devices: []string{"A", "S"}, States: states}, "cannot target itself"},
		{"step outside list", SceneConfig{Name: "S", Devices: []string{"B"}, States: states}, `device "A" is not in the scene's Devices list`},
		{"no states", SceneConfig{Name: "S", Devices: []string{"A"}}, "needs at least one state"},
		{"timeout negative", SceneConfig{Name: "S", Devices: []string{"A"}, ToggleTimeoutSeconds: -1, States: states}, "ToggleTimeoutSeconds -1 out of range"},
		{"timeout too long", SceneConfig{Name: "S", Devices: []string{"A"}, ToggleTimeoutSeconds: 86401, States: states}, "ToggleTimeoutSeconds 86401 out of range"},
		{"timeout max", SceneConfig{Name: "S", Devices: []string{"A"}, ToggleTimeoutSeconds: 86400, States: states}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewScene(tc.conf, resolve, log.New(nil))
			if tc.want == "" {
				if err != nil {
					t.Fatalf("NewScene: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("NewScene error = %v, want one containing %q", err, tc.want)
			}
			if !strings.HasPrefix(err.Error(), `scene "S": `) {
				t.Errorf("error %q should name the scene", err)
			}
		})
	}
}
