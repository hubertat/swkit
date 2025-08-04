package swkit

import (
	"fmt"
	"hash/fnv"
	"strings"

	"github.com/brutella/hap/accessory"
	"github.com/brutella/hap/characteristic"
	"github.com/brutella/hap/service"
	drivers "github.com/hubertat/swkit/drivers"
)

type ButtonConfig struct {
	Name           string
	EventInputName string
	ControlDevices []string

	DisableHomekit bool
}

type Button struct {
	name           string
	disableHomekit bool

	lastState bool

	controlThis []ControlDevice

	emitter drivers.PushEventEmitter

	hk    *accessory.A
	fault *characteristic.StatusFault
	ss    *service.StatelessProgrammableSwitch
}

type ControlDevice struct {
	dev    Controllable
	e      drivers.PushEvent
	action string
}

// ParseControlDeviceString parses a control device string and returns the event, action, and device name.
// control device string is expected in one of two formats:
// 1. <swkit_event>:<action>:<device_name>
// 2. <swkit_event>:<device_name>
func ParseControlDeviceString(s string) (e drivers.PushEvent, action string, devName string, err error) {
	if len(s) == 0 {
		err = fmt.Errorf("invalid control device string, it cannot be empty")
		return
	}

	sSlice := strings.Split(s, ":")
	if len(sSlice) < 2 || len(sSlice) > 3 {
		err = fmt.Errorf("invalid control device string format %s", s)
		return
	}

	eventFound := false
	for _, ev := range drivers.AllPushEvents() {
		if strings.EqualFold(ev.String(), sSlice[0]) {
			e = ev
			eventFound = true
			break
		}
	}

	if !eventFound {
		err = fmt.Errorf("invalid control device event %s", sSlice[0])
		return
	}

	if len(sSlice) == 3 {
		action = sSlice[1]
	}

	devName = sSlice[len(sSlice)-1]

	return
}

func NewButton(config ButtonConfig, emitter drivers.PushEventEmitter, control []ControlDevice) *Button {
	b := &Button{
		name:           config.Name,
		disableHomekit: config.DisableHomekit,
		controlThis:    control,
		emitter:        emitter,
	}

	var evs drivers.PushEvent
	for _, c := range control {
		evs &= c.e
	}

	emitter.Subscribe(evs, b.HandlePushEvent)

	return b
}

func (bu *Button) Name() string {
	return bu.name
}

func (bu *Button) HandlePushEvent(e drivers.PushEvent) {
	for _, ctrl := range bu.controlThis {
		if ctrl.e == e {
			switch ctrl.action {
			case "on":
				ctrl.dev.SetValue(true)
			case "off":
				ctrl.dev.SetValue(false)
			default:
				ctrl.dev.Toggle()
			}
		}
	}
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
func (bu *Button) Sync(force bool) (err error) {
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
