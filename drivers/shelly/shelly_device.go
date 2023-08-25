package shelly

import (
	"context"
	"encoding/json"
	"errors"
	"log"

	"net/http"
	"net/url"
	"time"

	"github.com/gorilla/websocket"

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

	origin *url.URL

	msg  chan interface{}
	done chan bool
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

func websocketDialer() websocket.Dialer {
	return websocket.Dialer{
		HandshakeTimeout: discoverConnectionTimeout,
		ReadBufferSize:   1024,
		WriteBufferSize:  1024,
	}
}

func (sd *ShellyDevice) SubscribeDeviceStatus() error {
	headers := http.Header{}
	sd.origin.Scheme = "ws"
	headers.Add("Origin", sd.origin.String())

	ctx := context.Background()
	ctx, close := context.WithTimeout(ctx, discoverConnectionTimeout)
	defer close()

	dialer := websocketDialer()
	sd.Addr.Scheme = "ws"
	conn, _, err := dialer.DialContext(ctx, sd.Addr.String(), headers)
	if err != nil {
		return errors.Join(errors.New("failed to ws dial"), err)
	}

	rpcClient := NewRpcClient("swkit")
	subStatus := rpcClient.NewRpcRequest("Shelly.GetStatus", nil)

	err = conn.WriteJSON(subStatus)
	if err != nil {
		return errors.Join(errors.New("failed to write json rpc message"), err)
	}

	go func() {
		for {
			select {
			case <-sd.done:
				conn.Close()
				return
			default:
				// mType, p, err := conn.ReadMessage()
				// if err == nil {
				// 	log.Println("got message", mType)
				// 	log.Println(string(p))
				// 	return
				// }
				resp := &ShellyNotifyStatusResponse{}
				if err = conn.ReadJSON(resp); err == nil {
					for id, jsonSwState := range resp.SwitchSlice() {
						if len(jsonSwState) > 5 && len(sd.Switches) > id {
							currentStatus := sd.Switches[id].Status
							err = json.Unmarshal([]byte(jsonSwState), &currentStatus)
							if err == nil {
								sd.Switches[id].Status = currentStatus
								log.Println("got switch not., state: ", *currentStatus.Output, ", power: ", *currentStatus.APower)
							} else {
								log.Println("failed to unmarshal switch status", err)
							}
						}
					}

				} else {
					log.Println("failed to websocket ReadJSON", err)
				}
			}
		}
	}()

	return nil
}

func (sd *ShellyDevice) SubscribeComponentsStatus() error {
	dialer := websocketDialer()

	originHeaders := http.Header{}
	// originHeaders.Add("Origin", "ws://10.100.70.173:80")
	originHeaders.Add("Origin", "ws://10.100.100.187:80")

	ctx := context.Background()
	ctx, close := context.WithTimeout(ctx, discoverConnectionTimeout)
	defer close()

	sd.Addr.Scheme = "ws"
	conn, _, err := dialer.DialContext(ctx, sd.Addr.String(), originHeaders)
	if err != nil {
		return errors.Join(errors.New("failed to dial"), err)
	}

	rpcClient := NewRpcClient("swkit")
	subSwitch0 := rpcClient.NewRpcRequest("Switch.GetStatus", map[string]interface{}{"id": 0})
	subIn0 := rpcClient.NewRpcRequest("Input.GetStatus", map[string]interface{}{"id": 0})

	for _, msg := range []RpcRequest{subSwitch0, subIn0} {
		err = conn.WriteJSON(msg)
		if err != nil {
			return errors.Join(errors.New("failed to write json rpc message"), err)
		}
	}

	go func() {
		for {
			select {
			case <-sd.done:
				conn.Close()
				return
			default:
				message := RpcResponse{}

				mType, p, err := conn.ReadMessage()
				if err == nil {
					log.Println("got message", mType)
					log.Println(string(p))
					return
				}
				if err = conn.ReadJSON(&message); err == nil {

					return
					req, err := rpcClient.GetRequest(message.Id)
					if err == nil {
						switch req.Method {
						case "Switch.GetStatus":
							swStatus := components.SwitchStatus{}
							if err = message.UnmarshalResult(&swStatus); err == nil {
								log.Println("got switch status", swStatus)
							}

						case "Input.GetStatus":
							inStatus := components.InputStatus{}
							if err = message.UnmarshalResult(&inStatus); err == nil {
								log.Println("got input status", inStatus)
							}
						}
					}
				} else {
					log.Println("failed to read json", err)
				}

			}
		}
	}()

	log.Println("subV finished")
	return nil
}

func (sd *ShellyDevice) SubscribeOther() error {

	dialer := websocketDialer()

	websocketOptions := rpc.WithWebsocketDialer(dialer)
	websocketOrigin := rpc.WithHeader("Origin", "ws://10.100.70.173:80")

	ctx := context.Background()
	ctx, cancel := context.WithTimeout(ctx, discoverConnectionTimeout)
	defer cancel()

	sd.Addr.Scheme = "ws"
	client, err := rpc.DialOptions(ctx, sd.Addr.String(), websocketOrigin, websocketOptions)
	if err != nil {
		panic(err)
	}

	// res := interface{}(nil)
	// err = client.CallContext(ctx, &res, "Shelly.GetStatus")

	sd.msg = make(chan interface{})
	sub, _ := client.Subscribe(ctx, "Shelly.GetStatus", sd.msg)

	// if err != nil {
	// 	return errors.Join(errors.New("failed to call GetStatus method"), err)
	// }

	// log.Println(res)

	go func() {
		for {
			log.Println("waiting for message")
			select {
			case <-sd.done:
				sub.Unsubscribe()
				client.Close()
			case message := <-sd.msg:
				log.Println("message: ", message)

			}
		}
	}()

	log.Println("sub finished")
	return nil
}

func DiscoverShelly(addr *url.URL, origin *url.URL) (device *ShellyDevice, err error) {

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
		Addr:   addr,
		Info:   *devInfo,
		origin: origin,
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

	err = device.SubscribeDeviceStatus()

	return
}

func (sd *ShellyDevice) Close() {
	sd.done <- true
	return
}
