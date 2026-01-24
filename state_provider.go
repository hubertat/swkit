package swkit

import (
	"context"
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

	if light.output != nil {
		if state, err := light.output.GetState(); err == nil {
			isOn = state
		}
		isHealthy = isOutputHealthy(light.output)
	}

	return app.DeviceState{
		Name:           light.name,
		Type:           app.DeviceTypeLight,
		IsOn:           isOn,
		IsHealthy:      isHealthy,
		IsFaulty:       light.isFaulty,
		HomeKitEnabled: !light.disableHomekit,
	}
}

func (p *SwKitProvider) buildColorLightState(cl *ColorLight) app.DeviceState {
	isOn := false
	isHealthy := true

	if cl.onDigitalOut != nil {
		if state, err := cl.onDigitalOut.GetState(); err == nil {
			isOn = state
		}
		isHealthy = isOutputHealthy(cl.onDigitalOut)
	}

	return app.DeviceState{
		Name:           cl.name,
		Type:           app.DeviceTypeColorLight,
		IsOn:           isOn,
		IsHealthy:      isHealthy,
		IsFaulty:       cl.isFaulty,
		HomeKitEnabled: !cl.disableHomekit,
	}
}

func (p *SwKitProvider) buildOutletState(outlet *Outlet) app.DeviceState {
	isOn := false
	isHealthy := true

	if outlet.output != nil {
		if state, err := outlet.output.GetState(); err == nil {
			isOn = state
		}
		isHealthy = isOutputHealthy(outlet.output)
	}

	return app.DeviceState{
		Name:           outlet.name,
		Type:           app.DeviceTypeOutlet,
		IsOn:           isOn,
		IsHealthy:      isHealthy,
		IsFaulty:       outlet.isFaulty,
		HomeKitEnabled: !outlet.disableHomekit,
	}
}

func (p *SwKitProvider) buildButtonState(button *Button) app.DeviceState {
	isHealthy := true
	if button.emitter != nil {
		isHealthy = button.emitter.IsHealthy()
	}

	return app.DeviceState{
		Name:           button.name,
		Type:           app.DeviceTypeButton,
		IsOn:           false, // Buttons are stateless
		IsHealthy:      isHealthy,
		IsFaulty:       false, // Buttons don't track faulty state
		HomeKitEnabled: !button.disableHomekit,
	}
}

func isOutputHealthy(output drivers.DigitalOutput) bool {
	if output == nil {
		return false
	}
	return output.IsHealthy()
}
