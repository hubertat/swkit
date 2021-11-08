package main

import (
	"github.com/racerxdl/go-mcp23017"
	"github.com/stianeikeland/go-rpio"
)

type IoDriver interface {
	Setup([]uint8, []uint8) error
	Close() error
	GetInput(uint8) DigitalInput
	GetOutput(uint8) DigitalOutput
}

type DigitalInput interface {
	GetState() bool
}

type DigitalOutput interface {
	GetState() bool
	Set(bool) error
}

type GpIO struct {
	Inputs  []GpInput
	Outputs []GpOutput

	invertInputs  bool
	invertOutputs bool
	isReady       bool
}

type GpInput struct {
	pin    uint8
	invert bool
}

func (gpi *GpInput) GetState() bool {
	if gpi.invert {
		return rpio.Pin(gpi.pin).Read() == rpio.Low
	} else {
		return rpio.Pin(gpi.pin).Read() == rpio.High
	}
}

type GpOutput struct {
	pin    uint8
	invert bool
}

func (gpo *GpOutput) Set(state bool) {
	if gpo.invert {
		state = !state
	}
	if state {
		rpio.Pin(gpo.pin).High()
	} else {
		rpio.Pin(gpo.pin).Low()
	}
}

func (gpo *GpOutput) GetState() bool {
	if gpo.invert {
		return rpio.Pin(gpo.pin).Read() == rpio.Low
	} else {
		return rpio.Pin(gpo.pin).Read() == rpio.High
	}
}

func (gpio *GpIO) Setup(inputs []uint8, outputs []uint8) error {
	for _, inPin := range inputs {
		pin := rpio.Pin(inPin)
		pin.Input()
		pin.PullUp()
		gpio.Inputs = append(gpio.Inputs, GpInput{pin: inPin, invert: gpio.invertInputs})
	}

	for _, outPin := range outputs {
		pin := rpio.Pin(outPin)
		pin.Output()
		gpio.Outputs = append(gpio.Outputs, GpOutput{pin: outPin, invert: gpio.invertOutputs})
	}

	gpio.isReady = true
	return nil
}

func (gpio *GpIO) Close() error {
	gpio.isReady = false
	return gpio.Close()
}

func (gpio *GpIO) GetInput(id uint8) (input *GpInput) {
	for _, in := range gpio.Inputs {
		if in.pin == id {
			return &in
		}
	}

	return
}

func (gpio *GpIO) GetOutput(id uint8) (output *GpOutput) {
	for _, out := range gpio.Outputs {
		if out.pin == id {
			return &out
		}
	}

	return
}

type McpIO struct {
	device *mcp23017.Device

	Inputs  []McpIn
	Outputs []McpOut
}

func NewMcpIO(Bus, DevNum uint8) (mcp *McpIO, err error) {
	d, err := mcp23017.Open(Bus, DevNum)
	if err != nil {
		return
	}

	mcp = &McpIO{}
	mcp.device = d
	return
}

func (mcp *McpIO) Setup(inputs []uint8, outputs []uint8) (err error) {
	for _, inputPin := range inputs {
		err = mcp.device.PinMode(inputPin, mcp23017.INPUT)
		if err != nil {
			return
		}
		err = mcp.device.SetPullUp(inputPin, true)
		if err != nil {
			return
		}
		mcp.Inputs = append(mcp.Inputs, McpIn{Pin: inputPin, Mcp: mcp})
	}

	for _, outputPin := range outputs {
		err = mcp.device.PinMode(outputPin, mcp23017.OUTPUT)
		mcp.Outputs = append(mcp.Outputs, McpOut{Pin: outputPin, Mcp: mcp})
	}

	return
}

type McpIn struct {
	Pin uint8
	Mcp *McpDevice
}

func (mi *McpIn) Read() (state bool, err error) {
	rawState, err := mi.Mcp.device.DigitalRead(mi.Pin)
	if err != nil {
		return
	}

	state = bool(rawState)
	return
}

type McpOut struct {
	Pin uint8
	Mcp *McpDevice
}
