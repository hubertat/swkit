package main

import (
	"strings"
	"time"

	"github.com/brutella/hc/accessory"
	"github.com/pkg/errors"
)

type SwKit struct {
	Lights   []*Light
	Buttons  []*Button
	Switches []*Switch
	Shutters []*Shutter
	Outlets  []*Outlet

	HkPin     string
	HkSetupId string

	Mcp23017 *McpIO

	drivers map[string]IoDriver
	ticker  *time.Ticker
}

type IO interface {
	Sync() error
	// GetHk() *accessory.Accessory
	Init(driver IoDriver) error
	GetDriverName() string
}

func (sw *SwKit) getInPins(driverName string) (pins []uint8) {
	for _, io := range sw.Buttons {
		if strings.EqualFold(io.DriverName, driverName) {
			pins = append(pins, io.InPin)
		}
	}
	for _, io := range sw.Switches {
		if strings.EqualFold(io.DriverName, driverName) {
			pins = append(pins, io.InPin)
		}
	}

	return
}

func (sw *SwKit) getOutPins(driverName string) (pins []uint8) {
	for _, io := range sw.Lights {
		if strings.EqualFold(io.DriverName, driverName) {
			pins = append(pins, io.OutPin)
		}
	}
	for _, io := range sw.Outlets {
		if strings.EqualFold(io.DriverName, driverName) {
			pins = append(pins, io.OutPin)
		}
	}

	return
}

func (sw *SwKit) getIoDriverByName(name string) (driver IoDriver, err error) {
	switch name {
	case "gpio":
		driver = &GpIO{}
	case "mcpio":
		if sw.Mcp23017 == nil {
			err = errors.New("cannot initialize Mcp23017 driver, config not present")
		} else {
			driver = &McpIO{DevNo: sw.Mcp23017.DevNo, BusNo: sw.Mcp23017.BusNo}
		}
	default:
		err = errors.Errorf("driver (%s) not found", name)
	}

	return
}

func (sw *SwKit) getIos() []IO {
	ios := []IO{}
	for _, li := range sw.Lights {
		ios = append(ios, li)
	}
	for _, li := range sw.Buttons {
		ios = append(ios, li)
	}
	for _, li := range sw.Switches {
		ios = append(ios, li)
	}
	for _, li := range sw.Outlets {
		ios = append(ios, li)
	}

	return ios
}

func (sw *SwKit) InitDrivers() error {
	sw.drivers = make(map[string]IoDriver)

	for _, io := range sw.getIos() {
		sw.drivers[io.GetDriverName()] = nil
	}

	for name := range sw.drivers {
		driver, err := sw.getIoDriverByName(name)
		if err != nil {
			return errors.Wrap(err, "Failed to get IoDriver")
		}
		err = driver.Setup(sw.getInPins(name), sw.getOutPins(name))
		if err != nil {
			return errors.Wrapf(err, "Failed to Setup %s driver", name)
		}
		sw.drivers[name] = driver
	}

	for _, io := range sw.getIos() {
		err := io.Init(sw.drivers[io.GetDriverName()])
		if err != nil {
			return err
		}
	}

	return nil
}

func (sw *SwKit) SyncAll() (errors []error) {

	for _, io := range sw.getIos() {
		errors = append(errors, io.Sync())
	}

	return
}

func (sw *SwKit) GetHkAccessories() (acc []*accessory.Accessory) {
	acc = []*accessory.Accessory{}

	for _, li := range sw.Lights {
		a := li.GetHk()
		if a != nil {
			acc = append(acc, a)
		}
	}
	for _, li := range sw.Buttons {
		a := li.GetHk()
		if a != nil {
			acc = append(acc, a)
		}

	}

	for _, shu := range sw.Shutters {
		hk := shu.GetHk()
		if hk != nil {
			acc = append(acc, hk)
		}
	}
	for _, ou := range sw.Outlets {
		hk := ou.GetHk()
		if hk != nil {
			acc = append(acc, hk)
		}
	}

	return
}

func (sw *SwKit) StartTicker(interval time.Duration) {

	sw.ticker = time.NewTicker(interval)

	for {
		select {
		case <-sw.ticker.C:
			{
				sw.SyncAll()
			}
		}
	}
}
