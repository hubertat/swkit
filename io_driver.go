package main

type IoDriver interface {
	Setup(inputs []uint8, outputs []uint8) error
	Close() error
	NameId() string
	GetUniqueId(ioPin uint8) uint64
	IsReady() bool
	GetInput(pin uint8) (DigitalInput, error)
	GetOutput(pin uint8) (DigitalOutput, error)
	GetAllIo() (inputs []uint8, outputs []uint8)
}

type DigitalInput interface {
	GetState() (bool, error)
}

type DigitalOutput interface {
	GetState() (bool, error)
	Set(bool) error
}
