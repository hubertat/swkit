package drivers

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"

	"github.com/hubertat/swkit/mqtt"
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

	log.Println("DEBUG: Setting mcpio pin", mout.pin, "to", state)
	err = mout.device.DigitalWrite(mout.pin, mcp23017.PinLevel(state))
	if err != nil {
		log.Println("DEBUG: Failed to set mcpio pin", mout.pin, "err ", err)
	}
	return
}

func (mcpio *McpIO) String() string {
	return mcpioDriverName
}

func (mcpio *McpIO) IsReady() bool {
	return mcpio.isReady
}

func (mcp *McpIO) Setup(ctx context.Context, ios []string) error {
	var err error
	mcp.device, err = mcp23017.Open(mcp.BusNo, mcp.DevNo)
	if err != nil {
		return errors.Join(err, errors.New("failed to open mcp23017 device"))
	}

	for _, io := range ios {
		ioIdSlice := strings.Split(io, "|")
		if len(ioIdSlice) != 3 {
			return errors.New("invalid io id format, expected 3 parts separated by '|'")
		}

		if !strings.EqualFold(ioIdSlice[0], mcp.String()) {
			return errors.New("invalid io, driver name mismatch")
		}

		switch ioIdSlice[1] {
		case "d_in":
			pin, err := strconv.Atoi(ioIdSlice[2])
			if err != nil {
				return errors.Join(err, errors.New("failed to convert input pin to int"))
			}
			if pin > 255 || pin < 0 {
				return errors.Join(err, errors.New("input pin out of range (mcpio takes uint8 pin id)"))
			}

			err = mcp.device.PinMode(uint8(pin), mcp23017.INPUT)
			if err != nil {
				return errors.Join(err, errors.New("failed to set pin mode"))
			}
			err = mcp.device.SetPullUp(uint8(pin), true)
			if err != nil {
				return errors.Join(err, errors.New("failed to set pullup"))
			}
			mcp.inputs = append(mcp.inputs, McpInput{pin: uint8(pin), invert: mcp.InvertInputs, device: mcp.device})

		case "d_out":
			pin, err := strconv.Atoi(ioIdSlice[2])
			if err != nil {
				return errors.Join(err, errors.New("failed to convert output pin to int"))
			}
			if pin > 255 || pin < 0 {
				return errors.Join(err, errors.New("output pin out of range (mcpio takes uint8 pin id)"))
			}

			err = mcp.device.PinMode(uint8(pin), mcp23017.OUTPUT)
			if err != nil {
				return errors.Join(err, errors.New("failed to set pin mode"))
			}
			mcp.outputs = append(mcp.outputs, McpOutput{pin: uint8(pin), device: mcp.device})

		default:
			return errors.New("unknown io type: " + ioIdSlice[1])
		}
	}

	mcp.isReady = err == nil

	return err
}

func (mcp *McpIO) SetMqtt(publisher mqtt.Publisher) (h []mqtt.MqttHandler) {
	return
}

func (mcp *McpIO) GetInput(id string) (input DigitalInput, err error) {
	pin, err := strconv.Atoi(id)
	if err != nil {
		err = errors.Join(err, errors.New("failed to convert input id to int"))
		return
	}
	if pin > 255 || pin < 0 {
		err = errors.New("input pin out of range (mcpio takes uint8 pin id)")
		return
	}
	for _, in := range mcp.inputs {
		if in.pin == uint8(pin) {
			return &in, nil
		}
	}

	err = fmt.Errorf("input (id: %d) not found", id)
	return
}

func (mcp *McpIO) GetOutput(id string) (output DigitalOutput, err error) {
	pin, err := strconv.Atoi(id)
	if err != nil {
		err = errors.Join(err, errors.New("failed to convert output id to int"))
		return
	}
	if pin > 255 || pin < 0 {
		err = errors.New("output pin out of range (mcpio takes uint8 pin id)")
		return
	}
	for _, out := range mcp.outputs {
		if out.pin == uint8(pin) {
			return &out, nil
		}
	}

	err = fmt.Errorf("input (id: %d) not found", id)
	return
}

func (mcp *McpIO) GetAnalogOutput(id string) (output AnalogOutput, err error) {
	pin, err := strconv.Atoi(id)
	if err != nil {
		err = errors.Join(err, errors.New("failed to convert analog output id to int"))
		return
	}
	if pin > 255 || pin < 0 {
		err = errors.New("output pin out of range (mcpio takes uint8 pin id)")
		return
	}
	for _, out := range mcp.outputs {
		if out.pin == uint8(pin) {
			return &out, nil
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

func (mcp *McpIO) GetAllIo() (inputs []string, outputs []string) {
	for _, input := range mcp.inputs {
		inputs = append(inputs, fmt.Sprintf("%d", input.pin))
	}

	for _, output := range mcp.outputs {
		outputs = append(outputs, fmt.Sprintf("%d", output.pin))
	}

	return
}
