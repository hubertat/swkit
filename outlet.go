package swkit

import (
	"fmt"
	"hash/fnv"
	"strings"
	"sync"

	"github.com/brutella/hap/accessory"
	drivers "github.com/hubertat/swkit/drivers"
	"github.com/pkg/errors"
)

type Outlet struct {
	Name           string
	State          bool
	DriverName     string
	OutPin         uint16
	DisableHomekit bool

	ControlBy []ControllingDevice

	output drivers.DigitalOutput
	driver drivers.IoDriver
	hk     *accessory.Outlet
	lock   sync.Mutex
}

func (ou *Outlet) GetDriverName() string {
	return ou.DriverName
}

func (ou *Outlet) GetUniqueId() uint64 {
	hash := fnv.New64()
	hash.Write([]byte("Outlet_" + ou.Name))
	return hash.Sum64()
}

func (ou *Outlet) Init(driver drivers.IoDriver) error {
	if !strings.EqualFold(driver.NameId(), ou.DriverName) {
		return fmt.Errorf("Init failed, mismatched or incorrect driver")
	}

	if !driver.IsReady() {
		return fmt.Errorf("Init failed, driver not ready")
	}
	ou.lock = sync.Mutex{}
	var err error

	ou.driver = driver
	ou.output, err = driver.GetOutput(ou.OutPin)
	if err != nil {
		return errors.Wrap(err, "Init failed")
	}

	if ou.DisableHomekit {
		return nil
	}
	info := accessory.Info{
		Name:         ou.Name,
		SerialNumber: fmt.Sprintf("outlet:%s:%02d", ou.DriverName, ou.OutPin),
	}
	ou.hk = accessory.NewOutlet(info)
	ou.hk.Outlet.On.OnValueRemoteUpdate(ou.SetValue)
	return nil
}

func (ou *Outlet) Sync() error {
	ou.lock.Lock()
	defer ou.lock.Unlock()

	if ou.hk != nil {
		ou.hk.Outlet.On.SetValue(ou.State)
	}

	return ou.output.Set(ou.State)
}

func (ou *Outlet) GetControllers() []ControllingDevice {
	return ou.ControlBy
}

func (ou *Outlet) GetHk() *accessory.A {

	return ou.hk.A
}

func (ou *Outlet) SetValue(state bool) {
	ou.State = state

	ou.Sync()
}

func (ou *Outlet) Toggle() {
	ou.State = !ou.State

	ou.Sync()
}
