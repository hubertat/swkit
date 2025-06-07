package drivers

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/hubertat/swkit/mqtt"
)

type MockOutput struct {
	state            bool
	id               string
	writeTo          io.Writer
	writeStateChange bool
}

func (mo *MockOutput) GetState() (bool, error) {
	return mo.state, nil
}

func (mo *MockOutput) Set(state bool) error {
	if mo.writeStateChange && state != mo.state {
		fmt.Fprintf(mo.writeTo, "[pin %s] state changed to %v\n", mo.id, mo.state)
	}
	mo.state = state
	return nil
}

func (mo *MockOutput) String() string {
	return fmt.Sprintf("mock_output:%s", mo.id)
}

type MockInput struct {
	State bool
	id    string
}

func (mi *MockInput) GetState() (bool, error) {
	return mi.State, nil
}

func (mi *MockInput) String() string {
	return fmt.Sprintf("mock_input:%s", mi.id)
}

type MockIoDriver struct {
	inputs  []*MockInput
	outputs []*MockOutput
	ready   bool
}

func (md *MockIoDriver) Setup(ctx context.Context, ios []string) (err error) {
	// For mock driver, treat all ios as both inputs and outputs for testing
	for _, io := range ios {
		md.inputs = append(md.inputs, &MockInput{id: io})
		md.outputs = append(md.outputs, &MockOutput{id: io})
	}
	md.ready = true
	return nil
}

func (md *MockIoDriver) SetMqtt(publisher mqtt.Publisher) (h []mqtt.MqttHandler) {
	return
}

func (md *MockIoDriver) Close() error {
	return nil
}

func (md *MockIoDriver) String() string {
	return "mock_driver"
}

func (md *MockIoDriver) GetUniqueId(unitId uint16) uint64 {
	baseId := uint64(0xABCDEF00)
	return baseId + uint64(unitId)
}

func (md *MockIoDriver) IsReady() bool {
	return md.ready
}

func (md *MockIoDriver) GetDigitalInput(id string) (DigitalInput, error) {
	for _, input := range md.inputs {
		if strings.EqualFold(id, input.id) {
			return input, nil
		}
	}
	return nil, fmt.Errorf("mock input %s not found", id)
}

func (md *MockIoDriver) GetDigitalOutput(id string) (DigitalOutput, error) {
	for _, output := range md.outputs {
		if strings.EqualFold(id, output.id) {
			return output, nil
		}
	}
	return nil, fmt.Errorf("mock output %s not found", id)
}

func (md *MockIoDriver) GetAnalogOutput(id string) (AnalogOutput, error) {
	return nil, fmt.Errorf("analog output not implemented in mock driver")
}

func (md *MockIoDriver) GetRgbwOutput(id string) (RgbwOutput, error) {
	return nil, fmt.Errorf("rgbw output not implemented in mock driver")
}

func (md *MockIoDriver) GetAllIo() (inputs []string, outputs []string) {
	for _, input := range md.inputs {
		inputs = append(inputs, input.id)
	}
	for _, output := range md.outputs {
		outputs = append(outputs, output.id)
	}
	return
}

func (md *MockIoDriver) MonitorStateChanges(writer io.Writer) {
	for _, out := range md.outputs {
		out.writeTo = writer
		out.writeStateChange = true
	}
}
