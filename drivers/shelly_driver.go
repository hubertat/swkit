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

const idSeparator = ":"
const shellyDriverName string = "shelly"

const setupDevicesTimeout = 45 * time.Second
const healthCheckInterval = 5 * time.Second
const unhealthyCountLimit = 5

func parseShellyIoId(ioId string) (string, int, error) {
	if len(ioId) == 0 {
		return "", 0, errors.New("empty io id")
	}

	split := strings.Split(ioId, idSeparator)
	if len(split) != 2 {
		return "", 0, errors.New("invalid io id format, expected 2 parts separated by '" + string(idSeparator) + "'")
	}

	deviceId := split[0]
	inputNoStr := split[1]

	inputNo, err := strconv.Atoi(inputNoStr)
	if err != nil {
		return "", 0, errors.New("invalid input number")
	}

	return deviceId, inputNo, nil
}

func getShellyIoId(deviceId string, inputNo int) string {
	return fmt.Sprintf("%s%s%d", deviceId, idSeparator, inputNo)
}

type ShellyIO struct {
	MqttBroker   string
	MqttClientId string

	outputs []ShellyOutput
	inputs  []ShellyInput

	devices []*shelly.ShellyDevice

	messenger *mqtt.JsonRpcMessenger

	isReady        bool
	healthTicker   *time.Ticker
	done           chan bool
	originUrl      *url.URL
	unhealthyCount int
}

func (she *ShellyIO) Setup(ctx context.Context, ios []string) (err error) {
	she.isReady = false
	logger := log.NewWithOptions(os.Stderr, log.Options{
		Prefix: "she🐢y",
		Level:  log.GetLevel(),
	})

	devicesMap := make(map[string]bool)
	for _, io := range ios {
		driver, ioType, ioId, err := resolveIoIdString(io)
		if err != nil {
			return errors.Join(err, errors.New("invalid io id format, expected 3 parts separated by '|'"))
		}

		if !strings.EqualFold(driver, she.String()) {
			return errors.New("invalid io, driver name mismatch")
		}

		actualDeviceId, ioNo, err := parseShellyIoId(ioId)
		if err != nil {
			return errors.Join(err, errors.New("invalid shelly io id format, expected 2 parts separated by '"+string(idSeparator)+"'"))
		}

		switch ioType {
		case ioTypeDigitalInput:
			devicesMap[actualDeviceId] = true
			she.inputs = append(she.inputs, ShellyInput{deviceId: actualDeviceId, inputNo: ioNo})

		case ioTypeDigitalOutput:
			devicesMap[actualDeviceId] = true
			she.outputs = append(she.outputs, ShellyOutput{deviceId: actualDeviceId, switchNo: ioNo})

		default:
			return errors.New("unsupported io type: " + ioType.String())
		}

	}

	logger.Debug("mapped devices", "deviceIds", devicesMap)

	if len(devicesMap) == 0 {
		err = errors.Join(err, errors.New("no device ids parsed from ios"))
		return
	}

	logger.Debug("creating mqtt client")
	mqttCli, mqErr := mqtt.NewMqttClient(she.MqttBroker, she.MqttClientId)
	if mqErr != nil {
		mqErr = errors.Join(mqErr, errors.New("failed to create mqtt client"))
		err = errors.Join(err, mqErr)
		return
	}

	logger.Debug("creating messenger (mqtt json rpc)")
	she.messenger, mqErr = mqtt.NewJsonRpcMessenger(ctx, mqttCli, she)
	if mqErr != nil {
		mqErr = errors.Join(mqErr, errors.New("failed to create mqtt json rpc messenger"))
		err = errors.Join(err, mqErr)
		return
	}

	for deviceId := range devicesMap {
		dev, err := shelly.NewShellyDevice(deviceId, she.messenger)
		if err != nil {
			err = errors.Join(err, errors.New("failed to create shelly device "+deviceId))
			return err
		}
		she.devices = append(she.devices, dev)
	}
	she.messenger.UpdateTopicRoots()

	logger.Debug("getting devices status")
	for _, dev := range she.devices {
		err = dev.GetStatus()
		if err != nil {
			return errors.Join(errors.New("failed to send GetStatus request for device "+dev.Id), err)
		}
	}

	logger.Debug("trying to match devices")

	matchTickDuration := 5 * time.Second
	matchTickCount := int(setupDevicesTimeout / matchTickDuration)
	matchErr := she.tryToMatchDevices(matchTickCount, matchTickDuration)

	if matchErr != nil {
		err = errors.Join(err, matchErr, errors.New("failed on matching devices"))
		return
	}

	go func() {
		for {
			select {
			case <-she.done:
				return
			case <-she.healthTicker.C:
				for _, d := range she.devices {
					d.GetStatus()
				}
			}
		}
	}()
	she.healthTicker = time.NewTicker(healthCheckInterval)

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
func (she *ShellyIO) tryToMatchDevices(maxTries int, matchTickPeriod time.Duration) error {
	if maxTries <= 1 {
		return she.matchDevices()
	}

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

func (she *ShellyIO) getDeviceByTopic(topic string) *shelly.ShellyDevice {
	for _, dev := range she.devices {
		if strings.HasPrefix(topic, dev.Id) {
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

func (she *ShellyIO) GetDigitalInput(id string) (DigitalInput, error) {
	return nil, errors.New("inputs not implemented")
}

func (she *ShellyIO) GetDigitalOutput(id string) (DigitalOutput, error) {
	for _, out := range she.outputs {
		if strings.EqualFold(out.getStringId(), id) {
			return &out, nil
		}
	}

	return nil, fmt.Errorf("shelly output pin = %d not found", id)
}

func (she *ShellyIO) GetAnalogOutput(id string) (AnalogOutput, error) {
	// TODO
	return nil, fmt.Errorf("analog output not implemented")
}

func (she *ShellyIO) GetRgbwOutput(id string) (RgbwOutput, error) {
	// TODO
	return nil, fmt.Errorf("rgbw output not implemented")
}

func (she *ShellyIO) GetAllIo() (inputs []string, outputs []string) {
	for _, out := range she.outputs {
		outputs = append(outputs, out.getStringId())
	}
	return
}

func (she *ShellyIO) MqttTopicRoots() []string {
	roots := make([]string, len(she.devices))
	for ix, dev := range she.devices {
		roots[ix] = dev.Id
	}
	return roots
}

func (she *ShellyIO) HandleRpcStatus(online bool, topic string) bool {
	dev := she.getDeviceByTopic(topic)
	if dev == nil {
		log.Warn("handling rpc status, device not found", "topic", topic)
		return false
	}

	log.Debug("got online status update", "device", dev.Id, "online", online)
	req := mqtt.RpcRequest{
		Dst:    dev.Id,
		Method: "Shelly.GetStatus",
	}
	err := she.messenger.SendRequest(dev.Id, req)
	if err != nil {
		log.Error("failed to send GetStatus request", "device", dev.Id, "error", err)
		return false
	}

	return true
}

func (she *ShellyIO) HandleRpcMessage(msg *mqtt.RpcMessage, topic string) bool {
	dev := she.getDevice(msg.Src)
	if dev == nil {
		log.Warn("handling rpc message, device not found", "device", msg.Src)
		return false
	}

	switch msg.MsgType {
	case mqtt.RpcResponseType:
		switch msg.Method {
		case "Shelly.GetStatus":
			status := shelly.GetStatus{}
			err := msg.UnmarshalResult(&status)
			if err != nil {
				log.Error("failed to unmarshal GetStatus response", "device", dev.Id, "error", err)
				return false
			}

			err = dev.FillStatus(status)
			if err != nil {
				log.Error("failed to fill device with status", "device", dev.Id, "error", err)
				return false
			}

			return true
		case "Switch.Set":
			log.Info("resp to handle", "method", msg.Method)
			return true
		default:
			log.Warn("handling response, unknown method", "method", msg.Method)
			return false
		}
	case mqtt.RpcNotificationType:
		switch msg.Method {
		case "NotifyStatus":
			status := shelly.GetStatus{}
			err := msg.UnmarshalParams(&status)
			if err != nil {
				log.Error("failed to unmarshal NotifyStatus params", "device", dev.Id, "error", err)
				return false
			}

			err = dev.UpdateFromStatus(status)
			if err != nil {
				log.Error("failed to update device from status", "device", dev.Id, "error", err)
				return false
			}

			return true
		default:
			log.Warn("handling notification, unknown method", "method", msg.Method)
			return false
		}

	default:
		return false
	}
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

func (sout *ShellyOutput) String() string {
	return fmt.Sprintf("shelly_output:%s:%d", sout.deviceId, sout.switchNo)
}

// getStringId() string
// return string representation of shelly output
func (sout *ShellyOutput) getStringId() string {
	return fmt.Sprintf("%s%s%d", sout.deviceId, idSeparator, sout.switchNo)
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

func (sin *ShellyInput) String() string {
	return fmt.Sprintf("shelly_input:%s:%d", sin.deviceId, sin.inputNo)
}

// PrintStatus() string prints status of device and its io in a readable way
func (sio *ShellyIO) PrintStatus() string {
	s := ""
	for _, dev := range sio.devices {
		s += fmt.Sprintf("%s\n", dev.String())
	}

	return s
}
