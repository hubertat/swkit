package swkit

import (
	"fmt"
	"hash/fnv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/brutella/hap/accessory"
	"github.com/brutella/hap/characteristic"
	"github.com/brutella/hap/service"
	"github.com/charmbracelet/log"
	"github.com/hubertat/swkit/app"
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
	logger  *log.Logger

	hk    *accessory.A
	fault *characteristic.StatusFault
	ss    *service.StatelessProgrammableSwitch

	// lastEventType and lastEventTime track the most recent push event.
	// Written from driver goroutines, read from state polling — use atomics.
	lastEventType atomic.Uint32
	lastEventTime atomic.Int64 // UnixNano; 0 = never
}

type ControlDevice struct {
	dev   Controllable
	e     drivers.PushEvent
	verb  string
	level int
}

// ParseControlDeviceString parses a control device string into its push event
// and the action to apply. The grammar is "<event>:" followed by the shared
// Action grammar (see app.ParseAction), i.e. one of:
//
//	<event>:<device>                       (defaults to toggle)
//	<event>:<verb>:<device>                (on|off|toggle)
//	<event>:<verb>:<level>:<device>        (brightness family)
func ParseControlDeviceString(s string) (e drivers.PushEvent, act app.Action, err error) {
	if len(s) == 0 {
		err = fmt.Errorf("invalid control device string, it cannot be empty")
		return
	}

	sSlice := strings.Split(s, ":")
	if len(sSlice) < 2 || len(sSlice) > 4 {
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

	remainder := sSlice[1:]
	if len(remainder) == 1 {
		// <event>:<device> -> default to toggle.
		act = app.Action{Verb: "toggle", Device: remainder[0]}
		return
	}

	act, err = app.ParseAction(strings.Join(remainder, ":"))
	return
}

func NewButton(config ButtonConfig, emitter drivers.PushEventEmitter, control []ControlDevice, logger *log.Logger) *Button {
	b := &Button{
		name:           config.Name,
		disableHomekit: config.DisableHomekit,
		controlThis:    control,
		emitter:        emitter,
		logger:         logger,
	}

	var evs drivers.PushEvent
	// TODO
	// consider should we detect from homekit which events are actually needed?
	if !config.DisableHomekit {
		evs |= drivers.PushEventSinglePress
		evs |= drivers.PushEventDoublePress
		evs |= drivers.PushEventLongPress
	}
	for _, c := range control {
		evs |= c.e
	}

	logger.Debug("button created", "name", config.Name, "emitter", emitter.String(), "controlDevices", len(control))
	emitter.Subscribe(evs, b.HandlePushEvent)

	return b
}

func (bu *Button) Name() string {
	return bu.name
}

// LastEvent returns the most recent push event type and the time it occurred.
// Returns a zero time if no event has been received since startup.
func (bu *Button) LastEvent() (drivers.PushEvent, time.Time) {
	nano := bu.lastEventTime.Load()
	if nano == 0 {
		return 0, time.Time{}
	}
	return drivers.PushEvent(bu.lastEventType.Load()), time.Unix(0, nano)
}

func (bu *Button) HandlePushEvent(e drivers.PushEvent) {
	bu.lastEventType.Store(uint32(e))
	bu.lastEventTime.Store(time.Now().UnixNano())
	bu.logger.Debug("received push event", "button", bu.name, "event", e.String())

	if !bu.disableHomekit {
		switch e {
		case drivers.PushEventSinglePress:
			bu.ss.ProgrammableSwitchEvent.SetValue(characteristic.ProgrammableSwitchEventSinglePress)
		case drivers.PushEventDoublePress:
			bu.ss.ProgrammableSwitchEvent.SetValue(characteristic.ProgrammableSwitchEventDoublePress)
		case drivers.PushEventLongPress:
			bu.ss.ProgrammableSwitchEvent.SetValue(characteristic.ProgrammableSwitchEventLongPress)
		}
	}

	for _, ctrl := range bu.controlThis {
		if ctrl.e == e {
			bu.logger.Debug("executing control action", "button", bu.name, "device", ctrl.dev.Name(), "verb", ctrl.verb, "level", ctrl.level)
			if err := applyVerb(ctrl.dev, ctrl.verb, ctrl.level); err != nil {
				bu.logger.Error("control action failed", "button", bu.name, "device", ctrl.dev.Name(), "err", err)
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
		bu.logger.Debug("homekit disabled for button", "name", bu.name)
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

	bu.logger.Debug("homekit accessory initialized", "button", bu.name)
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
