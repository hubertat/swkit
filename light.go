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

	output drivers.DigitalOutput

	hk    *accessory.Lightbulb
	fault *characteristic.StatusFault
	lock  sync.Mutex
}

func NewLight(config LightConfig, dOut drivers.DigitalOutput) *Light {
	return &Light{
		name:           config.Name,
		disableHomekit: config.DisableHomekit,

		output: dOut,
		lock:   sync.Mutex{},
	}
}

func (li *Light) GetUniqueId() uint64 {
	hash := fnv.New64()
	hash.Write([]byte("Light_" + li.name))
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

	li.hk.Lightbulb.On.OnValueRemoteUpdate(li.SetValue)

	return li.hk.A
}

// Sync() is called periodically by swkit managing server to sync from drivers io
// If subscribe model is available and used this should be skipped
// If there is no homekit, there is no internal state - skip
func (li *Light) Sync() (err error) {
	if li.disableHomekit {
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
		li.hk.Lightbulb.On.SetValue(onState)
	}

	return nil
}

func (li *Light) GetControllers() []ControllingDevice {
	return li.ControlBy
}

func (li *Light) SetValue(state bool) {
	li.output.Set(state)
}

func (li *Light) Toggle() {
	oldState, err := li.output.GetState()
	if err == nil {
		li.SetValue(!oldState)
	}
}
