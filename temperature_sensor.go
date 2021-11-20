package main

import (
	"time"

	"github.com/pkg/errors"
)

type TemperatureSensor interface {
	GetValue() (float64, error)
}

type InfluxTemperature struct {
	Id   string
	Tags map[string]string

	value    float64
	lastSync time.Time
}

func (it *InfluxTemperature) GetValue() (val float64, err error) {

	if it.lastSync.IsZero() {
		err = errors.Errorf("temperature sensor %s data never synced", it.Id)
		return
	}
	whenSynced := time.Since(it.lastSync)
	if whenSynced > time.Minute*20 {
		err = errors.Errorf("temperature sensor %s data too old (last synced %s ago)", it.Id, whenSynced.String())
	}
	val = it.value
	return
}
