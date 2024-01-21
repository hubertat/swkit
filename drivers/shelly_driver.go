package drivers

import (
	"context"
	"errors"
	"fmt"
	"log"

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
	Outputs []ShellyOutput
	Inputs  []ShellyInput

	Devices map[string]*shelly.ShellyDevice

	isReady        bool
	healthTicker   *time.Ticker
	done           chan bool
	originUrl      *url.URL
	unhealthyCount int
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
