package swkit

import (
	"errors"
	"fmt"
	"hash/fnv"
	"strings"

	"github.com/brutella/hap/accessory"
	"github.com/brutella/hap/service"
	drivers "github.com/hubertat/swkit/drivers"
)

type Button struct {
	Name       string
	State      bool
	DriverName string
	InPin      uint16

	DisableHomekit bool

	toggleThis []ClickableDevice
	input      drivers.DigitalInput
	driver     drivers.IoDriver

	hk *accessory.A
	ss *service.StatelessProgrammableSwitch
}

type ClickableDevice interface {
	Toggle()
}

func (bu *Button) GetDriverName() string {
	return bu.DriverName
}

func (bu *Button) GetUniqueId() uint64 {
	hash := fnv.New64()
	hash.Write([]byte("Button_" + bu.Name))
	return hash.Sum64()
}

func (bu *Button) Init(driver drivers.IoDriver) error {
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
		return errors.Join(errors.New("Init failed on getting input"), err)
	}

	err = bu.input.SubscribeToPushEvent(bu)
	if err != nil {
		return errors.Join(errors.New("Failed to subsribe to push event"), err)
	}

	if !bu.DisableHomekit {
		bu.hk = accessory.New(accessory.Info{
			Name: bu.Name,
		}, accessory.TypeProgrammableSwitch)

		bu.ss = service.NewStatelessProgrammableSwitch()
		bu.hk.AddS(bu.ss.S)
	}

	return nil
}

func (bu *Button) Sync() (err error) {

	return
}

func (bu *Button) GetHk() *accessory.A {
	return bu.hk
}

func (bu *Button) Set(value bool) {

}

func (bu *Button) GetValue() bool {

	state, _ := bu.input.GetState()
	return state
}

func (bu *Button) FireEvent(event drivers.PushEvent) {
	bu.ss.ProgrammableSwitchEvent.SetValue(int(event))
}
