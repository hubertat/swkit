package swkit

import (
	"fmt"
	"hash/fnv"

	"github.com/brutella/hap/accessory"
	"github.com/brutella/hap/characteristic"
	"github.com/brutella/hap/service"
	drivers "github.com/hubertat/swkit/drivers"
)

type ButtonConfig struct {
	Name           string
	DigitalInName  string
	DisableHomekit bool
}

type Button struct {
	name           string
	disableHomekit bool

	lastState bool

	toggleThis []ClickableDevice

	emitter drivers.PushEventEmitter

	hk    *accessory.A
	fault *characteristic.StatusFault
	ss    *service.StatelessProgrammableSwitch
}

type ClickableDevice interface {
	Toggle()
}

func (bu *Button) GetUniqueId() uint64 {
	hash := fnv.New64()
	hash.Write([]byte("Button_" + bu.name))
	return hash.Sum64()
}

func (bu *Button) InitHk() *accessory.A {
	if bu.disableHomekit {
		return nil
	}

	bu.hk = accessory.New(accessory.Info{
		Name:         bu.name,
		SerialNumber: fmt.Sprintf("button:%s", bu.emitter.String()),
	}, accessory.TypeProgrammableSwitch)

	bu.ss = service.NewStatelessProgrammableSwitch()
	bu.fault = characteristic.NewStatusFault()
	bu.fault.SetValue(characteristic.StatusFaultNoFault)

	bu.ss.AddC(bu.fault.C)
	bu.hk.AddS(bu.ss.S)

	return bu.hk
}

// Is Sync required for a stateless switch? Maybe only for error checking
// TODO to consider
func (bu *Button) Sync() (err error) {
	if bu.disableHomekit {
		return nil
	}

	return nil
}

// func (bu *Button) Set(value bool) {

// }

// func (bu *Button) GetValue() bool {

// 	state, _ := bu.input.GetState()
// 	return state
// }
