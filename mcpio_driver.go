package main

import (
	"fmt"

	"github.com/racerxdl/go-mcp23017"
)

const mcpioDriverName = "mcpio"

type McpIO struct {
	device *mcp23017.Device

	inputs  []McpInput
	outputs []McpOutput
	isReady bool

	BusNo         uint8
	DevNo         uint8
	InvertInputs  bool
	InvertOutputs bool
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
	return mcpioDriverName
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
		mcp.inputs = append(mcp.inputs, McpInput{pin: inputPin, invert: mcp.InvertInputs, device: mcp.device})
	}

	for _, outputPin := range outputs {
		err = mcp.device.PinMode(outputPin, mcp23017.OUTPUT)
		if err != nil {
			return
		}
		mcp.outputs = append(mcp.outputs, McpOutput{pin: outputPin, invert: mcp.InvertOutputs, device: mcp.device})
	}

	return
}

func (mcp *McpIO) GetInput(id uint8) (input DigitalInput, err error) {
	for _, in := range mcp.inputs {
		if in.pin == id {
			input = &in
			return
		}
	}

	err = fmt.Errorf("input (id: %d) not found", id)
	return
}

func (mcp *McpIO) GetOutput(id uint8) (output DigitalOutput, err error) {
	for _, out := range mcp.outputs {
		if out.pin == id {
			output = &out
			return
		}
	}

	err = fmt.Errorf("input (id: %d) not found", id)
	return
}

func (mcp *McpIO) Close() error {
	mcp.isReady = false
	for _, output := range mcp.outputs {
		output.Set(false)
	}
	return mcp.device.Close()
}

func (mcp *McpIO) GetAllIo() (inputs []uint8, outputs []uint8) {
	for _, input := range mcp.inputs {
		inputs = append(inputs, input.pin)
	}

	for _, output := range mcp.outputs {
		outputs = append(outputs, output.pin)
	}

	return
}
