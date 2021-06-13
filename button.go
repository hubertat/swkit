package main

import (
	"fmt"

	"github.com/brutella/hc/accessory"
	"github.com/stianeikeland/go-rpio"
)

type Button struct {
	Name           string
	Gpio           int
	Invert         bool
	State          bool
	HomeKitEnabled bool

	clickThis ClickableDevice
	hk        *accessory.Accessory
}

type ClickableDevice interface {
	Toggle()
}

func (bu *Button) SetupGpio() {
	pin := rpio.Pin(bu.Gpio)
	pin.Input()
	pin.PullUp()
}
func (bu *Button) Sync() {
	oldState := bu.State
	pin := rpio.Pin(bu.Gpio)
	if bu.Invert {
		bu.State = pin.Read() == rpio.High
	} else {
		bu.State = pin.Read() == rpio.Low
	}

	if bu.State != oldState && bu.State {
		if bu.clickThis != nil {
			bu.clickThis.Toggle()
		}
	}
}

func (bu *Button) GetHk() *accessory.Accessory {
	return bu.hk
}

func (bu *Button) Set(value bool) {
	fmt.Printf("DEBUG buttin setting value(%v) from HK\n", value)
	bu.State = value
}

func (bu *Button) GetValue() bool {
	fmt.Printf("DEBUG button getting value(%v)to -> HK\n", bu.State)
	return bu.State
}
