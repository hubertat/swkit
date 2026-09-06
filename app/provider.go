package app

import (
	"context"
	"time"
)

// StateProvider provides application state for UIs
type StateProvider interface {
	// GetState returns the current application state snapshot
	GetState() AppState

	// Subscribe returns a channel that receives state updates
	// The channel is closed when the context is cancelled
	Subscribe(ctx context.Context, interval time.Duration) <-chan AppState
}

// DeviceController extends StateProvider with control capabilities
type DeviceController interface {
	StateProvider
	// ToggleDevice toggles the device at the given index
	ToggleDevice(index int) ControlResult
	// SetDevice sets the device at the given index to the given state
	SetDevice(index int, state bool) ControlResult
	// SetDeviceBrightness sets the brightness (0-100) of the device at the given index
	SetDeviceBrightness(index int, pct int) ControlResult
	// AdjustDeviceBrightness changes the brightness of the device at the given index
	// by delta (relative, may be negative), clamped to 0-100.
	AdjustDeviceBrightness(index int, delta int) ControlResult
	// SetDeviceValueFor sets the device on/off for the given duration (seconds),
	// then reverts to its prior state.
	SetDeviceValueFor(index int, state bool, seconds int) ControlResult
}

// IoOutputController extends StateProvider with raw IO output toggle
type IoOutputController interface {
	ToggleIoOutput(driverName string, outputIndex int) error
}

// IoAnalogOutputController extends StateProvider with raw analog IO output set
type IoAnalogOutputController interface {
	SetIoAnalogOutput(driverName string, outputIndex int, value int) error
}
