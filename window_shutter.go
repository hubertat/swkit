package swkit

import (
	"errors"
	"fmt"
	"hash/fnv"
	"math"
	"sync"
	"time"

	"github.com/brutella/hap/accessory"
	"github.com/brutella/hap/characteristic"
	"github.com/charmbracelet/log"
	drivers "github.com/hubertat/swkit/drivers"
)

const (
	// endpointOverrunFraction is the fraction of the full directional travel
	// duration that is added to a commanded run targeting an endpoint (0 or
	// 100). The motor must have functioning travel end limits; the overrun
	// makes sure the shutter is pressed fully against the limit even when the
	// configured duration is slightly short. It is not configurable.
	endpointOverrunFraction = 0.10

	// outputPendingTimeout bounds how long a written output state is
	// preferred over a differing readback. Drivers report output state
	// asynchronously (Shelly via MQTT, Wago on its next poll), so a read
	// straight after a Set often returns the old value; past this window a
	// mismatch is taken as real (the write was lost, or the output was
	// changed externally) and the readback wins. Mirrors
	// analogPendingTimeout in DimmableLight.
	outputPendingTimeout = 3 * time.Second

	// defaultReverseDelay is the motor reversal delay used when
	// WindowShutterConfig.ReverseDelayMilliseconds is 0.
	defaultReverseDelay = 500 * time.Millisecond

	// hkUnknownPositionPercent is the HomeKit placeholder published for the
	// current and target position while the position is unknown. HomeKit has
	// no unknown-percentage value; PositionState Stopped plus this
	// placeholder signal "somewhere in the middle, uncalibrated". It must
	// never be interpreted as a calibrated position.
	hkUnknownPositionPercent = 50
)

// errShutterShutdown is returned by movement commands after Shutdown.
var errShutterShutdown = errors.New("window shutter is shut down")

// WindowShutterConfig configures a maintained two-output roller shutter:
// one digital output drives opening, the other closing. The selected output
// stays energized throughout movement; stopping deenergizes both.
type WindowShutterConfig struct {
	Name string

	// Name of the digital output control - open shutter function
	OpenDigitalOutName string
	// Name of the digital output control - close shutter function
	CloseDigitalOutName string

	// duration in seconds for shutters to open
	OpenDurationSeconds int
	// duration in seconds for shutter to close
	// if empty - OpenDurationSeconds is used
	CloseDurationSeconds int
	// ReverseDelayMilliseconds waits this long between deenergizing one
	// direction and energizing the opposite one, so the motor does not get a
	// reverse command while still spinning. 0 uses the 500 ms default;
	// otherwise 1-5000 ms (a true zero-delay reversal is intentionally not
	// configurable).
	ReverseDelayMilliseconds int `json:",omitzero"`
	DisableHomekit           bool
}

// motion is the shutter movement state.
type motion int

const (
	motionStopped motion = iota
	motionOpening
	motionClosing
)

func (m motion) String() string {
	switch m {
	case motionOpening:
		return "opening"
	case motionClosing:
		return "closing"
	default:
		return "stopped"
	}
}

func opposite(m motion) motion {
	if m == motionClosing {
		return motionOpening
	}
	return motionClosing
}

// motionFromString is the inverse of motion.String, for JSON round-trips.
func motionFromString(s string) motion {
	switch s {
	case "opening":
		return motionOpening
	case "closing":
		return motionClosing
	default:
		return motionStopped
	}
}

// outputExp is an output state written to the driver and not yet confirmed by
// a readback or callback (see outputPendingTimeout). gen is the write
// generation that installed it; a newer write replaces the expectation.
type outputExp struct {
	active    bool
	expected  bool
	confirmed bool // a matching observation arrived (e.g. a synchronous callback)
	deadline  time.Time
	gen       uint64
}

// outputState is the per-output bookkeeping: the pending expectation (if any)
// and the last confirmed/adopted logical value.
type outputState struct {
	exp outputExp
	on  bool
	// stopFailed records that a deactivation write failed while this output
	// was logically on: the relay may still be energized, so starting the
	// opposite direction is prohibited until the output is confirmed off.
	stopFailed bool
}

// pendingMovementStart is a movement start scheduled for the future: either a
// reversal wait (after deenergizing the opposite direction) or a delayed
// same-direction start. Outputs are off while it is pending.
type pendingMovementStart struct {
	dir      motion
	target   int
	endpoint bool
	at       time.Time
	gen      uint64
	cancel   func()
}

// obsValue is one output observation: a value (when fresh), or a read error.
// A non-fresh observation means "no data this round"; the output's logical
// value is used instead.
type obsValue struct {
	val   bool
	err   error
	fresh bool
}

// WindowShutterSnapshot is an atomic, lock-consistent snapshot of the
// shutter state, recomputed from the injected clock at read time.
type WindowShutterSnapshot struct {
	Motion                      string // "stopped", "opening", "closing"
	TargetPosition              int    // percent open; -1 = none/unknown
	CurrentPosition             int    // rounded percent open; 0 when !PositionKnown
	PositionKnown               bool   // false until a full endpoint run calibrates
	Faulty                      bool   // driver/IO errors only, never unknown position
	OpenIoId                    string // config IO id of the open output
	CloseIoId                   string // config IO id of the close output
	OpenDurationSeconds         int    // effective (config value)
	CloseDurationSeconds        int    // effective (after the opening fallback)
	RemainingTravelMilliseconds int64  // until the planned output stop, incl. reversal wait and overrun; 0 when idle
	PendingReversal             bool   // outputs stopped but a start is scheduled (not yet moving)
}

// WindowShutter controls a maintained two-output roller shutter. Position is
// percent open (0 closed, 100 open), estimated from elapsed movement time,
// never measured. Startup assumes both outputs inactive and position unknown;
// a full Open or Close (including the endpoint overrun) establishes the
// endpoint estimate. Intermediate position commands require a known position.
//
// Open, Close, SetPosition and Toggle are commands; Toggle cycles
// start -> stop -> reverse -> stop. Command decisions and output writes are
// serialized by cmdMu (held across driver writes, like DimmableLight);
// mu guards the state fields and is never held across a driver call. Both
// outputs are never energized at once: a direction change deenergizes the old
// direction and observes the reversal delay first. Timer callbacks re-check
// the movement generation under cmdMu, so a stale callback can never stop or
// restart later movement.
//
// Where the driver supports it (Shelly, Wago), SetOnStateUpdate callbacks
// observe external output changes promptly; they feed the same serialized
// reconciliation path as Sync's polling and never replace it. Drivers may
// invoke the callback synchronously from Set while cmdMu is held by the
// issuing command, so callbacks are dispatched asynchronously and stale ones
// (captured before a newer write) are discarded by generation.
type WindowShutter struct {
	name           string
	disableHomekit bool
	isFaulty       bool

	openDout  drivers.DigitalOutput
	closeDout drivers.DigitalOutput

	openDuration  time.Duration
	closeDuration time.Duration
	reverseDelay  time.Duration

	logger *log.Logger

	// now and afterFunc are injectable for deterministic tests, as in Scene.
	now       func() time.Time
	afterFunc func(d time.Duration, f func()) (cancel func())
	// dispatch schedules asynchronous work (the SetOnStateUpdate callbacks,
	// which a driver may invoke synchronously from Set while cmdMu is held
	// by the issuing command). Defaults to a goroutine; tests replace it
	// with a queue they drain explicitly.
	dispatch func(f func())

	hk    *accessory.WindowCovering
	hold  *characteristic.HoldPosition
	fault *characteristic.StatusFault

	cmdMu sync.Mutex // serializes commands, timer callbacks and reconciliation; held across driver writes
	mu    sync.Mutex // guards the fields below; never held across a driver call

	motion         motion
	targetPos      int // percent open; -1 = none/unknown
	position       float64
	positionKnown  bool
	moveStart      time.Time // when the current movement (logically) began
	moveStartPos   float64
	moveStartKnown bool
	moveStopAt     time.Time // planned deenergize time (includes endpoint overrun)
	overrun        bool      // current run targets an endpoint and adds the overrun
	external       bool      // current movement was observed, not commanded

	pendingStart *pendingMovementStart

	gen             uint64 // movement generation; bumped whenever existing timers are invalidated
	stopTimerCancel func()

	lastDir         motion // last direction that actually moved
	lastDirKnown    bool
	lastActiveDir   motion    // direction of the last energization
	energizedEver   bool      // any output has been energized (commanded or adopted)
	lastDeenergized time.Time // when an energized output was last deactivated

	open  outputState
	close outputState

	writeGen uint64 // bumped on every output write; stale observations are discarded against it
	closed   bool
}

// NewWindowShutter creates a window shutter. Both outputs are required.
// The shutter starts with both outputs assumed inactive and position unknown;
// no movement is started here.
func NewWindowShutter(config WindowShutterConfig, openOut, closeOut drivers.DigitalOutput, logger *log.Logger) *WindowShutter {
	openDuration := time.Duration(config.OpenDurationSeconds) * time.Second
	closeDuration := openDuration
	if config.CloseDurationSeconds > 0 {
		closeDuration = time.Duration(config.CloseDurationSeconds) * time.Second
	}
	reverseDelay := defaultReverseDelay
	if config.ReverseDelayMilliseconds > 0 {
		reverseDelay = time.Duration(config.ReverseDelayMilliseconds) * time.Millisecond
	}

	ws := &WindowShutter{
		name:           config.Name,
		disableHomekit: config.DisableHomekit,
		openDout:       openOut,
		closeDout:      closeOut,
		openDuration:   openDuration,
		closeDuration:  closeDuration,
		reverseDelay:   reverseDelay,
		logger:         logger,
		now:            time.Now,
		afterFunc: func(d time.Duration, f func()) func() {
			t := time.AfterFunc(d, f)
			return func() { t.Stop() }
		},
		dispatch:  func(f func()) { go f() },
		targetPos: -1,
	}

	// Observe external output changes promptly where supported. Shelly
	// reports them asynchronously from its MQTT goroutine; Wago invokes the
	// callback synchronously from Set, which is why the dispatch below is
	// asynchronous (cmdMu may be held by the issuing command). GPIO, MCP23017
	// and friends do not support it and rely on periodic Sync polling.
	if err := openOut.SetOnStateUpdate(func(v bool) { ws.onOutputUpdate(motionOpening, v) }); err != nil {
		logger.Debug("open output state updates not supported", "shutter", config.Name, "err", err)
	}
	if err := closeOut.SetOnStateUpdate(func(v bool) { ws.onOutputUpdate(motionClosing, v) }); err != nil {
		logger.Debug("close output state updates not supported", "shutter", config.Name, "err", err)
	}

	logger.Debug("window shutter created", "name", config.Name,
		"openOut", openOut.String(), "closeOut", closeOut.String(),
		"openDuration", openDuration, "closeDuration", closeDuration, "reverseDelay", reverseDelay)
	return ws
}

func (ws *WindowShutter) Name() string {
	return ws.name
}

func (ws *WindowShutter) GetUniqueId() uint64 {
	hash := fnv.New64()
	hash.Write([]byte("WindowShutter_" + ws.name))
	return hash.Sum64()
}

func (ws *WindowShutter) directionDuration(m motion) time.Duration {
	if m == motionClosing {
		return ws.closeDuration
	}
	return ws.openDuration
}

func (ws *WindowShutter) overrunDuration(m motion) time.Duration {
	return time.Duration(float64(ws.directionDuration(m)) * endpointOverrunFraction)
}

func (ws *WindowShutter) endpointOf(m motion) int {
	if m == motionClosing {
		return 0
	}
	return 100
}

// ---- Commands ----

// Open starts opening the shutter. Repeating an active Open while already
// opening toward 100 does not restart anything; an Open during travel toward
// another target retargets to the endpoint. When idle at a known open
// endpoint, Open runs just the endpoint-overrun interval to recalibrate.
func (ws *WindowShutter) Open() error {
	ws.cmdMu.Lock()
	defer ws.cmdMu.Unlock()
	return ws.dispatchOpenClose(motionOpening)
}

// Close starts closing the shutter (movement command; see Shutdown for the
// lifecycle counterpart). Same repeat/retarget/recalibrate rules as Open.
func (ws *WindowShutter) Close() error {
	ws.cmdMu.Lock()
	defer ws.cmdMu.Unlock()
	return ws.dispatchOpenClose(motionClosing)
}

func (ws *WindowShutter) dispatchOpenClose(dir motion) error {
	if err := ws.checkCommandLocked(); err != nil {
		return err
	}
	target := ws.endpointOf(dir)

	if ps := ws.pendingStartLocked(); ps != nil {
		if ps.dir == dir && ps.target == target {
			return nil // repeat of the pending command
		}
		ws.cancelPendingStartLocked()
	}

	ws.mu.Lock()
	curMotion, curTarget := ws.motion, ws.targetPos
	ws.mu.Unlock()

	if curMotion == dir {
		if curTarget == target {
			return nil // already traveling there
		}
		return ws.retargetLocked(dir, target, true)
	}
	if curMotion != motionStopped {
		// Reversal: the old direction must be deenergized successfully
		// before the opposite one may start.
		if err := ws.stopCurrentLocked(); err != nil {
			return err
		}
	}
	return ws.commandStartLocked(dir, target, true)
}

// Stop deenergizes both outputs, cancels a pending start, and accounts for
// the motion already elapsed. It does not reverse or clear the remembered
// movement direction. A failed deactivation returns the error and marks the
// position unknown.
func (ws *WindowShutter) Stop() error {
	ws.cmdMu.Lock()
	defer ws.cmdMu.Unlock()
	if err := ws.checkCommandLocked(); err != nil {
		return err
	}
	if ws.pendingStartLocked() != nil {
		ws.cancelPendingStartLocked()
		return nil
	}
	err := ws.stopCurrentLocked()
	ws.publishHk()
	return err
}

// SetPosition commands a target percent open (0-100). Intermediate targets
// require a known position; endpoint targets (0/100) also work from an
// unknown position and calibrate it. Repeating the accepted target while
// moving, or requesting the current position while idle, is a no-op.
func (ws *WindowShutter) SetPosition(p int) error {
	if p < 0 || p > 100 {
		return fmt.Errorf("window shutter %s: position %d out of range (0-100)", ws.name, p)
	}
	ws.cmdMu.Lock()
	defer ws.cmdMu.Unlock()
	if err := ws.checkCommandLocked(); err != nil {
		return err
	}

	if ps := ws.pendingStartLocked(); ps != nil {
		if ps.target == p {
			return nil // repeat of the accepted numeric target
		}
		ws.cancelPendingStartLocked()
	}

	ws.mu.Lock()
	curMotion, curTarget := ws.motion, ws.targetPos
	pos, known := ws.posAtLocked(ws.now())
	ws.mu.Unlock()
	endpoint := p == 0 || p == 100

	if curMotion != motionStopped {
		if curTarget == p {
			return nil // repeat of the accepted target
		}
		// Direction toward the target from the current estimate. With an
		// unknown position only endpoints are decidable (runs from unknown
		// always target endpoints).
		var toward motion
		if known {
			if float64(p) >= pos {
				toward = motionOpening
			} else {
				toward = motionClosing
			}
		} else if p == 100 {
			toward = motionOpening
		} else {
			toward = motionClosing
		}
		if !known && !endpoint {
			return fmt.Errorf("window shutter %s: position is unknown; run a full open or close first", ws.name)
		}
		if toward == curMotion {
			return ws.retargetLocked(curMotion, p, endpoint)
		}
		if err := ws.stopCurrentLocked(); err != nil {
			return err
		}
		return ws.commandStartLocked(toward, p, endpoint)
	}

	if endpoint {
		if known && int(math.Round(pos)) == p {
			return nil // numeric no-op at the endpoint (Open/Close would recalibrate)
		}
	} else {
		if !known {
			return fmt.Errorf("window shutter %s: position is unknown; run a full open or close first", ws.name)
		}
		if int(math.Round(pos)) == p {
			return nil
		}
	}
	dir := motionOpening
	if p < int(math.Round(pos)) {
		dir = motionClosing
	} else if !known && p == 0 {
		dir = motionClosing
	}
	return ws.commandStartLocked(dir, p, endpoint)
}

// ToggleMovement is the discrete toggle: start -> stop -> reverse direction
// and start -> stop -> repeat. A first toggle with unknown position starts
// opening. A toggle during a pending start cancels it and leaves the shutter
// stopped; the remembered direction stays the last direction that actually
// moved, so the next toggle starts the canceled direction.
func (ws *WindowShutter) ToggleMovement() error {
	ws.cmdMu.Lock()
	defer ws.cmdMu.Unlock()
	if err := ws.checkCommandLocked(); err != nil {
		return err
	}
	if ws.pendingStartLocked() != nil {
		ws.cancelPendingStartLocked()
		return nil
	}
	if ws.motion != motionStopped {
		if err := ws.stopCurrentLocked(); err != nil {
			return err
		}
	}
	nextDir := motionOpening
	ws.mu.Lock()
	if ws.lastDirKnown && ws.lastDir == motionOpening {
		nextDir = motionClosing
	}
	ws.mu.Unlock()
	return ws.commandStartLocked(nextDir, ws.endpointOf(nextDir), true)
}

// SetValue is the legacy Controllable compatibility method: true opens,
// false closes. Failures are logged; new control paths call Open/Close.
func (ws *WindowShutter) SetValue(v bool) {
	var err error
	if v {
		err = ws.Open()
	} else {
		err = ws.Close()
	}
	if err != nil {
		ws.logger.Error("window shutter command failed", "shutter", ws.name, "err", err)
	}
}

// Toggle is the legacy Controllable compatibility method for ToggleMovement.
func (ws *WindowShutter) Toggle() {
	if err := ws.ToggleMovement(); err != nil {
		ws.logger.Error("window shutter toggle failed", "shutter", ws.name, "err", err)
	}
}

func (ws *WindowShutter) checkCommandLocked() error {
	ws.mu.Lock()
	closed := ws.closed
	ws.mu.Unlock()
	if closed {
		return fmt.Errorf("window shutter %s: %w", ws.name, errShutterShutdown)
	}
	return nil
}

// ---- Command internals (cmdMu held) ----

// commandStartLocked starts movement toward target, honoring the reversal
// delay if the opposite direction was deenergized recently.
func (ws *WindowShutter) commandStartLocked(dir motion, target int, endpoint bool) error {
	// A failed deactivation of the opposite direction prohibits starting this
	// direction: the relay may still be energized, and energizing both is the
	// invalid state the controller must never produce.
	if other, _, otherLabel := ws.outputFor(opposite(dir)); other.stopFailed {
		return fmt.Errorf("window shutter %s: %s output may still be energized (a previous stop failed); stop it before starting movement",
			ws.name, otherLabel)
	}
	wait := ws.reversalWaitLocked(dir)
	if wait > 0 {
		now := ws.now()
		gen := ws.bumpGenLocked()
		at := now.Add(wait)
		ps := &pendingMovementStart{dir: dir, target: target, endpoint: endpoint, at: at, gen: gen}
		cancel := ws.afterFunc(wait, func() { ws.onStartTimer(gen) })
		ps.cancel = cancel
		ws.mu.Lock()
		ws.pendingStart = ps
		ws.mu.Unlock()
		ws.publishHk()
		return nil
	}
	err := ws.startMovementLocked(dir, target, endpoint)
	ws.publishHk()
	return err
}

// reversalWaitLocked returns how long the opposite direction must still stay
// deenergized before dir may be energized.
func (ws *WindowShutter) reversalWaitLocked(dir motion) time.Duration {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	if !ws.energizedEver || ws.lastActiveDir == dir || ws.reverseDelay <= 0 {
		return 0
	}
	if ws.lastDeenergized.IsZero() {
		return 0
	}
	elapsed := ws.now().Sub(ws.lastDeenergized)
	if elapsed >= ws.reverseDelay {
		return 0
	}
	return ws.reverseDelay - elapsed
}

// startMovementLocked energizes the direction output and schedules the
// planned stop (travel time, plus the endpoint overrun for endpoint runs).
func (ws *WindowShutter) startMovementLocked(dir motion, target int, endpoint bool) error {
	now := ws.now()
	ws.mu.Lock()
	pos, known := ws.posAtLocked(now)
	dur := ws.directionDuration(dir)
	ws.mu.Unlock()

	var travel time.Duration
	if endpoint {
		if known {
			travel = time.Duration(float64(dur) * math.Abs(float64(target)-pos) / 100.0)
		} else {
			travel = dur
		}
		travel += ws.overrunDuration(dir)
	} else {
		travel = time.Duration(float64(dur) * math.Abs(float64(target)-pos) / 100.0)
	}

	st, out, label := ws.outputFor(dir)
	// A failed deactivation of the opposite direction prohibits starting this
	// direction: the relay may still be energized, and energizing both is the
	// invalid state the controller must never produce.
	if other, _, otherLabel := ws.outputFor(opposite(dir)); other.stopFailed {
		return fmt.Errorf("window shutter %s: %s output may still be energized (a previous stop failed); stop it before starting %s",
			ws.name, otherLabel, label)
	}
	if err := ws.writeOutputLocked(st, out, true); err != nil {
		ws.setFaulty(true)
		return errors.Join(fmt.Errorf("window shutter %s: failed to energize %s output", ws.name, label), err)
	}

	ws.mu.Lock()
	ws.motion = dir
	ws.targetPos = target
	ws.overrun = endpoint
	ws.external = false
	ws.moveStart = now
	ws.moveStartPos = pos
	ws.moveStartKnown = known
	ws.moveStopAt = now.Add(travel)
	ws.lastDir = dir
	ws.lastDirKnown = true
	ws.lastActiveDir = dir
	ws.energizedEver = true
	ws.gen++
	g := ws.gen
	ws.mu.Unlock()

	cancel := ws.afterFunc(travel, func() { ws.onStopTimer(g) })
	ws.mu.Lock()
	if ws.gen == g {
		ws.stopTimerCancel = cancel
	}
	ws.mu.Unlock()
	return nil
}

// retargetLocked changes the target of an ongoing movement in the same
// direction (e.g. Open while traveling toward 50): the already-traveled
// estimate is accounted and a new stop deadline is scheduled.
func (ws *WindowShutter) retargetLocked(dir motion, target int, endpoint bool) error {
	now := ws.now()
	ws.mu.Lock()
	pos, known := ws.posAtLocked(now)
	if !known && !endpoint {
		ws.mu.Unlock()
		return fmt.Errorf("window shutter %s: position is unknown; run a full open or close first", ws.name)
	}
	ws.position, ws.positionKnown = pos, known
	dur := ws.directionDuration(dir)
	var travel time.Duration
	if endpoint {
		if known {
			travel = time.Duration(float64(dur) * math.Abs(float64(target)-pos) / 100.0)
		} else {
			travel = dur
		}
		travel += ws.overrunDuration(dir)
	} else {
		travel = time.Duration(float64(dur) * math.Abs(float64(target)-pos) / 100.0)
	}
	ws.motion = dir
	ws.targetPos = target
	ws.overrun = endpoint
	ws.external = false
	ws.moveStart = now
	ws.moveStartPos = pos
	ws.moveStartKnown = known
	ws.moveStopAt = now.Add(travel)
	ws.gen++
	g := ws.gen
	oldCancel := ws.stopTimerCancel
	ws.stopTimerCancel = nil
	ws.mu.Unlock()
	if oldCancel != nil {
		oldCancel()
	}

	cancel := ws.afterFunc(travel, func() { ws.onStopTimer(g) })
	ws.mu.Lock()
	if ws.gen == g {
		ws.stopTimerCancel = cancel
	}
	ws.mu.Unlock()
	ws.publishHk()
	return nil
}

// stopCurrentLocked deenergizes both outputs and accounts for the elapsed
// motion. The published target becomes the stopped position, so a later
// request for the abandoned target is a changed value.
func (ws *WindowShutter) stopCurrentLocked() error {
	now := ws.now()
	ws.cancelStopTimerLocked()
	stopErr := ws.deactivateOutputsLocked(now)

	ws.mu.Lock()
	pos, known := ws.posAtLocked(now)
	if stopErr != nil {
		known = false
	}
	ws.position, ws.positionKnown = pos, known
	ws.motion = motionStopped
	ws.overrun = false
	ws.external = false
	ws.moveStart = time.Time{}
	ws.moveStopAt = time.Time{}
	if ws.positionKnown {
		ws.targetPos = int(math.Round(ws.position))
	} else {
		ws.targetPos = -1
	}
	ws.mu.Unlock()

	if stopErr != nil {
		ws.setFaulty(true)
		return errors.Join(fmt.Errorf("window shutter %s: failed to stop outputs", ws.name), stopErr)
	}
	return nil
}

// completeMovementLocked is the planned-stop timer body: deenergize and
// finish. A commanded run that completes sets the position to its target (an
// endpoint run calibrates it); an externally adopted run that reaches its
// cap leaves the position unknown.
func (ws *WindowShutter) completeMovementLocked() error {
	now := ws.now()
	ws.mu.Lock()
	wasExternal := ws.external
	target := ws.targetPos
	moving := ws.motion != motionStopped
	ws.mu.Unlock()
	if !moving {
		return nil
	}

	stopErr := ws.deactivateOutputsLocked(now)

	ws.mu.Lock()
	ws.motion = motionStopped
	ws.overrun = false
	ws.external = false
	ws.moveStart = time.Time{}
	ws.moveStopAt = time.Time{}
	switch {
	case stopErr != nil:
		ws.positionKnown = false
		ws.targetPos = -1
	case wasExternal:
		ws.positionKnown = false
		ws.targetPos = -1
	default:
		ws.position = float64(target)
		ws.positionKnown = true
		ws.targetPos = target
	}
	ws.mu.Unlock()

	if stopErr != nil {
		ws.setFaulty(true)
		return errors.Join(fmt.Errorf("window shutter %s: failed to stop outputs", ws.name), stopErr)
	}
	return nil
}

func (ws *WindowShutter) pendingStartLocked() *pendingMovementStart {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	return ws.pendingStart
}

func (ws *WindowShutter) cancelPendingStartLocked() {
	ws.mu.Lock()
	ps := ws.pendingStart
	ws.pendingStart = nil
	ws.gen++
	ws.mu.Unlock()
	if ps != nil && ps.cancel != nil {
		ps.cancel()
	}
}

func (ws *WindowShutter) cancelStopTimerLocked() {
	ws.mu.Lock()
	cancel := ws.stopTimerCancel
	ws.stopTimerCancel = nil
	ws.gen++
	ws.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (ws *WindowShutter) bumpGenLocked() uint64 {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	ws.gen++
	return ws.gen
}

func (ws *WindowShutter) outputFor(dir motion) (*outputState, drivers.DigitalOutput, string) {
	if dir == motionClosing {
		return &ws.close, ws.closeDout, "close"
	}
	return &ws.open, ws.openDout, "open"
}

// logicalOnLocked returns the output's current logical value: the pending
// expectation's value while one is active, else the confirmed value.
func (ws *WindowShutter) logicalOnLocked(st *outputState) bool {
	if st.exp.active {
		return st.exp.expected
	}
	return st.on
}

// writeOutputLocked writes value to out, installing a pending expectation
// first so that (a)synchronous callbacks observe it, and rolling the
// expectation back on failure. A no-op write (logical value already equal and
// no pending expectation) skips the driver call and must not extend any
// deadline.
func (ws *WindowShutter) writeOutputLocked(st *outputState, out drivers.DigitalOutput, value bool) error {
	if ws.logicalOnLocked(st) == value && !st.stopFailed {
		return nil // no-op write: skip, never extends deadlines
	}
	now := ws.now()
	ws.mu.Lock()
	prev := st.exp
	ws.writeGen++
	st.exp = outputExp{active: true, expected: value, deadline: now.Add(outputPendingTimeout), gen: ws.writeGen}
	gen := st.exp.gen
	ws.mu.Unlock()

	err := out.Set(value)

	ws.mu.Lock()
	if err != nil {
		if st.exp.active && st.exp.gen == gen {
			st.exp = prev // roll back only this write's expectation
		}
	} else {
		st.stopFailed = false // a successful write re-establishes the logical state
		if st.exp.active && st.exp.gen == gen && !st.exp.confirmed {
			// Extend to completion time + timeout, unless a synchronous
			// callback already confirmed this write (async dispatch may defer
			// that past this point; the deferred confirmation then clears it).
			st.exp.deadline = ws.now().Add(outputPendingTimeout)
		}
	}
	ws.mu.Unlock()
	return err
}

// deactivateOutputsLocked deenergizes both outputs, attempting both even if
// one fails (joined errors). It records the deenergization time for the
// reversal delay whenever an output was logically on.
func (ws *WindowShutter) deactivateOutputsLocked(now time.Time) error {
	var errs error
	attempted := false
	if ws.logicalOnLocked(&ws.open) {
		attempted = true
		if err := ws.writeOutputLocked(&ws.open, ws.openDout, false); err != nil {
			ws.mu.Lock()
			ws.open.stopFailed = true
			ws.mu.Unlock()
			errs = errors.Join(errs, fmt.Errorf("window shutter %s: failed to deenergize open output: %w", ws.name, err))
		}
	}
	if ws.logicalOnLocked(&ws.close) {
		attempted = true
		if err := ws.writeOutputLocked(&ws.close, ws.closeDout, false); err != nil {
			ws.mu.Lock()
			ws.close.stopFailed = true
			ws.mu.Unlock()
			errs = errors.Join(errs, fmt.Errorf("window shutter %s: failed to deenergize close output: %w", ws.name, err))
		}
	}
	if attempted {
		ws.mu.Lock()
		ws.lastDeenergized = now
		ws.mu.Unlock()
	}
	return errs
}

// ---- Timers ----

func (ws *WindowShutter) onStopTimer(g uint64) {
	ws.cmdMu.Lock()
	defer ws.cmdMu.Unlock()
	ws.mu.Lock()
	closed := ws.closed
	cur := ws.gen
	ws.mu.Unlock()
	if closed || g != cur {
		return
	}
	if err := ws.completeMovementLocked(); err != nil {
		ws.logger.Error("window shutter failed to complete movement", "shutter", ws.name, "err", err)
	}
	ws.publishHk()
}

func (ws *WindowShutter) onStartTimer(g uint64) {
	ws.cmdMu.Lock()
	defer ws.cmdMu.Unlock()
	ws.mu.Lock()
	closed := ws.closed
	ps := ws.pendingStart
	ws.mu.Unlock()
	if closed || ps == nil || ps.gen != g {
		return
	}
	ws.mu.Lock()
	ws.pendingStart = nil
	ws.mu.Unlock()
	if err := ws.startMovementLocked(ps.dir, ps.target, ps.endpoint); err != nil {
		ws.logger.Error("window shutter failed to start pending movement", "shutter", ws.name, "err", err)
	}
	ws.publishHk()
}

// ---- Observation: callbacks and polling ----

// onOutputUpdate is the SetOnStateUpdate callback for one output. It is
// dispatched asynchronously: a driver may invoke it synchronously from Set
// while cmdMu is held by the issuing command. The write generation is
// captured at callback time; a callback captured before a newer write is
// discarded when it is processed.
func (ws *WindowShutter) onOutputUpdate(dir motion, value bool) {
	ws.mu.Lock()
	closed := ws.closed
	gen := ws.writeGen
	ws.mu.Unlock()
	if closed {
		return
	}
	ws.dispatch(func() { ws.processOutputUpdate(dir, value, gen) })
}

func (ws *WindowShutter) processOutputUpdate(dir motion, value bool, gen uint64) {
	ws.cmdMu.Lock()
	defer ws.cmdMu.Unlock()
	ws.mu.Lock()
	closed := ws.closed
	curGen := ws.writeGen
	ws.mu.Unlock()
	if closed || curGen != gen {
		return // superseded by a newer write
	}
	now := ws.now()

	// A callback carries one output value; reconcile the pair. If the pair
	// looks like both outputs are on (our logical state for the other output
	// disagrees with a fresh ON for this one), read the other output before
	// declaring the invalid state - the relay pair may have just switched.
	openObs := obsValue{}
	closeObs := obsValue{}
	if dir == motionClosing {
		closeObs = obsValue{val: value, fresh: true}
		if ws.logicalOnLocked(&ws.open) {
			if v, err := ws.openDout.GetState(); err == nil {
				openObs = obsValue{val: v, fresh: true}
			} else {
				ws.logger.Debug("could not verify open output state", "shutter", ws.name, "err", err)
			}
		}
	} else {
		openObs = obsValue{val: value, fresh: true}
		if ws.logicalOnLocked(&ws.close) {
			if v, err := ws.closeDout.GetState(); err == nil {
				closeObs = obsValue{val: v, fresh: true}
			} else {
				ws.logger.Debug("could not verify close output state", "shutter", ws.name, "err", err)
			}
		}
	}
	ws.reconcileLocked(openObs, closeObs, now, true)
	ws.publishHk()
}

// Sync advances the position estimate and reconciles the observed output
// states. It always runs, including with HomeKit disabled or before InitHk;
// only characteristic publication is conditional. IO read errors are
// returned (and marked as faults) rather than fabricated as observations.
func (ws *WindowShutter) Sync(force bool) error {
	ws.cmdMu.Lock()
	defer ws.cmdMu.Unlock()
	ws.mu.Lock()
	closed := ws.closed
	ws.mu.Unlock()
	if closed {
		return nil
	}
	now := ws.now()
	ws.advanceEstimateLocked(now)

	// Read outputs with the write generation captured before the reads; a
	// write that lands while the reads are in flight installs its own
	// expectation, so this round's observations are discarded.
	ws.mu.Lock()
	gen := ws.writeGen
	ws.mu.Unlock()
	openVal, openErr := ws.openDout.GetState()
	closeVal, closeErr := ws.closeDout.GetState()
	ws.mu.Lock()
	genNow := ws.writeGen
	ws.mu.Unlock()

	var errs error
	if genNow == gen {
		openObs := obsValue{val: openVal, err: openErr, fresh: true}
		closeObs := obsValue{val: closeVal, err: closeErr, fresh: true}
		errs = ws.reconcileLocked(openObs, closeObs, now, false)
	} else if openErr != nil || closeErr != nil {
		errs = errors.Join(
			fmt.Errorf("window shutter %s: outputs unreadable", ws.name),
			openErr, closeErr,
		)
		ws.setFaulty(true)
	}

	if openErr == nil && closeErr == nil && errs == nil {
		ws.setFaulty(false) // unknown position is not a fault
	}
	ws.publishHk()
	return errs
}

// advanceEstimateLocked persists the elapsed-motion position estimate.
func (ws *WindowShutter) advanceEstimateLocked(now time.Time) {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	pos, known := ws.posAtLocked(now)
	ws.position, ws.positionKnown = pos, known
}

// reconcileLocked brings the commanded model in line with the observed
// output pair. transitionKnown marks whether the observation time is the
// actual transition time (a callback), as opposed to a poll that only bounds
// it.
func (ws *WindowShutter) reconcileLocked(openObs, closeObs obsValue, now time.Time, transitionKnown bool) error {
	var errs error
	openLogical, _ := ws.resolveObservationLocked(&ws.open, openObs, now, &errs, "open")
	closeLogical, _ := ws.resolveObservationLocked(&ws.close, closeObs, now, &errs, "close")

	ws.mu.Lock()
	curMotion := ws.motion
	ws.mu.Unlock()

	switch {
	case openLogical && closeLogical:
		errs = errors.Join(errs, ws.handleBothActiveLocked(now))
	case !openLogical && !closeLogical:
		if curMotion != motionStopped {
			ws.adoptExternalStopLocked(now, transitionKnown)
		}
	default:
		dir := motionOpening
		if closeLogical {
			dir = motionClosing
		}
		if curMotion != dir {
			ws.adoptExternalMovementLocked(dir, now, transitionKnown)
		}
		// Same direction: continue the current estimate (a commanded partial
		// run keeps its earlier stop deadline; both are bounded by a full
		// directional duration).
	}
	return errs
}

// resolveObservationLocked folds one observation into the output's logical
// state. A healthy observation matching an active expectation clears it and
// confirms the output; a contradiction before the deadline is ignored (the
// commanded estimate continues); at or after the deadline it is adopted as
// external state. An unreadable output reports an IO error and never
// confirms a write.
func (ws *WindowShutter) resolveObservationLocked(st *outputState, obs obsValue, now time.Time, errs *error, label string) (logical bool, adopted bool) {
	if !obs.fresh {
		return ws.logicalOnLocked(st), false
	}
	if obs.err != nil {
		*errs = errors.Join(*errs, fmt.Errorf("window shutter %s: %s output unreadable: %w", ws.name, label, obs.err))
		ws.setFaulty(true)
		return ws.logicalOnLocked(st), false
	}
	if st.exp.active {
		if obs.val == st.exp.expected {
			// Matching observation: confirms the write. The expectation stays
			// armed until its deadline, so a stale contradictory readback
			// right after a confirmation is still ignored (the driver may
			// report cached state); at or after the deadline it is settled
			// and cleared. Repeated matching polls never extend the deadline.
			st.on = obs.val
			st.exp.confirmed = true
			if !now.Before(st.exp.deadline) {
				st.exp.active = false
			}
			return obs.val, true
		}
		if now.Before(st.exp.deadline) {
			return st.exp.expected, false // readback delay: keep the commanded state
		}
		// Deadline passed: the write was lost or the output changed externally.
		st.exp.active = false
		st.on = obs.val
		if !obs.val {
			st.stopFailed = false // the driver confirms the relay is actually off
		}
		return obs.val, true
	}
	st.on = obs.val
	if !obs.val {
		st.stopFailed = false
	}
	return obs.val, true
}

// handleBothActiveLocked deals with the invalid IO state of both outputs
// energized: try to stop both, mark the position unknown and report the fault.
func (ws *WindowShutter) handleBothActiveLocked(now time.Time) error {
	stopErr := ws.deactivateOutputsLocked(now)
	ws.cancelStopTimerLocked()
	ws.cancelPendingStartLocked()
	ws.mu.Lock()
	ws.motion = motionStopped
	ws.overrun = false
	ws.external = false
	ws.moveStart = time.Time{}
	ws.moveStopAt = time.Time{}
	ws.positionKnown = false
	ws.targetPos = -1
	ws.mu.Unlock()
	ws.setFaulty(true)
	return errors.Join(fmt.Errorf("window shutter %s: both outputs active (invalid IO state)", ws.name), stopErr)
}

// adoptExternalStopLocked adopts an externally observed stop: movement timers
// are cancelled and only a trustworthy partial estimate is retained (a poll
// cannot reconstruct the transition time, so the position becomes unknown).
// An unexpected stop never becomes a calibrated endpoint.
func (ws *WindowShutter) adoptExternalStopLocked(now time.Time, transitionKnown bool) {
	ws.cancelStopTimerLocked()
	ws.mu.Lock()
	if transitionKnown && ws.moveStartKnown {
		pos, _ := ws.posAtLocked(now)
		ws.position = pos
		ws.positionKnown = true
		ws.targetPos = int(math.Round(pos))
	} else {
		ws.positionKnown = false
		ws.targetPos = -1
	}
	ws.motion = motionStopped
	ws.overrun = false
	ws.external = false
	ws.moveStart = time.Time{}
	ws.moveStopAt = time.Time{}
	ws.mu.Unlock()
}

// adoptExternalMovementLocked adopts an externally observed single-direction
// movement into the estimate instead of forcing a stop: the observed
// direction is recorded, the target becomes that direction's endpoint, and a
// stop deadline no later than one full directional duration after the
// observation is enforced. At that cap the outputs are stopped and the
// position becomes unknown: an external observation alone never establishes
// a calibrated endpoint.
func (ws *WindowShutter) adoptExternalMovementLocked(dir motion, now time.Time, transitionKnown bool) {
	ws.cancelStopTimerLocked()
	ws.cancelPendingStartLocked()
	dur := ws.directionDuration(dir)
	ws.mu.Lock()
	pos, known := ws.posAtLocked(now)
	startKnown := known && ws.motion == motionStopped && transitionKnown
	if !startKnown {
		ws.positionKnown = false
	}
	ws.motion = dir
	ws.external = true
	ws.targetPos = ws.endpointOf(dir)
	ws.overrun = false
	ws.moveStart = now
	ws.moveStartPos = pos
	ws.moveStartKnown = startKnown
	ws.moveStopAt = now.Add(dur)
	ws.lastDir = dir
	ws.lastDirKnown = true
	ws.lastActiveDir = dir
	ws.energizedEver = true
	g := ws.gen
	ws.mu.Unlock()

	cancel := ws.afterFunc(dur, func() { ws.onStopTimer(g) })
	ws.mu.Lock()
	if ws.gen == g {
		ws.stopTimerCancel = cancel
	}
	ws.mu.Unlock()
}

// ---- Position estimate ----

// posAtLocked computes the position estimate at t. The fractional estimate
// avoids drift from repeated rounding; callers round once for display.
func (ws *WindowShutter) posAtLocked(t time.Time) (float64, bool) {
	if ws.motion == motionStopped || ws.moveStart.IsZero() {
		return ws.position, ws.positionKnown
	}
	if !ws.moveStartKnown {
		return ws.position, false
	}
	elapsed := t.Sub(ws.moveStart)
	if elapsed < 0 {
		elapsed = 0
	}
	dur := ws.directionDuration(ws.motion)
	if dur <= 0 {
		return ws.position, ws.positionKnown
	}
	delta := 100.0 * elapsed.Seconds() / dur.Seconds()
	if ws.motion == motionClosing {
		delta = -delta
	}
	pos := ws.moveStartPos + delta
	if pos < 0 {
		pos = 0
	}
	if pos > 100 {
		pos = 100
	}
	return pos, true
}

// Snapshot returns an atomic state snapshot, with the position estimate and
// remaining travel recomputed from the injected clock.
func (ws *WindowShutter) Snapshot() WindowShutterSnapshot {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	now := ws.now()
	pos, known := ws.posAtLocked(now)
	snap := WindowShutterSnapshot{
		Motion:               ws.motion.String(),
		TargetPosition:       ws.targetPos,
		CurrentPosition:      int(math.Round(pos)),
		PositionKnown:        known,
		Faulty:               ws.isFaulty,
		OpenIoId:             ws.openDout.String(),
		CloseIoId:            ws.closeDout.String(),
		OpenDurationSeconds:  int(ws.openDuration / time.Second),
		CloseDurationSeconds: int(ws.closeDuration / time.Second),
		PendingReversal:      ws.pendingStart != nil,
	}
	switch {
	case ws.motion != motionStopped && !ws.moveStopAt.IsZero():
		if rem := ws.moveStopAt.Sub(now); rem > 0 {
			snap.RemainingTravelMilliseconds = rem.Milliseconds()
		}
	case ws.pendingStart != nil:
		wait := ws.pendingStart.at.Sub(now)
		if wait < 0 {
			wait = 0
		}
		dur := ws.directionDuration(ws.pendingStart.dir)
		var travel time.Duration
		if ws.pendingStart.endpoint {
			if known {
				d := math.Abs(float64(ws.pendingStart.target) - pos)
				travel = time.Duration(float64(dur) * d / 100.0)
			} else {
				travel = dur
			}
			travel += ws.overrunDuration(ws.pendingStart.dir)
		} else {
			d := math.Abs(float64(ws.pendingStart.target) - pos)
			travel = time.Duration(float64(dur) * d / 100.0)
		}
		snap.RemainingTravelMilliseconds = (wait + travel).Milliseconds()
	}
	return snap
}

// ---- Health ----

func (ws *WindowShutter) setFaulty(faulty bool) {
	ws.mu.Lock()
	changed := ws.isFaulty != faulty
	ws.isFaulty = faulty
	fault := ws.fault
	ws.mu.Unlock()
	if changed {
		ws.logger.Debug("window shutter fault state changed", "shutter", ws.name, "faulty", faulty)
	}
	if fault != nil {
		if faulty {
			fault.SetValue(characteristic.StatusFaultGeneralFault)
		} else {
			fault.SetValue(characteristic.StatusFaultNoFault)
		}
	}
}

// ---- HomeKit ----

// InitHk creates the WindowCovering accessory. TargetPosition routes remote
// writes through SetPosition (with a nonzero HAP status on failure, via
// OnSetRemoteValue, so HAP aborts storing the value); HoldPosition stops
// movement. CurrentPosition/TargetPosition/PositionState are published from
// one snapshot; an unknown position uses the placeholder plus Stopped.
func (ws *WindowShutter) InitHk() *accessory.A {
	if ws.disableHomekit {
		ws.logger.Debug("homekit disabled for window shutter", "name", ws.name)
		return nil
	}
	ws.mu.Lock()
	already := ws.hk != nil
	ws.mu.Unlock()
	if already {
		return ws.hk.A
	}

	serial := fmt.Sprintf("window_shutter:%s:%s", ws.openDout.String(), ws.closeDout.String())
	ws.hk = accessory.NewWindowCovering(accessory.Info{
		Name:         ws.name,
		SerialNumber: serial,
	})

	// HoldPosition is not part of the generated service; add it for Stop.
	ws.hold = characteristic.NewHoldPosition()
	ws.hk.WindowCovering.AddC(ws.hold.C)

	ws.fault = characteristic.NewStatusFault()
	ws.fault.SetValue(characteristic.StatusFaultNoFault)
	ws.hk.WindowCovering.AddC(ws.fault.C)

	ws.hk.WindowCovering.TargetPosition.OnSetRemoteValue(func(v int) error {
		return ws.SetPosition(v)
	})
	ws.hold.OnSetRemoteValue(func(v bool) error {
		if v {
			return ws.Stop()
		}
		return nil
	})

	ws.logger.Debug("homekit accessory initialized", "window shutter", ws.name)
	ws.publishHk()
	return ws.hk.A
}

// publishHk publishes the current snapshot to the HomeKit characteristics.
// Called under cmdMu; only the publication is conditional on HomeKit being
// initialized.
func (ws *WindowShutter) publishHk() {
	ws.mu.Lock()
	hk := ws.hk
	if hk == nil {
		ws.mu.Unlock()
		return
	}
	now := ws.now()
	pos, known := ws.posAtLocked(now)
	cur := hkUnknownPositionPercent
	if known {
		cur = int(math.Round(pos))
	}
	tgt := hkUnknownPositionPercent
	if ws.targetPos >= 0 {
		tgt = ws.targetPos
	}
	state := characteristic.PositionStateStopped
	if ws.motion == motionOpening {
		state = characteristic.PositionStateIncreasing
	} else if ws.motion == motionClosing {
		state = characteristic.PositionStateDecreasing
	}
	faulty := ws.isFaulty
	ws.mu.Unlock()

	hk.WindowCovering.CurrentPosition.SetValue(cur)
	hk.WindowCovering.TargetPosition.SetValue(tgt)
	hk.WindowCovering.PositionState.SetValue(state)
	if ws.fault != nil {
		if faulty {
			ws.fault.SetValue(characteristic.StatusFaultGeneralFault)
		} else {
			ws.fault.SetValue(characteristic.StatusFaultNoFault)
		}
	}
}

// ---- Lifecycle ----

// Shutdown is the idempotent lifecycle cleanup: it cancels timers, rejects
// further commands and stops both outputs before the drivers close. It
// tolerates nil outputs, absent timers, partially initialized state and
// repeated calls, attempting every available output even if another fails.
// (The movement command to close the shutter is Close, not Shutdown.)
func (ws *WindowShutter) Shutdown() error {
	ws.cmdMu.Lock()
	defer ws.cmdMu.Unlock()
	ws.mu.Lock()
	if ws.closed {
		ws.mu.Unlock()
		return nil
	}
	ws.closed = true
	ws.gen++ // invalidate every outstanding timer
	ws.mu.Unlock()

	ws.cancelStopTimerLocked()
	ws.cancelPendingStartLocked()

	var errs error
	if ws.openDout != nil {
		if err := ws.openDout.Set(false); err != nil {
			errs = errors.Join(errs, fmt.Errorf("window shutter %s: failed to deenergize open output: %w", ws.name, err))
		}
	}
	if ws.closeDout != nil {
		if err := ws.closeDout.Set(false); err != nil {
			errs = errors.Join(errs, fmt.Errorf("window shutter %s: failed to deenergize close output: %w", ws.name, err))
		}
	}

	ws.mu.Lock()
	ws.motion = motionStopped
	ws.overrun = false
	ws.external = false
	ws.moveStart = time.Time{}
	ws.moveStopAt = time.Time{}
	ws.pendingStart = nil
	ws.mu.Unlock()
	return errs
}
