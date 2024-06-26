package swkit

import (
	"fmt"
	"hash/fnv"
	"strings"

	drivers "github.com/hubertat/swkit/drivers"

	"github.com/brutella/hap/accessory"
	"github.com/brutella/hap/characteristic"
	"github.com/pkg/errors"
)

type Switch struct {
	Name           string
	State          bool
	DriverName     string
	InPin          uint16
	DisableHomekit bool
	IsFaulty       bool

	switchSlice []SwitchableDevice

	input  drivers.DigitalInput
	driver drivers.IoDriver

	hk    *accessory.Switch
	fault *characteristic.StatusFault
}

type SwitchableDevice interface {
	SetValue(bool)
}

func (swb *Switch) GetDriverName() string {
	return swb.DriverName
}

func (swb *Switch) GetUniqueId() uint64 {
	hash := fnv.New64()
	hash.Write([]byte("Switch_" + swb.Name))
	return hash.Sum64()
}

func (swb *Switch) Init(driver drivers.IoDriver) error {
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

	if swb.DisableHomekit {
		return nil
	}

	info := accessory.Info{
		Name:         swb.Name,
		SerialNumber: fmt.Sprintf("switch:%s:%02d", swb.DriverName, swb.InPin),
	}
	swb.hk = accessory.NewSwitch(info)

	swb.fault = characteristic.NewStatusFault()
	swb.fault.SetValue(characteristic.StatusFaultNoFault)
	swb.hk.Switch.AddC(swb.fault.C)

	swb.hk.Switch.On.OnValueRemoteUpdate(swb.Set)

	return nil
}
func (swb *Switch) Sync() (err error) {
	oldState := swb.State

	swb.State, err = swb.input.GetState()

	if swb.hk != nil {
		if err != nil {
			swb.fault.SetValue(characteristic.StatusFaultGeneralFault)
			swb.IsFaulty = true
		} else {
			swb.fault.SetValue(characteristic.StatusFaultNoFault)
			swb.IsFaulty = false
		}
	}

	if err != nil {
		return errors.Wrap(err, "Sync failed")
	}

	if oldState != swb.State && swb.hk != nil {
		swb.hk.Switch.On.SetValue(swb.State)
	}

	if len(swb.switchSlice) > 0 {
		for _, controlledDevice := range swb.switchSlice {
			controlledDevice.SetValue(swb.State)
		}
	}

	return
}

func (swb *Switch) GetHk() *accessory.A {
	if swb.hk == nil {
		return nil
	}
	return swb.hk.A
}

func (swb *Switch) Set(value bool) {
	swb.State = value
}

func (swb *Switch) GetValue() bool {
	return swb.State
}
