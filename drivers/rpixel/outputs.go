package rpixel

import (
	"errors"
	"time"

	"github.com/hubertat/swkit/drivers"
)

// The three IO views onto one device. None of them does I/O in GetState: they
// read the state cached from the last S or accepted command, and report an
// error while the device has never been seen or is unhealthy.

// digitalOut is rpixel|d_out|<name>: the ring on or off.
type digitalOut struct {
	d   *Driver
	dev *device
}

// analogOut is rpixel|a_out|<name>: the strip-global brightness, 0..255. It is
// independent of on/off: setting it while off only changes the brightness the
// next "on" uses, and turning off keeps it.
type analogOut struct {
	d   *Driver
	dev *device
}

// rgbwOut is rpixel|rgbw_out|<name>: the colour the ring shows when on.
// Setting a colour while off only remembers it for the next "on"; setting the
// zero colour turns the ring off but keeps the remembered colour.
type rgbwOut struct {
	d   *Driver
	dev *device
}

var (
	_ drivers.DigitalOutput = (*digitalOut)(nil)
	_ drivers.AnalogOutput  = (*analogOut)(nil)
	_ drivers.RgbwOutput    = (*rgbwOut)(nil)
)

func (o *digitalOut) GetState() (bool, error) {
	o.d.mu.RLock()
	defer o.d.mu.RUnlock()
	if err := o.d.stateErrLocked(o.dev, time.Now()); err != nil {
		return false, err
	}
	return o.dev.disp.on, nil
}

func (o *digitalOut) Set(on bool) error {
	return o.d.setOn(o.dev, on)
}

func (o *digitalOut) String() string {
	return drivers.GetIoIdString(DriverName, drivers.IoTypeDigitalOutput, o.dev.name)
}

// SetOnStateUpdate adds a callback fired, with no driver lock held, whenever
// the ring's on state changes: from a poll (for example after an HTTP
// /set/toggle on the device) or from an accepted command. Every registered
// callback is kept, so several consumers of one ring all get updates.
func (o *digitalOut) SetOnStateUpdate(f func(bool)) error {
	if f == nil {
		return errors.New("rpixel driver: onStateUpdate function cannot be nil")
	}
	o.d.mu.Lock()
	// Copy on write: notify iterates a snapshot outside the lock.
	cbs := make([]func(bool), len(o.dev.onStateUpdates), len(o.dev.onStateUpdates)+1)
	copy(cbs, o.dev.onStateUpdates)
	o.dev.onStateUpdates = append(cbs, f)
	o.d.mu.Unlock()
	return nil
}

func (o *digitalOut) IsHealthy() bool {
	return o.d.healthy(o.dev)
}

func (o *analogOut) GetMinMax() (int, int) {
	return 0, 255
}

func (o *analogOut) GetState() (int, error) {
	o.d.mu.RLock()
	defer o.d.mu.RUnlock()
	if err := o.d.stateErrLocked(o.dev, time.Now()); err != nil {
		return 0, err
	}
	return int(o.dev.disp.brightness), nil
}

func (o *analogOut) Set(value int) error {
	return o.d.setBrightness(o.dev, value)
}

func (o *analogOut) String() string {
	return drivers.GetIoIdString(DriverName, drivers.IoTypeAnalogOutput, o.dev.name)
}

func (o *analogOut) IsHealthy() bool {
	return o.d.healthy(o.dev)
}

// GetState reports the colour the ring shows when on, or will show at the next
// "on": the last unmixed non-zero colour seen or requested, white by default.
// A mixed display (rings showing different colours) reports that remembered
// colour rather than the zero colour S carries, per the spec's client guidance.
func (o *rgbwOut) GetState() (uint8, uint8, uint8, uint8, error) {
	o.d.mu.RLock()
	defer o.d.mu.RUnlock()
	if err := o.d.stateErrLocked(o.dev, time.Now()); err != nil {
		return 0, 0, 0, 0, err
	}
	c := o.dev.onColourLocked()
	return c.R, c.G, c.B, c.W, nil
}

func (o *rgbwOut) Set(r, g, b, w uint8) error {
	return o.d.setColour(o.dev, Colour{R: r, G: g, B: b, W: w})
}

func (o *rgbwOut) String() string {
	return drivers.GetIoIdString(DriverName, drivers.IoTypeRgbwOutput, o.dev.name)
}

func (o *rgbwOut) IsHealthy() bool {
	return o.d.healthy(o.dev)
}
