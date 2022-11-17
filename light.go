package swkit

import (
	"fmt"
	"strings"
	"sync"

	"github.com/brutella/hap/accessory"
	drivers "github.com/hubertat/swkit/drivers"
	"github.com/pkg/errors"
)

type Light struct {
	Name       string
	State      bool
	DriverName string
	OutPin     uint16

	ControlBy []ControllingDevice

	output drivers.DigitalOutput
	driver drivers.IoDriver
	hk     *accessory.Lightbulb
	lock   sync.Mutex
}

func (li *Light) GetDriverName() string {
	return li.DriverName
}

func (li *Light) Init(driver drivers.IoDriver) error {
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

func (li *Light) GetHk() *accessory.A {
	info := accessory.Info{
		Name:         li.Name,
		SerialNumber: fmt.Sprintf("light:%s:%02d", li.DriverName, li.OutPin),
	}
	li.hk = accessory.NewLightbulb(info)
	li.hk.Lightbulb.On.OnValueRemoteUpdate(li.SetValue)

	return li.hk.A
}

func (li *Light) SetValue(state bool) {
	li.State = state

	li.Sync()
}

func (li *Light) Toggle() {
	li.State = !li.State

	li.Sync()
}
