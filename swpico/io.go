package swpico

import (
	"errors"
	"fmt"
	"machine"
	"sync"
	"time"
)

const debounceClickTime = 50 * time.Millisecond
const clickToClickDuration = 300 * time.Millisecond

type Input struct {
	pin machine.Pin

	clickedEvents []Event

	debounceClick *Debouncer

	clickTimer      *time.Timer
	clickInProgress int
	clickLock       sync.Mutex
}

func (i *Input) click() {
	i.clickLock.Lock()
	defer i.clickLock.Unlock()

	switch len(i.clickedEvents) {
	case 0:
		// nothing to do

	case 1:
		i.clickedEvents[0].Fire()

	case 2, 3:
		if i.clickTimer != nil {
			i.clickTimer.Stop()
		}

		// check if current click isn't out of events range
		if i.clickInProgress >= len(i.clickedEvents) {
			return
		}

		eventIndex := i.clickInProgress

		// check if it's the last click
		if len(i.clickedEvents)-eventIndex == 1 {
			i.clickedEvents[eventIndex].Fire()
			return
		}

		// it's not last, increment
		i.clickTimer = time.AfterFunc(clickToClickDuration, func() {
			i.clickLock.Lock()
			defer i.clickLock.Unlock()

			i.clickInProgress = 0
			i.clickedEvents[eventIndex].Fire()
		})
		i.clickInProgress++

	}
}

func (i *Input) call(pin machine.Pin) {
	i.click()
	return
}

func (i *Input) Read() bool {
	return i.pin.Get()
}

func (i *Input) AppendClickedEvent(e Event) {
	i.clickedEvents = append(i.clickedEvents, e)
}

func (i *Input) ClearClickedEvents() {
	i.clickedEvents = nil
}

type Output struct {
	pin      machine.Pin
	inverted bool
}

func (o *Output) State() bool {
	if o.inverted {
		return !o.pin.Get()
	}
	return o.pin.Get()
}

func (o *Output) set(on bool) {
	if o.inverted {
		on = !on
	}

	o.pin.Set(on)

	go func() {
		led := machine.LED

		led.Low()
		time.Sleep(time.Millisecond * 40)
		led.High()
		time.Sleep(time.Millisecond * 40)
		led.Low()
		time.Sleep(time.Millisecond * 40)
		led.High()
		time.Sleep(time.Millisecond * 40)
		led.Low()
	}()
}

func (o *Output) SwitchOn() {
	o.set(true)
}

func (o *Output) SwitchOff() {
	o.set(false)
}

func (o *Output) Toggle() {
	o.set(!o.State())
}

type InputSlice []Input

func (is InputSlice) SetupPins() error {
	for _, in := range is {
		in.pin.Configure(machine.PinConfig{Mode: machine.PinInputPullup})
		err := in.pin.SetInterrupt(machine.PinFalling, in.call)
		if err != nil {
			return errors.Join(err, fmt.Errorf("failed on setting interrupt for pin: %v", in.pin))
		}

		in.debounceClick = NewDebouncer(debounceClickTime)
	}

	return nil
}

type OutputSlice []Output

func (os OutputSlice) SetupPins() error {
	for _, out := range os {
		out.pin.Configure(machine.PinConfig{Mode: machine.PinOutput})
	}

	return nil
}

type Debouncer struct {
	sync.Mutex
	debounceTime time.Duration
	timer        *time.Timer
	active       bool
}

func NewDebouncer(debounceTime time.Duration) *Debouncer {
	return &Debouncer{
		debounceTime: debounceTime,
	}
}

func (d *Debouncer) Do(f func()) {
	d.Lock()
	defer d.Unlock()

	if d.active {
		return
	}

	d.active = true

	d.timer = time.AfterFunc(d.debounceTime, func() {
		d.Lock()
		defer d.Unlock()
		f()
		d.active = false
	})
}

func (d *Debouncer) DoNext(first, next func()) {
	d.Lock()
	defer d.Unlock()

	if d.active {
		d.timer.Stop()
		d.active = false
		first()
	}

	d.active = true

	d.timer = time.AfterFunc(d.debounceTime, func() {
		d.Lock()
		defer d.Unlock()
		next()
		d.active = false
	})
}
