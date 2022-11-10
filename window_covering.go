package swkit

import (
	"github.com/brutella/hc/accessory"
	"github.com/brutella/hc/service"
)

type WindowShutter struct {
	*accessory.Accessory
	WindowCovering *service.WindowCovering
}

func NewWindowShutter(info accessory.Info) *WindowShutter {
	acc := WindowShutter{}
	acc.Accessory = accessory.New(info, accessory.TypeWindowCovering)
	acc.WindowCovering = service.NewWindowCovering()

	acc.AddService(acc.WindowCovering.Service)
	return &acc
}
