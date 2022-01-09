package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	influxdb2 "github.com/influxdata/influxdb-client-go/v2"
	"github.com/influxdata/influxdb-client-go/v2/api"
	"github.com/influxdata/influxdb-client-go/v2/api/query"
	"github.com/pkg/errors"
)

type SensorDriver interface {
	Init() error
	IsReady() bool
	Name() string
	Sync() error
	FindSensor(string) (TemperatureSensor, error)
}

type InfluxSensors struct {
	Host         string
	Organization string
	Bucket       string
	Measurement  string
	Token        string

	GroupByTag []string

	Temperatures []*InfluxTemperature

	ready bool
}

func (is *InfluxSensors) Init() error {
	_, err := is.runQuery(is.prepareQuery())
	if err != nil {
		return errors.Wrap(err, "failed to init InfluxSensors driver")
	}

	is.ready = true
	return nil
}

func (is *InfluxSensors) IsReady() bool {
	return is.ready
}

func (is *InfluxSensors) Name() string {
	return "InfluxSensors"
}

func (is *InfluxSensors) runQuery(query string) (*api.QueryTableResult, error) {
	client := influxdb2.NewClient(is.Host, is.Token)
	queryApi := client.QueryAPI(is.Organization)

	return queryApi.Query(context.Background(), query)
}

func (is *InfluxSensors) Sync() error {
	tableResult, err := is.runQuery(is.prepareQuery())
	if err != nil {
		return errors.Wrap(err, "failed to get table results")
	}

	for tableResult.Next() {
		if tableResult.Err() != nil {
			return errors.Wrap(err, "got error parsing result table")
		}
		for _, t := range is.Temperatures {
			if checkTagsRecordMatch(tableResult.Record(), t.Tags) {
				value := tableResult.Record().Value()
				switch valT := value.(type) {
				case float64:
					t.value = float64(valT)
				case float32:
					t.value = float64(valT)
				default:
					return errors.Errorf("got value (for %s) of unsupported type", t.Id)
				}
				t.lastSync = time.Now()
			}
		}
	}

	return nil
}

func (is *InfluxSensors) FindSensor(id string) (sensor TemperatureSensor, err error) {
	for _, t := range is.Temperatures {
		if strings.EqualFold(t.Id, id) {
			sensor = t
			return
		}
	}
	err = errors.Errorf("InfluxSensors FindSensor: sensor with id = %s not found", id)
	return
}

func (is *InfluxSensors) prepareQuery() string {
	groupByString := ""
	for ix, groupBy := range is.GroupByTag {
		if ix > 0 {
			groupByString += ", "
		}
		groupByString += fmt.Sprintf(`"%s"`, groupBy)
	}

	return fmt.Sprintf(`
from(bucket: "%s")
|> range(start: -10m)
|> filter(fn: (r) => r["_measurement"] == "%s")
|> filter(fn: (r) => r["_field"] == "temperature")
|> group(columns: [%s])
|> aggregateWindow(every: 20m, fn: mean, createEmpty: false)
`, is.Bucket, is.Measurement, groupByString)
}

func checkTagsRecordMatch(record *query.FluxRecord, tags map[string]string) (match bool) {
	match = true
	for name, val := range tags {
		tagVal := record.ValueByKey(name)
		if !strings.EqualFold(fmt.Sprint(val), fmt.Sprint(tagVal)) {
			match = false
			return
		}
	}

	return
}

type Wire struct {
}

func (w1 *Wire) Init() error {
	return errors.Errorf("driver not functioning")
}

func (w1 *Wire) IsReady() bool {
	return false
}

func (w1 *Wire) Name() string {
	return "Wire"
}

func (w1 *Wire) Sync() error {
	return errors.Errorf("driver not functioning")
}

func (w1 *Wire) FindSensor(string) (TemperatureSensor, error) {
	return nil, errors.Errorf("driver not functioning")
}
