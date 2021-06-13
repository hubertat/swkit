package main

import (
	"fmt"

	"github.com/brutella/hc/accessory"
	"github.com/stianeikeland/go-rpio"
)

type Switch struct {
	Name           string
	Gpio           int
	Invert         bool
	State          bool
	HomeKitEnabled bool

	switchThis SwitchableDevice
	hk         *accessory.Switch
}

type SwitchableDevice interface {
	SetValue(bool)
}

func (swb *Switch) SetupGpio() {
	pin := rpio.Pin(swb.Gpio)
	pin.Input()
	pin.PullUp()
}
func (swb *Switch) Sync() {
	pin := rpio.Pin(swb.Gpio)
	if swb.Invert {
		swb.State = pin.Read() == rpio.High
	} else {
		swb.State = pin.Read() == rpio.Low
	}

	if swb.hk != nil {
		swb.hk.Switch.On.SetValue(swb.State)
	}

	if swb.switchThis != nil {
		swb.switchThis.SetValue(swb.State)
	}
}

func (swb *Switch) GetHk() *accessory.Accessory {
	if !swb.HomeKitEnabled {
		return nil
	}

	info := accessory.Info{
		Name:         swb.Name,
		ID:           uint64(swb.Gpio),
		SerialNumber: fmt.Sprintf("switch:gpio:%02d", swb.Gpio),
	}
	swb.hk = accessory.NewSwitch(info)
	swb.hk.Switch.On.OnValueRemoteGet(swb.GetValue)
	swb.hk.Switch.On.OnValueRemoteUpdate(swb.Set)

	return swb.hk.Accessory
}

func (swb *Switch) Set(value bool) {
	swb.State = value
}

func (swb *Switch) GetValue() bool {
	return swb.State
}
