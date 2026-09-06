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

	dnslog "github.com/brutella/dnssd/log"
	"github.com/brutella/hap"
	"github.com/brutella/hap/accessory"
	hklog "github.com/brutella/hap/log"
	"github.com/pkg/errors"

	drivers "github.com/hubertat/swkit/drivers"
)

const defaultHomeKitDirectory = "./homekit"
const homeKitBridgeName = "swkit"
const homeKitBridgeAuthor = "github.com/hubertat"

type SwKit struct {
	Name string

	Lights             []*Light
	Buttons            []*Button
	Switches           []*Switch
	Shutters           []*Shutter
	Outlets            []*Outlet
	Thermostats        []*Thermostat
	MotionSensors      []*MotionSensor
	TemperatureSensors []*TemperatureSensor

	HkPin       string
	HkDirectory string
	HkAddress   string
	HkDebug     bool

	Mcp23017      *drivers.McpIO
	Gpio          *drivers.GpIO
	Grenton       *drivers.GrentonIO
	FakeDriver    *drivers.MockIoDriver
	RemoteIoSlave *drivers.RemoteIoSlave
	Shelly        *drivers.ShellyIO

	InfluxSensors *drivers.InfluxSensors
	WireSensors   *drivers.Wire

	ioDrivers     map[string]drivers.IoDriver
	sensorDrivers map[string]drivers.SensorDriver
	ticker        *time.Ticker
	sensorsTicker *time.Ticker
}

type IO interface {
	Init(driver drivers.IoDriver) error
	GetDriverName() string
	Sync() error
}

type Sensor interface {
	Init(driver drivers.SensorDriver) error
	GetDriverName() string
	Sync() error
}

type HkThing interface {
	GetHk() *accessory.A
	GetUniqueId() uint64
	Sync() error
}

type ControllingDevice struct {
	Pin        uint16
	DriverName string
	Event      int
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
	for _, io := range sw.MotionSensors {
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

func (sw *SwKit) getTemperatureSensors(driverName string) (tss []drivers.TemperatureSensor) {
	for _, ts := range sw.TemperatureSensors {
		if strings.EqualFold(driverName, ts.DriverName) {
			tss = append(tss, ts)
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
	case "remoteio_slave":
		if sw.RemoteIoSlave == nil {
			err = errors.New("cannot initialize RemoteIOSlave driver, not configured")
		} else {
			driver = sw.RemoteIoSlave
		}
	case "shelly":
		if sw.Shelly == nil {
			err = errors.New("cannot initialize Shelly driver, not configured")
		} else {
			driver = sw.Shelly
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
	for _, mosens := range sw.MotionSensors {
		ios = append(ios, mosens)
	}

	return ios
}

func (sw *SwKit) getSensors() (sensors []Sensor) {
	for _, s := range sw.TemperatureSensors {
		sensors = append(sensors, s)
	}
	return
}

func (sw *SwKit) getHkThings() (things []HkThing) {
	for _, th := range sw.Lights {
		things = append(things, th)
	}
	for _, th := range sw.Buttons {
		things = append(things, th)
	}
	for _, th := range sw.Switches {
		things = append(things, th)
	}
	for _, th := range sw.Outlets {
		things = append(things, th)
	}
	for _, th := range sw.Thermostats {
		things = append(things, th)
	}
	for _, th := range sw.TemperatureSensors {
		things = append(things, th)
	}
	for _, th := range sw.MotionSensors {
		things = append(things, th)
	}

	return
}

func (sw *SwKit) InitDrivers(ctx context.Context) error {
	sw.ioDrivers = make(map[string]drivers.IoDriver)
	for _, io := range sw.getIos() {
		sw.ioDrivers[io.GetDriverName()] = nil
	}

	sw.sensorDrivers = make(map[string]drivers.SensorDriver)
	for _, s := range sw.getSensors() {
		sw.sensorDrivers[s.GetDriverName()] = nil
	}

	for ioDriverName := range sw.ioDrivers {
		ioDriver, err := sw.getIoDriverByName(ioDriverName)
		if err != nil {
			return errors.Wrapf(err, "failed initilaizing drivers: failed to get %s io driver by name", ioDriverName)
		}
		err = ioDriver.Setup(ctx, sw.getInPins(ioDriverName), sw.getOutPins(ioDriverName))
		if err != nil {
			return errors.Wrapf(err, "got error with setup for %s driver", ioDriverName)
		}
		sw.ioDrivers[ioDriverName] = ioDriver
	}

	for sensorDriverName := range sw.sensorDrivers {
		sensorDriver, err := sw.getSensorDriverByName(sensorDriverName)
		if err != nil {
			return errors.Wrapf(err, "failed initializing drivers: failed to get %s sensor driver by name", sensorDriverName)
		}
		err = sensorDriver.Setup(sw.getTemperatureSensors(sensorDriverName))
		if err != nil {
			return errors.Wrapf(err, "got error with setup %s sensor driver", sensorDriverName)
		}
		sw.sensorDrivers[sensorDriverName] = sensorDriver
	}

	return nil
}

func (sw *SwKit) InitIos() error {
	for _, io := range sw.getIos() {
		err := io.Init(sw.ioDrivers[io.GetDriverName()])
		if err != nil {
			return errors.Wrapf(err, "failed to init io")
		}
	}

	return nil
}

func (sw *SwKit) InitSensors() error {
	for _, s := range sw.getSensors() {
		err := s.Init(sw.sensorDrivers[s.GetDriverName()])
		if err != nil {
			return errors.Wrap(err, "faied to init sensor")
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
			_, driverReady := sw.ioDrivers[driverName]
			if !driverReady {
				return errors.Errorf("matching controlled failed, driver (%s) not present or not ready", driverName)
			}

			log.Println("| match ctrl | got controller driver: ", controller.DriverName, " pin: ", controller.Pin, " event: ", controller.Event)

			swb := sw.findSwitch(controller.Pin, driverName)
			but := sw.findButton(controller.Pin, driverName)
			if swb == nil && but == nil {
				return errors.Errorf("matching controlled failed, no button or switch found with pin = %d and driver %s", controller.Pin, driverName)
			}

			if swb != nil {
				swb.switchSlice = append(swb.switchSlice, controllable)

				log.Println("| match ctrl | matched to switch (driver: ", swb.DriverName, " pin: ", swb.InPin, ")")
			}

			if but != nil {
				event := drivers.PushEvent(controller.Event)
				toggleMap, exist := but.toggleMap[event]
				if !exist {
					toggleMap = []ClickableDevice{}
				}
				toggleMap = append(toggleMap, controllable)
				but.toggleMap[event] = toggleMap

				log.Println("| match ctrl | matched to button (driver: ", but.DriverName, " pin: ", but.InPin, ")")
			}
		}
	}

	return nil
}

func (sw *SwKit) GetHkAccessories(firmwareVersion string) (acc []*accessory.A) {
	acc = []*accessory.A{}

	for _, th := range sw.getHkThings() {
		accessory := th.GetHk()
		if accessory != nil {
			if accessory.Info != nil && accessory.Info.FirmwareRevision != nil {
				accessory.Info.FirmwareRevision.SetValue(firmwareVersion)
			}
			accessory.Id = th.GetUniqueId()
			acc = append(acc, accessory)
		}
	}

	return
}

func (sw *SwKit) getSensorDriverByName(name string) (driver drivers.SensorDriver, err error) {
	switch name {
	case "wire":
		if sw.WireSensors == nil {
			err = errors.Errorf("cannot initialize wire sensor driver, it is not configured")
		} else {
			driver = sw.WireSensors
		}
	case "influx_sensors":
		if sw.InfluxSensors == nil {
			err = errors.Errorf("cannot get influx sensor driver, it is not configured")
		} else {
			driver = sw.InfluxSensors
		}
	default:
		err = errors.Errorf("sensor driver (%s) not found", name)
	}

	return
}

func (sw *SwKit) findTemperatureSensor(id string) (temp drivers.TemperatureSensor, err error) {

	for _, driver := range sw.sensorDrivers {
		temp, err = driver.FindTemperatureSensor(id)
		if err == nil {
			return
		}
	}
	err = errors.Wrapf(err, "temperature sensor id = %s not found", id)
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

func (sw *SwKit) StartTicker(interval time.Duration) {

	sw.ticker = time.NewTicker(interval)

	for {
		select {
		case <-sw.ticker.C:
			{
				for _, io := range sw.getIos() {
					err := io.Sync()
					if err != nil {
						log.Printf("Received error(s) from syncing io:\n%v", err)
					}
				}
			}
		}
	}
}

func (sw *SwKit) syncSensorDriversAndSensors() {
	for sDName, sD := range sw.sensorDrivers {
		err := sD.Sync()
		if err != nil {
			log.Printf("receieved error when syncing %s sensor driver: %v", sDName, err)
		}
	}
	for _, s := range sw.getSensors() {
		err := s.Sync()
		if err != nil {
			log.Printf("received error when syncing sensor: %v", err)
		}
	}
}

func (sw *SwKit) StartSensorTicker(interval time.Duration) {
	sw.syncSensorDriversAndSensors()

	sw.sensorsTicker = time.NewTicker(interval)

	for {
		select {
		case <-sw.sensorsTicker.C:
			sw.syncSensorDriversAndSensors()
		}
	}
}

func (sw *SwKit) Close() (err error) {
	for _, driver := range sw.ioDrivers {
		if driver != nil {
			closeErr := driver.Close()
			if closeErr != nil {
				err = errors.Wrap(err, closeErr.Error())
			}
		}
	}

	for _, sDriver := range sw.sensorDrivers {
		if sDriver != nil {
			closeErr := sDriver.Close()
			if closeErr != nil {
				err = errors.Wrap(err, closeErr.Error())
			}
		}
	}

	return
}

func (sw *SwKit) PrintIoStatus(writer io.Writer) {
	fmt.Fprintln(writer)
	fmt.Fprintln(writer, "=== active io drivers ===")
	for driverName, driver := range sw.ioDrivers {
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
	for sDriverName, sDriver := range sw.sensorDrivers {
		fmt.Fprintln(writer, "________")
		fmt.Fprintf(writer, "| sensor driver: %s\n", sDriverName)
		fmt.Fprintf(writer, "|\tready?: %v\n", sDriver.IsReady())
		fmt.Fprintf(writer, "|\tsensor count: ?\n")
		fmt.Fprintln(writer)
		fmt.Fprintln(writer, "--------")
	}
	fmt.Fprintln(writer, "-----------------------------")
	fmt.Fprintln(writer)
}

func (sw *SwKit) StartHomeKit(ctx context.Context, firmwareVersion string) error {
	hkName := sw.Name
	if len(hkName) < 1 {
		hkName = homeKitBridgeName
	}
	bridge := accessory.NewBridge(accessory.Info{
		Name:         hkName,
		Manufacturer: homeKitBridgeAuthor,
		Firmware:     firmwareVersion,
	})

	var store hap.Store
	if len(sw.HkDirectory) > 1 {
		store = hap.NewFsStore(sw.HkDirectory)
	} else {
		store = hap.NewFsStore(defaultHomeKitDirectory)
	}
	hkServer, err := hap.NewServer(store, bridge.A, sw.GetHkAccessories(firmwareVersion)...)
	if err != nil {
		return errors.Wrap(err, "failed to create HomeKit server")
	}
	hkServer.Pin = sw.HkPin
	if len(sw.HkAddress) > 0 {
		hkServer.Addr = sw.HkAddress
	}

	if sw.HkDebug {
		hklog.Debug.Enable()
		dnslog.Debug.Enable()
	}

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
