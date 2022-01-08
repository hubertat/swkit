package main

import (
	"fmt"
	"strings"

	"github.com/brutella/hc/accessory"
	"github.com/pkg/errors"
)

type Switch struct {
	Name       string
	State      bool
	DriverName string
	InPin      uint16

	DisableHomeKit bool

	switchThis []SwitchableDevice
	input      DigitalInput
	driver     IoDriver
	hk         *accessory.Switch
}

type SwitchableDevice interface {
	SetValue(bool)
}

func (swb *Switch) GetDriverName() string {
	return swb.DriverName
}

func (swb *Switch) Init(driver IoDriver) error {
	if !strings.EqualFold(driver.NameId(), swb.DriverName) {
		return fmt.Errorf("Init failed, mismatched or incorrect driver")
	}

	if !driver.IsReady() {
		return fmt.Errorf("Init failed, driver not ready")
	}

	var err error

	swb.driver = driver
	swb.input, err = driver.GetInput(swb.InPin)
	if err != nil {
		return errors.Wrap(err, "Init failed")
	}

	return nil
}
func (swb *Switch) Sync() (err error) {
	swb.State, err = swb.input.GetState()

	if swb.hk != nil {
		swb.hk.Switch.On.SetValue(swb.State)
	}

	if len(swb.switchThis) > 0 {
		for _, controlledDevice := range swb.switchThis {
			controlledDevice.SetValue(swb.State)
		}
	}

	return
}

func (swb *Switch) GetHk() *accessory.Accessory {
	if swb.DisableHomeKit {
		return nil
	}

	info := accessory.Info{
		Name:         swb.Name,
		ID:           swb.driver.GetUniqueId(swb.InPin),
		SerialNumber: fmt.Sprintf("switch:%s:%02d", swb.DriverName, swb.InPin),
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
