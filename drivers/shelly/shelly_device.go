package shelly

import (
	"errors"
	"fmt"
	"strings"
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

	setError      error
	lastRefreshed time.Time

	messenger mqtt.Messenger

	done chan bool
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
		done:      make(chan bool),
	}, nil
}

func (sd *ShellyDevice) HealthCheck() error {
	if sd.setError != nil {
		return errors.Join(sd.setError, errors.New("set error present"))
	}
	if time.Since(sd.lastRefreshed) > maxTimeSinceRefresh {
		return errors.New("device is not healthy, last refresh was too long ago")
	}

	return nil
}

func (sd *ShellyDevice) IsReady() bool {
	if sd.lastRefreshed.IsZero() {
		return false
	}

	return true
}

func (sd *ShellyDevice) String() string {
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
	err := sd.messenger.SendRequest(sd.Id, req)
	sd.setError = err

	if err != nil {
		return errors.Join(errors.New("failed to send rpc Switch.Set message"), err)
	}

	return nil
}

func (sd *ShellyDevice) GetInputState(id int) (bool, error) {
	if len(sd.Inputs) <= id {
		return false, errors.New("input id out of range")
	}
	var state bool
	state = *sd.Inputs[id].Status.State
	return state, sd.HealthCheck()
}

func (sd *ShellyDevice) GetOutputState(id int) (bool, error) {
	if sd == nil {
		return false, errors.New("shelly device is nil!")
	}
	if len(sd.Switches) <= id {
		return false, errors.New("switch id out of range")
	}
	state := *sd.Switches[id].Status.Output
	return state, sd.HealthCheck()
}

func (sd *ShellyDevice) FillStatus(status GetStatus) error {
	switches := status.GetSwitches()
	if len(sd.Switches) > len(switches) {
		return fmt.Errorf("FillStatus failed: tried to fill %d switched from GetStatus into existing Switches array len = %d", len(switches), len(sd.Switches))
	}

	sd.Switches = make([]components.Switch, len(switches))
	for _, sw := range switches {
		if len(sd.Switches) <= sw.ID {
			return fmt.Errorf("FillStatus failed: switch id %d out of range", sw.ID)
		}
		sd.Switches[sw.ID] = components.Switch{
			Status: sw,
		}
	}

	inputs := status.GetInputs()
	sd.Inputs = make([]components.Input, len(inputs))
	for _, in := range inputs {
		if len(sd.Inputs) <= in.ID {
			return fmt.Errorf("FillStatus failed: input id %d out of range", in.ID)
		}
		sd.Inputs[in.ID] = components.Input{
			Status: in,
		}
	}

	if wifiStatus := status.GetWifi(); wifiStatus != nil {
		sd.Wifi = &components.Wifi{
			Status: *wifiStatus,
		}
	}

	if ethernetStatus := status.GetEthernet(); ethernetStatus != nil {
		sd.Ethernet = &components.Ethernet{
			Status: *ethernetStatus,
		}
	}

	sd.lastRefreshed = time.Now()
	return nil
}

func (sd *ShellyDevice) UpdateFromStatus(status GetStatus) error {
	for _, sw := range status.GetSwitches() {
		if sw.ID >= len(sd.Switches) {
			return errors.New("update from status failed, switch id out of range")
		}
		sd.Switches[sw.ID].Status.Update(sw)
	}

	for _, in := range status.GetInputs() {
		if in.ID >= len(sd.Inputs) {
			return errors.New("update from status failed, input id out of range")
		}
		sd.Inputs[in.ID].Status = in
	}

	if wifiStatus := status.GetWifi(); wifiStatus != nil {
		sd.Wifi.Status = *wifiStatus
	}

	if ethernetStatus := status.GetEthernet(); ethernetStatus != nil {
		sd.Ethernet.Status = *ethernetStatus
	}

	sd.lastRefreshed = time.Now()
	return nil
}

func (sd *ShellyDevice) Close() {
	sd.done <- true
}

func (sd *ShellyDevice) GetStatus() error {
	req := mqtt.RpcRequest{
		Method: "Shelly.GetStatus",
		Dst:    sd.Id,
	}
	return sd.messenger.SendRequest(sd.Id, req)
}

func (sd *ShellyDevice) SinceLastRefreshed() time.Duration {
	return time.Since(sd.lastRefreshed)
}
