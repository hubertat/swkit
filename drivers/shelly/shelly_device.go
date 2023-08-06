package shelly

import (
	"context"
	"errors"
	"log"
	"net/url"
	"time"

	"github.com/hubertat/go-ethereum/rpc"

	"github.com/hubertat/swkit/drivers/shelly/components"
)

const maxSwitchNumber = 4
const maxInputNumber = 4
const discoverConnectionTimeout = 5 * time.Second

type ShellyDevice struct {
	Addr *url.URL
	Info components.DeviceInfo

	IsMultiProfile bool
	Profiles       *[]string

	Wifi     *components.Wifi
	Ethernet *components.Ethernet

	Switches []components.Switch
	Inputs   []components.Input

	sub *rpc.ClientSubscription
	rec chan Receiver
}

type Receiver struct {
	TS        int64       `json:"ts"`
	Component interface{} `json:"component"`
}

func (sd *ShellyDevice) SetSwitch(id int, state bool) error {
	sd.Addr.Scheme = "http"
	client, err := rpc.Dial(sd.Addr.String())
	if err != nil {
		return errors.Join(errors.New("failed to rpc Dial"), err)
	}

	return client.Call(nil, "Switch.Set", map[string]interface{}{"id": id, "on": state})
}

func (sd *ShellyDevice) Subscribe() error {

	sd.Addr.Scheme = "ws"
	ctx, cancel := context.WithTimeout(context.Background(), discoverConnectionTimeout)
	defer cancel()

	client, err := rpc.DialWebsocket(ctx, sd.Addr.String(), "10.100.100.161")
	if err != nil {
		return errors.Join(errors.New("failed to rpc Dial"), err)
	}

	sd.rec = make(chan Receiver)

	sd.sub, err = client.Subscribe(ctx, "NotifyStatus", sd.rec)
	if err != nil {
		return errors.Join(errors.New("failed to subscribe to NotifyStatus"), err)
	}

	for {
		r := <-sd.rec
		log.Println(r)
	}
}

func DiscoverShelly(addr *url.URL) (device *ShellyDevice, err error) {

	addr.Scheme = "http"
	ctx, cancel := context.WithTimeout(context.Background(), discoverConnectionTimeout)
	defer cancel()

	client, err := rpc.Dial(addr.String())
	if err != nil {
		err = errors.Join(errors.New("failed to rpc Dial"), err)
		return
	}
	defer client.Close()

	devInfo := &components.DeviceInfo{}

	err = client.CallContext(ctx, devInfo, "Shelly.GetDeviceInfo")
	if err != nil {
		err = errors.Join(errors.New("failed to call GetDeviceInfo method"), err)
		return
	}
	device = &ShellyDevice{
		Addr: addr,
		Info: *devInfo,
	}

	profiles := &[]string{}
	err = client.Call(profiles, "Shelly.ListProfiles")
	if err == nil {
		device.IsMultiProfile = true
		device.Profiles = profiles
	}

	wifiInfo := &components.WifiStatus{}
	ethInfo := &components.EthernetStatus{}

	err = client.Call(wifiInfo, "Wifi.GetStatus")
	if err == nil {
		device.Wifi = &components.Wifi{
			Status: *wifiInfo,
		}
		err = client.Call(&device.Wifi.Config, "Wifi.GetConfig")
		if err != nil {
			err = errors.Join(errors.New("failed to call Wifi.GetConfig method"), err)
			return
		}
	}

	err = client.Call(ethInfo, "Ethernet.GetStatus")
	if err == nil {
		device.Ethernet = &components.Ethernet{
			Status: *ethInfo,
		}
		err = client.Call(&device.Ethernet.Config, "Ethernet.GetConfig")
		if err != nil {
			err = errors.Join(errors.New("failed to call Ethernet.GetConfig method"), err)
			return
		}
	}

	device.Switches = []components.Switch{}
	for no := 0; no < maxSwitchNumber; no++ {
		switchInfo := &components.SwitchStatus{}
		err = client.Call(switchInfo, "Switch.GetStatus", map[string]interface{}{"id": no})
		if err == nil {
			sw := components.Switch{Status: *switchInfo}
			err = client.Call(&sw.Config, "Switch.GetConfig", map[string]interface{}{"id": no})
			if err != nil {
				err = errors.Join(errors.New("failed to call Switch.GetConfig method"), err)
				return
			}
			device.Switches = append(device.Switches, sw)
		}
	}

	device.Inputs = []components.Input{}
	for no := 0; no < maxInputNumber; no++ {
		inputInfo := &components.InputStatus{}
		err = client.Call(inputInfo, "Input.GetStatus", map[string]interface{}{"id": no})
		if err == nil {
			in := components.Input{Status: *inputInfo}
			err = client.Call(&in.Config, "Input.GetConfig", map[string]interface{}{"id": no})
			if err != nil {
				err = errors.Join(errors.New("failed to call Input.GetConfig method"), err)
				return
			}
			device.Inputs = append(device.Inputs, in)
		}
	}

	err = nil
	return
}
