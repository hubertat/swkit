package main

import (
	"fmt"

	"github.com/stianeikeland/go-rpio"
)

const gpioDriverName = "gpio"

type GpIO struct {
	inputs  []GpInput
	outputs []GpOutput

	InvertInputs  bool
	InvertOutputs bool

	isReady bool
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
		gp.inputs = append(gp.inputs, GpInput{pin: inPin, invert: gp.InvertInputs})
	}

	for _, outPin := range outputs {
		pin := rpio.Pin(outPin)
		pin.Output()
		gp.outputs = append(gp.outputs, GpOutput{pin: outPin, invert: gp.InvertOutputs})
	}

	gp.isReady = true
	return nil
}

func (gp *GpIO) NameId() string {
	return gpioDriverName
}

func (gp *GpIO) IsReady() bool {
	return gp.isReady
}

func (gp *GpIO) Close() error {
	gp.isReady = false
	for _, output := range gp.outputs {
		output.Set(false)
	}
	return rpio.Close()
}

func (gp *GpIO) GetInput(id uint8) (input DigitalInput, err error) {
	for _, in := range gp.inputs {
		if in.pin == id {
			input = &in
			return
		}
	}

	err = fmt.Errorf("GpIO Input (id: %d) not found", id)
	return
}

func (gp *GpIO) GetOutput(id uint8) (output DigitalOutput, err error) {
	for _, out := range gp.outputs {
		if out.pin == id {
			output = &out
			return
		}
	}

	err = fmt.Errorf("GpIO Output (id: %d) not found", id)
	return
}

func (gp *GpIO) GetUniqueId(ioPin uint8) uint64 {
	baseId := uint64(0x01000000)
	return baseId + uint64(ioPin)
}

func (gp *GpIO) GetAllIo() (inputs []uint8, outputs []uint8) {
	for _, input := range gp.inputs {
		inputs = append(inputs, input.pin)
	}

	for _, output := range gp.outputs {
		outputs = append(outputs, output.pin)
	}

	return
}
