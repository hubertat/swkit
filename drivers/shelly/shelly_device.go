package shelly

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/eclipse/paho.golang/paho"
	"github.com/hubertat/swkit/drivers/shelly/components"
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

	pub Publisher

	done chan bool
}

type Publisher interface {
	Publish(payload []byte) error
	GetClientId() string
}

func (sd *ShellyDevice) MqttHandler(pub *paho.Publish) {

}

func (sd *ShellyDevice) MqttSubscribeTopic() string {
	return sd.Id + "/events/rpc"
}

func (sd *ShellyDevice) MqttPublishTopic() string {
	return sd.Id + "/events/rpc"
}

func (sd *ShellyDevice) HealthCheck() (healthy bool, err error) {
	if sd.setError != nil {
		err = sd.setError
		return
	}
	if time.Since(sd.lastRefreshed) > maxTimeSinceRefresh {
		err = errors.New("device is not healthy, last refresh was too long ago")
		return
	}
	healthy = true
	return
}

func (sd *ShellyDevice) String() string {
	str := strings.Builder{}

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
		if sw.Status.Output {
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
	str.WriteString("## End ##\n")

	return str.String()
}

func (sd *ShellyDevice) SetSwitch(id int, state bool) error {
	msg := rpcRequest{
		Jsonrpc: "2.0",
		Src:     sd.pub.GetClientId(),
		Method:  "Switch.Set",
		Params: map[string]interface{}{
			"id": id,
			"on": state,
		}}

	b, err := msg.Bytes()
	if err == nil {
		err = sd.pub.Publish(b)
	}

	sd.setError = err

	if err != nil {
		return errors.Join(errors.New("failed to send rpc Switch.Set message"), err)
	}

	return nil
}

func (sd *ShellyDevice) Close() {
	sd.done <- true
}
