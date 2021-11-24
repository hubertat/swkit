package main

import (
	"fmt"
	"strings"

	"github.com/brutella/hc/accessory"
)

type RemoteIoSlave struct {
	Token   string
	Inputs  []InFromRemoteIo
	Outputs []OutFromRemoteIo
}

type OutFromRemoteIo struct {
	DriverName string
	OutPin     uint8

	output DigitalOutput
	driver IoDriver
}

func (ofr *OutFromRemoteIo) Sync() error {
	return nil
}

func (ofr *OutFromRemoteIo) GetHk() *accessory.Accessory {
	return nil
}

func (ofr *OutFromRemoteIo) Init(driver IoDriver) error {
	if !strings.EqualFold(driver.NameId(), ofr.DriverName) {
		return fmt.Errorf("Init failed, mismatched or incorrect driver")
	}

	if !driver.IsReady() {
		return fmt.Errorf("Init failed, driver not ready")
	}

	return nil
}

func (ofr *OutFromRemoteIo) GetDriverName() string {
	return ofr.DriverName
}

type InFromRemoteIo struct {
	DriverName string
	InPin      uint8

	input  DigitalInput
	driver IoDriver
}

func (ifr *InFromRemoteIo) Sync() error {
	return nil
}

func (ifr *InFromRemoteIo) GetHk() *accessory.Accessory {
	return nil
}

func (ifr *InFromRemoteIo) Init(driver IoDriver) error {
	if !strings.EqualFold(driver.NameId(), ifr.DriverName) {
		return fmt.Errorf("Init failed, mismatched or incorrect driver")
	}

	if !driver.IsReady() {
		return fmt.Errorf("Init failed, driver not ready")
	}

	return nil
}

func (ifr *InFromRemoteIo) GetDriverName() string {
	return ifr.DriverName
}
