package drivers

import (
	"context"
	"fmt"
	"strconv"

	"github.com/hubertat/swkit/mqtt"
	"github.com/pkg/errors"
	"github.com/stianeikeland/go-rpio/v4"
)

const gpioDriverName = "gpio"

type GpIO struct {
	InvertInputs  bool
	InvertOutputs bool

	inputs  []GpInput
	outputs []GpOutput
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

func (gpi *GpInput) SubscribeToPushEvent(listener EventListener) error {
	return errors.New("SubscribeToPushEvent not implemented")
}

func (gpo *GpOutput) Set(state bool) error {

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

func (gp *GpIO) Setup(ctx context.Context, inputs []string, outputs []string) error {
	err := rpio.Open()
	if err != nil {
		return errors.Wrapf(err, "failed to Setup gpio driver for pins: %v, %v; ", inputs, outputs)
	}
	for _, input := range inputs {
		inPin, err := strconv.Atoi(input)
		if err != nil {
			return errors.Wrap(err, "failed to convert input pin to int")
		}
		if inPin > 255 || inPin < 0 {
			return errors.Errorf("inpin out of range (gpio takes uint8 pin)")
		}
		pin := rpio.Pin(inPin)
		pin.Input()
		pin.PullUp()
		gp.inputs = append(gp.inputs, GpInput{pin: uint8(inPin), invert: gp.InvertInputs})
	}

	for _, output := range outputs {
		outPin, err := strconv.Atoi(output)
		if err != nil {
			return errors.Wrap(err, "failed to convert output pin to int")
		}
		if outPin > 255 || outPin < 0 {
			return errors.Errorf("outpin out of range (gpio takes uint8 pin)")
		}
		pin := rpio.Pin(outPin)
		pin.Output()
		gp.outputs = append(gp.outputs, GpOutput{pin: uint8(outPin), invert: gp.InvertOutputs})
	}

	gp.isReady = true
	return nil
}

func (gp *GpIO) SetMqtt(publisher mqtt.Publisher) (topics []mqtt.MqttHandler) {
	return
}

func (gp *GpIO) String() string {
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

func (gp *GpIO) GetInput(id string) (input DigitalInput, err error) {
	pin, err := strconv.Atoi(id)
	if err != nil {
		err = errors.Wrap(err, "failed to convert output pin to int")
		return
	}
	if pin < 0 || pin > 255 {
		err = errors.Errorf("pin id out (%d) of range gpio takes uint8 pin", pin)
		return
	}
	for _, in := range gp.inputs {
		if in.pin == uint8(pin) {
			input = &in
			return
		}
	}

	err = fmt.Errorf("GpIO Input (id: %d) not found", id)
	return
}

func (gp *GpIO) GetOutput(id string) (output DigitalOutput, err error) {
	pin, err := strconv.Atoi(id)
	if err != nil {
		err = errors.Wrap(err, "failed to convert output pin to int")
		return
	}
	if pin > 255 || pin < 0 {
		err = errors.Errorf("pin id out (%d) of range gpio takes uint8 pin", pin)
		return
	}
	for _, out := range gp.outputs {
		if out.pin == uint8(pin) {
			output = &out
			return
		}
	}

	err = fmt.Errorf("GpIO Output (id: %d) not found", id)
	return
}

func (gp *GpIO) GetAllIo() (inputs []string, outputs []string) {
	for _, input := range gp.inputs {
		inputs = append(inputs, fmt.Sprintf("%d", input.pin))
	}

	for _, output := range gp.outputs {
		outputs = append(outputs, fmt.Sprintf("%d", output.pin))
	}

	return
}
