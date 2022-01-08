package main

import (
	"fmt"

	"github.com/pkg/errors"
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

func (gp *GpIO) Setup(inputs []uint16, outputs []uint16) error {
	err := rpio.Open()
	if err != nil {
		return err
	}
	for _, inPin := range inputs {
		if inPin > 255 {
			return errors.Errorf("inpin out of range (gpio takes uint8 pin)")
		}
		pin := rpio.Pin(inPin)
		pin.Input()
		pin.PullUp()
		gp.inputs = append(gp.inputs, GpInput{pin: uint8(inPin), invert: gp.InvertInputs})
	}

	for _, outPin := range outputs {
		if outPin > 255 {
			return errors.Errorf("outpin out of range (gpio takes uint8 pin)")
		}
		pin := rpio.Pin(outPin)
		pin.Output()
		gp.outputs = append(gp.outputs, GpOutput{pin: uint8(outPin), invert: gp.InvertOutputs})
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

func (gp *GpIO) GetInput(id uint16) (input DigitalInput, err error) {
	if id > 255 {
		err = errors.Errorf("pin id out of range (gpio takes uint8 pin)")
		return
	}
	for _, in := range gp.inputs {
		if in.pin == uint8(id) {
			input = &in
			return
		}
	}

	err = fmt.Errorf("GpIO Input (id: %d) not found", id)
	return
}

func (gp *GpIO) GetOutput(id uint16) (output DigitalOutput, err error) {
	if id > 255 {
		err = errors.Errorf("pin id out of range (gpio takes uint8 pin)")
		return
	}
	for _, out := range gp.outputs {
		if out.pin == uint8(id) {
			output = &out
			return
		}
	}

	err = fmt.Errorf("GpIO Output (id: %d) not found", id)
	return
}

func (gp *GpIO) GetUniqueId(ioPin uint16) uint64 {
	baseId := uint64(0x01000000)
	return baseId + uint64(ioPin)
}

func (gp *GpIO) GetAllIo() (inputs []uint16, outputs []uint16) {
	for _, input := range gp.inputs {
		inputs = append(inputs, uint16(input.pin))
	}

	for _, output := range gp.outputs {
		outputs = append(outputs, uint16(output.pin))
	}

	return
}
