package swkit

import (
	"fmt"
	"strings"

	"github.com/brutella/hap/accessory"
	"github.com/brutella/hap/characteristic"
	"github.com/brutella/hap/service"
	drivers "github.com/hubertat/swkit/drivers"
	"github.com/pkg/errors"
)

type MotionSensor struct {
	Name       string
	State      bool
	DriverName string
	InPin      uint16

	DisableHomeKit bool

	input       drivers.DigitalInput
	driver      drivers.IoDriver
	hkAccessory *accessory.A
	hkService   *service.MotionSensor
	hkFault     *characteristic.StatusFault
}

func (ms *MotionSensor) GetDriverName() string {
	return ms.DriverName
}

func (ms *MotionSensor) Init(driver drivers.IoDriver) error {
	if !strings.EqualFold(driver.NameId(), ms.DriverName) {
		return fmt.Errorf("Init failed, mismatched or incorrect driver")
	}

	if !driver.IsReady() {
		return fmt.Errorf("Init failed, driver not ready")
	}

	var err error

	ms.driver = driver
	ms.input, err = driver.GetInput(ms.InPin)
	if err != nil {
		return errors.Wrap(err, "Init failed on getting input")
	}

	initState, err := ms.input.GetState()
	if err != nil {
		return errors.Wrap(err, "Init failed, on reading state")
	}

	if ms.DisableHomeKit {
		return nil
	}

	info := accessory.Info{
		Name:         ms.Name,
		SerialNumber: fmt.Sprintf("motion_sensor:%s:%02d", ms.DriverName, ms.InPin),
	}

	ms.hkAccessory = accessory.New(info, accessory.TypeSensor)
	ms.hkService = service.NewMotionSensor()
	ms.hkFault = characteristic.NewStatusFault()
	ms.hkFault.SetValue(characteristic.StatusFaultGeneralFault)

	ms.hkService.AddC(ms.hkFault.C)
	ms.hkAccessory.AddS(ms.hkService.S)
	ms.hkService.MotionDetected.SetValue(initState)

	return nil
}

func (ms *MotionSensor) Sync() (err error) {
	ms.State, err = ms.input.GetState()
	ms.hkService.MotionDetected.SetValue(ms.State)

	if err != nil {
		ms.hkFault.SetValue(characteristic.StatusFaultGeneralFault)
	} else {
		ms.hkFault.SetValue(characteristic.StatusFaultNoFault)
	}

	return
}

func (ms *MotionSensor) GetHk() *accessory.A {

	return ms.hkAccessory
}

func (ms *MotionSensor) GetValue() bool {
	return ms.State
}
