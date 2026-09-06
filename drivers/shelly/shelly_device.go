package shelly

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/hubertat/swkit/drivers/shelly/components"
	"github.com/hubertat/swkit/mqtt"
)

const maxTimeSinceRefresh = 15 * time.Minute

type ShellyDevice struct {
	Id   string
	Info components.DeviceInfo

	IsMultiProfile bool
	Profiles       *[]string

	Wifi     *components.Wifi
	Ethernet *components.Ethernet

	Switches []components.Switch
	Inputs   []components.Input
	Lights   []components.Light

	setError      error
	lastRefreshed time.Time

	messenger mqtt.Messenger

	mu sync.RWMutex
}

func NewShellyDevice(id string, messenger mqtt.Messenger) (*ShellyDevice, error) {
	if messenger == nil {
		return nil, errors.New("messenger is nil")
	}
	if len(id) == 0 {
		return nil, errors.New("id is empty")
	}

	return &ShellyDevice{
		Id:        id,
		messenger: messenger,
	}, nil
}

// healthCheckLocked must be called with mu held (read or write).
func (sd *ShellyDevice) healthCheckLocked() error {
	if sd.setError != nil {
		return errors.Join(sd.setError, errors.New("set error present"))
	}
	if time.Since(sd.lastRefreshed) > maxTimeSinceRefresh {
		return errors.New("device is not healthy, last refresh was too long ago")
	}
	return nil
}

func (sd *ShellyDevice) HealthCheck() error {
	sd.mu.RLock()
	defer sd.mu.RUnlock()
	return sd.healthCheckLocked()
}

func (sd *ShellyDevice) IsReady() bool {
	sd.mu.RLock()
	defer sd.mu.RUnlock()
	return !sd.lastRefreshed.IsZero()
}

func (sd *ShellyDevice) String() string {
	sd.mu.RLock()
	defer sd.mu.RUnlock()

	str := strings.Builder{}

	str.WriteString("__________________\n")
	str.WriteString("## ShellyDevice ##\n")
	str.WriteString("## ID: " + sd.Id + "\n")
	str.WriteString("## Info ID: " + sd.Info.ID + "\n")
	str.WriteString("## MAC: " + sd.Info.MAC + "\n")
	str.WriteString("## Model: " + sd.Info.Model + "\n")
	if sd.Wifi != nil {
		str.WriteString("## Wifi: " + sd.Wifi.Status.Status + "\n")
		if sd.Wifi.Status.StaIP != nil {
			str.WriteString("## Wifi.StaIP: " + *sd.Wifi.Status.StaIP + "\n")
		}
		if sd.Wifi.Status.SSID != nil {
			str.WriteString("## Wifi.SSID: " + *sd.Wifi.Status.SSID + "\n")
		}
	}
	if sd.Ethernet != nil {
		str.WriteString("## Ethernet:\n")
		str.WriteString("## Ethernet.IP: " + *sd.Ethernet.Status.Ip + "\n")
	}
	str.WriteString("## Switches:\n")
	for _, sw := range sd.Switches {
		stateString := "[ ] off"
		if sw.Status.Output != nil && *sw.Status.Output {
			stateString = "[x]  on"
		}
		str.WriteString(fmt.Sprintf("## Switch:%d %s\t", sw.Status.ID, stateString))
		if sw.Status.APower != nil {
			str.WriteString(fmt.Sprintf("[APower: %.2f W]\n", *sw.Status.APower))
		} else {
			str.WriteString("\n")
		}
	}
	str.WriteString("## Lights:\n")
	for _, li := range sd.Lights {
		stateString := "[ ] off"
		if li.Status.Output != nil && *li.Status.Output {
			stateString = "[x]  on"
		}
		str.WriteString(fmt.Sprintf("## Light:%d %s\t", li.Status.ID, stateString))
		if li.Status.Brightness != nil {
			str.WriteString(fmt.Sprintf("[brightness: %.0f%%]\n", *li.Status.Brightness))
		} else {
			str.WriteString("\n")
		}
	}
	str.WriteString("## Inputs:\n")
	for _, in := range sd.Inputs {
		if in.Status.State == nil {
			str.WriteString(fmt.Sprintf("## Input:%d\n [no binary state]", in.Status.ID))
		} else {
			str.WriteString(fmt.Sprintf("## Input:%d.State:%v\n", in.Status.ID, *in.Status.State))
		}
	}
	str.WriteString("##          end ##\n")
	str.WriteString("------------------\n")

	return str.String()
}

func (sd *ShellyDevice) SetSwitch(id int, state bool) error {
	req := mqtt.RpcRequest{
		Method: "Switch.Set",
		Params: map[string]interface{}{
			"id": id,
			"on": state,
		},
		Dst: sd.Id,
	}
	// SendRequest is a network call; keep it outside the lock.
	err := sd.messenger.SendRequest(sd.Id, req)

	sd.mu.Lock()
	sd.setError = err
	sd.mu.Unlock()

	if err != nil {
		return errors.Join(errors.New("failed to send rpc Switch.Set message"), err)
	}
	return nil
}

// SetLight controls a light component via the Light.Set RPC. on and brightness
// are optional (nil = leave unchanged), allowing on/off and brightness to be
// driven independently. brightness is 0-100.
func (sd *ShellyDevice) SetLight(id int, on *bool, brightness *float64) error {
	params := map[string]interface{}{"id": id}
	if on != nil {
		params["on"] = *on
	}
	if brightness != nil {
		params["brightness"] = *brightness
	}

	req := mqtt.RpcRequest{
		Method: "Light.Set",
		Params: params,
		Dst:    sd.Id,
	}
	// SendRequest is a network call; keep it outside the lock.
	err := sd.messenger.SendRequest(sd.Id, req)

	sd.mu.Lock()
	sd.setError = err
	sd.mu.Unlock()

	if err != nil {
		return errors.Join(errors.New("failed to send rpc Light.Set message"), err)
	}
	return nil
}

// GetLightState returns the on state and brightness (0-100) of a light component.
func (sd *ShellyDevice) GetLightState(id int) (on bool, brightness float64, err error) {
	sd.mu.RLock()
	defer sd.mu.RUnlock()
	if len(sd.Lights) <= id {
		return false, 0, errors.New("light id out of range")
	}
	if sd.Lights[id].Status.Output == nil {
		return false, 0, fmt.Errorf("light %d output state not yet available", id)
	}
	on = *sd.Lights[id].Status.Output
	if sd.Lights[id].Status.Brightness != nil {
		brightness = *sd.Lights[id].Status.Brightness
	}
	return on, brightness, sd.healthCheckLocked()
}

func (sd *ShellyDevice) GetInputState(id int) (bool, error) {
	sd.mu.RLock()
	defer sd.mu.RUnlock()
	if len(sd.Inputs) <= id {
		return false, errors.New("input id out of range")
	}
	if sd.Inputs[id].Status.State == nil {
		return false, fmt.Errorf("input %d state not available (analog or not yet received)", id)
	}
	state := *sd.Inputs[id].Status.State
	return state, sd.healthCheckLocked()
}

func (sd *ShellyDevice) GetOutputState(id int) (bool, error) {
	if sd == nil {
		return false, errors.New("shelly device is nil!")
	}
	sd.mu.RLock()
	defer sd.mu.RUnlock()
	if len(sd.Switches) <= id {
		return false, errors.New("switch id out of range")
	}
	if sd.Switches[id].Status.Output == nil {
		return false, fmt.Errorf("switch %d output state not yet available", id)
	}
	state := *sd.Switches[id].Status.Output
	return state, sd.healthCheckLocked()
}

func (sd *ShellyDevice) FillStatus(status GetStatus) error {
	// Parse incoming data before acquiring the lock (no shared state accessed here).
	switches := status.GetSwitches()
	inputs := status.GetInputs()
	lights := status.GetLights()
	wifiStatus := status.GetWifi()
	ethernetStatus := status.GetEthernet()

	sd.mu.Lock()
	defer sd.mu.Unlock()

	// Reject if response contains fewer switches than previously known — likely partial/corrupted data.
	// Note: multi-profile devices changing profiles could legitimately change switch count, which would
	// permanently block FillStatus until restart.
	if len(sd.Switches) > len(switches) {
		return fmt.Errorf("rejecting GetStatus response: device %s has %d known switches but response contains only %d (partial or corrupted data?)", sd.Id, len(sd.Switches), len(switches))
	}
	if len(sd.Lights) > len(lights) {
		return fmt.Errorf("rejecting GetStatus response: device %s has %d known lights but response contains only %d (partial or corrupted data?)", sd.Id, len(sd.Lights), len(lights))
	}

	sd.Switches = make([]components.Switch, len(switches))
	for _, sw := range switches {
		if len(sd.Switches) <= sw.ID {
			return fmt.Errorf("FillStatus failed: switch id %d out of range", sw.ID)
		}
		sd.Switches[sw.ID] = components.Switch{Status: sw}
	}

	sd.Lights = make([]components.Light, len(lights))
	for _, li := range lights {
		if len(sd.Lights) <= li.ID {
			return fmt.Errorf("FillStatus failed: light id %d out of range", li.ID)
		}
		sd.Lights[li.ID] = components.Light{Status: li}
	}

	sd.Inputs = make([]components.Input, len(inputs))
	for _, in := range inputs {
		if len(sd.Inputs) <= in.ID {
			return fmt.Errorf("FillStatus failed: input id %d out of range", in.ID)
		}
		sd.Inputs[in.ID] = components.Input{Status: in}
	}

	if wifiStatus != nil {
		sd.Wifi = &components.Wifi{Status: *wifiStatus}
	}
	if ethernetStatus != nil {
		sd.Ethernet = &components.Ethernet{Status: *ethernetStatus}
	}

	sd.lastRefreshed = time.Now()
	return nil
}

func (sd *ShellyDevice) UpdateFromStatus(status GetStatus) error {
	// Parse incoming data before acquiring the lock (no shared state accessed here).
	switches := status.GetSwitches()
	inputs := status.GetInputs()
	lights := status.GetLights()
	wifiStatus := status.GetWifi()
	ethernetStatus := status.GetEthernet()

	sd.mu.Lock()
	defer sd.mu.Unlock()

	for _, sw := range switches {
		if sw.ID >= len(sd.Switches) {
			return errors.New("update from status failed, switch id out of range")
		}
		sd.Switches[sw.ID].Status.Update(sw)
	}

	for _, li := range lights {
		if li.ID >= len(sd.Lights) {
			return errors.New("update from status failed, light id out of range")
		}
		sd.Lights[li.ID].Status.Update(li)
	}

	for _, in := range inputs {
		if in.ID >= len(sd.Inputs) {
			return errors.New("update from status failed, input id out of range")
		}
		sd.Inputs[in.ID].Status = in
	}

	if wifiStatus != nil {
		if sd.Wifi == nil {
			sd.Wifi = &components.Wifi{}
		}
		sd.Wifi.Status = *wifiStatus
	}

	if ethernetStatus != nil {
		if sd.Ethernet == nil {
			sd.Ethernet = &components.Ethernet{}
		}
		sd.Ethernet.Status = *ethernetStatus
	}

	sd.lastRefreshed = time.Now()
	return nil
}

func (sd *ShellyDevice) Close() {}

func (sd *ShellyDevice) GetStatus() error {
	req := mqtt.RpcRequest{
		Method: "Shelly.GetStatus",
		Dst:    sd.Id,
	}
	return sd.messenger.SendRequest(sd.Id, req)
}

func (sd *ShellyDevice) SwitchCount() int {
	sd.mu.RLock()
	defer sd.mu.RUnlock()
	return len(sd.Switches)
}

func (sd *ShellyDevice) LightCount() int {
	sd.mu.RLock()
	defer sd.mu.RUnlock()
	return len(sd.Lights)
}

func (sd *ShellyDevice) InputCount() int {
	sd.mu.RLock()
	defer sd.mu.RUnlock()
	return len(sd.Inputs)
}

func (sd *ShellyDevice) SinceLastRefreshed() time.Duration {
	sd.mu.RLock()
	defer sd.mu.RUnlock()
	return time.Since(sd.lastRefreshed)
}

// Model returns the device model string.
func (sd *ShellyDevice) Model() string {
	sd.mu.RLock()
	defer sd.mu.RUnlock()
	return sd.Info.Model
}

// NetworkInfo returns a human-readable network address string, preferring WiFi over Ethernet.
func (sd *ShellyDevice) NetworkInfo() string {
	sd.mu.RLock()
	defer sd.mu.RUnlock()
	if sd.Wifi != nil && sd.Wifi.Status.StaIP != nil {
		return "WiFi: " + *sd.Wifi.Status.StaIP
	}
	if sd.Ethernet != nil && sd.Ethernet.Status.Ip != nil {
		return "Eth: " + *sd.Ethernet.Status.Ip
	}
	return ""
}
