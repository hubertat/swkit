package main

import (
	"fmt"
	"strings"

	"github.com/brutella/hc/accessory"
	"github.com/pkg/errors"
)

type Light struct {
	Name       string
	State      bool
	DriverName string
	OutPin     uint8

	ControlByName string
	SwitchByName  string

	output DigitalOutput
	driver IoDriver
	hk     *accessory.Lightbulb
}

func (li *Light) Init(driver IoDriver) error {
	if !strings.EqualFold(driver.NameId(), li.DriverName) {
		return fmt.Errorf("Init failed, mismatched or incorrect driver")
	}

	if !driver.IsReady() {
		return fmt.Errorf("Init failed, driver not ready")
	}

	var err error

	li.driver = driver
	li.output, err = driver.GetOutput(li.OutPin)
	if err != nil {
		return errors.Wrap(err, "Init failed")
	}

	return nil
}

func (li *Light) Sync() {
	li.output.Set(li.State)
}

func (li *Light) GetHk() *accessory.Accessory {
	info := accessory.Info{
		Name:         li.Name,
		ID:           li.driver.GetUniqueId(li.OutPin),
		SerialNumber: fmt.Sprintf("light:%s:%02d", li.DriverName, li.OutPin),
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
