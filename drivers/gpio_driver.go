package drivers

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/hubertat/swkit/mqtt"

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

func (gpi *GpInput) String() string {
	return GetIoIdString(gpioDriverName, IoTypeDigitalInput, strconv.Itoa(int(gpi.pin)))
}

func (gpi *GpInput) IsHealthy() bool {
	return true
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

func (gpo *GpOutput) String() string {
	return GetIoIdString(gpioDriverName, IoTypeDigitalOutput, strconv.Itoa(int(gpo.pin)))
}

func (gpo *GpOutput) SetOnStateUpdate(onStateUpdate func(bool)) error {
	return errors.New("SetOnStateUpdate not supported")
}

func (gpo *GpOutput) IsHealthy() bool {
	return true
}

func (gp *GpIO) Setup(ctx context.Context, ios []string) error {
	err := rpio.Open()
	if err != nil {
		return errors.Join(err, fmt.Errorf("failed to Setup gpio driver: failed to open rpio"))
	}
	for _, io := range ios {
		driver, ioType, ioId, err := ResolveIoIdString(io)
		if err != nil {
			return errors.Join(errors.New("invalid io id format, expected 3 parts separated by '|'"), err)
		}

		if !strings.EqualFold(driver, gp.String()) {
			return errors.New("invalid io, driver name mismatch")
		}

		switch ioType {
		case IoTypeDigitalInput:
			pin, err := strconv.Atoi(ioId)
			if err != nil {
				return errors.Join(err, errors.New("failed to convert input pin to int"))
			}
			if pin > 255 || pin < 0 {
				return errors.Join(err, errors.New("input pin out of range (gpio takes uint8 pin id)"))
			}
			gpioPin := rpio.Pin(pin)
			gpioPin.Input()
			gpioPin.PullUp()
			gp.inputs = append(gp.inputs, GpInput{pin: uint8(pin), invert: gp.InvertInputs})

		case IoTypeDigitalOutput:
			pin, err := strconv.Atoi(ioId)
			if err != nil {
				return errors.Join(err, errors.New("failed to convert output pin to int"))
			}
			if pin > 255 || pin < 0 {
				return errors.Join(err, errors.New("output pin out of range (gpio takes uint8 pin id)"))
			}

			gpioPin := rpio.Pin(pin)
			gpioPin.Output()
			gp.outputs = append(gp.outputs, GpOutput{pin: uint8(pin), invert: gp.InvertOutputs})
		default:
			return errors.New("unsupported io type: " + ioType.String())
		}
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

func (gp *GpIO) GetDigitalInput(id string) (input DigitalInput, err error) {
	pin, err := strconv.Atoi(id)
	if err != nil {
		err = errors.Join(err, errors.New("failed to convert output pin to int"))
		return
	}
	if pin < 0 || pin > 255 {
		err = fmt.Errorf("pin id out (%d) of range gpio takes uint8 pin", pin)
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

func (gp *GpIO) GetDigitalOutput(id string) (output DigitalOutput, err error) {
	pin, err := strconv.Atoi(id)
	if err != nil {
		err = errors.Join(err, errors.New("failed to convert output pin to int"))
		return
	}
	if pin > 255 || pin < 0 {
		err = fmt.Errorf("pin id out (%d) of range gpio takes uint8 pin", pin)
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

func (gp *GpIO) GetAnalogOutput(id string) (AnalogOutput, error) {
	return nil, errors.New("analog ouput not implemented in GPIO driver")
}

func (gp *GpIO) GetRgbwOutput(id string) (RgbwOutput, error) {
	return nil, errors.New("rgbw ouput not implemented in GPIO driver")
}
