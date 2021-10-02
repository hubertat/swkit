package main

import (
	"fmt"

	"github.com/brutella/hc/accessory"
	"github.com/stianeikeland/go-rpio"
)

type Outlet struct {
	Name          string
	Gpio          int
	Invert        bool
	State         bool
	ControlByGpio int

	hk *accessory.Outlet
}

func (ou *Outlet) SetupGpio() {
	pin := rpio.Pin(ou.Gpio)
	pin.Output()
}

func (ou *Outlet) Sync() {
	pin := rpio.Pin(ou.Gpio)

	state := ou.State
	if ou.Invert {
		state = !state
	}
	if state {
		pin.High()
	} else {
		pin.Low()
	}
}

func (ou *Outlet) GetHk() *accessory.Accessory {
	info := accessory.Info{
		Name:         ou.Name,
		ID:           uint64(ou.Gpio),
		SerialNumber: fmt.Sprintf("outlet:gpio:%02d", ou.Gpio),
	}
	ou.hk = accessory.NewOutlet(info)
	ou.hk.Outlet.On.OnValueRemoteUpdate(ou.SetValue)

	return ou.hk.Accessory
}

func (ou *Outlet) SetValue(state bool) {
	ou.State = state
	ou.hk.Outlet.On.SetValue(ou.State)
}

func (ou *Outlet) Toggle() {
	ou.State = !ou.State
	ou.hk.Outlet.On.SetValue(ou.State)
}
