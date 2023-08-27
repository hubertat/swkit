package drivers

import (
	"context"
	"errors"
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

func (min *McpInput) SubscribeToPushEvent(listener EventListener) error {
	return errors.New("SubscribeToPushEvent not implemented")
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

func (mcpio *McpIO) NameId() string {
	return mcpioDriverName
}

func (mcpio *McpIO) IsReady() bool {
	return mcpio.isReady
}

func (mcp *McpIO) Setup(ctx context.Context, inputs []uint16, outputs []uint16) (err error) {
	mcp.device, err = mcp23017.Open(mcp.BusNo, mcp.DevNo)
	if err != nil {
		return
	}

	for _, inputPin := range inputs {
		if inputPin > 255 {
			err = fmt.Errorf("input pin out of range (mcpio takes uint8 pin id)")
			return
		}
		err = mcp.device.PinMode(uint8(inputPin), mcp23017.INPUT)
		if err != nil {
			return
		}
		err = mcp.device.SetPullUp(uint8(inputPin), true)
		if err != nil {
			return
		}
		mcp.inputs = append(mcp.inputs, McpInput{pin: uint8(inputPin), invert: mcp.InvertInputs, device: mcp.device})
	}

	for _, outputPin := range outputs {
		if outputPin > 255 {
			err = fmt.Errorf("output pin out of range (mcpio takes uint8 pin id)")
			return
		}
		err = mcp.device.PinMode(uint8(outputPin), mcp23017.OUTPUT)
		if err != nil {
			return
		}
		mcp.outputs = append(mcp.outputs, McpOutput{pin: uint8(outputPin), invert: mcp.InvertOutputs, device: mcp.device})
	}

	mcp.isReady = err == nil

	return
}

func (mcp *McpIO) GetInput(id uint16) (input DigitalInput, err error) {
	for _, in := range mcp.inputs {
		if in.pin == uint8(id) {
			input = &in
			return
		}
	}

	err = fmt.Errorf("input (id: %d) not found", id)
	return
}

func (mcp *McpIO) GetOutput(id uint16) (output DigitalOutput, err error) {
	for _, out := range mcp.outputs {
		if out.pin == uint8(id) {
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

func (mcp *McpIO) GetAllIo() (inputs []uint16, outputs []uint16) {
	for _, input := range mcp.inputs {
		inputs = append(inputs, uint16(input.pin))
	}

	for _, output := range mcp.outputs {
		outputs = append(outputs, uint16(output.pin))
	}

	return
}
