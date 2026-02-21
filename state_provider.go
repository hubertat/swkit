package swkit

import (
	"context"
	"fmt"
	"time"

	"github.com/hubertat/swkit/app"
	"github.com/hubertat/swkit/drivers"
)

// SwKitProvider implements app.StateProvider for SwKit
type SwKitProvider struct {
	sw *SwKit
}

// NewStateProvider creates a new state provider for the given SwKit instance
func NewStateProvider(sw *SwKit) *SwKitProvider {
	return &SwKitProvider{sw: sw}
}

// GetState returns the current application state snapshot
func (p *SwKitProvider) GetState() app.AppState {
	state := app.AppState{
		Name:      p.sw.Name,
		Timestamp: time.Now(),
	}

	// Collect driver states
	for name, driver := range p.sw.ioDrivers {
		ds := app.DriverState{
			Name:  name,
			Ready: driver.IsReady(),
		}

		// Get status info if available
		if statusProvider, ok := driver.(DriverStatusProvider); ok {
			ds.StatusInfo = statusProvider.Status()
		}

		state.Drivers = append(state.Drivers, ds)
	}

	// Collect device states
	for _, light := range p.sw.lights {
		ds := p.buildLightState(light)
		state.Devices = append(state.Devices, ds)
	}

	for _, colorLight := range p.sw.colorLights {
		ds := p.buildColorLightState(colorLight)
		state.Devices = append(state.Devices, ds)
	}

	for _, outlet := range p.sw.outlets {
		ds := p.buildOutletState(outlet)
		state.Devices = append(state.Devices, ds)
	}

	for _, button := range p.sw.buttons {
		ds := p.buildButtonState(button)
		state.Devices = append(state.Devices, ds)
	}

	// Collect IO debug data from drivers that support it (typed fields for stable order)
	collectIoDebug := func(driverName string, provider drivers.IoDebugProvider) {
		snapshot := provider.GetIoDebugSnapshot()
		for _, pt := range snapshot.Points {
			ioType := "input"
			if pt.Type == drivers.IoTypeDigitalOutput {
				ioType = "output"
			}
			state.IoDebug = append(state.IoDebug, app.IoPointDebugState{
				DriverName:  driverName,
				Index:       pt.Index,
				Name:        pt.Name,
				Type:        ioType,
				State:       pt.State,
				Healthy:     pt.Healthy,
				LastChanged: pt.LastChanged,
				LastEvent:   pt.LastEvent,
			})
		}
	}
	if p.sw.Wago != nil {
		collectIoDebug(p.sw.Wago.String(), p.sw.Wago)
	}
	if p.sw.Shelly != nil {
		collectIoDebug(p.sw.Shelly.String(), p.sw.Shelly)
	}

	// Collect HomeKit state
	state.HomeKit = app.HomeKitState{
		Enabled:     len(p.sw.HkPin) == 8,
		Pin:         p.sw.HkPin,
		Address:     p.sw.HkAddress,
		DeviceCount: len(p.sw.getHkThings()),
	}

	return state
}

// Subscribe returns a channel that receives state updates at the specified interval
func (p *SwKitProvider) Subscribe(ctx context.Context, interval time.Duration) <-chan app.AppState {
	ch := make(chan app.AppState)

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		defer close(ch)

		// Send initial state
		select {
		case ch <- p.GetState():
		case <-ctx.Done():
			return
		}

		for {
			select {
			case <-ticker.C:
				select {
				case ch <- p.GetState():
				case <-ctx.Done():
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	return ch
}

func (p *SwKitProvider) buildLightState(light *Light) app.DeviceState {
	isOn := false
	isHealthy := true
	outputIoId := ""

	if light.output != nil {
		if state, err := light.output.GetState(); err == nil {
			isOn = state
		}
		isHealthy = isOutputHealthy(light.output)
		outputIoId = light.output.String()
	}

	return app.DeviceState{
		Name:           light.name,
		Type:           app.DeviceTypeLight,
		IsOn:           isOn,
		IsHealthy:      isHealthy,
		IsFaulty:       light.isFaulty,
		HomeKitEnabled: !light.disableHomekit,
		OutputIoId:     outputIoId,
	}
}

func (p *SwKitProvider) buildColorLightState(cl *ColorLight) app.DeviceState {
	isOn := false
	isHealthy := true
	outputIoId := ""
	rgbwIoId := ""

	if cl.onDigitalOut != nil {
		if state, err := cl.onDigitalOut.GetState(); err == nil {
			isOn = state
		}
		isHealthy = isOutputHealthy(cl.onDigitalOut)
		outputIoId = cl.onDigitalOut.String()
	}

	if cl.rgbwOut != nil {
		rgbwIoId = cl.rgbwOut.String()
	}

	return app.DeviceState{
		Name:           cl.name,
		Type:           app.DeviceTypeColorLight,
		IsOn:           isOn,
		IsHealthy:      isHealthy,
		IsFaulty:       cl.isFaulty,
		HomeKitEnabled: !cl.disableHomekit,
		OutputIoId:     outputIoId,
		RgbwIoId:       rgbwIoId,
	}
}

func (p *SwKitProvider) buildOutletState(outlet *Outlet) app.DeviceState {
	isOn := false
	isHealthy := true
	outputIoId := ""

	if outlet.output != nil {
		if state, err := outlet.output.GetState(); err == nil {
			isOn = state
		}
		isHealthy = isOutputHealthy(outlet.output)
		outputIoId = outlet.output.String()
	}

	return app.DeviceState{
		Name:           outlet.name,
		Type:           app.DeviceTypeOutlet,
		IsOn:           isOn,
		IsHealthy:      isHealthy,
		IsFaulty:       outlet.isFaulty,
		HomeKitEnabled: !outlet.disableHomekit,
		OutputIoId:     outputIoId,
	}
}

func (p *SwKitProvider) buildButtonState(button *Button) app.DeviceState {
	isHealthy := true
	eventInputId := ""
	if button.emitter != nil {
		isHealthy = button.emitter.IsHealthy()
		eventInputId = button.emitter.String()
	}

	var relations []app.ButtonControlRelation
	for _, ctrl := range button.controlThis {
		action := ctrl.action
		if action == "" {
			action = "toggle"
		}
		relations = append(relations, app.ButtonControlRelation{
			EventType:  ctrl.e.String(),
			Action:     action,
			DeviceName: ctrl.dev.Name(),
		})
	}

	return app.DeviceState{
		Name:             button.name,
		Type:             app.DeviceTypeButton,
		IsOn:             false, // Buttons are stateless
		IsHealthy:        isHealthy,
		IsFaulty:         false, // Buttons don't track faulty state
		HomeKitEnabled:   !button.disableHomekit,
		EventInputId:     eventInputId,
		ControlRelations: relations,
	}
}

func isOutputHealthy(output drivers.DigitalOutput) bool {
	if output == nil {
		return false
	}
	return output.IsHealthy()
}

// ToggleDevice toggles the device at the given index
func (p *SwKitProvider) ToggleDevice(index int) app.ControlResult {
	device, deviceName, _, err := p.getControllableByIndex(index)
	if err != nil {
		return app.ControlResult{Error: err}
	}
	if device == nil {
		return app.ControlResult{
			DeviceName: deviceName,
			Action:     "toggle",
			Error:      fmt.Errorf("buttons cannot be toggled"),
		}
	}

	device.Toggle()

	// Get new state by reading from the device
	newState := p.getDeviceState(device)

	return app.ControlResult{
		DeviceName: device.Name(),
		Action:     "toggle",
		NewState:   newState,
	}
}

// SetDevice sets the device at the given index to the given state
func (p *SwKitProvider) SetDevice(index int, state bool) app.ControlResult {
	device, deviceName, _, err := p.getControllableByIndex(index)
	if err != nil {
		return app.ControlResult{Error: err}
	}
	if device == nil {
		return app.ControlResult{
			DeviceName: deviceName,
			Action:     actionName(state),
			Error:      fmt.Errorf("buttons cannot be controlled"),
		}
	}

	device.SetValue(state)

	return app.ControlResult{
		DeviceName: device.Name(),
		Action:     actionName(state),
		NewState:   state,
	}
}

// getControllableByIndex returns the Controllable device at the given index
// Device order matches GetState(): lights -> colorLights -> outlets -> buttons
// Returns nil Controllable for buttons since they don't implement the interface
func (p *SwKitProvider) getControllableByIndex(index int) (Controllable, string, app.DeviceType, error) {
	lightsCount := len(p.sw.lights)
	colorLightsCount := len(p.sw.colorLights)
	outletsCount := len(p.sw.outlets)
	buttonsCount := len(p.sw.buttons)

	if index < 0 || index >= lightsCount+colorLightsCount+outletsCount+buttonsCount {
		return nil, "", "", fmt.Errorf("device index %d out of range", index)
	}

	// Lights
	if index < lightsCount {
		return p.sw.lights[index], p.sw.lights[index].name, app.DeviceTypeLight, nil
	}
	index -= lightsCount

	// ColorLights
	if index < colorLightsCount {
		return p.sw.colorLights[index], p.sw.colorLights[index].name, app.DeviceTypeColorLight, nil
	}
	index -= colorLightsCount

	// Outlets
	if index < outletsCount {
		return p.sw.outlets[index], p.sw.outlets[index].name, app.DeviceTypeOutlet, nil
	}
	index -= outletsCount

	// Buttons - return nil for Controllable since they don't implement the interface
	if index < buttonsCount {
		return nil, p.sw.buttons[index].name, app.DeviceTypeButton, nil
	}

	return nil, "", "", fmt.Errorf("device index %d out of range", index)
}

// getDeviceState reads the current on/off state from the device
func (p *SwKitProvider) getDeviceState(device Controllable) bool {
	switch d := device.(type) {
	case *Light:
		if d.output != nil {
			if state, err := d.output.GetState(); err == nil {
				return state
			}
		}
	case *ColorLight:
		if d.onDigitalOut != nil {
			if state, err := d.onDigitalOut.GetState(); err == nil {
				return state
			}
		}
	case *Outlet:
		if d.output != nil {
			if state, err := d.output.GetState(); err == nil {
				return state
			}
		}
	}
	return false
}

func actionName(state bool) string {
	if state {
		return "on"
	}
	return "off"
}

// ToggleIoOutput toggles a raw IO output by driver name and output index
func (p *SwKitProvider) ToggleIoOutput(driverName string, outputIndex int) error {
	driver, ok := p.sw.ioDrivers[driverName]
	if !ok {
		return fmt.Errorf("driver %q not found", driverName)
	}
	toggler, ok := driver.(drivers.IoOutputToggler)
	if !ok {
		return fmt.Errorf("driver %q does not support output toggling", driverName)
	}
	return toggler.ToggleOutput(outputIndex)
}
