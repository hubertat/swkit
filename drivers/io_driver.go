package drivers

import (
	"context"

	"github.com/hubertat/swkit/mqtt"
)

type IoDriver interface {
	Setup(ctx context.Context, inputs []uint16, outputs []uint16) error
	SetMqtt(publisher mqtt.Publisher) []mqtt.MqttHandler
	Close() error
	String() string
	IsReady() bool
	GetInput(pin uint16) (DigitalInput, error)
	GetOutput(pin uint16) (DigitalOutput, error)
	GetAllIo() (inputs []uint16, outputs []uint16)
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
