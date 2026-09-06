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

type ColorLightConfig struct {
	Name           string
	DigitalOutName string
	RgbwOutName    string
	DisableHomekit bool
}

type ColorLight struct {
	name           string
	disableHomekit bool
	isFaulty       bool

	onDigitalOut drivers.DigitalOutput
	rgbwOut      drivers.RgbwOutput
	logger       *log.Logger

	hk    *accessory.ColoredLightbulb
	fault *characteristic.StatusFault

	lock sync.Mutex
}

func NewColorLight(config ColorLightConfig, dOut drivers.DigitalOutput, rgbwOut drivers.RgbwOutput, logger *log.Logger) *ColorLight {
	logger.Debug("color light created", "name", config.Name, "digitalOut", dOut.String(), "rgbwOut", rgbwOut.String())
	return &ColorLight{
		name:           config.Name,
		disableHomekit: config.DisableHomekit,
		lock:           sync.Mutex{},
		logger:         logger,
		onDigitalOut:   dOut,
		rgbwOut:        rgbwOut,
	}
}

// Name returns the name of the color light object
func (cl *ColorLight) Name() string {
	return cl.name
}

func (cl *ColorLight) GetUniqueId() uint64 {
	hash := fnv.New64()
	hash.Write([]byte("ColorLight_" + cl.name))
	return hash.Sum64()
}

func (cl *ColorLight) InitHk() *accessory.A {
	if cl.disableHomekit {
		cl.logger.Debug("homekit disabled for color light", "name", cl.name)
		return nil
	}

	info := accessory.Info{
		Name:         cl.name,
		SerialNumber: fmt.Sprintf("color_light:%s:%s", cl.onDigitalOut.String(), cl.rgbwOut.String()),
	}
	cl.hk = accessory.NewColoredLightbulb(info)

	cl.fault = characteristic.NewStatusFault()
	cl.fault.SetValue(characteristic.StatusFaultNoFault)
	cl.hk.Lightbulb.AddC(cl.fault.C)

	cl.hk.Lightbulb.On.OnValueRemoteUpdate(cl.SetValue)
	cl.hk.Lightbulb.Brightness.OnValueRemoteUpdate(cl.updateBrightness)
	cl.hk.Lightbulb.Hue.OnValueRemoteUpdate(cl.updateHue)
	cl.hk.Lightbulb.Saturation.OnValueRemoteUpdate(cl.updateSaturation)

	cl.logger.Debug("homekit accessory initialized", "colorLight", cl.name)
	return cl.hk.A
}

// updateBrightness is called when Brightness state is changed by some kind of controller device
// brightness between 0 and 100
func (cl *ColorLight) updateBrightness(newBrightness int) {
	hue := cl.hk.Lightbulb.Hue.Value()
	saturation := cl.hk.Lightbulb.Saturation.Value()

	r, g, b, w := rgbToRgbw(hsvToRgb(hue, saturation, newBrightness))
	cl.rgbwOut.Set(r, g, b, w)
}

// updateHue is called when Hue state is changed by some kind of controller device
// hue in degrees 0° to 360°
func (cl *ColorLight) updateHue(newHue float64) {
	saturation := cl.hk.Lightbulb.Saturation.Value()
	brightness := cl.hk.Lightbulb.Brightness.Value()

	r, g, b, w := rgbToRgbw(hsvToRgb(newHue, saturation, brightness))
	cl.rgbwOut.Set(r, g, b, w)
}

// updateSaturation is called when Saturation state is changed by some kind of controller device
// saturation between 0° and 360°
func (cl *ColorLight) updateSaturation(newSaturation float64) {
	hue := cl.hk.Lightbulb.Hue.Value()
	brightness := cl.hk.Lightbulb.Brightness.Value()

	r, g, b, w := rgbToRgbw(hsvToRgb(hue, newSaturation, brightness))
	cl.rgbwOut.Set(r, g, b, w)
}

// Sync() is called periodically by swkit managing server to sync from drivers io
// If subscribe model is available and used this should be skipped
// If there is no homekit, there is no internal state - skip
func (cl *ColorLight) Sync(force bool) (err error) {
	if cl.hk == nil {
		return nil
	}

	cl.lock.Lock()
	defer cl.lock.Unlock()

	onState, onErr := cl.onDigitalOut.GetState()
	r, g, b, w, rgbwErr := cl.rgbwOut.GetState()
	// brighntess, briErr := cl.brightnessOut.GetState()
	var briErr error
	if onErr != nil || rgbwErr != nil || briErr != nil {
		err = errors.Join(errors.New("failed to get one of ColorLight io values: On, rgbw, Brighntess"), onErr, rgbwErr, briErr)
		cl.isFaulty = true
		cl.fault.SetValue(characteristic.StatusFaultGeneralFault)
		return
	}

	cl.isFaulty = false
	cl.fault.SetValue(characteristic.StatusFaultNoFault)

	cl.hk.Lightbulb.On.SetValue(onState)

	// briInMin, briInMax := cl.brightnessOut.GetMinMax()
	// briOut := convertIntRange(brighntess, briInMin, briInMax, 0, 100)

	h, s, v := rgbToHsv(rgbwToRgb(r, g, b, w))
	cl.hk.Lightbulb.Hue.SetValue(h)
	cl.hk.Lightbulb.Saturation.SetValue(s)
	cl.hk.Lightbulb.Brightness.SetValue(v)

	return nil
}

// GetState reports the current on/off state from the digital output.
func (cl *ColorLight) GetState() (bool, error) {
	return cl.onDigitalOut.GetState()
}

func (cl *ColorLight) SetValue(state bool) {
	cl.logger.Debug("setting color light value", "colorLight", cl.name, "state", state)
	cl.onDigitalOut.Set(state)
}

func (cl *ColorLight) Toggle() {
	currentState, err := cl.onDigitalOut.GetState()
	if err == nil {
		cl.logger.Debug("toggling color light", "colorLight", cl.name, "oldState", currentState, "newState", !currentState)
		cl.SetValue(!currentState)
	} else {
		cl.logger.Debug("toggle failed to get current state", "colorLight", cl.name, "err", err)
	}
}
