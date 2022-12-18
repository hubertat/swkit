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
}

type DigitalOutput interface {
	GetState() (bool, error)
	Set(bool) error
}
