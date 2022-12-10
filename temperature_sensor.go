package swkit

import (
	"fmt"
	"time"

	"github.com/brutella/hap/accessory"
	"github.com/brutella/hap/characteristic"
	"github.com/hubertat/swkit/drivers"
	"github.com/pkg/errors"
)

const oldDataDuration = 10 * time.Minute

type TemperatureSensor struct {
	Id         string
	Name       string
	DriverName string
	Tags       map[string]string

	value         float64
	lastSync      time.Time
	hkA           *accessory.Thermometer
	hkStatusFault *characteristic.StatusFault
}

func (ts *TemperatureSensor) GetDriverName() string {
	return ts.DriverName
}

func (ts *TemperatureSensor) GetId() string {
	return ts.Id
}

func (ts *TemperatureSensor) GetTags() map[string]string {
	return ts.Tags
}

func (ts *TemperatureSensor) Init(driver drivers.SensorDriver) error {
	info := accessory.Info{
		Name:         ts.Name,
		SerialNumber: fmt.Sprintf("temp_sensor:%s:%s", ts.DriverName, ts.Id),
	}
	ts.hkA = accessory.NewTemperatureSensor(info)
	ts.hkStatusFault = characteristic.NewStatusFault()
	ts.hkStatusFault.SetValue(characteristic.StatusFaultGeneralFault)
	ts.hkA.TempSensor.AddC(ts.hkStatusFault.C)

	return nil
}

func (ts *TemperatureSensor) Sync() error {
	val, err := ts.GetValue()
	if err == nil {
		ts.hkStatusFault.SetValue(characteristic.StatusFaultNoFault)
		ts.hkA.TempSensor.CurrentTemperature.SetValue(val)
		return nil
	}

	ts.hkStatusFault.SetValue(characteristic.StatusFaultGeneralFault)
	return errors.Wrapf(err, "failed to sync %s temperature sensor %s", ts.Name, ts.Id)
}

func (ts *TemperatureSensor) GetHk() *accessory.A {
	return ts.hkA.A
}

func (ts *TemperatureSensor) GetValue() (value float64, err error) {
	if ts.lastSync.IsZero() {
		err = errors.Errorf("cannot get sensor %s value, never synced", ts.Id)
		return
	}

	if time.Since(ts.lastSync) > oldDataDuration {
		err = errors.Errorf("cannot get value of sensor %s, data is too old (%v old)", ts.Id, time.Since(ts.lastSync))
		return
	}

	value = ts.value
	return
}

func (ts *TemperatureSensor) SetValue(val float64) error {
	ts.value = val
	ts.lastSync = time.Now()
	return nil
}
