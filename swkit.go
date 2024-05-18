package swkit

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/log"

	dnslog "github.com/brutella/dnssd/log"
	"github.com/brutella/hap"
	"github.com/brutella/hap/accessory"
	hklog "github.com/brutella/hap/log"
	"github.com/pkg/errors"

	"github.com/hubertat/swkit/drivers"
	"github.com/hubertat/swkit/mqtt"
)

const defaultHomeKitDirectory = "./homekit"
const homeKitBridgeName = "swkit"
const homeKitBridgeAuthor = "github.com/hubertat"

type SwKit struct {
	Name string

	Lights        []*Light
	Buttons       []*Button
	Switches      []*Switch
	Outlets       []*Outlet
	MotionSensors []*MotionSensor

	HkPin       string
	HkDirectory string
	HkAddress   string
	HkDebug     bool

	MqttBroker string

	Mcp23017   *drivers.McpIO
	Gpio       *drivers.GpIO
	Grenton    *drivers.GrentonIO
	FakeDriver *drivers.MockIoDriver
	Shelly     *drivers.ShellyIO

	ioDrivers  map[string]drivers.IoDriver
	mqttClient *mqtt.MqttClient
	ticker     *time.Ticker
}

type IO interface {
	Init(driver drivers.IoDriver) error
	GetDriverName() string
	Sync() error
}

type HkThing interface {
	GetHk() *accessory.A
	GetUniqueId() uint64
	Sync() error
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
	for _, mosens := range sw.MotionSensors {
		ios = append(ios, mosens)
	}
	for _, but := range sw.Buttons {
		ios = append(ios, but)
	}

	return ios
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
	for _, th := range sw.MotionSensors {
		things = append(things, th)
	}

	return
}

func (sw *SwKit) InitDrivers(ctx context.Context) error {
	sw.ioDrivers = make(map[string]drivers.IoDriver)

	if sw.Gpio != nil {
		sw.ioDrivers[sw.Gpio.String()] = sw.Gpio
	}

	if sw.Mcp23017 != nil {
		sw.ioDrivers[sw.Mcp23017.String()] = sw.Mcp23017
	}

	if sw.Grenton != nil {
		sw.ioDrivers[sw.Grenton.String()] = sw.Grenton
	}

	if sw.FakeDriver != nil {
		sw.ioDrivers[sw.FakeDriver.String()] = sw.FakeDriver
	}

	if sw.Shelly != nil {
		sw.ioDrivers[sw.Shelly.String()] = sw.Shelly
	}

	for _, driver := range sw.ioDrivers {
		err := driver.Setup(ctx, sw.getInPins(driver.String()), sw.getOutPins(driver.String()))
		if err != nil {
			return errors.Wrapf(err, "failed to setup %s driver", driver)
		}
	}

	for _, io := range sw.getIos() {
		_, driverFound := sw.ioDrivers[io.GetDriverName()]
		if !driverFound {
			return errors.Errorf("driver %s not set up", io.GetDriverName())
		}
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

func (sw *SwKit) Close() (err error) {
	for _, driver := range sw.ioDrivers {
		if driver != nil {
			closeErr := driver.Close()
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

func (sw *SwKit) InitMqtt() (err error) {
	if len(sw.MqttBroker) == 0 {
		err = errors.New("mqtt broker not set")
		return
	}

	mc, err := mqtt.NewMqttClient(sw.MqttBroker, sw.Name)
	if err != nil {
		err = errors.Wrap(err, "failed to create mqtt client")
		return
	}

	sw.mqttClient = mc

	mqttHandlers := []mqtt.MqttHandler{}
	for _, driver := range sw.ioDrivers {
		mqttHandlers = append(mqttHandlers, driver.SetMqtt(mc)...)
	}

	err = mc.Connect(mqttHandlers)
	if err != nil {
		err = errors.Wrap(err, "failed to connect to mqtt broker")
	}

	return
}
