package swkit

import (
	"fmt"
	"hash/fnv"
	"strings"
	"sync"

	drivers "github.com/hubertat/swkit/drivers"

	"github.com/brutella/hap/accessory"
	"github.com/pkg/errors"
)

const thermostatMinimumTemperature = float64(0)
const thermostatMaximumTemperature = float64(50)
const thermostatTemperatureStep = float64(0.5)
const defaultThermostatThreshold = float64(0.4)

type Thermostat struct {
	Name               string
	CurrentTemperature float64
	TargetTemperature  float64
	TargetState        int
	DisableHomekit     bool

	DriverName string
	HeatPin    uint16
	CoolPin    uint16
	SensorId   string

	MinimumTemperature float64
	MaximumTemperature float64
	StepTemperature    float64
	HeatingThreshold   float64
	CoolingThreshold   float64
	CoolingEnabled     bool

	heatOut           drivers.DigitalOutput
	coolOut           drivers.DigitalOutput
	driver            drivers.IoDriver
	hk                *accessory.Thermostat
	lock              sync.Mutex
	temperatureSensor drivers.TemperatureSensor
}

func (th *Thermostat) GetDriverName() string {
	return th.DriverName
}

func (th *Thermostat) GetUniqueId() uint64 {
	hash := fnv.New64()
	hash.Write([]byte("Thermostat_" + th.Name))
	return hash.Sum64()
}

func (th *Thermostat) Init(driver drivers.IoDriver) error {
	if !strings.EqualFold(driver.NameId(), th.DriverName) {
		return fmt.Errorf("Init failed, mismatched or incorrect driver")
	}

	if !driver.IsReady() {
		return fmt.Errorf("Init failed, driver not ready")
	}

	th.lock = sync.Mutex{}
	if th.MaximumTemperature == 0 {
		th.MaximumTemperature = thermostatMaximumTemperature
	}
	if th.StepTemperature == 0 {
		th.StepTemperature = thermostatTemperatureStep
	}
	if th.HeatingThreshold == 0 {
		th.HeatingThreshold = defaultThermostatThreshold
	}
	if th.CoolingThreshold == 0 {
		th.CoolingThreshold = defaultThermostatThreshold
	}

	var err error
	th.driver = driver
	th.heatOut, err = driver.GetOutput(th.HeatPin)
	if err != nil {
		return errors.Wrap(err, "Thermostat Init failed")
	}

	if th.CoolingEnabled {
		th.coolOut, err = driver.GetOutput(th.CoolPin)
		if err != nil {
			return errors.Wrap(err, "Thermostat Init failed on coolpin")
		}
	}

	if th.DisableHomekit {
		return nil
	}

	info := accessory.Info{
		Name:         th.Name,
		SerialNumber: fmt.Sprintf("thermostat:%s:%02d", th.DriverName, th.HeatPin),
	}

	th.hk = accessory.NewThermostat(info)

	th.hk.Thermostat.TargetHeatingCoolingState.OnValueRemoteUpdate(th.updateTargetState)
	th.hk.Thermostat.TargetTemperature.OnValueRemoteUpdate(th.updateTargetTemperature)

	return nil
}

func (th *Thermostat) GetHk() *accessory.A {

	return th.hk.A
}

func (th *Thermostat) checkHeatingCondition() bool {
	heatState, _ := th.heatOut.GetState()
	var threshold float64
	if heatState {
		threshold = th.HeatingThreshold
	} else {
		threshold = -th.HeatingThreshold
	}
	return th.CurrentTemperature < (th.TargetTemperature + threshold)
}

func (th *Thermostat) checkCoolingCondition() bool {
	coolState, _ := th.coolOut.GetState()
	var threshold float64
	if coolState {
		threshold = -th.CoolingThreshold
	} else {
		threshold = th.CoolingThreshold
	}

	return th.CurrentTemperature > (th.CurrentTemperature + threshold)
}

func (th *Thermostat) Sync() (err error) {
	if th.temperatureSensor == nil {
		return errors.Errorf("missing temperature sensor for thermostat %s", th.Name)
	}
	th.CurrentTemperature, err = th.temperatureSensor.GetValue()
	if err != nil {
		err = errors.Wrap(err, "error with getting sensor value")
		return
	}

	err = th.calculateAndSetOutputs()
	if err != nil {
		err = errors.Wrap(err, "failed to set heating/cooling outputs")
		return
	}

	if th.hk == nil {
		return
	}

	th.hk.Thermostat.CurrentTemperature.SetValue(th.CurrentTemperature)
	th.hk.Thermostat.TargetTemperature.SetValue(th.TargetTemperature)
	th.hk.Thermostat.CurrentHeatingCoolingState.SetValue(th.getCurrentHeatingCoolingState())
	th.hk.Thermostat.TargetHeatingCoolingState.SetValue(th.TargetState)

	return
}

func (th *Thermostat) calculateAndSetOutputs() (err error) {
	switch th.TargetState {
	default:
		err = th.heatOut.Set(false)
		if err != nil {
			return
		}
		if th.CoolingEnabled {
			err = th.coolOut.Set(false)
		}
	case 1:
		err = th.heatOut.Set(th.checkHeatingCondition())
		if err != nil {
			return
		}
		if th.CoolingEnabled {
			err = th.coolOut.Set(false)
		}
	case 2:
		err = th.heatOut.Set(false)
		if err != nil {
			return
		}
		err = th.coolOut.Set(th.checkCoolingCondition())
	case 3:
		if th.checkHeatingCondition() {
			err = th.heatOut.Set(true)
			if err != nil {
				return
			}
			err = th.coolOut.Set(false)
		} else {
			if th.checkCoolingCondition() {
				err = th.heatOut.Set(false)
				if err != nil {
					return
				}
				err = th.coolOut.Set(true)
			}
		}
	}
	return
}

func (th *Thermostat) getCurrentHeatingCoolingState() (currentHeatingCoolingState int) {
	heatingOn, _ := th.heatOut.GetState()
	if heatingOn {
		currentHeatingCoolingState = 1
	}
	if th.CoolingEnabled {
		coolingOn, _ := th.coolOut.GetState()
		if coolingOn {
			currentHeatingCoolingState = 2
		}
	}
	return
}

func (th *Thermostat) updateTargetState(state int) {
	switch state {
	default:
		th.TargetState = 0
	case 1:
		th.TargetState = 1
	case 2:
		if th.CoolingEnabled {
			th.TargetState = 2
		}
	case 3:
		if th.CoolingEnabled {
			th.TargetState = 3
		} else {
			th.TargetState = 1
		}
	}

	th.Sync()
}

func (th *Thermostat) updateTargetTemperature(target float64) {
	th.TargetTemperature = target
}
