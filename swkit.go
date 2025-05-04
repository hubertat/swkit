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

	Lights      []LightConfig
	ColorLights []ColorLightConfig

	lights      []*Light
	colorLights []*ColorLight
	// Buttons       []*Button
	// Switches      []*Switch
	// Outlets       []*Outlet
	// MotionSensors []*MotionSensor

	HkPin       string
	HkDirectory string
	HkAddress   string
	HkDebug     bool

	Mcp23017   *drivers.McpIO
	Gpio       *drivers.GpIO
	Grenton    *drivers.GrentonIO
	FakeDriver *drivers.MockIoDriver
	Shelly     *drivers.ShellyIO

	ioDrivers  map[string]drivers.IoDriver
	mqttClient *mqtt.MqttClient
	ticker     *time.Ticker
}

type Device interface {
	Sync() error
}

type HkThing interface {
	InitHk() *accessory.A
	GetUniqueId() uint64
	Sync() error
}

type ControllingDevice struct {
	Enable     bool
	IoName     string
	DriverName string
}

type Controllable interface {
	GetControllers() []ControllingDevice
	GetDriverName() string
	SetValue(value bool)
	Toggle()
}

func (sw *SwKit) getHkThings() (things []HkThing) {
	for _, th := range sw.lights {
		things = append(things, th)
	}

	for _, th := range sw.colorLights {
		things = append(things, th)
	}

	// for _, th := range sw.Buttons {
	// 	things = append(things, th)
	// }
	// for _, th := range sw.Switches {
	// 	things = append(things, th)
	// }
	// for _, th := range sw.Outlets {
	// 	things = append(things, th)
	// }
	// for _, th := range sw.MotionSensors {
	// 	things = append(things, th)
	// }

	return
}

func (sw *SwKit) getDevices() (devices []Device) {
	for _, li := range sw.lights {
		devices = append(devices, li)
	}

	for _, cl := range sw.colorLights {
		devices = append(devices, cl)
	}

	return
}

func (sw *SwKit) getAllIoIds() []string {
	allIds := []string{}

	for _, liConf := range sw.Lights {
		allIds = append(allIds, liConf.DigitalOutName)
	}

	for _, clConf := range sw.ColorLights {
		allIds = append(allIds, clConf.DigitalOutName)
		allIds = append(allIds, clConf.RgbwOutName)
	}
}

func (sw *SwKit) getDriverForIo(ioId string) (driver, error) {
	ioIdSlice := strings.Split(ioId, "|")
	if len(ioIdSlice) != 3 {
		return nil, errors.Errorf("got invalid io id: %s", ioId)
	}
	driverId := ioIdSlice[0]
	driver, driverPresent := sw.ioDrivers[driverId]
	if !driverPresent {
		return nil, errors.Errorf("driver not found for id: %s", driverId)
	}
	return driver, nil
}

func (sw *SwKit) Setup(ctx context.Context) error {
	sw.ioDrivers = make(map[string]drivers.IoDriver)
	ioSlice := map[string][]string{}

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
		ioSlice[driver.String()] = []string{}
	}

	for _, ioId := range sw.getAllIoIds() {
		ioIdSlice := strings.Split(ioId, "|")
		if len(ioIdSlice) != 3 {
			return errors.Errorf("got invalid io id: %s", ioId)
		}
		driverId := ioIdSlice[0]
		ios, driverPresent := ioSlice[driverId]
		if !driverPresent {
			return errors.Errorf("failed during swkit Setup: found io id: %s, but driver (%s) is not present/configured", driverId, ioId)
		}
		ioSlice[driverId] = append(ios, ioId)
	}

	for _, driver := range sw.ioDrivers {
		ioSlice, present := ioSlice[driver.String()]
		if !present {
			return errors.Errorf("failed during swkit Setup: io slice for driver (%s) is not present", driver.String())
		}
		err := driver.Setup(ctx, ioSlice)
		if err != nil {
			return errors.Wrapf(err, "failed to setup %s driver", driver)
		}
	}

	for _, light := range sw.Lights {
		driver, err := sw.getDriverForIo(light.DigitalOutName)
		if err != nil {
			return errors.Wrapf(err, "failed to get driver for light %s", light.Name)
		}

		dOut, err := driver.GetDigitalOutput(light.DigitalOutName)
		if err != nil {
			return errors.Wrapf(err, "failed to get digital output for light %s", light.Name)
		}

		sw.lights = append(sw.lights, NewLight(light, dOut))
	}

	for _, coloLight := range sw.ColorLights {
		dOutDriver, err := sw.getDriverForIo(coloLight.DigitalOutName)
		if err != nil {
			return errors.Wrapf(err, "failed to get driver for color light %s", coloLight.Name)
		}

		dOut, err := dOutDriver.GetDigitalOutput(coloLight.DigitalOutName)
		if err != nil {
			return errors.Wrapf(err, "failed to get digital output for color light %s", coloLight.Name)
		}

		rgbwDriver, err := sw.getDriverForIo(coloLight.RgbwOutName)
		if err != nil {
			return errors.Wrapf(err, "failed to get driver for color light %s", coloLight.Name)
		}

		rgbw, err := rgbwDriver.GetRgbw(coloLight.RgbwOutName)
		if err != nil {
			return errors.Wrapf(err, "failed to get rgbw for color light %s", coloLight.Name)
		}

		sw.colorLights = append(sw.colorLights, NewColorLight(coloLight, dOut, rgbw))
	}

	return nil
}

func (sw *SwKit) StartTicker(interval time.Duration) {

	sw.ticker = time.NewTicker(interval)

	for {
		select {
		case <-sw.ticker.C:
			{
				for _, io := range sw.getDevices() {
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
	acc := []*accessory.A{}

	for _, th := range sw.getHkThings() {
		accessory := th.InitHk()
		if accessory != nil {
			accessory.Id = th.GetUniqueId()
			acc = append(acc, accessory)
		}
	}

	if len(sw.HkDirectory) > 1 {
		store = hap.NewFsStore(sw.HkDirectory)
	} else {
		store = hap.NewFsStore(defaultHomeKitDirectory)
	}
	hkServer, err := hap.NewServer(store, bridge.A, acc...)
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
