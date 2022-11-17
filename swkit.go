package swkit

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/brutella/hap"
	"github.com/brutella/hap/accessory"
	"github.com/pkg/errors"

	drivers "github.com/hubertat/swkit/drivers"
)

const defaultHomeKitDirectory = "./homekit"
const homeKitBridgeName = "swkit"
const homeKitBridgeAuthor = "github.com/hubertat"

type SwKit struct {
	Lights      []*Light
	Buttons     []*Button
	Switches    []*Switch
	Shutters    []*Shutter
	Outlets     []*Outlet
	Thermostats []*Thermostat

	HkPin       string
	HkDirectory string

	Mcp23017   *drivers.McpIO
	Gpio       *drivers.GpIO
	Grenton    *drivers.GrentonIO
	FakeDriver *drivers.MockIoDriver

	InfluxSensors *drivers.InfluxSensors
	WireSensors   *drivers.Wire

	drivers       map[string]drivers.IoDriver
	ticker        *time.Ticker
	sensorsTicker *time.Ticker
}

type IO interface {
	Sync() error
	GetHk() *accessory.A
	Init(driver drivers.IoDriver) error
	GetDriverName() string
}

type ControllingDevice struct {
	Enable     bool
	Pin        uint16
	DriverName string
}

type Controllable interface {
	GetControllers() []ControllingDevice
	GetDriverName() string
	SetValue(value bool)
	Toggle()
}

func (sw *SwKit) getInPins(driverName string) (pins []uint16) {
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

func (sw *SwKit) getOutPins(driverName string) (pins []uint16) {
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
	for _, th := range sw.Thermostats {
		if strings.EqualFold(th.DriverName, driverName) {
			pins = append(pins, th.HeatPin)
			if th.CoolingEnabled {
				pins = append(pins, th.CoolPin)
			}
		}
	}

	return
}

func (sw *SwKit) getIoDriverByName(name string) (driver drivers.IoDriver, err error) {
	switch name {
	case "gpio":
		if sw.Gpio == nil {
			driver = &drivers.GpIO{}
		} else {
			driver = sw.Gpio
		}
	case "mcpio":
		if sw.Mcp23017 == nil {
			err = errors.New("cannot initialize Mcp23017 driver, config not present")
		} else {
			driver = sw.Mcp23017
		}
	case "grenton":
		if sw.Grenton == nil {
			err = errors.Errorf("cannot initialize GrentonIO driver, config not present")
		} else {
			driver = sw.Grenton
		}
	case "mock_driver":
		if sw.FakeDriver == nil {
			err = errors.Errorf("cannot initialize mock (fake) driver, wasn't configured")
		} else {
			driver = sw.FakeDriver
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
	for _, thermo := range sw.Thermostats {
		ios = append(ios, thermo)
	}

	return ios
}

func (sw *SwKit) InitDrivers() error {
	sw.drivers = make(map[string]drivers.IoDriver)

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

func (sw *SwKit) findSwitch(pinNo uint16, driverName string) *Switch {
	for _, swb := range sw.Switches {
		if swb.InPin == pinNo && swb.DriverName == driverName {
			return swb
		}
	}

	return nil
}

func (sw *SwKit) findButton(pinNo uint16, driverName string) *Button {
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

func (sw *SwKit) GetHkAccessories() (acc []*accessory.A) {
	acc = []*accessory.A{}

	for _, io := range sw.getIos() {
		accessory := io.GetHk()
		if accessory != nil {
			acc = append(acc, accessory)
		}
	}

	return
}

func (sw *SwKit) GetSensorDrivers() (drivers []drivers.SensorDriver) {
	if sw.InfluxSensors != nil {
		drivers = append(drivers, sw.InfluxSensors)
	}
	if sw.WireSensors != nil {
		drivers = append(drivers, sw.WireSensors)
	}

	return
}

func (sw *SwKit) findTemperatureSensor(id string) (temp drivers.TemperatureSensor, err error) {
	var foundErr error
	drivers := sw.GetSensorDrivers()
	if len(drivers) == 0 {
		err = errors.Errorf("temperature sensor (id = %s) can't be found, there are no sensor drivers present or ready", id)
		return
	}
	for _, driver := range drivers {
		if driver.IsReady() {
			temp, foundErr = driver.FindSensor(id)
			if foundErr == nil {
				return
			}
		}
	}
	err = errors.Errorf("temperature sensor id = %s not found", id)
	return
}

func (sw *SwKit) MatchSensors() error {
	for _, thermo := range sw.Thermostats {
		thermoFound, err := sw.findTemperatureSensor(thermo.SensorId)
		if err != nil {
			return errors.Wrap(err, "MatchSensors failed")
		}
		thermo.temperatureSensor = thermoFound
	}
	return nil
}

func (sw *SwKit) SyncSensors() (err error) {
	if sw.InfluxSensors != nil {
		err = sw.InfluxSensors.Sync()
	}

	return
}

func (sw *SwKit) StartTicker(interval time.Duration, sensorsInterval time.Duration) {

	sw.ticker = time.NewTicker(interval)
	sw.sensorsTicker = time.NewTicker(sensorsInterval)
	sw.SyncSensors()

	for {
		select {
		case <-sw.sensorsTicker.C:
			err := sw.SyncSensors()
			if err != nil {
				log.Printf("Received error from syncing sensors:\n%v", err)
			}
		case <-sw.ticker.C:
			{
				for _, err := range sw.SyncAll() {
					if err != nil {
						log.Printf("Received error(s) from syncing sensors:\n%v", err)
					}
				}
			}
		}
	}
}

func (sw *SwKit) Close() (errors []error) {
	for _, driver := range sw.drivers {
		if driver != nil {
			err := driver.Close()
			if err != nil {
				errors = append(errors, err)
			}
		}
	}

	return
}

func (sw *SwKit) PrintIoStatus(writer io.Writer) {
	fmt.Fprintln(writer)
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
	fmt.Fprintln(writer, "-----------------------------")
	fmt.Fprintln(writer)
	fmt.Fprintln(writer, "=== active sensor drivers ===")
	for _, sDriver := range sw.GetSensorDrivers() {
		fmt.Fprintln(writer, "________")
		fmt.Fprintf(writer, "| sensor driver: %s\n", sDriver.Name())
		fmt.Fprintf(writer, "|\tready?: %v\n", sDriver.IsReady())
		fmt.Fprintln(writer)
		fmt.Fprintln(writer, "--------")
	}
	fmt.Fprintln(writer, "-----------------------------")
	fmt.Fprintln(writer)
}

func (sw *SwKit) StartHomeKit(ctx context.Context) error {
	bridge := accessory.NewBridge(accessory.Info{
		Name:         homeKitBridgeName,
		Manufacturer: homeKitBridgeAuthor,
	})

	var store hap.Store
	if len(sw.HkDirectory) > 1 {
		store = hap.NewFsStore(sw.HkDirectory)
	} else {
		store = hap.NewFsStore(defaultHomeKitDirectory)
	}

	hkServer, err := hap.NewServer(store, bridge.A, sw.GetHkAccessories()...)
	if err != nil {
		return errors.Wrap(err, "failed to create HomeKit server")
	}
	hkServer.Pin = sw.HkPin

	c := make(chan os.Signal)
	signal.Notify(c, os.Interrupt)
	signal.Notify(c, syscall.SIGTERM)

	ctx, cancel := context.WithCancel(ctx)
	go func() {
		<-c
		// Stop delivering signals.
		signal.Stop(c)
		// Cancel the context to stop the server.
		cancel()
	}()

	return hkServer.ListenAndServe(ctx)
}
