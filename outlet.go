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

	ControlBy []ControllingDevice

	output DigitalOutput
	driver IoDriver
	hk     *accessory.Outlet
}

func (ou *Outlet) GetDriverName() string {
	return ou.DriverName
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

func (ou *Outlet) Sync() error {
	return ou.output.Set(ou.State)
}

func (ou *Outlet) GetControllers() []ControllingDevice {
	return ou.ControlBy
}

func (ou *Outlet) GetHk() *accessory.Accessory {
	info := accessory.Info{
		Name:         ou.Name,
		ID:           ou.driver.GetUniqueId(ou.OutPin),
		SerialNumber: fmt.Sprintf("outlet:%s:%02d", ou.DriverName, ou.OutPin),
	}
	ou.hk = accessory.NewOutlet(info)
	ou.hk.Outlet.On.OnValueRemoteUpdate(ou.SetValue)

	return ou.hk.Accessory
}

func (ou *Outlet) SetValue(state bool) {
	ou.State = state
	ou.hk.Outlet.On.SetValue(ou.State)

	ou.Sync()
}

func (ou *Outlet) Toggle() {
	ou.State = !ou.State
	ou.hk.Outlet.On.SetValue(ou.State)

	ou.Sync()
}
