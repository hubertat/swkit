package main

import (
	"fmt"

	"github.com/racerxdl/go-mcp23017"
	"github.com/stianeikeland/go-rpio"
)

type IoDriver interface {
	Setup(inputs []uint8, outputs []uint8) error
	Close() error
	NameId() string
	GetUniqueId(ioPin uint8) uint64
	IsReady() bool
	GetInput(pin uint8) (DigitalInput, error)
	GetOutput(pin uint8) (DigitalOutput, error)
}

type DigitalInput interface {
	GetState() (bool, error)
}

type DigitalOutput interface {
	GetState() (bool, error)
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

type GpOutput struct {
	pin    uint8
	invert bool
}

func (gpi *GpInput) GetState() (state bool, err error) {
	if gpi.invert {
		state = rpio.Pin(gpi.pin).Read() == rpio.Low
	} else {
		state = rpio.Pin(gpi.pin).Read() == rpio.High
	}

	return
}

func (gpo *GpOutput) Set(state bool) error {
	rpio.Open()
	if gpo.invert {
		state = !state
	}
	if state {
		rpio.Pin(gpo.pin).High()
	} else {
		rpio.Pin(gpo.pin).Low()
	}

	return nil
}

func (gpo *GpOutput) GetState() (state bool, err error) {
	if gpo.invert {
		state = rpio.Pin(gpo.pin).Read() == rpio.Low
	} else {
		state = rpio.Pin(gpo.pin).Read() == rpio.High
	}

	return
}

func (gp *GpIO) Setup(inputs []uint8, outputs []uint8) error {
	err := rpio.Open()
	if err != nil {
		return err
	}
	for _, inPin := range inputs {
		pin := rpio.Pin(inPin)
		pin.Input()
		pin.PullUp()
		gp.Inputs = append(gp.Inputs, GpInput{pin: inPin, invert: gp.invertInputs})
	}

	for _, outPin := range outputs {
		pin := rpio.Pin(outPin)
		pin.Output()
		gp.Outputs = append(gp.Outputs, GpOutput{pin: outPin, invert: gp.invertOutputs})
	}

	gp.isReady = true
	return nil
}

func (gp *GpIO) NameId() string {
	return "gpio"
}

func (gp *GpIO) IsReady() bool {
	return gp.isReady
}

func (gp *GpIO) Close() error {
	gp.isReady = false
	return rpio.Close()
}

func (gp *GpIO) GetInput(id uint8) (input DigitalInput, err error) {
	for _, in := range gp.Inputs {
		if in.pin == id {
			input = &in
			return
		}
	}

	err = fmt.Errorf("input (id: %d) not found", id)
	return
}

func (gp *GpIO) GetOutput(id uint8) (output DigitalOutput, err error) {
	for _, out := range gp.Outputs {
		if out.pin == id {
			output = &out
			return
		}
	}

	err = fmt.Errorf("input (id: %d) not found", id)
	return
}

func (gp *GpIO) GetUniqueId(ioPin uint8) uint64 {
	baseId := uint64(0x01000000)
	return baseId + uint64(ioPin)
}

type McpIO struct {
	device *mcp23017.Device

	Inputs  []McpInput
	Outputs []McpOutput

	BusNo         uint8
	DevNo         uint8
	invertInputs  bool
	invertOutputs bool
	isReady       bool
}

type McpInput struct {
	pin    uint8
	invert bool

	device *mcp23017.Device
}

type McpOutput struct {
	pin    uint8
	invert bool

	device *mcp23017.Device
}

func (min *McpInput) GetState() (state bool, err error) {
	rawState, err := min.device.DigitalRead(min.pin)
	if err != nil {
		return
	}

	if min.invert {
		state = !bool(rawState)
	} else {
		state = bool(rawState)
	}
	return
}

func (mout *McpOutput) GetState() (state bool, err error) {
	rawState, err := mout.device.DigitalRead(mout.pin)
	if err != nil {
		return
	}

	if mout.invert {
		state = !bool(rawState)
	} else {
		state = bool(rawState)
	}
	return
}

func (mout *McpOutput) Set(state bool) (err error) {
	if mout.invert {
		state = !state
	}

	err = mout.device.DigitalWrite(mout.pin, mcp23017.PinLevel(state))

	return
}

func (mcpio *McpIO) GetUniqueId(ioPin uint8) uint64 {
	baseId := uint64(0x02000000)
	baseId += uint64(mcpio.BusNo) << 16
	baseId += uint64(mcpio.DevNo) << 8
	return baseId + uint64(ioPin)
}

func (mcpio *McpIO) NameId() string {
	return "mcpio"
}

func (mcpio *McpIO) IsReady() bool {
	return mcpio.isReady
}

func (mcp *McpIO) Setup(inputs []uint8, outputs []uint8) (err error) {
	mcp.device, err = mcp23017.Open(mcp.BusNo, mcp.DevNo)
	if err != nil {
		return
	}

	for _, inputPin := range inputs {
		err = mcp.device.PinMode(inputPin, mcp23017.INPUT)
		if err != nil {
			return
		}
		err = mcp.device.SetPullUp(inputPin, true)
		if err != nil {
			return
		}
		mcp.Inputs = append(mcp.Inputs, McpInput{pin: inputPin, invert: mcp.invertInputs, device: mcp.device})
	}

	for _, outputPin := range outputs {
		err = mcp.device.PinMode(outputPin, mcp23017.OUTPUT)
		if err != nil {
			return
		}
		mcp.Outputs = append(mcp.Outputs, McpOutput{pin: outputPin, invert: mcp.invertOutputs, device: mcp.device})
	}

	return
}

func (mcp *McpIO) GetInput(id uint8) (input DigitalInput, err error) {
	for _, in := range mcp.Inputs {
		if in.pin == id {
			input = &in
			return
		}
	}

	err = fmt.Errorf("input (id: %d) not found", id)
	return
}

func (mcp *McpIO) GetOutput(id uint8) (output DigitalOutput, err error) {
	for _, out := range mcp.Outputs {
		if out.pin == id {
			output = &out
			return
		}
	}

	err = fmt.Errorf("input (id: %d) not found", id)
	return
}

func (mcp *McpIO) Close() error {
	return mcp.device.Close()
}
