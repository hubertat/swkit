package drivers

import (
	"context"
)

type IoDriver interface {
	Setup(ctx context.Context, inputs []string, outputs []string) error
	Close() error
	String() string
	IsReady() bool
	GetInput(id string) (DigitalInput, error)
	GetOutput(id string) (DigitalOutput, error)
	GetAllIo() (inputs []string, outputs []string)
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

type DigitalInput interface {
	GetState() (bool, error)
	SubscribeToPushEvent(EventListener) error
}

type DigitalOutput interface {
	GetState() (bool, error)
	Set(bool) error
}

type PushEvent int

const (
	PushEventSinglePress PushEvent = 0
	PushEventDoublePress PushEvent = 1
	PushEventLongPress   PushEvent = 2
)

type EventListener interface {
	FireEvent(PushEvent)
}
