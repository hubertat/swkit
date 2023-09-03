package shelly

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"net/url"

	"github.com/hubertat/swkit/drivers/shelly/components"
)

const sendCommandTimeout = 5 * time.Second
const maxTimeSinceRefresh = 15 * time.Minute

type ShellyDevice struct {
	Addr *url.URL
	Info components.DeviceInfo

	IsMultiProfile bool
	Profiles       *[]string

	Wifi     *components.Wifi
	Ethernet *components.Ethernet

	Switches []components.Switch
	Inputs   []components.Input

	setError      error
	lastRefreshed time.Time

	rpcClient *RpcClient

	done chan bool
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
	str.WriteString("## ID: " + sd.Info.ID + "\n")
	str.WriteString("## MAC: " + sd.Info.MAC + "\n")
	str.WriteString("## Model: " + sd.Info.Model + "\n")
	str.WriteString("## Addr: " + sd.Addr.String() + "\n")
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
	err := sd.rpcClient.SendJson("Switch.Set", map[string]interface{}{"id": id, "on": state})
	sd.setError = err

	if err != nil {
		return errors.Join(errors.New("failed to send rpc Switch.Set message"), err)
	}

	return nil
}

func (sd *ShellyDevice) ListenForNotifications() {
	errChan := make(chan error)
	msgChan := make(chan RpcMessage)

	sd.lastRefreshed = time.Now()

	go func() {
		for {
			msg, err := sd.rpcClient.ReadJsonMessage()
			if err != nil {
				errChan <- errors.Join(errors.New("failed to read json rpc message"), err)
				return
			}
			msgChan <- msg
		}
	}()

	for {
		select {
		case <-sd.done:
			sd.rpcClient.Close()
			return
		case err := <-errChan:
			log.Println("shelly listen | got error", err)
		case msg := <-msgChan:
			// mType, p, err := conn.ReadMessage()
			// if err == nil {
			// 	log.Println("got message", mType)
			// 	log.Println(string(p))
			// }

			// log.Println("got msg: ", string(msg.Params))

			switch msg.Method {
			case "NotifyStatus":
				notify := NotifyStatus{}
				err := msg.UnmarshalParams(&notify)
				if err != nil {
					log.Println("failed to unmarshal params", err)
				} else {
					err = notify.FillSwitches(sd.Switches)
					if err != nil {
						log.Println("failed to fill switches", err)
					} else {
						sd.lastRefreshed = time.Now()
						log.Println("[she] filled switches for device:\n", sd.String())
					}
				}
			default:
				log.Println("got unsupported message: ", msg.Id, msg.Method)
			}

		}
	}

}

func DiscoverShelly(ctx context.Context, addr *url.URL, origin *url.URL) (device *ShellyDevice, err error) {

	rpcClient, err := NewRpcClient(ctx, origin, addr)
	if err != nil {
		err = errors.Join(errors.New("failed to create rpc client"), err)
		return
	}

	device = &ShellyDevice{
		Addr:      addr,
		rpcClient: rpcClient,
	}

	var msg RpcMessage

	msg, err = rpcClient.SendJsonAwait(ctx, "Shelly.GetDeviceInfo", nil)
	if err != nil {
		err = errors.Join(errors.New("failed to send rpc GetDeviceInfo message"), err)
		return
	}

	err = msg.UnmarshalResult(&device.Info)
	if err != nil {
		err = errors.Join(errors.New("failed to unmarshal rpc GetDeviceInfo message"), err)
		return
	}

	msg, err = rpcClient.SendJsonAwait(ctx, "Shelly.GetStatus", nil)
	if err != nil {
		err = errors.Join(errors.New("failed to send rpc GetStatus message"), err)
		return
	}

	getStatus := GetStatus{}
	err = msg.UnmarshalResult(&getStatus)
	if err != nil {
		err = errors.Join(errors.New("failed to unmarshal rpc GetStatus message"), err)
	}

	if ethInfo := getStatus.GetEthernet(); ethInfo != nil {
		device.Ethernet = &components.Ethernet{
			Status: *ethInfo,
		}
	}

	if wifiInfo := getStatus.GetWifi(); wifiInfo != nil {
		device.Wifi = &components.Wifi{
			Status: *wifiInfo,
		}
	}

	for _, sw := range getStatus.GetSwitches() {
		device.Switches = append(device.Switches, components.Switch{Status: sw})
	}

	for _, in := range getStatus.GetInputs() {
		device.Inputs = append(device.Inputs, components.Input{Status: in})
	}

	go device.ListenForNotifications()

	return
}

func (sd *ShellyDevice) Close() {
	sd.done <- true
}
