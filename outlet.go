package main

import (
	"fmt"
	"strings"

	"github.com/brutella/hc/accessory"
	"github.com/pkg/errors"
)

type Outlet struct {
	Name       string
	State      bool
	DriverName string
	OutPin     uint8

	ControlByName string

	output DigitalOutput
	driver IoDriver
	hk     *accessory.Outlet
}

func (ou *Outlet) Init(driver IoDriver) error {
	if !strings.EqualFold(driver.NameId(), ou.DriverName) {
		return fmt.Errorf("Init failed, mismatched or incorrect driver")
	}

	if !driver.IsReady() {
		return fmt.Errorf("Init failed, driver not ready")
	}

	var err error

	ou.driver = driver
	ou.output, err = driver.GetOutput(ou.OutPin)
	if err != nil {
		return errors.Wrap(err, "Init failed")
	}

	return nil
}

func (ou *Outlet) Sync() {
	ou.output.Set(ou.State)
}

func (ou *Outlet) GetHk() *accessory.Accessory {
	info := accessory.Info{
		Name:         ou.Name,
		ID:           uint64(ou.OutPin), // TODO change ID, use driver type
		SerialNumber: fmt.Sprintf("outlet:%s:%02d", ou.DriverName, ou.OutPin),
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
