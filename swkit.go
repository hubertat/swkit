package main

import (
	"fmt"
	"io"
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
	Gpio     *GpIO

	drivers map[string]IoDriver
	ticker  *time.Ticker
}

type IO interface {
	Sync() error
	// GetHk() *accessory.Accessory
	Init(driver IoDriver) error
	GetDriverName() string
}

type ControllingDevice struct {
	Enable     bool
	Pin        uint8
	DriverName string
}

type Controllable interface {
	GetControllers() []ControllingDevice
	GetDriverName() string
	SetValue(value bool)
	Toggle()
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
		if sw.Gpio == nil {
			driver = &GpIO{}
		} else {
			driver = sw.Gpio
		}
	case "mcpio":
		if sw.Mcp23017 == nil {
			err = errors.New("cannot initialize Mcp23017 driver, config not present")
		} else {
			driver = sw.Mcp23017
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

func (sw *SwKit) findSwitch(pinNo uint8, driverName string) *Switch {
	for _, swb := range sw.Switches {
		if swb.InPin == pinNo && swb.DriverName == driverName {
			return swb
		}
	}

	return nil
}

func (sw *SwKit) findButton(pinNo uint8, driverName string) *Button {
	for _, but := range sw.Buttons {
		if but.InPin == pinNo && but.DriverName == driverName {
			return but
		}
	}

	return nil
}

func (sw *SwKit) MatchControllers() error {
	controllables := []Controllable{}

	for _, li := range sw.Lights {
		controllables = append(controllables, li)
	}

	for _, ou := range sw.Outlets {
		controllables = append(controllables, ou)
	}

	for _, controllable := range controllables {
		driverName := controllable.GetDriverName()
		for _, controller := range controllable.GetControllers() {
			if len(controller.DriverName) > 0 {
				driverName = controller.DriverName
			}
			_, driverReady := sw.drivers[driverName]
			if !driverReady {
				return errors.Errorf("matching controlled failed, driver (%s) not present or not ready", driverName)
			}

			swb := sw.findSwitch(controller.Pin, driverName)
			but := sw.findButton(controller.Pin, driverName)
			if swb == nil && but == nil {
				return errors.Errorf("matching controlled failed, no button or switch found with pin = %d and driver %s", controller.Pin, driverName)
			}

			if swb != nil {
				swb.switchThis = append(swb.switchThis, controllable)
			}

			if but != nil {
				but.toggleThis = append(but.toggleThis, controllable)
			}
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

func (sw *SwKit) Close() (errors []error) {
	for _, driver := range sw.drivers {
		err := driver.Close()
		if err != nil {
			errors = append(errors, err)
		}
	}

	return
}

func (sw *SwKit) PrintIoStatus(writer io.Writer) {
	fmt.Fprintln(writer, "=== active io drivers ===")
	for driverName, driver := range sw.drivers {
		fmt.Fprintln(writer, "________")
		fmt.Fprintf(writer, "| driver: %s\n", driverName)
		inputs, outputs := driver.GetAllIo()
		fmt.Fprintf(writer, "| in pins: ")
		for _, inpin := range inputs {
			fmt.Fprintf(writer, "%d, ", inpin)
		}
		fmt.Fprintf(writer, "\n| out pins: ")
		for _, outpin := range outputs {
			fmt.Fprintf(writer, "%d, ", outpin)
		}
		fmt.Fprintln(writer)
		fmt.Fprintln(writer, "--------")
	}
}
