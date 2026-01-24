package swkit

import (
	"fmt"
	"hash/fnv"
	"sync"

	"github.com/brutella/hap/accessory"
	"github.com/brutella/hap/characteristic"
	"github.com/charmbracelet/log"
	drivers "github.com/hubertat/swkit/drivers"
	"github.com/pkg/errors"
)

type OutletConfig struct {
	Name           string
	DigitalOutName string
	DisableHomekit bool
}

type Outlet struct {
	name           string
	disableHomekit bool
	isFaulty       bool

	output       drivers.DigitalOutput
	withCallback bool
	logger       *log.Logger

	hk    *accessory.Outlet
	fault *characteristic.StatusFault

	lock sync.Mutex
}

func NewOutlet(config OutletConfig, dOut drivers.DigitalOutput, logger *log.Logger) *Outlet {
	logger.Debug("outlet created", "name", config.Name, "output", dOut.String())
	return &Outlet{
		name:           config.Name,
		disableHomekit: config.DisableHomekit,
		output:         dOut,
		logger:         logger,
		lock:           sync.Mutex{},
	}
}

// Name returns the name of the outlet object
func (ou *Outlet) Name() string {
	return ou.name
}

func (ou *Outlet) GetUniqueId() uint64 {
	hash := fnv.New64()
	hash.Write([]byte("Outlet_" + ou.name))
	return hash.Sum64()
}

func (ou *Outlet) InitHk() *accessory.A {
	if ou.disableHomekit {
		ou.logger.Debug("homekit disabled for outlet", "name", ou.name)
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

	ou.logger.Debug("homekit accessory initialized", "outlet", ou.name, "withCallback", ou.withCallback)
	return ou.hk.A
}

func (ou *Outlet) Sync(force bool) error {
	if ou.disableHomekit {
		return nil
	}

	if ou.withCallback && !force {
		if ou.output.IsHealthy() {
			if ou.isFaulty {
				ou.logger.Debug("outlet health restored", "outlet", ou.name)
			}
			ou.isFaulty = false
			ou.fault.SetValue(characteristic.StatusFaultNoFault)
		} else {
			if !ou.isFaulty {
				ou.logger.Debug("outlet became faulty", "outlet", ou.name)
			}
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
		ou.logger.Debug("sync failed to get state", "outlet", ou.name, "err", err)
		return errors.Wrap(err, "Sync failed for Outlet, GetState failed")
	}

	ou.fault.SetValue(characteristic.StatusFaultNoFault)
	ou.isFaulty = false

	if onState != ou.hk.Outlet.On.Value() {
		ou.logger.Debug("sync detected state mismatch, updating homekit", "outlet", ou.name, "hwState", onState, "hkState", ou.hk.Outlet.On.Value())
		ou.hk.Outlet.On.SetValue(onState)
	}

	return nil
}

func (ou *Outlet) SetValue(state bool) {
	ou.logger.Debug("setting outlet value", "outlet", ou.name, "state", state)
	ou.output.Set(state)
}

func (ou *Outlet) Toggle() {
	oldState, err := ou.output.GetState()
	if err != nil {
		ou.logger.Debug("toggle failed to get current state", "outlet", ou.name, "err", err)
		return
	}
	ou.logger.Debug("toggling outlet", "outlet", ou.name, "oldState", oldState, "newState", !oldState)
	ou.SetValue(!oldState)
}
