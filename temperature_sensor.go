package swkit

import (
	"fmt"
	"hash/fnv"
	"time"

	"github.com/brutella/hap/accessory"
	"github.com/brutella/hap/characteristic"
	"github.com/hubertat/swkit/drivers"
	"github.com/pkg/errors"
)

const oldDataDuration = 10 * time.Minute

type TemperatureSensor struct {
	Id             string
	Name           string
	DriverName     string
	Tags           map[string]string
	DisableHomekit bool

	driver        drivers.SensorDriver
	value         float64
	lastSync      time.Time
	hkA           *accessory.Thermometer
	hkStatusFault *characteristic.StatusFault
}

func (ts *TemperatureSensor) GetDriverName() string {
	return ts.DriverName
}

func (ts *TemperatureSensor) GetUniqueId() uint64 {
	hash := fnv.New64()
	hash.Write([]byte("TemperatureSensor_" + ts.Name))
	return hash.Sum64()
}

func (ts *TemperatureSensor) GetId() string {
	return ts.Id
}

func (ts *TemperatureSensor) GetTags() map[string]string {
	return ts.Tags
}

func (ts *TemperatureSensor) Init(driver drivers.SensorDriver) error {
	if len(ts.Name) < 2 {
		return errors.Errorf("name of temperature sensor (%s) is too short", ts.Name)
	}

	ts.driver = driver

	if ts.DisableHomekit {
		return nil
	}

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
	if err != nil {
		err = errors.Wrapf(err, "failed to sync %s temperature sensor %s", ts.Name, ts.Id)
	}

	ts.updateHomekitFaultStatus(err)

	if err == nil && ts.hkA != nil {
		ts.hkA.TempSensor.CurrentTemperature.SetValue(val)
	}

	return err
}

func (ts *TemperatureSensor) updateHomekitFaultStatus(err error) {
	if ts.hkStatusFault == nil {
		return
	}

	if err != nil {
		ts.hkStatusFault.SetValue(characteristic.StatusFaultGeneralFault)
	} else {
		ts.hkStatusFault.SetValue(characteristic.StatusFaultNoFault)
	}
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
