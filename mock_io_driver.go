package main

import "fmt"

type MockOutput struct {
	state bool
	pin   uint8
}

func (mo *MockOutput) GetState() (bool, error) {
	return mo.state, nil
}

func (mo *MockOutput) Set(state bool) error {
	mo.state = state
	return nil
}

type MockInput struct {
	State bool
	pin   uint8
}

func (mi *MockInput) GetState() (bool, error) {
	return mi.State, nil
}

type MockIoDriver struct {
	inputs  []*MockInput
	outputs []*MockOutput
	ready   bool
}

func (md *MockIoDriver) Setup(inputs []uint8, outputs []uint8) error {
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

func (md *MockIoDriver) GetUniqueId(ioPin uint8) uint64 {
	baseId := uint64(0xABCDEF00)
	return baseId + uint64(ioPin)
}

func (md *MockIoDriver) IsReady() bool {
	return md.ready
}

func (md *MockIoDriver) GetInput(pin uint8) (DigitalInput, error) {
	for _, input := range md.inputs {
		if pin == input.pin {
			return input, nil
		}
	}
	return nil, fmt.Errorf("mock input %d not found", pin)
}

func (md *MockIoDriver) GetOutput(pin uint8) (DigitalOutput, error) {
	for _, output := range md.outputs {
		if pin == output.pin {
			return output, nil
		}
	}
	return nil, fmt.Errorf("mock output %d not found", pin)
}

func (md *MockIoDriver) GetAllIo() (inputs []uint8, outputs []uint8) {
	for _, input := range md.inputs {
		inputs = append(inputs, input.pin)
	}
	for _, output := range md.outputs {
		outputs = append(outputs, output.pin)
	}
	return
}
