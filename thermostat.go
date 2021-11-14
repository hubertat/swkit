package main

import (
	"fmt"

	"github.com/brutella/hc/accessory"
)

const minimumTemperature = float64(0)
const maximumTemperature = float64(50)
const temperatureStep = float64(0.5)

type Thermostat struct {
	Name               string
	Id                 uint64
	CurrentTemperature float64
	TargetTemperature  float64
	State              int

	hk *accessory.Thermostat
}

func (th *Thermostat) GetHk() *accessory.Accessory {
	info := accessory.Info{
		Name:         th.Name,
		ID:           th.Id,
		SerialNumber: fmt.Sprintf("thermostat:%02d", th.Id),
	}
	th.hk = accessory.NewThermostat(info, th.CurrentTemperature, minimumTemperature, maximumTemperature, temperatureStep)
	th.hk.Thermostat.TargetHeatingCoolingState.OnValueRemoteUpdate(th.updateTargetState)
	th.hk.Thermostat.TargetTemperature.OnValueRemoteUpdate(th.updateTargetTemperature)

	return th.hk.Accessory
}

func (th *Thermostat) Sync() {

}

func (th *Thermostat) updateTargetState(state int) {
	th.State = state
}

func (th *Thermostat) updateTargetTemperature(target float64) {
	th.TargetTemperature = target
}
