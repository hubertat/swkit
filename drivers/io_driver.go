package drivers

type IoDriver interface {
	Setup(inputs []uint16, outputs []uint16) error
	Close() error
	NameId() string
	IsReady() bool
	GetInput(pin uint16) (DigitalInput, error)
	GetOutput(pin uint16) (DigitalOutput, error)
	GetAllIo() (inputs []uint16, outputs []uint16)
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
