package drivers

import (
	"context"
)

type PushEvent uint16

const (
	PushEventSinglePress = 0x02 << iota
	PushEventDoublePress
	PushEventTriplePress
	PushEventLongPress
)

type IoType uint16

const (
	ioTypeDigitalOutput = 0x01 << iota
	ioTypeDigitalInput
	ioTypePushEventEmitter
	ioTypeAnalogOutput
	ioTypeAnalogInput
	ioTypeRgbwOutput
	ioTypeRgbwInput
)

type IoDriver interface {
	Setup(ctx context.Context, ios []string) error
	Close() error
	String() string
	IsReady() bool
	GetDigitalInput(id string) (DigitalInput, error)
	GetDigitalOutput(id string) (DigitalOutput, error)
	GetAnalogOutput(id string) (AnalogOutput, error)
	GetRgbwOutput(id string) (RgbwOutput, error)
}

func MapAllIoDrivers() map[string]IoDriver {
	drivers := []IoDriver{
		&ShellyIO{},
		&GpIO{},
		&McpIO{},
		&MockIoDriver{},
		&GrentonIO{},
	}

	mapped := make(map[string]IoDriver)
	for _, driver := range drivers {
		mapped[driver.String()] = driver
	}
	return mapped
}

type AnyIO interface {
	String() string
}

// DigitalInput represents simple two state digital input
// It's configuration (like pullup, filtering etc) is made in the driver
// It could be an abstraction, eg for double click event - should be handled by driver and driver setup
type DigitalInput interface {
	GetState() (bool, error)
	String() string
}

// DigitalOutput represents two state output
type DigitalOutput interface {
	GetState() (bool, error)
	Set(bool) error
	String() string
}

// AnalogOutput represents an output that could take int value from defined minimum and maximum
// Min and max are defined by the driver, and could be queried by GetMinMax func.
type AnalogOutput interface {
	GetMinMax() (int, int)
	GetState() (int, error)
	Set(int) error
	String() string
}

// PushEventEmitter represents an input that emits events, as in PushEvent type
type PushEventEmitter interface {
	Subscribe(eventTypes PushEvent, handler func(PushEvent)) error
	String() string
}

type RgbwOutput interface {
	GetState() (uint8, uint8, uint8, uint8, error)
	Set(uint8, uint8, uint8, uint8) error
	String() string
}
