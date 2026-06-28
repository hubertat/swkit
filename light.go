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

type LightConfig struct {
	Name           string
	DigitalOutName string
	DisableHomekit bool
}

type Light struct {
	name           string
	disableHomekit bool
	isFaulty       bool

	output       drivers.DigitalOutput
	withCallback bool
	logger       *log.Logger

	hk    *accessory.Lightbulb
	fault *characteristic.StatusFault
	lock  sync.Mutex
}

func NewLight(config LightConfig, dOut drivers.DigitalOutput, logger *log.Logger) *Light {
	logger.Debug("light created", "name", config.Name, "output", dOut.String())
	return &Light{
		name:           config.Name,
		disableHomekit: config.DisableHomekit,
		output:         dOut,
		logger:         logger,
		lock:           sync.Mutex{},
	}
}

func (li *Light) GetUniqueId() uint64 {
	hash := fnv.New64()
	hash.Write([]byte("Light_" + li.name))
	return hash.Sum64()
}

func (li *Light) InitHk() *accessory.A {
	if li.disableHomekit {
		li.logger.Debug("homekit disabled for light", "name", li.name)
		return nil
	}

	info := accessory.Info{
		Name:         li.name,
		SerialNumber: fmt.Sprintf("light:%s", li.output.String()),
	}
	li.hk = accessory.NewLightbulb(info)

	li.fault = characteristic.NewStatusFault()
	li.fault.SetValue(characteristic.StatusFaultNoFault)
	li.hk.Lightbulb.AddC(li.fault.C)

	li.hk.Lightbulb.On.OnValueRemoteUpdate(li.SetValue)

	// set callback to update on remote value change
	li.withCallback = li.output.SetOnStateUpdate(li.hk.Lightbulb.On.SetValue) == nil

	state, err := li.output.GetState()
	if err == nil {
		li.hk.Lightbulb.On.SetValue(state)
	}

	li.logger.Debug("homekit accessory initialized", "light", li.name, "withCallback", li.withCallback, "initialState", state)
	return li.hk.A
}

// Name() returns an objects name
func (li *Light) Name() string {
	return li.name
}

// Sync() is called periodically by swkit managing server to sync from drivers io
// If subscribe model is available and used this should be skipped
// If there is no homekit, there is no internal state - skip
func (li *Light) Sync(force bool) (err error) {
	if li.disableHomekit {
		return nil
	}

	if li.withCallback && !force {
		if li.output.IsHealthy() {
			if li.isFaulty {
				li.logger.Debug("light health restored", "light", li.name)
			}
			li.fault.SetValue(characteristic.StatusFaultNoFault)
			li.isFaulty = false
		} else {
			if !li.isFaulty {
				li.logger.Debug("light became faulty", "light", li.name)
			}
			li.fault.SetValue(characteristic.StatusFaultGeneralFault)
			li.isFaulty = true
		}
		return nil
	}

	li.lock.Lock()
	defer li.lock.Unlock()

	onState, err := li.output.GetState()

	if err != nil {
		li.fault.SetValue(characteristic.StatusFaultGeneralFault)
		li.isFaulty = true
		li.logger.Debug("sync failed to get state", "light", li.name, "err", err)
		return errors.Wrap(err, "Sync failed on output.GetState()")
	}

	li.fault.SetValue(characteristic.StatusFaultNoFault)
	li.isFaulty = false

	if onState != li.hk.Lightbulb.On.Value() {
		li.logger.Debug("sync detected state mismatch, updating homekit", "light", li.name, "hwState", onState, "hkState", li.hk.Lightbulb.On.Value())
		li.hk.Lightbulb.On.SetValue(onState)
	}

	return nil
}

// GetState reports the current on/off state from the digital output.
func (li *Light) GetState() (bool, error) {
	return li.output.GetState()
}

func (li *Light) SetValue(state bool) {
	li.logger.Debug("setting light value", "light", li.name, "state", state)
	li.output.Set(state)
}

func (li *Light) Toggle() {
	oldState, err := li.output.GetState()
	if err == nil {
		li.logger.Debug("toggling light", "light", li.name, "oldState", oldState, "newState", !oldState)
		li.SetValue(!oldState)
	} else {
		li.logger.Debug("toggle failed to get current state", "light", li.name, "err", err)
	}
}
