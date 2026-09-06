package drivers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/charmbracelet/log"
	"github.com/hubertat/swkit/logging"
	"github.com/hubertat/swkit/mqtt"
)

type MockOutput struct {
	state            bool
	id               string
	writeTo          io.Writer
	writeStateChange bool
}

// NewMockOutput creates a MockOutput with the given id (for tests).
func NewMockOutput(id string) *MockOutput {
	return &MockOutput{id: id}
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

func (mo *MockOutput) SetOnStateUpdate(onStateUpdate func(bool)) error {
	return errors.New("SetOnStateUpdate not supported")
}

// IsHealthy returns always true (mock)
func (mo *MockOutput) IsHealthy() bool {
	return true
}

// IsHealthy returns always true (mock)
func (mi *MockInput) IsHealthy() bool {
	return true
}

// MockAnalogOutput is an in-memory AnalogOutput for testing.
type MockAnalogOutput struct {
	value    int
	id       string
	min, max int
}

// NewMockAnalogOutput creates a MockAnalogOutput with the given id and range (for tests).
func NewMockAnalogOutput(id string, min, max int) *MockAnalogOutput {
	return &MockAnalogOutput{id: id, min: min, max: max}
}

func (mao *MockAnalogOutput) GetMinMax() (int, int) {
	return mao.min, mao.max
}

func (mao *MockAnalogOutput) GetState() (int, error) {
	return mao.value, nil
}

func (mao *MockAnalogOutput) Set(value int) error {
	mao.value = value
	return nil
}

func (mao *MockAnalogOutput) String() string {
	return fmt.Sprintf("mock_analog_output:%s", mao.id)
}

// IsHealthy returns always true (mock)
func (mao *MockAnalogOutput) IsHealthy() bool {
	return true
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
	inputs        []*MockInput
	outputs       []*MockOutput
	analogOutputs []*MockAnalogOutput
	ready         bool
	logger        *log.Logger
}

func (md *MockIoDriver) Setup(ctx context.Context, ios []string) (err error) {
	md.logger = logging.NewLogger(logging.PrefixMock)

	// For mock driver, treat all ios as both inputs and outputs for testing.
	// Store by the resolved name part (e.g. "1"), since lookups use the name
	// returned by ResolveIoIdString, not the full "driver|type|name" id.
	for _, io := range ios {
		_, _, name, resolveErr := ResolveIoIdString(io)
		if resolveErr != nil {
			return resolveErr
		}
		md.inputs = append(md.inputs, &MockInput{id: name})
		md.outputs = append(md.outputs, &MockOutput{id: name})
	}
	md.ready = true
	md.logger.Debug("setup complete", "ioCount", len(ios))
	return nil
}

// SetupIO creates separate input and output lists for testing purposes.
func (md *MockIoDriver) SetupIO(ctx context.Context, inputs, outputs []string) error {
	md.logger = logging.NewLogger(logging.PrefixMock)
	for _, id := range inputs {
		md.inputs = append(md.inputs, &MockInput{id: id})
	}
	for _, id := range outputs {
		md.outputs = append(md.outputs, &MockOutput{id: id})
	}
	md.ready = true
	return nil
}

// GetOutput is an alias for GetDigitalOutput.
func (md *MockIoDriver) GetOutput(id string) (DigitalOutput, error) {
	return md.GetDigitalOutput(id)
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
	for _, output := range md.analogOutputs {
		if strings.EqualFold(id, output.id) {
			return output, nil
		}
	}
	// Create on demand with a default 0-100 range so wiring works without
	// explicit analog setup (mirrors how Setup pre-creates digital ios).
	mao := NewMockAnalogOutput(id, 0, 100)
	md.analogOutputs = append(md.analogOutputs, mao)
	return mao, nil
}

func (md *MockIoDriver) GetRgbwOutput(id string) (RgbwOutput, error) {
	return nil, fmt.Errorf("rgbw output not implemented in mock driver")
}

func (md *MockIoDriver) GetPushEventEmitter(id string) (PushEventEmitter, error) {
	return nil, fmt.Errorf("push event emitter not implemented in mock driver")
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

// Status returns a summary of the driver's current state
func (md *MockIoDriver) Status() string {
	return fmt.Sprintf("in:%d out:%d (mock)", len(md.inputs), len(md.outputs))
}
