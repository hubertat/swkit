package drivers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/netip"
	"strings"

	"net/url"
	"time"

	"github.com/hubertat/swkit/drivers/shelly"
	"github.com/hubertat/swkit/drivers/shelly/components"
)

const shellyDriverName = "shelly"
const httpDetectReadTimeout = 400 * time.Millisecond
const shellyDiscoverTimeout = 20 * time.Second
const healthCheckInterval = 2 * time.Second
const unhealthyCountLimit = 5

type ShellyIO struct {
	OriginAddr string
	IpCidr     string

	IpStart string
	IpEnd   string

	Outputs []ShellyOutput
	Inputs  []ShellyInput

	Devices map[string]*shelly.ShellyDevice

	isReady        bool
	healthTicker   *time.Ticker
	done           chan bool
	originUrl      *url.URL
	unhealthyCount int
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

func checkForShellyGen2(addr *url.URL) bool {
	addr.Scheme = "http"

	addr = addr.JoinPath("shelly", "rpc")

	httpClient := http.DefaultClient
	httpClient.Timeout = httpDetectReadTimeout

	resp, err := httpClient.Get(addr.String())
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

func (she *ShellyIO) getAddressesToTry() (addrToTry []*url.URL, err error) {
	addrToTry = []*url.URL{}
	ipStart, ipEnd, ipRangeErr := she.getStartEndIp()

	var addr *url.URL
	if ipRangeErr == nil {
		for ip := ipStart; ip.Less(ipEnd); ip = ip.Next() {
			addr, err = url.Parse(ip.String())
			if err != nil {
				err = errors.Join(errors.New("failed to parse ip address "+ip.String()), err)
				return
			}
			if checkForShellyGen2(addr) {
				addrToTry = append(addrToTry, addr)
			}
		}
	} else {
		var prefix netip.Prefix
		prefix, err = netip.ParsePrefix(she.IpCidr)
		if err != nil {
			err = errors.Join(errors.New("failed to parse ip address cidr notation and ip address start end values, cannot continue"), err, ipRangeErr)
			return

		}

		for ip := prefix.Masked().Addr().Next(); prefix.Contains(ip.Next()); ip = ip.Next() {
			addr, err = url.Parse(ip.String())
			if err != nil {
				err = errors.Join(errors.New("failed to parse ip address "+ip.String()), err)
				return
			}
			if checkForShellyGen2(addr) {
				addrToTry = append(addrToTry, addr)
			}
		}
	}

	return
}

func (she *ShellyIO) startHealthCheck(ctx context.Context) {
	she.healthTicker = time.NewTicker(healthCheckInterval)

	for {
		select {
		case <-she.done:
			she.healthTicker.Stop()
			return
		case <-ctx.Done():
			she.healthTicker.Stop()
			return
		case <-she.healthTicker.C:
			for _, dev := range she.Devices {
				healthy, err := dev.HealthCheck()
				if !healthy {
					she.unhealthyCount++
					log.Println("device", dev.Info.ID, "is not healthy, err:", err)
				}
			}
			if unhealthyCountLimit > 0 && she.unhealthyCount > unhealthyCountLimit {
				log.Println("too many unhealthy devices, performing discovery")
				err := she.discoverDevices(ctx)
				if err != nil {
					log.Println("failed to discover devices", err)
				} else {
					she.unhealthyCount = 0
				}
			}
		}
	}
}

func (she *ShellyIO) Setup(ctx context.Context, inputs []uint16, outputs []uint16) error {
	var err error
	she.originUrl, err = url.Parse(she.OriginAddr)
	if err != nil {
		return errors.Join(errors.New("failed to parse origin address"), err)
	}

	she.Devices = make(map[string]*shelly.ShellyDevice)

	err = she.discoverDevices(ctx)
	if err != nil {
		return errors.Join(errors.New("failed to discover devices"), err)
	}

	go she.startHealthCheck(ctx)

	she.isReady = true

	return nil
}

func (she *ShellyIO) discoverDevices(ctx context.Context) error {
	log.Println("checking provided ip range for shelly devices")
	addrToTry, err := she.getAddressesToTry()
	if err != nil {
		return errors.Join(errors.New("failed to get addresses to try"), err)
	}

	log.Println("checking devices list for already discovered devices")
	for _, dev := range she.Devices {
		healthy, _ := dev.HealthCheck()
		if healthy {
			for i, addr := range addrToTry {
				if strings.EqualFold(addr.Host, dev.Addr.Host) {
					log.Println("device", dev.Info.ID, "addr: ", addr, "already discovered and healthy, removing from list")
					addrToTry = append(addrToTry[:i], addrToTry[i+1:]...)
				}
			}
		}
	}

	log.Println("found ", len(addrToTry), " addresses to try, will try discover")
	for _, addr := range addrToTry {
		ctx, cancel := context.WithTimeout(ctx, shellyDiscoverTimeout)
		defer cancel()
		dev, err := shelly.DiscoverShelly(ctx, addr, she.originUrl)
		if err != nil {
			log.Println(errors.Join(errors.New("failed to discover shelly device at ip address "+addr.String()), err))
		} else {
			she.Devices[dev.Info.ID] = dev
			log.Println("found and subscribed device:\n", dev.String())
		}
	}

	return she.matchIOs()
}

func (she *ShellyIO) matchIOs() error {
	for ix, out := range she.Outputs {
		dev, exist := she.Devices[out.Id]
		if !exist {
			return fmt.Errorf("device with id %s not found", out.Id)
		}
		out.dev = dev

		if out.SwitchNo >= len(dev.Switches) {
			return fmt.Errorf("device %s does not have output pin %d", out.Id, out.SwitchNo)
		}
		out.sw = &dev.Switches[out.SwitchNo]

		she.Outputs[ix] = out
	}

	// TODO: inputs
	// for _, in := range dev.Inputs {
	// 	she.Inputs = append(she.Inputs, ShellyInput{
	// 		Pin: uint16(len(she.Inputs)),
	// 		in:  &in.Status,
	// 	})
	// }

	return nil
}

func (she *ShellyIO) Close() error {
	for _, dev := range she.Devices {
		dev.Close()
	}
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
	if sout.sw == nil || sout.dev == nil {
		return false, errors.New("shelly output internal Switch/Device nil error")
	}

	healthy, err := sout.dev.HealthCheck()
	if healthy {
		return sout.sw.Status.Output, nil
	}

	return false, errors.Join(errors.New("shelly output is not healthy"), err)
}

func (sout *ShellyOutput) Set(state bool) error {
	if sout.sw == nil || sout.dev == nil {
		return errors.New("shelly output internal Switch/Device nil error")
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

	in  *components.InputStatus
	dev *shelly.ShellyDevice
}

func (sin *ShellyInput) GetState() (bool, error) {
	if sin.in == nil || sin.dev == nil {
		return false, errors.New("shelly input internal InputStatus/Device nil error")
	}

	healthy, err := sin.dev.HealthCheck()
	if healthy {
		return *sin.in.State, nil
	}

	return false, errors.Join(errors.New("shelly input is not healthy"), err)
}
