package swkit

import (
	"github.com/brutella/hap/accessory"
	"github.com/brutella/hap/service"
)

type WindowShutter struct {
	*accessory.A
	WindowCovering *service.WindowCovering
}

func NewWindowShutter(info accessory.Info) *WindowShutter {
	acc := WindowShutter{}
	acc.A = accessory.New(info, accessory.TypeWindowCovering)
	acc.WindowCovering = service.NewWindowCovering()

	acc.AddS(acc.WindowCovering.S)
	return &acc
}
