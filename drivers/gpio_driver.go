package drivers

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/log"
	"github.com/hubertat/swkit/logging"
	"github.com/hubertat/swkit/mqtt"

	"github.com/stianeikeland/go-rpio/v4"
)

const gpioDriverName = "gpio"
const filterLoopInterval = 1 * time.Millisecond

type GpIO struct {
	InvertInputs  bool
	InvertOutputs bool

	FilterInputsMs uint

	inputs         []GpInput
	outputs        []GpOutput
	isReady        bool
	filterTimer    *time.Ticker
	quitFilterLoop chan bool
	logger         *log.Logger
}

type GpInput struct {
	pin    uint8
	invert bool
	// debouncing/filter:
	filterDuration time.Duration
	filteredState  bool
	lastChanged    time.Time
	lastState      bool
}

type GpOutput struct {
	pin    uint8
	invert bool
}

func (gpi *GpInput) ioStateToBool(state rpio.State) bool {
	if gpi.invert {
		return state == 0
	} else {
		return state == 1
	}
}

func (gpi *GpInput) filterState() {
	currentState := gpi.ioStateToBool(rpio.Pin(gpi.pin).Read())

	if gpi.lastChanged.IsZero() {
		if currentState == gpi.filteredState {
			return
		}
		gpi.lastChanged = time.Now()
		gpi.lastState = currentState
	} else {
		if time.Since(gpi.lastChanged) >= gpi.filterDuration {
			if currentState == gpi.lastState {
				gpi.filteredState = currentState
			}
			gpi.lastChanged = time.Time{}
		}
	}
}

func (gpi *GpInput) GetState() (state bool, err error) {
	if gpi.filterDuration == 0 {
		return gpi.ioStateToBool(rpio.Pin(gpi.pin).Read()), nil
	}

	return gpi.filteredState, nil
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

func (gp *GpIO) filterLoop() {
	for {
		select {
		case <-gp.quitFilterLoop:
			return
		case <-gp.filterTimer.C:
			for ix := range gp.inputs {
				gp.inputs[ix].filterState()
			}
		}
	}
}

func (gp *GpIO) Setup(ctx context.Context, ios []string) error {
	gp.logger = logging.NewLogger(logging.PrefixGpio)

	err := rpio.Open()
	if err != nil {
		return errors.Join(err, fmt.Errorf("failed to Setup gpio driver: failed to open rpio"))
	}

	gp.logger.Debug("setup starting", "inputCount", len(ios))
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
			gp.inputs = append(gp.inputs, GpInput{
				pin:            uint8(pin),
				invert:         gp.InvertInputs,
				filterDuration: time.Millisecond * time.Duration(gp.FilterInputsMs),
			})

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

	gp.quitFilterLoop = make(chan bool)
	if gp.FilterInputsMs > 0 {
		if time.Duration(gp.FilterInputsMs)*time.Millisecond <= filterLoopInterval {
			return errors.New("failed to start filter loop: filter duration must be longer than loop interval (1ms)")
		}
		gp.filterTimer = time.NewTicker(filterLoopInterval)
		go gp.filterLoop()
	}

	for ix, in := range gp.inputs {
		if in.filterDuration > 0 {
			in.filterDuration = 0
			state, _ := in.GetState()
			gp.inputs[ix].filteredState = state
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

	if gp.filterTimer != nil {
		gp.filterTimer.Stop()
		gp.quitFilterLoop <- true
		close(gp.quitFilterLoop)
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
	for ix, in := range gp.inputs {
		if in.pin == uint8(pin) {
			input = &gp.inputs[ix]
			return
		}
	}

	err = fmt.Errorf("GpIO Input (id: %s) not found", id)
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
	for ix, out := range gp.outputs {
		if out.pin == uint8(pin) {
			output = &gp.outputs[ix]
			return
		}
	}

	err = fmt.Errorf("GpIO Output (id: %s) not found", id)
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

// GetPushEventEmitter returns a PushEventEmitter for the given pin.
func (gp *GpIO) GetPushEventEmitter(id string) (PushEventEmitter, error) {
	return nil, errors.New("push event emitter not implemented in GPIO driver")
}

// Status returns a summary of the driver's current state
func (gp *GpIO) Status() string {
	filterInfo := ""
	if gp.FilterInputsMs > 0 {
		filterInfo = fmt.Sprintf(" filter:%dms", gp.FilterInputsMs)
	}
	return fmt.Sprintf("in:%d out:%d%s", len(gp.inputs), len(gp.outputs), filterInfo)
}
