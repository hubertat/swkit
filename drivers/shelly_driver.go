package drivers

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"net/url"
	"time"

	"github.com/charmbracelet/log"

	"github.com/hubertat/swkit/drivers/shelly"
	"github.com/hubertat/swkit/drivers/shelly/components"
	"github.com/hubertat/swkit/drivers/shelly/events"
	"github.com/hubertat/swkit/logging"
	"github.com/hubertat/swkit/mqtt"
)

const idSeparator = ":"
const shellyDriverName string = "shelly"

const firstMatchDelay = 2 * time.Second
const periodicMatchInterval = 300 * time.Second
const healthCheckInterval = 5 * time.Second
const stateUpToDateDuration = 5 * time.Minute
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

	outputs []*ShellyOutput
	inputs  []*ShellyInput

	devices []*shelly.ShellyDevice

	messenger *mqtt.JsonRpcMessenger

	healthTicker *time.Ticker
	matchTicker  *time.Ticker

	isReady        bool
	done           chan bool
	originUrl      *url.URL
	unhealthyCount int
	logger         *log.Logger
}

func (she *ShellyIO) Setup(ctx context.Context, ios []string) (err error) {
	she.isReady = false
	she.logger = logging.NewLogger(logging.PrefixShelly)
	logger := she.logger

	devicesMap := make(map[string]bool)
	for _, io := range ios {
		driver, ioType, ioId, err := ResolveIoIdString(io)
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

		logger.Debug("will process io", "id", ioId, "type", ioType.String())

		switch ioType {
		case IoTypeDigitalInput:
			devicesMap[actualDeviceId] = true
			she.inputs = append(she.inputs, &ShellyInput{deviceId: actualDeviceId, inputNo: ioNo})
			logger.Debug("adding d_in", "dev id:", actualDeviceId, "io no:", ioNo)

		case IoTypeDigitalOutput:
			devicesMap[actualDeviceId] = true
			she.outputs = append(she.outputs, &ShellyOutput{deviceId: actualDeviceId, switchNo: ioNo})
			logger.Debug("adding d_out", "dev id:", actualDeviceId, "io no:", ioNo)

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

	err = she.messenger.ConnectMqttClient(ctx)
	if err != nil {
		err = errors.Join(err, errors.New("failed to connect mqtt client"))
		return err
	}

	logger.Info("getting devices status")
	for _, dev := range she.devices {
		err = dev.GetStatus()
		logger.Debug("device status request sent", "device", dev.Id, "err", err)
		if err != nil {
			return errors.Join(errors.New("failed to send GetStatus request for device "+dev.Id), err)
		}
	}

	logger.Info("trying to match devices")
	time.Sleep(firstMatchDelay)
	matchErr := she.matchDevices()
	if matchErr != nil {
		logger.Warn("first match fo shelly devices failed", "err", matchErr)
		go func() {
			logger.Info("trying to match devices again")
			time.Sleep(firstMatchDelay)
			matchErr = she.matchDevices()
			if matchErr != nil {
				logger.Warn("failed to match devices (2nd match)", "err", matchErr)
			}
		}()
	}

	logger.Info("periodic matching set", "interval", periodicMatchInterval)

	she.matchTicker = time.NewTicker(periodicMatchInterval)
	she.healthTicker = time.NewTicker(healthCheckInterval)
	go func() {
		for {
			select {
			case <-she.done:
				return
			case <-she.matchTicker.C:
				for _, d := range she.devices {
					if !d.IsReady() {
						log.Info("healthTicker: found unititialized device, will try to match", "id", d.Id)

						matchErr = she.matchDevices()
						if matchErr != nil {
							logger.Warn("failed to match devices", "err", matchErr)
						}
					}
				}

			case <-she.healthTicker.C:
				for _, d := range she.devices {
					if d.IsReady() && d.SinceLastRefreshed() > stateUpToDateDuration {
						log.Debug("healthTicker: refreshing device", "id", d.Id)
						d.GetStatus()
					}
				}
			}
		}
	}()

	// go she.startHealthCheck(ctx)

	logger.Info("shelly driver setup finished")
	she.isReady = true

	return
}

// matchDevices() will match defined ios with actual devices
// return error when device is not present and healthy or does not have specified io channel
func (she *ShellyIO) matchDevices() error {
	var err error
	for ix, output := range she.outputs {
		if output.dev == nil {
			dev := she.getDevice(output.deviceId)
			if dev == nil {
				err = errors.Join(err, fmt.Errorf("device %s not found", output.deviceId))
			} else {
				if !dev.IsReady() {
					err = errors.Join(err, fmt.Errorf("device %s is not ready", output.deviceId))
				} else {
					if len(dev.Switches) <= output.switchNo {
						err = errors.Join(err, fmt.Errorf("device %s does not have switch %d", output.deviceId, output.switchNo))
					} else {
						output.dev = dev
						she.outputs[ix] = output
					}
				}
			}
		}
	}

	for ix, input := range she.inputs {
		if input.dev == nil {
			dev := she.getDevice(input.deviceId)
			if dev == nil {
				err = errors.Join(err, fmt.Errorf("device %s not found", input.deviceId))
			} else {
				if !dev.IsReady() {
					err = errors.Join(err, fmt.Errorf("device %s is not ready", input.deviceId))
				} else {
					if len(dev.Switches) <= input.inputNo {
						err = errors.Join(err, fmt.Errorf("device %s does not have input %d", input.deviceId, input.inputNo))
					} else {
						input.dev = dev
						she.inputs[ix] = input
					}
				}
			}
		}
	}

	return err
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
			return out, nil
		}
	}

	return nil, fmt.Errorf("shelly output: %s not found", id)
}

func (she *ShellyIO) GetAnalogOutput(id string) (AnalogOutput, error) {
	// TODO
	return nil, fmt.Errorf("analog output not implemented")
}

func (she *ShellyIO) GetRgbwOutput(id string) (RgbwOutput, error) {
	// TODO
	return nil, fmt.Errorf("rgbw output not implemented")
}

func (she *ShellyIO) GetPushEventEmitter(id string) (PushEventEmitter, error) {
	for _, in := range she.inputs {
		if strings.EqualFold(in.getStringId(), id) {
			return in, nil
		}
	}

	return nil, fmt.Errorf("shelly event emitter: %s not found", id)
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

			// Capture state before updating to detect changes
			outStates := she.captureOutputStates(dev.Id)

			log.Debug("will fill from status", "switches", status.GetSwitches())
			err = dev.FillStatus(status)
			if err != nil {
				log.Error("failed to fill device with status", "device", dev.Id, "error", err)
				return false
			}

			she.notifyStateChanges(outStates, msg.MsgType.String(), msg.Method)

			return true
		case "Switch.Set":
			// For Switch.Set responses, capture current state and check after a brief delay
			// to allow for any device state synchronization
			outStates := she.captureOutputStates(dev.Id)
			go func() {
				time.Sleep(50 * time.Millisecond) // Brief delay for state to settle
				she.notifyStateChanges(outStates, msg.MsgType.String(), msg.Method)
			}()
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

			// Capture state before updating to detect changes
			outStates := she.captureOutputStates(dev.Id)

			log.Debug("will update from status", "switches", status.GetSwitches())
			err = dev.UpdateFromStatus(status)
			if err != nil {
				log.Error("failed to update device from status", "device", dev.Id, "error", err)
				return false
			}

			she.notifyStateChanges(outStates, msg.MsgType.String(), msg.Method)

			return true
		case "NotifyEvent":
			raw := shelly.RawEvents{}
			err := msg.UnmarshalParams(&raw)
			if err != nil {
				log.Error("failed to unmarshal NotifyEvent params", "device", dev.Id, "error", err)
				return false
			}

			evs, err := raw.GetEvents()
			if err != nil {
				log.Error("failed to get events from raw", "device", dev.Id, "error", err)
				return false
			}

			for _, e := range evs {
				switch e.ComponentType {
				case components.ComponentTypeInput:
					for _, in := range she.inputs {
						if uint(in.inputNo) == e.ComponentId {
							in.findAndFireEvent(e.EventType)
						}
					}
				default:
					log.Debug("unsupported component type", "device", dev.Id, "componentType", e.ComponentType)
				}
			}

			if err != nil {
				log.Error("failed to parse events", "device", dev.Id, "error", err)
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

// captureOutputStates captures current states for outputs belonging to a specific device
func (she *ShellyIO) captureOutputStates(deviceId string) map[string]bool {
	outStates := map[string]bool{}
	for _, o := range she.outputs {
		if o.deviceId == deviceId {
			if state, err := o.dev.GetOutputState(o.switchNo); err == nil {
				outStates[o.String()] = state
			}
		}
	}
	return outStates
}

// notifyStateChanges compares current states with captured states and notifies callbacks
func (she *ShellyIO) notifyStateChanges(oldStates map[string]bool, msgType, method string) {
	for _, o := range she.outputs {
		if oldState, present := oldStates[o.String()]; present && o.onStateUpdate != nil {
			if state, err := o.dev.GetOutputState(o.switchNo); err == nil && state != oldState {
				log.Info("State change detected", "output", o.String(), "old", oldState, "new", state, "msgType", msgType, "method", method)
				o.onStateUpdate(state)
			}
		}
	}
}

type ShellyOutput struct {
	switchNo int
	deviceId string

	onStateUpdate func(bool)

	dev *shelly.ShellyDevice
}

func (sout *ShellyOutput) SetOnStateUpdate(onStateUpdate func(bool)) error {
	if onStateUpdate == nil {
		return errors.New("onStateUpdate function cannot be nil")
	}
	sout.onStateUpdate = onStateUpdate
	return nil
}

func (sout *ShellyOutput) GetState() (bool, error) {
	if sout.dev == nil {
		return false, fmt.Errorf("shelly device (%s) internal dev nil error", sout.deviceId)
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
	return fmt.Sprintf("shelly:%s:switch%d", sout.deviceId, sout.switchNo)
}

// getStringId() string
// return string representation of shelly output
func (sout *ShellyOutput) getStringId() string {
	return fmt.Sprintf("%s%s%d", sout.deviceId, idSeparator, sout.switchNo)
}

func (sout *ShellyOutput) IsHealthy() bool {
	if sout.dev == nil {
		return false
	}
	return sout.dev.HealthCheck() == nil
}

type ShellyInput struct {
	inputNo  int
	deviceId string

	dev *shelly.ShellyDevice

	subscriptions []struct {
		shellyEvent events.ShellyEventType
		swkitEvent  PushEvent
		handler     func(PushEvent)
	}
}

func (sin *ShellyInput) getStringId() string {
	return fmt.Sprintf("%s%s%d", sin.deviceId, idSeparator, sin.inputNo)
}

func (sin *ShellyInput) Subscribe(eventType PushEvent, handler func(PushEvent)) error {
	// TODO: make sure its not required and delete
	// if sin.dev == nil {
	// 	return errors.New("shelly input internal InputStatus/Device nil error")
	// }

	var shellE events.ShellyEventType
	switch eventType {
	case PushEventSinglePress:
		shellE = events.ShellyEventSinglePush

	case PushEventDoublePress:
		shellE = events.ShellyEventDoublePush

	case PushEventTriplePress:
		shellE = events.ShellyEventTriplePush

	case PushEventLongPress:
		shellE = events.ShellyEventLongPush

	default:
		return errors.New("unsupported event type for shelly input: " + eventType.String())
	}

	sin.subscriptions = append(sin.subscriptions, struct {
		shellyEvent events.ShellyEventType
		swkitEvent  PushEvent
		handler     func(PushEvent)
	}{
		shellyEvent: shellE,
		swkitEvent:  eventType,
		handler:     handler,
	})

	return nil
}

func (sin *ShellyInput) findAndFireEvent(shellyEventType events.ShellyEventType) bool {
	fired := false
	for _, sub := range sin.subscriptions {
		if sub.shellyEvent == shellyEventType {
			sub.handler(sub.swkitEvent)
			fired = true
		}
	}

	return fired
}

func (sin *ShellyInput) GetState() (bool, error) {
	if sin.dev == nil {
		return false, fmt.Errorf("shelly device (%s) internal dev nil error", sin.deviceId)
	}

	return sin.dev.GetInputState(sin.inputNo)
}

func (sin *ShellyInput) String() string {
	return fmt.Sprintf("shelly:%s:input%d", sin.deviceId, sin.inputNo)
}

func (sin *ShellyInput) IsHealthy() bool {
	if sin.dev == nil {
		return false
	}
	return sin.dev.HealthCheck() == nil
}

// PrintStatus() string prints status of device and its io in a readable way
func (sio *ShellyIO) PrintStatus() string {
	s := ""
	for _, dev := range sio.devices {
		s += fmt.Sprintf("%s\n", dev.String())
	}

	return s
}

// Status returns a summary of the driver's current state
func (sio *ShellyIO) Status() string {
	readyCount := 0
	for _, dev := range sio.devices {
		if dev.IsReady() {
			readyCount++
		}
	}
	broker := sio.MqttBroker
	if len(broker) > 25 {
		broker = broker[:22] + "..."
	}
	return fmt.Sprintf("devices:%d/%d broker:%s", readyCount, len(sio.devices), broker)
}
