package main

import (
	"fmt"
	"strings"

	"github.com/brutella/hc/accessory"
	"github.com/pkg/errors"
)

type Button struct {
	Name       string
	State      bool
	DriverName string
	InPin      uint16

	DisableHomeKit bool

	toggleThis []ClickableDevice
	input      DigitalInput
	driver     IoDriver
	hk         *accessory.Accessory
}

type ClickableDevice interface {
	Toggle()
}

func (bu *Button) GetDriverName() string {
	return bu.DriverName
}

func (bu *Button) Init(driver IoDriver) error {
	if !strings.EqualFold(driver.NameId(), bu.DriverName) {
		return fmt.Errorf("Init failed, mismatched or incorrect driver")
	}

	if !driver.IsReady() {
		return fmt.Errorf("Init failed, driver not ready")
	}

	var err error

	bu.driver = driver
	bu.input, err = driver.GetInput(bu.InPin)
	if err != nil {
		return errors.Wrap(err, "Init failed on getting input")
	}
	bu.State, err = bu.input.GetState()
	if err != nil {
		return errors.Wrap(err, "Init failed, on reading state")
	}

	return nil
}

func (bu *Button) Sync() (err error) {
	oldState := bu.State
	bu.State, err = bu.input.GetState()

	if bu.State != oldState && bu.State {
		for _, clickable := range bu.toggleThis {
			clickable.Toggle()
		}
	}

	return
}

func (bu *Button) GetHk() *accessory.Accessory {
	return bu.hk
}

func (bu *Button) Set(value bool) {
	fmt.Printf("DEBUG buttin setting value(%v) from HK\n", value)
	bu.State = value
}

func (bu *Button) GetValue() bool {
	fmt.Printf("DEBUG button getting value(%v)to -> HK\n", bu.State)
	return bu.State
}
