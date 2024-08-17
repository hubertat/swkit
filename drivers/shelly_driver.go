package drivers

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"net/url"
	"time"

	"github.com/charmbracelet/log"

	"github.com/hubertat/swkit/drivers/shelly"
	"github.com/hubertat/swkit/mqtt"
)

const idSeparator byte = '|'

const shellyDriverName string = "shelly"

const setupDevicesTimeout = 15 * time.Second
const healthCheckInterval = 2 * time.Second
const unhealthyCountLimit = 5

type ShellyIO struct {
	MqttBroker   string
	MqttClientId string

	outputs []ShellyOutput
	inputs  []ShellyInput

	devices     []*shelly.ShellyDevice
	mqttHandler *shelly.ShellyMqtt
	mqttClient  *mqtt.MqttClient

	isReady        bool
	healthTicker   *time.Ticker
	done           chan bool
	originUrl      *url.URL
	unhealthyCount int
}

func (she *ShellyIO) Setup(ctx context.Context, inputs []string, outputs []string) (err error) {
	she.isReady = false
	logger := log.NewWithOptions(os.Stderr, log.Options{
		Prefix: "shell 🐢",
		Level:  log.GetLevel(),
	})

	devicesMap := make(map[string]bool)
	for _, output := range outputs {
		deviceId, ioNo, parsErr := she.parseIoId(output)
		if parsErr != nil {
			logger.Info("failed to parse device", "output id", output, "error", parsErr)
			parsErr = errors.Join(parsErr, errors.New("failed to parse output id: "+output))
			err = errors.Join(err, parsErr)
		} else {
			devicesMap[deviceId] = true
			she.outputs = append(she.outputs, ShellyOutput{deviceId: deviceId, switchNo: ioNo})
		}
	}

	for _, input := range inputs {
		deviceId, ioNo, parsErr := she.parseIoId(input)
		if parsErr != nil {
			logger.Info("failed to parse device", "input id", input, "error", parsErr)
			parsErr = errors.Join(parsErr, errors.New("failed to parse input id: "+input))
			err = errors.Join(err, parsErr)
		} else {
			devicesMap[deviceId] = true
			she.inputs = append(she.inputs, ShellyInput{deviceId: deviceId, inputNo: ioNo})
		}
	}

	logger.Debug("mapped devices", "deviceIds", devicesMap)

	if len(devicesMap) == 0 {
		err = errors.Join(err, errors.New("no device ids parsed from ios"))
		return
	}

	for deviceId := range devicesMap {
		she.devices = append(she.devices, &shelly.ShellyDevice{Id: deviceId})
	}

	logger.Debug("creating mqtt client")

	var mqErr error
	she.mqttClient, mqErr = mqtt.NewMqttClient(she.MqttBroker, she.MqttClientId)

	if mqErr != nil {
		mqErr = errors.Join(mqErr, errors.New("failed to create mqtt client"))
		err = errors.Join(err, mqErr)
		return
	}

	logger.Debug("creating mqtt handler")
	she.mqttHandler = shelly.NewShellyMqtt(she.devices, she.mqttClient)

	logger.Debug("connecting to mqtt broker")
	mqErr = she.mqttClient.Connect(ctx, []mqtt.MqttHandler{she.mqttHandler})
	if mqErr != nil {
		mqErr = errors.Join(mqErr, errors.New("failed to connect to mqtt broker"))
		err = errors.Join(err, mqErr)
		return
	}

	logger.Debug("trying to match devices")
	matchErr := she.tryToMatchDevices(10)
	if matchErr != nil {
		err = errors.Join(err, matchErr)
		return
	}

	// go she.startHealthCheck(ctx)

	she.isReady = true

	return
}

// matchDevices() will match defined ios with actual devices
// return error when device is not present and healthy or does not have specified io channel
func (she *ShellyIO) matchDevices() error {
	for ix, output := range she.outputs {
		dev := she.getDevice(output.deviceId)
		if dev == nil {
			return fmt.Errorf("device %s not found", output.deviceId)
		}

		if !dev.IsReady() {
			return fmt.Errorf("device %s is not ready", output.deviceId)
		}

		if len(dev.Switches) <= output.switchNo {
			return fmt.Errorf("device %s does not have switch %d", output.deviceId, output.switchNo)
		}

		output.dev = dev
		she.outputs[ix] = output
	}

	for ix, input := range she.inputs {
		dev := she.getDevice(input.deviceId)
		if dev == nil {
			return fmt.Errorf("device %s not found", input.deviceId)
		}

		if !dev.IsReady() {
			return fmt.Errorf("device %s is not ready", input.deviceId)
		}

		if len(dev.Switches) <= input.inputNo {
			return fmt.Errorf("device %s does not have input %d", input.deviceId, input.inputNo)
		}

		input.dev = dev
		she.inputs[ix] = input
	}

	return nil
}

// tryToMatchDevices(maxTries int) will try to match devices
// if it fails, it will retry until maxTries is reached
// considering total setupDevicesTimeout
func (she *ShellyIO) tryToMatchDevices(maxTries int) error {
	if maxTries <= 1 {
		return she.matchDevices()
	}

	matchTickPeriod := setupDevicesTimeout / time.Duration(maxTries)

	ticker := time.NewTicker(matchTickPeriod)
	defer ticker.Stop()

	var matchErr error

	for ix := 0; ix < maxTries; ix++ {
		select {
		case <-ticker.C:
			matchErr = she.matchDevices()
			if matchErr == nil {
				return nil
			}

		}
	}

	return matchErr
}

func (she *ShellyIO) getDevice(id string) *shelly.ShellyDevice {
	for _, dev := range she.devices {
		if strings.EqualFold(dev.Id, id) {
			return dev
		}
	}
	return nil
}

func (she *ShellyIO) parseIoId(id string) (string, int, error) {
	parts := strings.Split(id, string(idSeparator))
	if len(parts) != 2 {
		return "", 0, fmt.Errorf("invalid id format: %s", id)
	}

	port, err := strconv.Atoi(parts[1])
	if err != nil {
		return "", 0, fmt.Errorf("invalid port number: %s", parts[1])
	}

	return parts[0], port, nil
}

func (she *ShellyIO) Close() error {
	for _, dev := range she.devices {
		dev.Close()
	}
	she.isReady = false
	return nil
}

func (she *ShellyIO) String() string {
	return shellyDriverName
}

func (she *ShellyIO) IsReady() bool {
	return she.isReady
}

func (she *ShellyIO) GetInput(id string) (DigitalInput, error) {
	return nil, errors.New("inputs not implemented")
}

func (she *ShellyIO) GetOutput(id string) (DigitalOutput, error) {
	for _, out := range she.outputs {
		if strings.EqualFold(out.getStringId(), id) {
			return &out, nil
		}
	}

	return nil, fmt.Errorf("shelly output pin = %d not found", id)
}

func (she *ShellyIO) GetAllIo() (inputs []string, outputs []string) {
	for _, out := range she.outputs {
		outputs = append(outputs, out.getStringId())
	}
	return
}

type ShellyOutput struct {
	switchNo int
	deviceId string

	dev *shelly.ShellyDevice
}

func (sout *ShellyOutput) GetState() (bool, error) {
	if sout.dev == nil {
		return false, errors.New("shelly output internal Switch/Device nil error")
	}

	return sout.dev.GetOutputState(sout.switchNo)
}

func (sout *ShellyOutput) Set(state bool) error {
	if sout.dev == nil {
		return errors.New("shelly output internal Switch/Device nil error")
	}
	err := sout.dev.SetSwitch(sout.switchNo, state)
	if err != nil {
		return errors.Join(errors.New("failed to set shelly output state"), err)
	}
	return nil
}

// getStringId() string
// return string representation of shelly output
func (sout *ShellyOutput) getStringId() string {
	return fmt.Sprintf("%s%c%d", sout.deviceId, idSeparator, sout.switchNo)
}

type ShellyInput struct {
	inputNo  int
	deviceId string

	dev *shelly.ShellyDevice
}

func (sin *ShellyInput) GetState() (bool, error) {
	if sin.dev == nil {
		return false, errors.New("shelly input internal InputStatus/Device nil error")
	}

	return sin.dev.GetInputState(sin.inputNo)
}
