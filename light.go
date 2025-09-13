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

type LightConfig struct {
	Name           string
	DigitalOutName string
	DisableHomekit bool
}

type Light struct {
	ControlBy []ControllingDevice

	name           string
	disableHomekit bool
	isFaulty       bool

	output       drivers.DigitalOutput
	withCallback bool

	hk    *accessory.Lightbulb
	fault *characteristic.StatusFault
	lock  sync.Mutex
	log   logger.EventLogger
}

func NewLight(config LightConfig, dOut drivers.DigitalOutput, logger logger.EventLogger) *Light {
	return &Light{
		name:           config.Name,
		disableHomekit: config.DisableHomekit,

		output: dOut,
		lock:   sync.Mutex{},
		log:    logger,
	}
}

func (li *Light) getUniqueName() string {
	return fmt.Sprintf("Light_%s", li.name)
}

func (li *Light) GetUniqueId() uint64 {
	hash := fnv.New64()
	hash.Write([]byte(li.getUniqueName()))
	return hash.Sum64()
}

func (li *Light) InitHk() *accessory.A {
	if li.disableHomekit {
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

	li.hk.Lightbulb.On.OnValueRemoteUpdate(func(state bool) {
		li.SetValue(state, "homekit")
	})

	// set callback to update on remote value change
	// TODO consider altering sync method if digital output gives this option (check error)
	li.withCallback = li.output.SetOnStateUpdate(li.UpdateState) == nil

	state, err := li.output.GetState()
	if err != nil {
		li.hk.Lightbulb.On.SetValue(state)
	}

	return li.hk.A
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
			li.fault.SetValue(characteristic.StatusFaultNoFault)
			li.isFaulty = false
		} else {
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
		return errors.Wrap(err, "Sync failed on output.GetState()")
	}

	li.fault.SetValue(characteristic.StatusFaultNoFault)
	li.isFaulty = false

	if onState != li.hk.Lightbulb.On.Value() {
		li.UpdateState(onState, "internal: sync")
	}

	return nil
}

func (li *Light) GetControllers() []ControllingDevice {
	return li.ControlBy
}

func (li *Light) SetValue(state bool, source string) {
	var uintState uint64
	if state {
		uintState = 1
	}
	li.log.LogEvent(logger.EventTypeSetValue, uintState, source, li.getUniqueName())

	li.output.Set(state)
}

func (li *Light) UpdateState(state bool, source string) {
	var uintState uint64
	if state {
		uintState = 1
	}
	li.log.LogEvent(logger.EventTypeUpdateState, uintState, source, li.getUniqueName())

	if li.disableHomekit {
		return
	}
	li.hk.Lightbulb.On.SetValue(state)
}

func (li *Light) Toggle(source string) {
	li.log.LogEvent(logger.EventTypeToggle, 0, source, li.getUniqueName())

	oldState, err := li.output.GetState()
	if err == nil {
		li.SetValue(!oldState, "toggle from: "+source)
	}
}
