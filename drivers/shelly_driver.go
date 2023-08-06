package drivers

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	"time"

	"github.com/hubertat/swkit/drivers/shelly"
	"github.com/hubertat/swkit/drivers/shelly/components"
)

const shellyDriverName = "shelly"
const httpDetectReadTimeout = 400 * time.Millisecond

type ShellyIO struct {
	IpCidr string

	IpStart string
	IpEnd   string

	Outputs []ShellyOutput
	Inputs  []ShellyInput

	Devices map[string]*shelly.ShellyDevice

	isReady bool
}

func (she *ShellyIO) getStartEndIp() (start netip.Addr, end netip.Addr, err error) {
	if len(she.IpStart) == 0 || len(she.IpEnd) == 0 {
		err = errors.New("parameters IpStart and/or IpEnd are empty")
		return
	}

	firstAddr, err := netip.ParseAddr(she.IpStart)
	if err != nil {
		err = errors.Join(errors.New("failed to parse IpStart"), err)
		return
	}

	secondAddr, err := netip.ParseAddr(she.IpEnd)
	if err != nil {
		err = errors.Join(errors.New("failed to parse IpEnd"), err)
		return
	}

	if !firstAddr.Is4() || !secondAddr.Is4() {
		err = errors.New("provided ip address is not IPv4, IPv6 is not supported")
		return
	}

	if firstAddr.Less(secondAddr) {
		start = firstAddr
		end = secondAddr
	} else {
		start = secondAddr
		end = firstAddr
	}
	return
}

func checkForShellyGen2(addr string) bool {
	httpClient := http.DefaultClient
	httpClient.Timeout = httpDetectReadTimeout

	resp, err := httpClient.Get(addr)
	if err != nil {
		return false
	}

	defer resp.Body.Close()
	dec := json.NewDecoder(resp.Body)

	type ShellyGen2 struct {
		Id  string
		Gen int
	}

	shellyInfo := &ShellyGen2{}
	err = dec.Decode(shellyInfo)
	if err != nil {
		return false
	}

	return shellyInfo.Gen == 2
}

func tryShellyDevice(addr netip.Addr) (device *shelly.ShellyDevice, err error) {

	uri, err := url.Parse(addr.String())
	if err != nil {
		err = errors.Join(errors.New("failed to parse url from ip addr: "+addr.String()), err)
	}
	uri.Scheme = "http"

	testUri := uri.JoinPath("shelly")
	uri = uri.JoinPath("rpc")

	if !checkForShellyGen2(testUri.String()) {
		return
	}

	return shelly.DiscoverShelly(uri)
}

func (she *ShellyIO) Setup(inputs []uint16, outputs []uint16) error {

	she.Devices = make(map[string]*shelly.ShellyDevice)

	ipStart, ipEnd, ipRangeErr := she.getStartEndIp()
	if ipRangeErr == nil {
		for addr := ipStart; addr.Less(ipEnd); addr = addr.Next() {
			dev, err := tryShellyDevice(addr)
			if err != nil {
				return errors.Join(errors.New("failed to discover shelly device at ip address "+addr.String()), err)
			}
			if dev != nil {
				she.Devices[dev.Info.ID] = dev
			}
		}
	} else {
		prefix, err := netip.ParsePrefix(she.IpCidr)
		if err != nil {
			return errors.Join(errors.New("failed to parse ip address cidr notation and ip address start end values, cannot continue"), err, ipRangeErr)
		}

		for addr := prefix.Masked().Addr().Next(); prefix.Contains(addr.Next()); addr = addr.Next() {
			dev, err := tryShellyDevice(addr)
			if err != nil {
				return errors.Join(errors.New("failed to discover shelly device at ip address "+addr.String()), err)
			}
			if dev != nil {
				she.Devices[dev.Info.ID] = dev
			}
		}
	}

	for _, dev := range she.Devices {
		// log.Printf("DEBUG device:\n%+v\n\n", dev)
		// err := dev.Subscribe()
		// if err != nil {
		// 	log.Fatal(err)
		// }
		for _, sw := range dev.Switches {
			she.Outputs = append(she.Outputs, ShellyOutput{
				Pin: uint16(len(she.Outputs)),
				sw:  &sw,
				dev: dev,
			})
		}

		for _, in := range dev.Inputs {
			she.Inputs = append(she.Inputs, ShellyInput{
				Pin: uint16(len(she.Inputs)),
				in:  &in.Status,
			})
		}
	}

	she.isReady = true

	return nil
}

func (she *ShellyIO) Close() error {
	she.isReady = false
	return nil
}

func (she *ShellyIO) NameId() string {
	return shellyDriverName
}

func (she *ShellyIO) IsReady() bool {
	return she.isReady
}

func (she *ShellyIO) GetInput(pin uint16) (DigitalInput, error) {
	return nil, errors.New("inputs not implemented")
}

func (she *ShellyIO) GetOutput(pin uint16) (DigitalOutput, error) {
	for _, out := range she.Outputs {
		if out.Pin == pin {
			return &out, nil
		}
	}

	return nil, fmt.Errorf("shelly output pin = %d not found", pin)
}

func (she *ShellyIO) GetAllIo() (inputs []uint16, outputs []uint16) {
	for _, out := range she.Outputs {
		outputs = append(outputs, out.Pin)
	}
	return
}

type ShellyOutput struct {
	Pin      uint16
	Id       string
	SwitchNo int

	sw  *components.Switch
	dev *shelly.ShellyDevice
}

func (sout *ShellyOutput) GetState() (bool, error) {
	if sout.sw == nil {
		return false, errors.New("shelly output internal SwitchStatus nil error")
	}
	return sout.sw.Status.Output, nil
}

func (sout *ShellyOutput) Set(state bool) error {
	if sout.sw == nil || sout.dev == nil {
		return errors.New("shelly output internal Switch nil error")
	}
	err := sout.dev.SetSwitch(sout.sw.Status.ID, state)
	if err != nil {
		return errors.Join(errors.New("failed to set shelly output state"), err)
	}
	return nil
}

type ShellyInput struct {
	Pin     uint16
	Id      string
	InputNo int

	in *components.InputStatus
}

func (sin *ShellyInput) GetState() (bool, error) {
	if sin.in == nil {
		return false, errors.New("shelly input internal InputStatus nil error")
	}
	return *sin.in.State, nil
}
