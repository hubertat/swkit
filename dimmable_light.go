package swkit

import (
	"errors"
	"fmt"
	"hash/fnv"
	"sync"

	"github.com/brutella/hap/accessory"
	"github.com/brutella/hap/characteristic"
	"github.com/charmbracelet/log"
	drivers "github.com/hubertat/swkit/drivers"
)

type DimmableLightConfig struct {
	Name           string
	DigitalOutName string
	AnalogOutName  string
	// DefaultSetpoint is the brightness (0-100, HomeKit %) written to the
	// analog output on startup. Zero means "do not initialise" (hardware
	// keeps its own power-on default). Set to e.g. 100 to ensure full
	// brightness after every restart.
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

// DimmableLight is an on/off light with brightness control, composing a
// DigitalOutput (on/off) and an AnalogOutput (brightness). HomeKit brightness
// (0-100) is scaled to the analog output's native range via GetMinMax().
type DimmableLight struct {
	name           string
	disableHomekit bool
	isFaulty       bool

	onOut  drivers.DigitalOutput
	briOut drivers.AnalogOutput
	logger *log.Logger

	hk         *accessory.Lightbulb
	brightness *characteristic.Brightness
	fault      *characteristic.StatusFault

	lock sync.Mutex
}

func NewDimmableLight(config DimmableLightConfig, dOut drivers.DigitalOutput, aOut drivers.AnalogOutput, logger *log.Logger) *DimmableLight {
	logger.Debug("dimmable light created", "name", config.Name, "digitalOut", dOut.String(), "analogOut", aOut.String())
	dl := &DimmableLight{
		name:           config.Name,
		disableHomekit: config.DisableHomekit,
		lock:           sync.Mutex{},
		logger:         logger,
		onOut:          dOut,
		briOut:         aOut,
	}
	if config.DefaultSetpoint > 0 {
		min, max := aOut.GetMinMax()
		native := convertIntRange(config.DefaultSetpoint, 0, 100, min, max)
		if err := aOut.Set(native); err != nil {
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

	info := accessory.Info{
		Name:         dl.name,
		SerialNumber: fmt.Sprintf("dimmable_light:%s:%s", dl.onOut.String(), dl.briOut.String()),
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
// analog output's native range. Values are clamped to [0, 100]. Setting
// brightness does not toggle the On state - the two are independent.
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
	if err := dl.briOut.Set(native); err != nil {
		dl.logger.Error("failed to set dimmable light brightness", "dimmableLight", dl.name, "err", err)
	}
}

// Sync() is called periodically by swkit managing server to sync from drivers io.
// If there is no homekit, there is no internal state - skip.
func (dl *DimmableLight) Sync(force bool) (err error) {
	if dl.hk == nil {
		return nil
	}

	dl.lock.Lock()
	defer dl.lock.Unlock()

	onState, onErr := dl.onOut.GetState()
	native, briErr := dl.briOut.GetState()
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

// GetState reports the current on/off state from the digital output.
func (dl *DimmableLight) GetState() (bool, error) {
	return dl.onOut.GetState()
}

func (dl *DimmableLight) SetValue(state bool) {
	dl.logger.Debug("setting dimmable light value", "dimmableLight", dl.name, "state", state)
	if err := dl.onOut.Set(state); err != nil {
		dl.logger.Error("failed to set dimmable light on state", "dimmableLight", dl.name, "err", err)
	}
}

func (dl *DimmableLight) Toggle() {
	currentState, err := dl.onOut.GetState()
	if err == nil {
		dl.logger.Debug("toggling dimmable light", "dimmableLight", dl.name, "oldState", currentState, "newState", !currentState)
		dl.SetValue(!currentState)
	} else {
		dl.logger.Debug("toggle failed to get current state", "dimmableLight", dl.name, "err", err)
	}
}
