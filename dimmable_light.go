package swkit

import (
	"errors"
	"fmt"
	"hash/fnv"
	"sync"
	"time"

	"github.com/brutella/hap/accessory"
	"github.com/brutella/hap/characteristic"
	"github.com/charmbracelet/log"
	drivers "github.com/hubertat/swkit/drivers"
)

type DimmableLightConfig struct {
	Name string
	// DigitalOutName is optional. When set (the typical wiring) it switches
	// the light on/off while AnalogOutName only sets brightness. When empty,
	// the analog output does both: zero is off, anything above zero is on.
	DigitalOutName string
	// AnalogOutName is required.
	AnalogOutName string
	// DefaultSetpoint is a brightness (0-100, HomeKit %). Zero means unset.
	//
	// With a DigitalOutName it is written to the analog output on startup
	// (hardware otherwise keeps its own power-on default); set to e.g. 100 to
	// ensure full brightness after every restart. The light's on/off state is
	// untouched, since the digital output owns it.
	//
	// Without a DigitalOutName nothing is written on startup, because any
	// non-zero level would switch the light on after every restart. Instead
	// it is the level used when the light is switched on with no earlier
	// level to restore (unset = 100%).
	//
	// Pattern: any device type that owns an AnalogOutput and needs a
	// configurable startup value should follow this same approach —
	// store the setpoint in config (0 = skip), scale to native range in
	// the constructor, and call Set() before returning.
	DefaultSetpoint int
	DisableHomekit  bool
}

// DimmableLight supports brightness control.
var _ Dimmable = (*DimmableLight)(nil)

// DimmableLight is an on/off light with brightness control. It always has an
// AnalogOutput (brightness) and optionally a DigitalOutput (on/off). HomeKit
// brightness (0-100) is scaled to the analog output's native range via
// GetMinMax().
//
// With a DigitalOutput, on/off and brightness are independent: the digital
// output alone decides on/off. Without one (onOut == nil, "analog-only"), the
// analog output carries both: the light is on while it is above its minimum,
// switching off writes the minimum, and switching on restores the last level
// it had (see restoreLevel).
type DimmableLight struct {
	name           string
	disableHomekit bool
	isFaulty       bool

	onOut  drivers.DigitalOutput // nil for an analog-only light
	briOut drivers.AnalogOutput
	logger *log.Logger

	// cmdMu serializes analog commands (SetBrightness, analog-only
	// SetValue/Toggle): each one's read-decide-write, driver write and
	// bookkeeping run as a unit. Power and brightness arrive from independent
	// client request slots, so without it an off could land in the middle of
	// a brightness write and the bookkeeping would disagree with the
	// hardware. It is held across the driver write, so a wedged write blocks
	// further commands to this light only; Sync and reads never take it.
	cmdMu sync.Mutex

	// Analog-only mode: lastLevel is the most recent native level above the
	// minimum, restored when the light is switched back on. Until one is seen
	// (hasLastLevel false), onLevel is used instead. Guarded by levelMu, not lock:
	// Sync holds lock across driver reads, and control calls must not queue
	// behind a slow one. levelMu is never held across a driver call.
	levelMu      sync.Mutex
	lastLevel    int
	hasLastLevel bool
	onLevel      int // native level for switch-on with no lastLevel

	// pending is the last level written to briOut that the driver has not
	// reported back yet (see readLevel). Guarded by levelMu.
	pending pendingLevel
	now     func() time.Time // clock, swappable in tests

	hk         *accessory.Lightbulb
	brightness *characteristic.Brightness
	fault      *characteristic.StatusFault

	lock sync.Mutex
}

// analogPendingTimeout bounds how long readLevel prefers a written level over
// a differing readback. Drivers report analog state asynchronously (Wago only
// on its next poll), so a read straight after Set often returns the old
// level; past this window a mismatch is taken as real (the write was lost, or
// the level changed at the dimmer) and the readback wins.
const analogPendingTimeout = 3 * time.Second

// pendingLevel is a level written to the analog output and not yet confirmed
// by a readback.
type pendingLevel struct {
	active bool
	level  int
	at     time.Time
}

// NewDimmableLight creates a dimmable light. dOut may be nil for an
// analog-only light (see DimmableLight); aOut is required.
func NewDimmableLight(config DimmableLightConfig, dOut drivers.DigitalOutput, aOut drivers.AnalogOutput, logger *log.Logger) *DimmableLight {
	dl := &DimmableLight{
		name:           config.Name,
		disableHomekit: config.DisableHomekit,
		lock:           sync.Mutex{},
		logger:         logger,
		onOut:          dOut,
		briOut:         aOut,
		now:            time.Now,
	}
	min, max := aOut.GetMinMax()
	if dOut == nil {
		logger.Debug("dimmable light created (analog only)", "name", config.Name, "analogOut", aOut.String())
		// Outputs that set only a level by default must carry on/off here.
		if ls, ok := aOut.(drivers.LevelSwitchable); ok {
			ls.SetLevelSwitchesPower(true)
		}
		dl.onLevel = max
		if config.DefaultSetpoint > 0 {
			// A setpoint that scales down to the minimum would make switch-on
			// a no-op; fall back to full brightness then.
			if native := convertIntRange(config.DefaultSetpoint, 0, 100, min, max); native > min {
				dl.onLevel = native
			}
		}
		return dl
	}

	logger.Debug("dimmable light created", "name", config.Name, "digitalOut", dOut.String(), "analogOut", aOut.String())
	if config.DefaultSetpoint > 0 {
		native := convertIntRange(config.DefaultSetpoint, 0, 100, min, max)
		if err := dl.writeLevel(native, false); err != nil {
			logger.Warn("failed to apply default setpoint", "dimmableLight", config.Name, "err", err)
		} else {
			logger.Debug("applied default setpoint", "dimmableLight", config.Name, "setpoint", config.DefaultSetpoint, "native", native)
		}
	}
	return dl
}

// Name returns the name of the dimmable light object
func (dl *DimmableLight) Name() string {
	return dl.name
}

func (dl *DimmableLight) GetUniqueId() uint64 {
	hash := fnv.New64()
	hash.Write([]byte("DimmableLight_" + dl.name))
	return hash.Sum64()
}

func (dl *DimmableLight) InitHk() *accessory.A {
	if dl.disableHomekit {
		dl.logger.Debug("homekit disabled for dimmable light", "name", dl.name)
		return nil
	}

	serial := fmt.Sprintf("dimmable_light:%s", dl.briOut.String())
	if dl.onOut != nil {
		serial = fmt.Sprintf("dimmable_light:%s:%s", dl.onOut.String(), dl.briOut.String())
	}
	info := accessory.Info{
		Name:         dl.name,
		SerialNumber: serial,
	}
	dl.hk = accessory.NewLightbulb(info)

	// The plain Lightbulb service only carries On; add Brightness explicitly.
	dl.brightness = characteristic.NewBrightness()
	dl.hk.Lightbulb.AddC(dl.brightness.C)

	dl.fault = characteristic.NewStatusFault()
	dl.fault.SetValue(characteristic.StatusFaultNoFault)
	dl.hk.Lightbulb.AddC(dl.fault.C)

	dl.hk.Lightbulb.On.OnValueRemoteUpdate(dl.SetValue)
	dl.brightness.OnValueRemoteUpdate(dl.updateBrightness)

	dl.logger.Debug("homekit accessory initialized", "dimmableLight", dl.name)
	return dl.hk.A
}

// updateBrightness is the HomeKit callback for Brightness changes; it delegates
// to SetBrightness. Brightness is independent of the On state.
func (dl *DimmableLight) updateBrightness(newBrightness int) {
	dl.SetBrightness(newBrightness)
}

// SetBrightness sets brightness as a HomeKit percentage (0-100), scaled to the
// analog output's native range. Values are clamped to [0, 100]. With a digital
// output, setting brightness does not toggle the On state - the two are
// independent. Analog-only, brightness is the On state: 0 switches the light
// off and anything above 0 switches it on.
func (dl *DimmableLight) SetBrightness(pct int) {
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	min, max := dl.briOut.GetMinMax()
	native := convertIntRange(pct, 0, 100, min, max)
	dl.logger.Debug("setting dimmable light brightness", "dimmableLight", dl.name, "brightness", pct, "native", native)

	dl.cmdMu.Lock()
	defer dl.cmdMu.Unlock()
	if err := dl.writeLevel(native, true); err != nil {
		dl.logger.Error("failed to set dimmable light brightness", "dimmableLight", dl.name, "err", err)
	}
}

// writeLevel writes native to the analog output. Callers hold cmdMu (or are
// the constructor), so writes and their bookkeeping never interleave.
//
// The bookkeeping is recorded before the driver call, not after it: once the
// write starts, a concurrent readLevel (Sync, the state API) must already
// report the new level, and must not get a window in which to remember the
// old one over it. remember also stores native as the level to restore. If
// the write fails, the bookkeeping is rolled back.
func (dl *DimmableLight) writeLevel(native int, remember bool) error {
	dl.levelMu.Lock()
	prevPending, prevLast, prevHasLast := dl.pending, dl.lastLevel, dl.hasLastLevel
	dl.pending = pendingLevel{active: true, level: native, at: dl.now()}
	if remember {
		dl.rememberLevelLocked(native)
	}
	dl.levelMu.Unlock()

	if err := dl.briOut.Set(native); err != nil {
		dl.levelMu.Lock()
		dl.pending, dl.lastLevel, dl.hasLastLevel = prevPending, prevLast, prevHasLast
		dl.levelMu.Unlock()
		return err
	}

	// Start the confirmation window when the write lands, not when it began.
	dl.levelMu.Lock()
	if dl.pending.active && dl.pending.level == native {
		dl.pending.at = dl.now()
	}
	dl.levelMu.Unlock()
	return nil
}

// readLevel returns the analog output's current native level. Every read of
// briOut goes through here so that decisions taken right after a write (is
// the light on? which level to remember?) see the written level rather than
// a stale readback. The pending level is reported until the driver reads it
// back, or until analogPendingTimeout passes, whichever comes first.
//
// With remember, the resulting level is also stored as the level to restore,
// in the same critical section that resolved it, so a write that starts in
// between cannot have its newer level overwritten by this older read.
func (dl *DimmableLight) readLevel(remember bool) (int, error) {
	native, err := dl.briOut.GetState()
	if err != nil {
		return 0, err
	}
	dl.levelMu.Lock()
	defer dl.levelMu.Unlock()
	if dl.pending.active {
		if native == dl.pending.level || dl.now().Sub(dl.pending.at) >= analogPendingTimeout {
			dl.pending.active = false
		} else {
			native = dl.pending.level
		}
	}
	if remember {
		dl.rememberLevelLocked(native)
	}
	return native, nil
}

// rememberLevelLocked records native as the level to restore on switch-on, if
// it is above the analog minimum. Only meaningful for an analog-only light.
// Callers hold levelMu.
func (dl *DimmableLight) rememberLevelLocked(native int) {
	if dl.onOut != nil {
		return
	}
	min, _ := dl.briOut.GetMinMax()
	if native <= min {
		return
	}
	dl.lastLevel = native
	dl.hasLastLevel = true
}

// restoreLevel returns the native level an analog-only light switches on to:
// the last level above the minimum it had, else onLevel (DefaultSetpoint, or
// the analog maximum when unset).
func (dl *DimmableLight) restoreLevel() int {
	dl.levelMu.Lock()
	defer dl.levelMu.Unlock()
	if dl.hasLastLevel {
		return dl.lastLevel
	}
	return dl.onLevel
}

// Sync() is called periodically by swkit managing server to sync from drivers io.
// If there is no homekit, there is no internal state - skip.
func (dl *DimmableLight) Sync(force bool) (err error) {
	if dl.hk == nil {
		return nil
	}

	dl.lock.Lock()
	defer dl.lock.Unlock()

	var onState bool
	var onErr error
	// Analog-only, remember the level read: tracks levels set outside swkit
	// (e.g. at the dimmer itself) so a later switch-on restores what the
	// user last saw.
	native, briErr := dl.readLevel(dl.onOut == nil)
	if dl.onOut != nil {
		onState, onErr = dl.onOut.GetState()
	} else if briErr == nil {
		onState = dl.isOnLevel(native)
	}
	if onErr != nil || briErr != nil {
		err = errors.Join(errors.New("failed to get one of DimmableLight io values: On, Brightness"), onErr, briErr)
		dl.isFaulty = true
		dl.fault.SetValue(characteristic.StatusFaultGeneralFault)
		return
	}

	dl.isFaulty = false
	dl.fault.SetValue(characteristic.StatusFaultNoFault)

	dl.hk.Lightbulb.On.SetValue(onState)

	min, max := dl.briOut.GetMinMax()
	brightness := convertIntRange(native, min, max, 0, 100)
	dl.brightness.SetValue(brightness)

	return nil
}

// isOnLevel reports whether a native analog level counts as "on" for an
// analog-only light: anything above the analog minimum.
func (dl *DimmableLight) isOnLevel(native int) bool {
	min, _ := dl.briOut.GetMinMax()
	return native > min
}

// GetState reports the current on/off state: from the digital output if there
// is one, otherwise whether the analog output is above its minimum.
func (dl *DimmableLight) GetState() (bool, error) {
	if dl.onOut != nil {
		return dl.onOut.GetState()
	}
	native, err := dl.readLevel(false)
	if err != nil {
		return false, err
	}
	return dl.isOnLevel(native), nil
}

// GetBrightness reports the current brightness as a HomeKit percentage (0-100),
// scaling the analog output's native value the same way Sync does.
func (dl *DimmableLight) GetBrightness() (int, error) {
	native, err := dl.readLevel(false)
	if err != nil {
		return 0, err
	}
	min, max := dl.briOut.GetMinMax()
	return convertIntRange(native, min, max, 0, 100), nil
}

// SetValue switches the light on or off. With a digital output it drives that
// output only. Analog-only, off writes the analog minimum (remembering the
// current level first) and on restores the remembered level; switching on a
// light that is already on leaves its level unchanged.
func (dl *DimmableLight) SetValue(state bool) {
	dl.logger.Debug("setting dimmable light value", "dimmableLight", dl.name, "state", state)
	if dl.onOut != nil {
		if err := dl.onOut.Set(state); err != nil {
			dl.logger.Error("failed to set dimmable light on state", "dimmableLight", dl.name, "err", err)
		}
		return
	}
	dl.cmdMu.Lock()
	defer dl.cmdMu.Unlock()
	current, err := dl.readLevel(true)
	dl.setAnalogOnlyLocked(state, current, err)
}

// setAnalogOnlyLocked switches an analog-only light given its current level
// (and the error reading it), as just read by the caller under cmdMu. The
// level comes from readLevel, not briOut.GetState: right after an earlier
// command the driver may still report the old level, which would both skip a
// needed switch-on and overwrite the newer remembered level.
func (dl *DimmableLight) setAnalogOnlyLocked(state bool, current int, readErr error) {
	min, _ := dl.briOut.GetMinMax()
	target := min
	if state {
		if readErr == nil && dl.isOnLevel(current) {
			return // already on: keep the current level
		}
		target = dl.restoreLevel()
	}
	dl.logger.Debug("setting analog-only dimmable light level", "dimmableLight", dl.name, "state", state, "native", target)
	if err := dl.writeLevel(target, false); err != nil {
		dl.logger.Error("failed to set dimmable light on state", "dimmableLight", dl.name, "err", err)
	}
}

func (dl *DimmableLight) Toggle() {
	if dl.onOut == nil {
		// Read and switch as one command, so concurrent toggles alternate
		// instead of both acting on the same reading.
		dl.cmdMu.Lock()
		defer dl.cmdMu.Unlock()
		current, err := dl.readLevel(true)
		if err != nil {
			dl.logger.Debug("toggle failed to get current state", "dimmableLight", dl.name, "err", err)
			return
		}
		dl.logger.Debug("toggling dimmable light", "dimmableLight", dl.name, "oldState", dl.isOnLevel(current), "newState", !dl.isOnLevel(current))
		dl.setAnalogOnlyLocked(!dl.isOnLevel(current), current, nil)
		return
	}
	currentState, err := dl.GetState()
	if err == nil {
		dl.logger.Debug("toggling dimmable light", "dimmableLight", dl.name, "oldState", currentState, "newState", !currentState)
		dl.SetValue(!currentState)
	} else {
		dl.logger.Debug("toggle failed to get current state", "dimmableLight", dl.name, "err", err)
	}
}
