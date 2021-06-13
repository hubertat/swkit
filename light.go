package main

import (
	"fmt"

	"github.com/brutella/hc/accessory"
	"github.com/stianeikeland/go-rpio"
)

type Light struct {
	Name          string
	Gpio          int
	Invert        bool
	State         bool
	ControlByGpio int

	hk *accessory.Lightbulb
}

func (li *Light) SetupGpio() {
	pin := rpio.Pin(li.Gpio)
	pin.Output()

}

func (li *Light) Sync() {
	pin := rpio.Pin(li.Gpio)

	state := li.State
	if li.Invert {
		state = !state
	}
	if state {
		pin.High()
	} else {
		pin.Low()
	}
}

func (li *Light) GetHk() *accessory.Accessory {
	info := accessory.Info{
		Name:         li.Name,
		ID:           uint64(li.Gpio),
		SerialNumber: fmt.Sprintf("light:gpio:%02d", li.Gpio),
	}
	li.hk = accessory.NewLightbulb(info)
	li.hk.Lightbulb.On.OnValueRemoteUpdate(li.SetValue)

	return li.hk.Accessory
}

func (li *Light) SetValue(state bool) {
	li.State = state
	li.hk.Lightbulb.On.SetValue(li.State)
}

func (li *Light) Toggle() {
	li.State = !li.State
	li.hk.Lightbulb.On.SetValue(li.State)
}
