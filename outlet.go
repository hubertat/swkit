package swkit

import (
	"fmt"
	"hash/fnv"
	"sync"

	"github.com/brutella/hap/accessory"
	"github.com/brutella/hap/characteristic"
	drivers "github.com/hubertat/swkit/drivers"
	"github.com/hubertat/swkit/logger"
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

	log  logger.EventLogger
	lock sync.Mutex
}

func NewOutlet(config OutletConfig, dOut drivers.DigitalOutput, logger logger.EventLogger) *Outlet {
	return &Outlet{
		name:           config.Name,
		disableHomekit: config.DisableHomekit,
		output:         dOut,
		lock:           sync.Mutex{},
		log:            logger,
	}
}

func (ou *Outlet) getUniqueName() string {
	return fmt.Sprintf("outlet_%s", ou.name)
}

func (ou *Outlet) GetUniqueId() uint64 {
	hash := fnv.New64()
	hash.Write([]byte(ou.getUniqueName()))
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

	ou.hk.Outlet.On.OnValueRemoteUpdate(func(state bool) {
		ou.SetValue(state, "homekit")
	})

	ou.withCallback = ou.output.SetOnStateUpdate(ou.UpdateState) == nil

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
		ou.UpdateState(onState, "internal: sync")
	}

	return nil
}

func (ou *Outlet) GetControllers() []ControllingDevice {
	return ou.ControlBy
}

func (ou *Outlet) SetValue(state bool, source string) {
	var uintState uint64
	if state {
		uintState = 1
	}
	ou.log.LogEvent(logger.EventTypeSetValue, uintState, source, ou.getUniqueName())

	ou.output.Set(state)
}

func (ou *Outlet) UpdateState(state bool, source string) {
	var uintState uint64
	if state {
		uintState = 1
	}
	ou.log.LogEvent(logger.EventTypeUpdateState, uintState, source, ou.getUniqueName())

	if ou.disableHomekit {
		return
	}
	ou.hk.Outlet.On.SetValue(state)
}

func (ou *Outlet) Toggle(source string) {
	ou.log.LogEvent(logger.EventTypeToggle, 0, source, ou.getUniqueName())

	oldState, err := ou.output.GetState()
	if err != nil {
		return
	}
	ou.SetValue(!oldState, source)
}
