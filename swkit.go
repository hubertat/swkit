package main

import (
	"time"

	"github.com/brutella/hc/accessory"
	"github.com/stianeikeland/go-rpio"
)

type SwKit struct {
	Lights   []*Light
	Buttons  []*Button
	Switches []*Switch
	Shutters []*Shutter

	ticker *time.Ticker
}

type IO interface {
	Sync()
	GetHk() *accessory.Accessory
}

func (sw *SwKit) SyncAll() error {
	err := rpio.Open()
	if err != nil {
		return err
	}
	defer rpio.Close()

	for _, li := range sw.Lights {
		li.Sync()
	}
	for _, li := range sw.Buttons {
		li.Sync()
	}

	return nil
}

func (sw *SwKit) GetHkAccessories() (acc []*accessory.Accessory) {
	acc = []*accessory.Accessory{}

	for _, li := range sw.Lights {
		a := li.GetHk()
		if a != nil {
			acc = append(acc, a)
		}
	}
	for _, li := range sw.Buttons {
		a := li.GetHk()
		if a != nil {
			acc = append(acc, a)
		}

	}

	return
}

func (sw *SwKit) findSwitch(gpio int) *Switch {
	for _, bu := range sw.Switches {
		if bu.Gpio == gpio {
			return bu
		}
	}
	return nil
}

func (sw *SwKit) findButton(gpio int) *Button {
	for _, bu := range sw.Buttons {
		if bu.Gpio == gpio {
			return bu
		}
	}
	return nil
}

func (sw *SwKit) SetupGpio() error {
	err := rpio.Open()
	if err != nil {
		return err
	}
	defer rpio.Close()

	for _, li := range sw.Lights {
		li.SetupGpio()
		if li.ControlByGpio > 0 {
			swButton := sw.findSwitch(li.ControlByGpio)
			clickButton := sw.findButton(li.ControlByGpio)
			if swButton != nil {
				swButton.switchThis = li
			}
			if clickButton != nil {
				clickButton.clickThis = li
			}
		}
	}
	for _, li := range sw.Buttons {
		li.SetupGpio()
	}

	return nil
}

func (sw *SwKit) StartTicker(interval time.Duration) {
	err := sw.SetupGpio()
	if err != nil {
		panic(err)
	}
	sw.ticker = time.NewTicker(interval)

	for {
		select {
		case <-sw.ticker.C:
			{
				sw.SyncAll()
			}
		}
	}
}
