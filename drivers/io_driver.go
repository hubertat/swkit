package drivers

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

type PushEvent uint16

const (
	PushEventSinglePress = PushEvent(0x02 << iota)
	PushEventDoublePress
	PushEventTriplePress
	PushEventLongPress
)

func AllPushEvents() []PushEvent {
	return []PushEvent{
		PushEventSinglePress,
		PushEventDoublePress,
		PushEventTriplePress,
		PushEventLongPress,
	}
}

func (pe PushEvent) String() string {
	switch pe {
	case PushEventSinglePress:
		return "single_press"
	case PushEventDoublePress:
		return "double_press"
	case PushEventTriplePress:
		return "triple_press"
	case PushEventLongPress:
		return "long_press"
	default:
		return "unknown"
	}
}

type IoType uint16

const (
	IoTypeUndefined = 0

	IoTypeDigitalOutput = 0x01 << iota
	IoTypeDigitalInput
	IoTypePushEventEmitter
	IoTypeAnalogOutput
	IoTypeAnalogInput
	IoTypeRgbwOutput
	IoTypeRgbwInput
)

func allIoTypes() []IoType {
	return []IoType{
		IoTypeDigitalOutput,
		IoTypeDigitalInput,
		IoTypePushEventEmitter,
		IoTypeAnalogOutput,
		IoTypeAnalogInput,
		IoTypeRgbwOutput,
		IoTypeRgbwInput,
	}
}

func (iot IoType) IdString() string {
	switch iot {
	case IoTypeDigitalInput:
		return "d_in"
	case IoTypeDigitalOutput:
		return "d_out"
	case IoTypePushEventEmitter:
		return "push_event"
	case IoTypeAnalogOutput:
		return "a_out"
	case IoTypeAnalogInput:
		return "a_in"
	case IoTypeRgbwOutput:
		return "rgbw_out"
	case IoTypeRgbwInput:
		return "rgbw_in"
	default:
		return "n/a"
	}
}

func (iot IoType) String() string {
	switch iot {
	case IoTypeDigitalOutput:
		return "DigitalOutput"
	case IoTypeDigitalInput:
		return "DigitalInput"
	case IoTypePushEventEmitter:
		return "PushEventEmitter"
	case IoTypeAnalogOutput:
		return "AnalogOutput"
	case IoTypeAnalogInput:
		return "AnalogInput"
	case IoTypeRgbwOutput:
		return "RgbwOutput"
	case IoTypeRgbwInput:
		return "RgbwInput"
	default:
		return "Unknown"
	}
}

type IoDriver interface {
	Setup(ctx context.Context, ios []string) error
	Close() error
	String() string
	IsReady() bool
	GetDigitalInput(id string) (DigitalInput, error)
	GetDigitalOutput(id string) (DigitalOutput, error)
	GetAnalogOutput(id string) (AnalogOutput, error)
	GetRgbwOutput(id string) (RgbwOutput, error)
	GetPushEventEmitter(id string) (PushEventEmitter, error)
}

func MapAllIoDrivers() map[string]IoDriver {
	drivers := []IoDriver{
		&ShellyIO{},
		&GpIO{},
		&McpIO{},
		&MockIoDriver{},
		&GrentonIO{},
		&WagoIO{},
	}

	mapped := make(map[string]IoDriver)
	for _, driver := range drivers {
		mapped[driver.String()] = driver
	}
	return mapped
}

type AnyIO interface {
	String() string
	IsHealthy() bool
}

// DigitalInput represents simple two state digital input
// It's configuration (like pullup, filtering etc) is made in the driver
// It could be an abstraction, eg for double click event - should be handled by driver and driver setup
type DigitalInput interface {
	GetState() (bool, error)
	String() string
	IsHealthy() bool
}

// DigitalOutput represents two state output
type DigitalOutput interface {
	GetState() (bool, error)
	Set(bool) error
	String() string
	SetOnStateUpdate(func(bool)) error
	IsHealthy() bool
}

// AnalogOutput represents an output that could take int value from defined minimum and maximum
// Min and max are defined by the driver, and could be queried by GetMinMax func.
type AnalogOutput interface {
	GetMinMax() (int, int)
	GetState() (int, error)
	Set(int) error
	String() string
	IsHealthy() bool
}

// PushEventEmitter represents an input that emits events, as in PushEvent type
type PushEventEmitter interface {
	Subscribe(eventTypes PushEvent, handler func(PushEvent)) error
	String() string
	IsHealthy() bool
}

type RgbwOutput interface {
	GetState() (uint8, uint8, uint8, uint8, error)
	Set(uint8, uint8, uint8, uint8) error
	String() string
	IsHealthy() bool
}

// IoPointState represents the state of a single IO point for debug display
type IoPointState struct {
	Index       int
	Name        string // e.g. "M1:DI3" (Module 1, Digital Input 3)
	Type        IoType
	State       bool
	Healthy     bool
	LastChanged time.Time // zero value if never changed since startup
	LastEvent   time.Time // zero value if no event received; set for explicit button-press events
}

// IoDebugSnapshot contains the state of all IO points from a driver
type IoDebugSnapshot struct {
	Points []IoPointState
}

// IoDebugProvider is an optional interface for drivers that can provide
// real-time debug snapshots of all their IO points
type IoDebugProvider interface {
	GetIoDebugSnapshot() IoDebugSnapshot
}

// IoOutputToggler is an optional interface for drivers that support
// toggling a digital output by its global index
type IoOutputToggler interface {
	ToggleOutput(index int) error
}

func ResolveIoIdString(ioId string) (driver string, ioType IoType, name string, err error) {
	ioIdSlice := strings.Split(ioId, "|")
	if len(ioIdSlice) != 3 {
		err = errors.New("invalid io id format, expected 3 parts separated by '|'")
		return
	}

	driver = ioIdSlice[0]

	for _, t := range allIoTypes() {
		if strings.EqualFold(t.IdString(), ioIdSlice[1]) {
			ioType = t
		}
	}
	if ioType == IoTypeUndefined {
		err = fmt.Errorf("invalid io type, couldn't match io type from id string: %s", ioIdSlice[1])
		return
	}

	name = ioIdSlice[2]
	return
}

func GetIoIdString(driver string, ioType IoType, id string) string {
	return fmt.Sprintf("%s|%s|%s", driver, ioType.IdString(), id)
}
