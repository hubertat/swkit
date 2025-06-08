package swkit

import (
	"fmt"
	"hash/fnv"
	"sync"

	"github.com/brutella/hap/accessory"
	"github.com/brutella/hap/characteristic"
	drivers "github.com/hubertat/swkit/drivers"
	"github.com/pkg/errors"
)

type OutletConfig struct {
	Name           string
	DigitalOutName string
	DisableHomekit bool
}

type Outlet struct {
	ControlBy []ControllingDevice

	name           string
	disableHomekit bool
	isFaulty       bool

	output       drivers.DigitalOutput
	withCallback bool

	hk    *accessory.Outlet
	fault *characteristic.StatusFault

	lock sync.Mutex
}

func NewOutlet(config OutletConfig, dOut drivers.DigitalOutput) *Outlet {
	return &Outlet{
		name:           config.Name,
		disableHomekit: config.DisableHomekit,
		output:         dOut,
		lock:           sync.Mutex{},
	}
}

func (ou *Outlet) GetUniqueId() uint64 {
	hash := fnv.New64()
	hash.Write([]byte("Outlet_" + ou.name))
	return hash.Sum64()
}

func (ou *Outlet) InitHk() *accessory.A {

	if ou.disableHomekit {
		return nil
	}

	info := accessory.Info{
		Name:         ou.name,
		SerialNumber: fmt.Sprintf("outlet:%s", ou.output.String()),
	}
	ou.hk = accessory.NewOutlet(info)

	ou.fault = characteristic.NewStatusFault()
	ou.fault.SetValue(characteristic.StatusFaultNoFault)
	ou.hk.Outlet.AddC(ou.fault.C)

	ou.hk.Outlet.On.OnValueRemoteUpdate(ou.SetValue)

	ou.withCallback = ou.output.SetOnStateUpdate(ou.hk.Outlet.On.SetValue) == nil

	return ou.hk.A
}

func (ou *Outlet) Sync(force bool) error {
	if ou.disableHomekit {
		return nil
	}

	if ou.withCallback && !force {
		if ou.output.IsHealthy() {
			ou.isFaulty = false
			ou.fault.SetValue(characteristic.StatusFaultNoFault)
		} else {
			ou.isFaulty = true
			ou.fault.SetValue(characteristic.StatusFaultGeneralFault)
		}
		return nil
	}

	ou.lock.Lock()
	defer ou.lock.Unlock()

	onState, err := ou.output.GetState()
	if err != nil {
		ou.fault.SetValue(characteristic.StatusFaultGeneralFault)
		ou.isFaulty = true
		return errors.Wrap(err, "Sync failed for Outlet, GetState failed")
	}

	ou.fault.SetValue(characteristic.StatusFaultNoFault)
	ou.isFaulty = false

	if onState != ou.hk.Outlet.On.Value() {
		ou.hk.Outlet.On.SetValue(onState)
	}

	return nil
}

func (ou *Outlet) GetControllers() []ControllingDevice {
	return ou.ControlBy
}

func (ou *Outlet) SetValue(state bool) {
	ou.output.Set(state)
}

func (ou *Outlet) Toggle() {
	oldState, err := ou.output.GetState()
	if err != nil {
		return
	}
	ou.SetValue(!oldState)
}
