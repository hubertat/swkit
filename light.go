package main

import (
	"fmt"
	"strings"
	"sync"

	"github.com/brutella/hc/accessory"
	"github.com/pkg/errors"
)

type Light struct {
	Name       string
	State      bool
	DriverName string
	OutPin     uint8

	ControlBy []ControllingDevice

	output DigitalOutput
	driver IoDriver
	hk     *accessory.Lightbulb
	lock   sync.Mutex
}

func (li *Light) GetDriverName() string {
	return li.DriverName
}

func (li *Light) Init(driver IoDriver) error {
	if !strings.EqualFold(driver.NameId(), li.DriverName) {
		return fmt.Errorf("Init failed, mismatched or incorrect driver")
	}

	if !driver.IsReady() {
		return fmt.Errorf("Init failed, driver not ready")
	}

	li.lock = sync.Mutex{}

	var err error

	li.driver = driver
	li.output, err = driver.GetOutput(li.OutPin)
	if err != nil {
		return errors.Wrap(err, "Init failed")
	}

	return nil
}

func (li *Light) Sync() error {
	li.lock.Lock()
	defer li.lock.Unlock()

	li.hk.Lightbulb.On.SetValue(li.State)
	return li.output.Set(li.State)
}

func (li *Light) GetControllers() []ControllingDevice {
	return li.ControlBy
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

	li.Sync()
}

func (li *Light) Toggle() {
	li.State = !li.State

	li.Sync()
}
