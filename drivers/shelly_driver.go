package drivers

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"net/url"
	"time"

	"github.com/charmbracelet/log"
	"github.com/eclipse/paho.golang/paho"

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

// parseShellyIoId parses a shelly io name into its parts. Two forms are accepted:
//   - "<device>:<index>"             (legacy; component defaults to switch/input by io type)
//   - "<device>:<component>:<index>" (explicit component, e.g. "<device>:light:0")
//
// The returned component is lower-cased and may be empty for the legacy form.
func parseShellyIoId(ioId string) (deviceId string, component string, ioNo int, err error) {
	if len(ioId) == 0 {
		return "", "", 0, errors.New("empty io id")
	}

	split := strings.Split(ioId, idSeparator)
	switch len(split) {
	case 2:
		deviceId = split[0]
	case 3:
		deviceId = split[0]
		component = strings.ToLower(split[1])
	default:
		return "", "", 0, errors.New("invalid shelly io id format, expected '<device>:<index>' or '<device>:<component>:<index>'")
	}

	ioNo, err = strconv.Atoi(split[len(split)-1])
	if err != nil {
		return "", "", 0, errors.New("invalid io index number")
	}

	return deviceId, component, ioNo, nil
}

// shellyOutputKind distinguishes which Shelly component backs a digital output.
type shellyOutputKind int

const (
	outputKindSwitch shellyOutputKind = iota
	outputKindLight
)

const componentLight = "light"

func getShellyIoId(deviceId string, inputNo int) string {
	return fmt.Sprintf("%s%s%d", deviceId, idSeparator, inputNo)
}

type ShellyIO struct {
	MqttBroker   string
	MqttClientId string
	DiscoverAll  bool // when true, auto-discover all devices on the MQTT broker

	outputs       []*ShellyOutput
	inputs        []*ShellyInput
	analogOutputs []*ShellyAnalogOutput

	devices    []*shelly.ShellyDevice
	topicRoots map[string]string // topicRoot → deviceId for non-default topic roots

	messenger *mqtt.JsonRpcMessenger

	healthTicker *time.Ticker
	matchTicker  *time.Ticker

	isReady        bool
	done           chan bool
	originUrl      *url.URL
	unhealthyCount int
	logger         *log.Logger

	ioMu      sync.RWMutex // protects outputs and inputs slices
	devicesMu sync.RWMutex // protects devices slice and topicRoots map
	stateMu   sync.RWMutex // protects outputLastChanged and inputLastEvent maps

	outputLastChanged map[string]time.Time // key: "deviceId:switchNo"
	inputLastEvent    map[string]time.Time // key: "deviceId:inputNo"
	prevSwitchStates  map[string]bool      // key: "deviceId:switchNo", last known state for change detection
}

func (she *ShellyIO) Setup(ctx context.Context, ios []string) (err error) {
	she.isReady = false
	she.done = make(chan bool, 1)
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

		actualDeviceId, component, ioNo, err := parseShellyIoId(ioId)
		if err != nil {
			return errors.Join(err, errors.New("invalid shelly io id, expected '<device>:<index>' or '<device>:<component>:<index>'"))
		}

		logger.Debug("will process io", "id", ioId, "type", ioType.String(), "component", component)

		switch ioType {
		case IoTypeDigitalInput, IoTypePushEventEmitter:
			devicesMap[actualDeviceId] = true
			she.inputs = append(she.inputs, &ShellyInput{
				deviceId: actualDeviceId,
				inputNo:  ioNo,
				logger:   logger,
			})
			logger.Debug("adding input", "type", ioType.String(), "dev id:", actualDeviceId, "io no:", ioNo)

		case IoTypeDigitalOutput:
			devicesMap[actualDeviceId] = true
			kind := outputKindSwitch
			if component == componentLight {
				kind = outputKindLight
			}
			she.outputs = append(she.outputs, &ShellyOutput{deviceId: actualDeviceId, switchNo: ioNo, kind: kind})
			logger.Debug("adding d_out", "dev id:", actualDeviceId, "io no:", ioNo, "kind", component)

		case IoTypeAnalogOutput:
			if component != componentLight {
				return fmt.Errorf("shelly analog output requires the 'light' component, e.g. '<device>:light:<index>' (got io id: %s)", ioId)
			}
			devicesMap[actualDeviceId] = true
			she.analogOutputs = append(she.analogOutputs, &ShellyAnalogOutput{deviceId: actualDeviceId, lightNo: ioNo})
			logger.Debug("adding a_out (light brightness)", "dev id:", actualDeviceId, "io no:", ioNo)

		default:
			return errors.New("unsupported io type: " + ioType.String())
		}

	}

	logger.Debug("mapped devices", "deviceIds", devicesMap)

	if len(devicesMap) == 0 && !she.DiscoverAll {
		err = errors.Join(err, errors.New("no device ids parsed from ios"))
		return
	}

	she.topicRoots = make(map[string]string)
	she.outputLastChanged = make(map[string]time.Time)
	she.inputLastEvent = make(map[string]time.Time)
	she.prevSwitchStates = make(map[string]bool)

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

	she.devicesMu.Lock()
	for deviceId := range devicesMap {
		dev, devErr := shelly.NewShellyDevice(deviceId, she.messenger)
		if devErr != nil {
			she.devicesMu.Unlock()
			return errors.Join(devErr, errors.New("failed to create shelly device "+deviceId))
		}
		she.devices = append(she.devices, dev)
	}
	she.devicesMu.Unlock()

	var extraHandlers []mqtt.MqttHandler
	if she.DiscoverAll {
		extraHandlers = append(extraHandlers, &shellyDiscoveryHandler{ctx: ctx, parent: she})
	}

	err = she.messenger.ConnectMqttClientWithHandlers(ctx, extraHandlers)
	if err != nil {
		err = errors.Join(err, errors.New("failed to connect mqtt client"))
		return err
	}

	if she.DiscoverAll {
		logger.Info("DiscoverAll enabled, sending announce command")
		if pubErr := she.messenger.Publish("shellies/command", []byte("announce")); pubErr != nil {
			logger.Warn("failed to publish announce command", "err", pubErr)
		}
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
				she.devicesMu.RLock()
				needsMatch := false
				for _, d := range she.devices {
					if !d.IsReady() {
						she.logger.Info("matchTicker: found uninitialized device, will try to match", "id", d.Id)
						needsMatch = true
					}
				}
				she.devicesMu.RUnlock()
				if needsMatch {
					matchErr = she.matchDevices()
					if matchErr != nil {
						logger.Warn("failed to match devices", "err", matchErr)
					}
				}

			case <-she.healthTicker.C:
				she.devicesMu.RLock()
				var toRefresh []*shelly.ShellyDevice
				for _, d := range she.devices {
					if d.IsReady() && d.SinceLastRefreshed() > stateUpToDateDuration {
						toRefresh = append(toRefresh, d)
					}
				}
				she.devicesMu.RUnlock()
				for _, d := range toRefresh {
					she.logger.Debug("healthTicker: refreshing device", "id", d.Id)
					if err := d.GetStatus(); err != nil {
						logger.Warn("healthTicker: failed to send GetStatus", "id", d.Id, "err", err)
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
	she.ioMu.Lock()
	defer she.ioMu.Unlock()

	var err error
	for ix, output := range she.outputs {
		if output.dev == nil {
			dev := she.getDevice(output.deviceId)
			if dev == nil {
				err = errors.Join(err, fmt.Errorf("device %s not found", output.deviceId))
			} else if !dev.IsReady() {
				err = errors.Join(err, fmt.Errorf("device %s is not ready", output.deviceId))
			} else if output.kind == outputKindLight {
				if dev.LightCount() <= output.switchNo {
					err = errors.Join(err, fmt.Errorf("device %s does not have light %d", output.deviceId, output.switchNo))
				} else {
					output.dev = dev
					she.outputs[ix] = output
				}
			} else if dev.SwitchCount() <= output.switchNo {
				err = errors.Join(err, fmt.Errorf("device %s does not have switch %d", output.deviceId, output.switchNo))
			} else {
				output.dev = dev
				she.outputs[ix] = output
			}
		}
	}

	for ix, aout := range she.analogOutputs {
		if aout.dev == nil {
			dev := she.getDevice(aout.deviceId)
			if dev == nil {
				err = errors.Join(err, fmt.Errorf("device %s not found", aout.deviceId))
			} else if !dev.IsReady() {
				err = errors.Join(err, fmt.Errorf("device %s is not ready", aout.deviceId))
			} else if dev.LightCount() <= aout.lightNo {
				err = errors.Join(err, fmt.Errorf("device %s does not have light %d", aout.deviceId, aout.lightNo))
			} else {
				aout.dev = dev
				she.analogOutputs[ix] = aout
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
					if dev.InputCount() <= input.inputNo {
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
	she.devicesMu.RLock()
	defer she.devicesMu.RUnlock()
	return she.getDeviceLocked(id)
}

// getDeviceLocked must be called with devicesMu held (at least read-locked).
func (she *ShellyIO) getDeviceLocked(id string) *shelly.ShellyDevice {
	for _, dev := range she.devices {
		if strings.EqualFold(dev.Id, id) {
			return dev
		}
	}
	return nil
}

func (she *ShellyIO) getDeviceByTopic(topic string) *shelly.ShellyDevice {
	she.devicesMu.RLock()
	defer she.devicesMu.RUnlock()
	for _, dev := range she.devices {
		if strings.HasPrefix(topic, dev.Id) {
			return dev
		}
	}
	for topicRoot, devId := range she.topicRoots {
		if strings.HasPrefix(topic, topicRoot) {
			return she.getDeviceLocked(devId)
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
	if she.done != nil {
		she.done <- true
	}
	if she.matchTicker != nil {
		she.matchTicker.Stop()
	}
	if she.healthTicker != nil {
		she.healthTicker.Stop()
	}
	she.devicesMu.RLock()
	for _, dev := range she.devices {
		dev.Close()
	}
	she.devicesMu.RUnlock()
	if she.messenger != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := she.messenger.Disconnect(ctx); err != nil {
			return err
		}
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
	she.ioMu.RLock()
	defer she.ioMu.RUnlock()
	for _, out := range she.outputs {
		if strings.EqualFold(out.getStringId(), id) {
			return out, nil
		}
	}

	return nil, fmt.Errorf("shelly output: %s not found", id)
}

func (she *ShellyIO) GetAnalogOutput(id string) (AnalogOutput, error) {
	she.ioMu.RLock()
	defer she.ioMu.RUnlock()
	for _, aout := range she.analogOutputs {
		if strings.EqualFold(aout.getStringId(), id) {
			return aout, nil
		}
	}

	return nil, fmt.Errorf("shelly analog output: %s not found", id)
}

func (she *ShellyIO) GetRgbwOutput(id string) (RgbwOutput, error) {
	// TODO
	return nil, fmt.Errorf("rgbw output not implemented")
}

func (she *ShellyIO) GetPushEventEmitter(id string) (PushEventEmitter, error) {
	she.ioMu.RLock()
	defer she.ioMu.RUnlock()
	for _, in := range she.inputs {
		if strings.EqualFold(in.getStringId(), id) {
			return in, nil
		}
	}

	return nil, fmt.Errorf("shelly event emitter: %s not found", id)
}

func (she *ShellyIO) GetAllIo() (inputs []string, outputs []string) {
	she.ioMu.RLock()
	defer she.ioMu.RUnlock()
	for _, out := range she.outputs {
		outputs = append(outputs, out.getStringId())
	}
	return
}

func (she *ShellyIO) MqttTopicRoots() []string {
	she.devicesMu.RLock()
	defer she.devicesMu.RUnlock()
	var roots []string
	for _, dev := range she.devices {
		roots = append(roots, dev.Id)
	}
	for topicRoot := range she.topicRoots {
		roots = append(roots, topicRoot)
	}
	return roots
}

func (she *ShellyIO) HandleRpcStatus(online bool, topic string) bool {
	dev := she.getDeviceByTopic(topic)
	if dev == nil {
		she.logger.Warn("handling rpc status, device not found", "topic", topic)
		return false
	}

	she.logger.Debug("got online status update", "device", dev.Id, "online", online)
	req := mqtt.RpcRequest{
		Dst:    dev.Id,
		Method: "Shelly.GetStatus",
	}
	err := she.messenger.SendRequest(dev.Id, req)
	if err != nil {
		she.logger.Error("failed to send GetStatus request", "device", dev.Id, "error", err)
		return false
	}

	return true
}

func (she *ShellyIO) HandleRpcMessage(msg *mqtt.RpcMessage, topic string) bool {
	dev := she.getDevice(msg.Src)
	if dev == nil {
		she.logger.Warn("handling rpc message, device not found", "device", msg.Src)
		return false
	}

	switch msg.MsgType {
	case mqtt.RpcResponseType:
		switch msg.Method {
		case "Shelly.GetStatus":
			status := shelly.GetStatus{}
			err := msg.UnmarshalResult(&status)
			if err != nil {
				she.logger.Error("failed to unmarshal GetStatus response", "device", dev.Id, "error", err)
				return false
			}

			// Capture state before updating to detect changes
			outStates := she.captureOutputStates(dev.Id)

			she.logger.Debug("will fill from status", "switches", status.GetSwitches())
			err = dev.FillStatus(status)
			if err != nil {
				she.logger.Error("failed to fill device with status", "device", dev.Id, "error", err)
				return false
			}

			she.notifyStateChanges(outStates, msg.MsgType.String(), msg.Method)
			she.detectSwitchChanges(dev)

			return true
		case "Switch.Set":
			// For Switch.Set responses, capture current state and check after a brief delay
			// to allow for any device state synchronization
			outStates := she.captureOutputStates(dev.Id)
			go func() {
				time.Sleep(50 * time.Millisecond) // Brief delay for state to settle
				she.notifyStateChanges(outStates, msg.MsgType.String(), msg.Method)
			}()
			she.logger.Info("resp to handle", "method", msg.Method)
			return true
		default:
			she.logger.Warn("handling response, unknown method", "method", msg.Method)
			return false
		}
	case mqtt.RpcNotificationType:
		switch msg.Method {
		case "NotifyStatus":
			status := shelly.GetStatus{}
			err := msg.UnmarshalParams(&status)
			if err != nil {
				she.logger.Error("failed to unmarshal NotifyStatus params", "device", dev.Id, "error", err)
				return false
			}

			// Capture state before updating to detect changes
			outStates := she.captureOutputStates(dev.Id)

			she.logger.Debug("will update from status", "switches", status.GetSwitches())
			err = dev.UpdateFromStatus(status)
			if err != nil {
				she.logger.Error("failed to update device from status", "device", dev.Id, "error", err)
				return false
			}

			she.notifyStateChanges(outStates, msg.MsgType.String(), msg.Method)
			she.detectSwitchChanges(dev)

			return true
		case "NotifyEvent":
			raw := shelly.RawEvents{}
			err := msg.UnmarshalParams(&raw)
			if err != nil {
				she.logger.Error("failed to unmarshal NotifyEvent params", "device", dev.Id, "error", err)
				return false
			}

			evs, err := raw.GetEvents()
			if err != nil {
				she.logger.Error("failed to get events from raw", "device", dev.Id, "error", err)
				return false
			}

			for _, e := range evs {
				switch e.ComponentType {
				case components.ComponentTypeInput:
					she.ioMu.RLock()
					for _, in := range she.inputs {
						if in.deviceId == dev.Id && uint(in.inputNo) == e.ComponentId {
							if !in.findAndFireEvent(e.EventType) {
								she.logger.Debug("shelly input event not handled (no matching subscription)", "device", dev.Id, "input", in.inputNo, "event", e.EventType)
							}
						}
					}
					she.ioMu.RUnlock()
					key := fmt.Sprintf("%s:%d", dev.Id, e.ComponentId)
					she.stateMu.Lock()
					she.inputLastEvent[key] = time.Now()
					she.stateMu.Unlock()
				default:
					she.logger.Debug("unsupported component type", "device", dev.Id, "componentType", e.ComponentType)
				}
			}

			if err != nil {
				she.logger.Error("failed to parse events", "device", dev.Id, "error", err)
				return false
			}

			return true
		default:
			she.logger.Warn("handling notification, unknown method", "method", msg.Method)
			return false
		}

	default:
		return false
	}
}

// captureOutputStates captures current states for outputs belonging to a specific device
func (she *ShellyIO) captureOutputStates(deviceId string) map[string]bool {
	outStates := map[string]bool{}
	she.ioMu.RLock()
	defer she.ioMu.RUnlock()
	for _, o := range she.outputs {
		if o.deviceId == deviceId {
			if state, err := o.dev.GetOutputState(o.switchNo); err == nil {
				outStates[o.String()] = state
			}
		}
	}
	return outStates
}

// notifyStateChanges compares current states with captured states and notifies callbacks.
// Callbacks are invoked outside the lock to avoid holding ioMu during user-provided handlers.
func (she *ShellyIO) notifyStateChanges(oldStates map[string]bool, msgType, method string) {
	type pendingNotification struct {
		name     string
		key      string
		newState bool
		callback func(bool)
		oldState bool
	}

	she.ioMu.RLock()
	var pending []pendingNotification
	for _, o := range she.outputs {
		if oldState, present := oldStates[o.String()]; present && o.onStateUpdate != nil {
			if state, err := o.dev.GetOutputState(o.switchNo); err == nil && state != oldState {
				key := fmt.Sprintf("%s:%d", o.deviceId, o.switchNo)
				pending = append(pending, pendingNotification{
					name:     o.String(),
					key:      key,
					newState: state,
					callback: o.onStateUpdate,
					oldState: oldState,
				})
			}
		}
	}
	she.ioMu.RUnlock()

	if len(pending) > 0 {
		now := time.Now()
		she.stateMu.Lock()
		for _, n := range pending {
			she.outputLastChanged[n.key] = now
		}
		she.stateMu.Unlock()
	}

	for _, n := range pending {
		she.logger.Info("State change detected", "output", n.name, "old", n.oldState, "new", n.newState, "msgType", msgType, "method", method)
		n.callback(n.newState)
	}
}

// detectSwitchChanges compares each switch's current state against the last recorded state
// and updates outputLastChanged for any that changed. Works for all discovered devices,
// regardless of whether they have entries in she.outputs.
// It also snapshots the current state into prevSwitchStates for the next comparison.
func (she *ShellyIO) detectSwitchChanges(dev *shelly.ShellyDevice) {
	type kv struct {
		key   string
		state bool
	}
	var current []kv
	for i := 0; i < dev.SwitchCount(); i++ {
		if state, err := dev.GetOutputState(i); err == nil {
			current = append(current, kv{fmt.Sprintf("%s:%d", dev.Id, i), state})
		}
	}
	if len(current) == 0 {
		return
	}
	now := time.Now()
	she.stateMu.Lock()
	for _, s := range current {
		if prev, ok := she.prevSwitchStates[s.key]; ok && prev != s.state {
			she.outputLastChanged[s.key] = now
		}
		she.prevSwitchStates[s.key] = s.state
	}
	she.stateMu.Unlock()
}

type ShellyOutput struct {
	switchNo int
	deviceId string
	kind     shellyOutputKind // switch (relay) or light (dimmer on/off)

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

	if sout.kind == outputKindLight {
		on, _, err := sout.dev.GetLightState(sout.switchNo)
		return on, err
	}
	return sout.dev.GetOutputState(sout.switchNo)
}

func (sout *ShellyOutput) Set(state bool) error {
	if sout.dev == nil {
		return errors.New("shelly output internal Switch/Device nil error")
	}
	var err error
	if sout.kind == outputKindLight {
		err = sout.dev.SetLight(sout.switchNo, &state, nil)
	} else {
		err = sout.dev.SetSwitch(sout.switchNo, state)
	}
	if err != nil {
		return errors.Join(errors.New("failed to set shelly output state"), err)
	}
	return nil
}

func (sout *ShellyOutput) String() string {
	if sout.kind == outputKindLight {
		return fmt.Sprintf("shelly:%s:light%d", sout.deviceId, sout.switchNo)
	}
	return fmt.Sprintf("shelly:%s:switch%d", sout.deviceId, sout.switchNo)
}

// getStringId returns the io name (third part of the io id) used to resolve this
// output, matching the configured form: "<device>:<index>" for a switch or
// "<device>:light:<index>" for a light.
func (sout *ShellyOutput) getStringId() string {
	if sout.kind == outputKindLight {
		return fmt.Sprintf("%s%slight%s%d", sout.deviceId, idSeparator, idSeparator, sout.switchNo)
	}
	return fmt.Sprintf("%s%s%d", sout.deviceId, idSeparator, sout.switchNo)
}

func (sout *ShellyOutput) IsHealthy() bool {
	if sout.dev == nil {
		return false
	}
	return sout.dev.HealthCheck() == nil
}

// ShellyAnalogOutput exposes a Shelly Light component's brightness (0-100) as an
// AnalogOutput. On/off for the same light is exposed separately via a
// ShellyOutput with kind outputKindLight.
type ShellyAnalogOutput struct {
	lightNo  int
	deviceId string

	dev *shelly.ShellyDevice
}

func (sao *ShellyAnalogOutput) GetMinMax() (int, int) {
	return 0, 100
}

func (sao *ShellyAnalogOutput) GetState() (int, error) {
	if sao.dev == nil {
		return 0, fmt.Errorf("shelly device (%s) internal dev nil error", sao.deviceId)
	}
	_, brightness, err := sao.dev.GetLightState(sao.lightNo)
	return int(brightness + 0.5), err
}

func (sao *ShellyAnalogOutput) Set(value int) error {
	if sao.dev == nil {
		return errors.New("shelly analog output internal Light/Device nil error")
	}
	brightness := float64(value)
	if err := sao.dev.SetLight(sao.lightNo, nil, &brightness); err != nil {
		return errors.Join(errors.New("failed to set shelly light brightness"), err)
	}
	return nil
}

func (sao *ShellyAnalogOutput) String() string {
	return fmt.Sprintf("shelly:%s:light%d:brightness", sao.deviceId, sao.lightNo)
}

func (sao *ShellyAnalogOutput) getStringId() string {
	return fmt.Sprintf("%s%slight%s%d", sao.deviceId, idSeparator, idSeparator, sao.lightNo)
}

func (sao *ShellyAnalogOutput) IsHealthy() bool {
	if sao.dev == nil {
		return false
	}
	return sao.dev.HealthCheck() == nil
}

type ShellyInput struct {
	inputNo  int
	deviceId string

	dev *shelly.ShellyDevice

	logger *log.Logger

	subscriptions []struct {
		shellyEvent events.ShellyEventType
		swkitEvent  PushEvent
		handler     func(PushEvent)
	}
}

func (sin *ShellyInput) getStringId() string {
	return fmt.Sprintf("%s%s%d", sin.deviceId, idSeparator, sin.inputNo)
}

var shellyEventMap = map[PushEvent]events.ShellyEventType{
	PushEventSinglePress: events.ShellyEventSinglePush,
	PushEventDoublePress: events.ShellyEventDoublePush,
	PushEventTriplePress: events.ShellyEventTriplePush,
	PushEventLongPress:   events.ShellyEventLongPush,
}

func (sin *ShellyInput) Subscribe(eventTypes PushEvent, handler func(PushEvent)) error {
	registered := 0
	for _, evt := range AllPushEvents() {
		if eventTypes&evt != evt {
			continue
		}
		shellE, ok := shellyEventMap[evt]
		if !ok {
			return errors.New("unsupported event type for shelly input: " + evt.String())
		}
		sin.subscriptions = append(sin.subscriptions, struct {
			shellyEvent events.ShellyEventType
			swkitEvent  PushEvent
			handler     func(PushEvent)
		}{
			shellyEvent: shellE,
			swkitEvent:  evt,
			handler:     handler,
		})
		registered++
	}
	if registered == 0 {
		return errors.New("no valid event types in bitmask")
	}
	return nil
}

func (sin *ShellyInput) findAndFireEvent(shellyEventType events.ShellyEventType) bool {
	if sin.logger == nil {
		sin.logger = logging.NewLogger(logging.PrefixShelly)
	}

	fired := false
	for _, sub := range sin.subscriptions {
		if sub.shellyEvent == shellyEventType {
			sin.logger.Debug("shelly input event fired", "device", sin.deviceId, "input", sin.inputNo, "event", shellyEventType)
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
	sio.devicesMu.RLock()
	defer sio.devicesMu.RUnlock()
	var sb strings.Builder
	for _, dev := range sio.devices {
		sb.WriteString(dev.String())
		sb.WriteByte('\n')
	}
	return sb.String()
}

// ShellyDriverDetails holds structured detail info for the Shelly driver.
type ShellyDriverDetails struct {
	Broker  string             `json:"broker"`
	Devices []ShellyDeviceInfo `json:"devices"`
}

// ShellyDeviceInfo holds summary info for a single discovered Shelly device.
type ShellyDeviceInfo struct {
	Id       string `json:"id"`
	Model    string `json:"model"`
	Network  string `json:"network"`
	Switches int    `json:"switches"`
	Inputs   int    `json:"inputs"`
	Healthy  bool   `json:"healthy"`
}

// DriverDetails returns structured details about discovered Shelly devices.
func (sio *ShellyIO) DriverDetails() interface{} {
	sio.devicesMu.RLock()
	defer sio.devicesMu.RUnlock()
	details := ShellyDriverDetails{Broker: sio.MqttBroker}
	for _, dev := range sio.devices {
		details.Devices = append(details.Devices, ShellyDeviceInfo{
			Id:       dev.Id,
			Model:    dev.Model(),
			Network:  dev.NetworkInfo(),
			Switches: dev.SwitchCount(),
			Inputs:   dev.InputCount(),
			Healthy:  dev.HealthCheck() == nil,
		})
	}
	return details
}

// Status returns a summary of the driver's current state
func (sio *ShellyIO) Status() string {
	sio.devicesMu.RLock()
	readyCount := 0
	total := len(sio.devices)
	for _, dev := range sio.devices {
		if dev.IsReady() {
			readyCount++
		}
	}
	sio.devicesMu.RUnlock()
	broker := sio.MqttBroker
	if len(broker) > 25 {
		broker = broker[:22] + "..."
	}
	return fmt.Sprintf("devices:%d/%d broker:%s", readyCount, total, broker)
}

// GetIoDebugSnapshot returns a snapshot of all IO points from discovered Shelly devices.
// Satisfies drivers.IoDebugProvider.
func (she *ShellyIO) GetIoDebugSnapshot() IoDebugSnapshot {
	she.devicesMu.RLock()
	defer she.devicesMu.RUnlock()
	she.stateMu.RLock()
	defer she.stateMu.RUnlock()

	var points []IoPointState
	outputIdx := 0
	inputIdx := 0
	analogIdx := 0
	for _, dev := range she.devices {
		healthy := dev.HealthCheck() == nil && dev.IsReady()
		for i := 0; i < dev.SwitchCount(); i++ {
			state := false
			if s, err := dev.GetOutputState(i); err == nil {
				state = s
			}
			key := fmt.Sprintf("%s:%d", dev.Id, i)
			points = append(points, IoPointState{
				Index:       outputIdx,
				Name:        fmt.Sprintf("%s:switch%d", dev.Id, i),
				Type:        IoTypeDigitalOutput,
				State:       state,
				Healthy:     healthy,
				LastChanged: she.outputLastChanged[key],
			})
			outputIdx++
		}
		for i := 0; i < dev.InputCount(); i++ {
			state := false
			if s, err := dev.GetInputState(i); err == nil {
				state = s
			}
			key := fmt.Sprintf("%s:%d", dev.Id, i)
			eventTime := she.inputLastEvent[key]
			points = append(points, IoPointState{
				Index:       inputIdx,
				Name:        fmt.Sprintf("%s:input%d", dev.Id, i),
				Type:        IoTypeDigitalInput,
				State:       state,
				Healthy:     healthy,
				LastChanged: eventTime,
				LastEvent:   eventTime,
			})
			inputIdx++
		}
		for i := 0; i < dev.LightCount(); i++ {
			on := false
			brightness := 0
			if o, b, err := dev.GetLightState(i); err == nil {
				on = o
				brightness = int(b + 0.5)
			}
			key := fmt.Sprintf("%s:%d", dev.Id, i)
			points = append(points, IoPointState{
				Index:       analogIdx,
				Name:        fmt.Sprintf("%s:light%d", dev.Id, i),
				Type:        IoTypeAnalogOutput,
				State:       on,
				Healthy:     healthy,
				LastChanged: she.outputLastChanged[key],
				Value:       brightness,
				Min:         0,
				Max:         100,
			})
			analogIdx++
		}
	}
	return IoDebugSnapshot{Points: points}
}

// SetAnalogOutput sets a Shelly light's brightness (0-100) by its global analog
// output index. Satisfies drivers.IoAnalogOutputSetter.
func (she *ShellyIO) SetAnalogOutput(index int, value int) error {
	she.devicesMu.RLock()
	defer she.devicesMu.RUnlock()
	if value < 0 {
		value = 0
	}
	if value > 100 {
		value = 100
	}
	analogIdx := 0
	for _, dev := range she.devices {
		for i := 0; i < dev.LightCount(); i++ {
			if analogIdx == index {
				brightness := float64(value)
				return dev.SetLight(i, nil, &brightness)
			}
			analogIdx++
		}
	}
	return fmt.Errorf("shelly: analog output index %d out of range", index)
}

// ToggleOutput toggles a Shelly relay by its global output index.
// Satisfies drivers.IoOutputToggler.
func (she *ShellyIO) ToggleOutput(index int) error {
	she.devicesMu.RLock()
	defer she.devicesMu.RUnlock()
	outputIdx := 0
	for _, dev := range she.devices {
		for i := 0; i < dev.SwitchCount(); i++ {
			if outputIdx == index {
				state, err := dev.GetOutputState(i)
				if err != nil {
					return fmt.Errorf("shelly: get state for toggle: %w", err)
				}
				return dev.SetSwitch(i, !state)
			}
			outputIdx++
		}
	}
	return fmt.Errorf("shelly: output index %d out of range", index)
}

// shellyDiscoveryHandler handles MQTT announce messages for automatic device discovery.
type shellyDiscoveryHandler struct {
	ctx    context.Context
	parent *ShellyIO
}

func (h *shellyDiscoveryHandler) MqttSubscribeTopics() []string {
	return []string{"shellies/announce", "+/announce"}
}

func (h *shellyDiscoveryHandler) MqttHandle(pub paho.PublishReceived) (bool, error) {
	topic := pub.Packet.Topic
	if !strings.HasSuffix(topic, "/announce") {
		return false, nil
	}

	ap, err := shelly.ParseAnnounce(pub.Packet.Payload)
	if err != nil {
		return false, fmt.Errorf("failed to parse announce payload: %w", err)
	}
	if ap.Id == "" {
		return false, fmt.Errorf("announce payload has empty device id")
	}

	// Extract topicRoot: everything before "/announce"
	topicRoot := strings.TrimSuffix(topic, "/announce")
	if topicRoot == "" || topicRoot == topic {
		topicRoot = ap.Id
	}

	h.parent.logger.Info("discovered shelly device via announce", "id", ap.Id, "topicRoot", topicRoot, "model", ap.Model)
	go h.parent.onDeviceDiscovered(h.ctx, ap.Id, topicRoot)
	return true, nil
}

// onDeviceDiscovered registers a newly discovered Shelly device and subscribes to its topics.
func (she *ShellyIO) onDeviceDiscovered(ctx context.Context, deviceId, topicRoot string) {
	she.devicesMu.Lock()
	if she.getDeviceLocked(deviceId) != nil {
		she.devicesMu.Unlock()
		she.logger.Debug("device already known, skipping discovery", "id", deviceId)
		return
	}

	dev, err := shelly.NewShellyDevice(deviceId, she.messenger)
	if err != nil {
		she.devicesMu.Unlock()
		she.logger.Error("failed to create discovered shelly device", "id", deviceId, "err", err)
		return
	}
	she.devices = append(she.devices, dev)
	if topicRoot != deviceId {
		she.topicRoots[topicRoot] = deviceId
	}
	she.devicesMu.Unlock()

	if subErr := she.messenger.SubscribeToDevice(ctx, topicRoot); subErr != nil {
		she.logger.Warn("failed to subscribe to discovered device topics", "id", deviceId, "err", subErr)
	}

	if statusErr := dev.GetStatus(); statusErr != nil {
		she.logger.Warn("failed to request status for discovered device", "id", deviceId, "err", statusErr)
	}

	if matchErr := she.matchDevices(); matchErr != nil {
		she.logger.Debug("match after discovery had errors (expected if ios not configured)", "err", matchErr)
	}
}
