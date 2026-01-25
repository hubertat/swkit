package swkit

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/log"

	dnslog "github.com/brutella/dnssd/log"
	"github.com/brutella/hap"
	"github.com/brutella/hap/accessory"
	hklog "github.com/brutella/hap/log"

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
	Outlets     []OutletConfig
	Buttons     []ButtonConfig

	lights      []*Light
	colorLights []*ColorLight
	outlets     []*Outlet
	buttons     []*Button
	// Switches      []*Switch
	// MotionSensors []*MotionSensor

	HkPin       string
	HkDirectory string
	HkAddress   string
	HkDebug     bool

	SshServer *SshServerConfig `json:",omitempty"`

	Mcp23017   *drivers.McpIO
	Gpio       *drivers.GpIO
	Grenton    *drivers.GrentonIO
	FakeDriver *drivers.MockIoDriver
	Shelly     *drivers.ShellyIO
	Wago       *drivers.WagoIO

	ioDrivers  map[string]drivers.IoDriver
	mqttClient *mqtt.MqttClient
	ticker     *time.Ticker
	logger     *log.Logger
}

// SshServerConfig configures the SSH TUI server
type SshServerConfig struct {
	Enabled     bool
	Port        int    // default 2222
	HostKeyPath string // default ".ssh/swkit_host_key"
}

type Device interface {
	Sync(bool) error
	Name() string
}

type HkThing interface {
	InitHk() *accessory.A
	GetUniqueId() uint64
	Sync(bool) error
}

type Controllable interface {
	SetValue(value bool)
	Toggle()
	Name() string
}

func (sw *SwKit) getHkThings() (things []HkThing) {
	for _, th := range sw.lights {
		things = append(things, th)
	}

	for _, th := range sw.colorLights {
		things = append(things, th)
	}

	for _, th := range sw.outlets {
		things = append(things, th)
	}

	for _, th := range sw.buttons {
		things = append(things, th)
	}

	// for _, th := range sw.Buttons {
	// 	things = append(things, th)
	// }
	// for _, th := range sw.Switches {
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

	for _, d := range sw.outlets {
		devices = append(devices, d)
	}

	for _, d := range sw.buttons {
		devices = append(devices, d)
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

	for _, d := range sw.Outlets {
		allIds = append(allIds, d.DigitalOutName)
	}

	for _, b := range sw.Buttons {
		allIds = append(allIds, b.EventInputName)
	}

	return allIds
}

// getControllableDevices returns a slice of all controllable devices.
func (sw *SwKit) getControllableDevices() []Controllable {
	devices := []Controllable{}

	for _, li := range sw.lights {
		devices = append(devices, li)
	}

	for _, cl := range sw.colorLights {
		devices = append(devices, cl)
	}

	for _, d := range sw.outlets {
		devices = append(devices, d)
	}

	return devices
}

func (sw *SwKit) Setup(ctx context.Context, logger *log.Logger) error {
	sw.logger = logger
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

	if sw.Wago != nil {
		sw.ioDrivers[sw.Wago.String()] = sw.Wago
	}

	for _, driver := range sw.ioDrivers {
		ioSlice[driver.String()] = []string{}
	}

	for _, ioId := range sw.getAllIoIds() {
		driverId, _, _, e := drivers.ResolveIoIdString(ioId)
		if e != nil {
			return errors.Join(e, fmt.Errorf("got invalid io id: %s", ioId))
		}
		ios, driverPresent := ioSlice[driverId]
		if !driverPresent {
			return fmt.Errorf("failed during swkit Setup: found io id: %s, but driver (%s) is not present/configured", driverId, ioId)
		}
		ioSlice[driverId] = append(ios, ioId)
	}

	for _, driver := range sw.ioDrivers {
		ioSlice, present := ioSlice[driver.String()]
		logger.Debug("looking for driver", "driver", driver.String(), "present", present)
		if !present {
			return fmt.Errorf("failed during swkit Setup: io slice for driver (%s) is not present", driver.String())
		}
		err := driver.Setup(ctx, ioSlice)
		logger.Debug("setup the driver", "driver", driver.String(), "err", err)
		if err != nil {
			return errors.Join(err, fmt.Errorf("failed to setup %s driver", driver.String()))
		}
	}

	for _, light := range sw.Lights {
		ioName, driver, err := sw.getDriverAndNameForIo(light.DigitalOutName, drivers.IoTypeDigitalOutput)
		if err != nil {
			return errors.Join(err, fmt.Errorf("failed to get driver and name for io %s", light.DigitalOutName))
		}

		dOut, err := driver.GetDigitalOutput(ioName)
		if err != nil {
			return errors.Join(err, fmt.Errorf("failed to get digital output for light %s", light.Name))
		}

		sw.lights = append(sw.lights, NewLight(light, dOut, logger))
	}

	for _, outlet := range sw.Outlets {
		ioName, driver, err := sw.getDriverAndNameForIo(outlet.DigitalOutName, drivers.IoTypeDigitalOutput)
		if err != nil {
			return errors.Join(err, fmt.Errorf("failed to get driver and name for io %s", outlet.DigitalOutName))
		}

		dOut, err := driver.GetDigitalOutput(ioName)
		if err != nil {
			return errors.Join(err, fmt.Errorf("failed to get digital output for outlet %s", outlet.Name))
		}

		sw.outlets = append(sw.outlets, NewOutlet(outlet, dOut, logger))
	}

	for _, coloLight := range sw.ColorLights {
		ioName, driver, err := sw.getDriverAndNameForIo(coloLight.DigitalOutName, drivers.IoTypeDigitalOutput)
		if err != nil {
			return errors.Join(err, fmt.Errorf("failed to get driver and name for io %s", coloLight.DigitalOutName))
		}

		dOut, err := driver.GetDigitalOutput(ioName)
		if err != nil {
			return errors.Join(err, fmt.Errorf("failed to get digital output for color light %s", coloLight.Name))
		}

		ioName, driver, err = sw.getDriverAndNameForIo(coloLight.RgbwOutName, drivers.IoTypeRgbwOutput)
		if err != nil {
			return errors.Join(err, fmt.Errorf("failed to get driver and name for io %s", coloLight.RgbwOutName))
		}

		rgbw, err := driver.GetRgbwOutput(ioName)
		if err != nil {
			return errors.Join(err, fmt.Errorf("failed to get rgbw for color light %s", coloLight.Name))
		}

		sw.colorLights = append(sw.colorLights, NewColorLight(coloLight, dOut, rgbw, logger))
	}

	for _, button := range sw.Buttons {
		ioName, driver, err := sw.getDriverAndNameForIo(button.EventInputName, drivers.IoTypePushEventEmitter)
		if err != nil {
			return errors.Join(err, fmt.Errorf("failed to get driver and name for io %s", button.EventInputName))
		}

		eventEmitter, err := driver.GetPushEventEmitter(ioName)
		if err != nil {
			return errors.Join(err, fmt.Errorf("failed to get push event emitter for button %s", button.Name))
		}

		ctrlDevs := []ControlDevice{}

		for _, ctrlDevId := range button.ControlDevices {
			e, action, devName, err := ParseControlDeviceString(ctrlDevId)
			if err != nil {
				return errors.Join(err, fmt.Errorf("failed to parse control device string for button %s", button.Name))
			}

			ctrlDevFound := false
			for _, ctrlDev := range sw.getControllableDevices() {
				if ctrlDev.Name() == devName {
					ctrlDevs = append(ctrlDevs, ControlDevice{
						dev:    ctrlDev,
						e:      e,
						action: action,
					})
					ctrlDevFound = true
					break
				}
			}
			if !ctrlDevFound {
				return fmt.Errorf("control device (%s) not found", devName)
			}
		}

		sw.buttons = append(sw.buttons, NewButton(button, eventEmitter, ctrlDevs, logger))
	}

	return nil
}

func (sw *SwKit) getDriverAndNameForIo(ioIdString string, expectedType drivers.IoType) (string, drivers.IoDriver, error) {
	driverName, outType, ioName, err := drivers.ResolveIoIdString(ioIdString)
	if err != nil {
		return "", nil, errors.Join(errors.New("failed to resolve io id string: "+ioIdString), err)
	}

	if outType != expectedType {
		return "", nil, fmt.Errorf("invalid io type for digital output: %s, wanted: %s, got: %s", ioIdString, expectedType.String(), outType.String())
	}

	driver, driverPresent := sw.ioDrivers[driverName]
	if !driverPresent {
		return "", nil, fmt.Errorf("driver not found in ioDrivers slice for id: %s", driverName)
	}

	return ioName, driver, nil
}

func (sw *SwKit) StartTicker(ctx context.Context, interval time.Duration, forceEachCount int) {
	counter := 0
	sw.ticker = time.NewTicker(interval)
	defer sw.ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			sw.logger.Info("ticker stopped")
			return
		case <-sw.ticker.C:
			force := counter%forceEachCount == 0
			for _, io := range sw.getDevices() {
				err := io.Sync(force)
				if err != nil {
					sw.logger.Error("received error(s) from syncing io", "err", err)
				}
			}
			counter++
		}
	}
}

func (sw *SwKit) Close() (err error) {
	for _, driver := range sw.ioDrivers {
		if driver != nil {
			closeErr := driver.Close()
			if closeErr != nil {
				err = errors.Join(err, closeErr)
			}
		}
	}

	return
}

func (sw *SwKit) PrintIoStatus(writer io.Writer) {
	// Define styles
	headerStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("86")).
		MarginBottom(1)

	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("240")).
		Padding(0, 1)

	driverNameStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("39"))

	readyStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("42")).
		Bold(true)

	notReadyStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("196")).
		Bold(true)

	// Build content
	var lines []string
	for driverName, driver := range sw.ioDrivers {
		statusText := readyStyle.Render("Ready")
		if !driver.IsReady() {
			statusText = notReadyStyle.Render("Not Ready")
		}

		// Get status info if available
		statusInfo := ""
		if statusProvider, ok := driver.(DriverStatusProvider); ok {
			statusInfo = statusProvider.Status()
			if statusInfo != "" {
				statusInfo = "  " + lipgloss.NewStyle().Foreground(lipgloss.Color("245")).Render(statusInfo)
			}
		}

		line := fmt.Sprintf("%s  %s%s",
			driverNameStyle.Render(fmt.Sprintf("%-10s", driverName)),
			statusText,
			statusInfo,
		)
		lines = append(lines, line)
	}

	header := headerStyle.Render("Active IO Drivers")
	content := strings.Join(lines, "\n")
	box := boxStyle.Render(content)

	fmt.Fprintln(writer)
	fmt.Fprintln(writer, header)
	fmt.Fprintln(writer, box)
	fmt.Fprintln(writer)
}

// DriverStatusProvider is an optional interface for drivers to provide status info
type DriverStatusProvider interface {
	Status() string
}

func (sw *SwKit) StartHomeKit(ctx context.Context, firmwareVersion string) (cancel func(), errCh <-chan error, err error) {
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
		return nil, nil, errors.Join(err, errors.New("failed to create HomeKit server"))
	}
	hkServer.Pin = sw.HkPin
	if len(sw.HkAddress) > 0 {
		hkServer.Addr = sw.HkAddress
	}

	if sw.HkDebug {
		hklog.Debug.Enable()
		dnslog.Debug.Enable()
	}

	hkCtx, hkCancel := context.WithCancel(ctx)
	resultCh := make(chan error, 1)

	go func() {
		resultCh <- hkServer.ListenAndServe(hkCtx)
		close(resultCh)
	}()

	return hkCancel, resultCh, nil
}
