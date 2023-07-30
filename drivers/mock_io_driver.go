package drivers

import (
	"fmt"
	"io"

	"errors"
)

type MockOutput struct {
	state            bool
	pin              uint16
	writeTo          io.Writer
	writeStateChange bool
}

func (mo *MockOutput) GetState() (bool, error) {
	return mo.state, nil
}

func (mo *MockOutput) Set(state bool) error {
	if mo.writeStateChange && state != mo.state {
		fmt.Fprintf(mo.writeTo, "[pin %d] state changed to %v\n", mo.pin, mo.state)
	}
	mo.state = state
	return nil
}

type MockInput struct {
	State bool
	pin   uint16
}

func (mi *MockInput) GetState() (bool, error) {
	return mi.State, nil
}

func (mi *MockInput) SubscribeToPushEvent(listener EventListener) error {
	return errors.New("SubscribeToPushEvent not implemented")
}

type MockIoDriver struct {
	inputs  []*MockInput
	outputs []*MockOutput
	ready   bool
}

func (md *MockIoDriver) Setup(inputs []uint16, outputs []uint16) error {
	for _, inPin := range inputs {
		md.inputs = append(md.inputs, &MockInput{pin: inPin})
	}
	for _, outPin := range outputs {
		md.outputs = append(md.outputs, &MockOutput{pin: outPin})
	}
	md.ready = true
	return nil
}

func (md *MockIoDriver) Close() error {
	return nil
}

func (md *MockIoDriver) NameId() string {
	return "mock_driver"
}

func (md *MockIoDriver) GetUniqueId(unitId uint16) uint64 {
	baseId := uint64(0xABCDEF00)
	return baseId + uint64(unitId)
}

func (md *MockIoDriver) IsReady() bool {
	return md.ready
}

func (md *MockIoDriver) GetInput(pin uint16) (DigitalInput, error) {
	for _, input := range md.inputs {
		if pin == input.pin {
			return input, nil
		}
	}
	return nil, fmt.Errorf("mock input %d not found", pin)
}

func (md *MockIoDriver) GetOutput(pin uint16) (DigitalOutput, error) {
	for _, output := range md.outputs {
		if pin == output.pin {
			return output, nil
		}
	}
	return nil, fmt.Errorf("mock output %d not found", pin)
}

func (md *MockIoDriver) GetAllIo() (inputs []uint16, outputs []uint16) {
	for _, input := range md.inputs {
		inputs = append(inputs, input.pin)
	}
	for _, output := range md.outputs {
		outputs = append(outputs, output.pin)
	}
	return
}

func (md *MockIoDriver) MonitorStateChanges(writer io.Writer) {
	for _, out := range md.outputs {
		out.writeTo = writer
		out.writeStateChange = true
	}
}
