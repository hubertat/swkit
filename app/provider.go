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
}

// IoOutputController extends StateProvider with raw IO output toggle
type IoOutputController interface {
	ToggleIoOutput(driverName string, outputIndex int) error
}
