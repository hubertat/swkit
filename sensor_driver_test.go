package main

import (
	"strings"
	"testing"
)

func TestPrepareInfluxQuery(t *testing.T) {
	inf := InfluxSensors{}
	inf.Bucket = "some-bucket"
	inf.Measurement = "measure"

	inf.GroupByTag = []string{"one", "this-is-two"}

	want := `from(bucket: "some-bucket")
|> range(start: -10m)
|> filter(fn: (r) => r["_measurement"] == "measure")
|> filter(fn: (r) => r["_field"] == "temperature")
|> group(columns: ["one", "this-is-two"])
|> aggregateWindow(every: 20m, fn: mean, createEmpty: false)`

	got := strings.TrimSpace(inf.prepareQuery())

	if !strings.EqualFold(want, got) {
		t.Errorf("prepared influx query mismatch, got:\n%s\nwant:\n%s\n", got, want)
	}
}
