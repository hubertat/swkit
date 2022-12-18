package drivers

import (
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"strconv"
	"strings"

	"github.com/pkg/errors"
)

const wireSystemPath string = "/sys/bus/w1/devices"
const wireSensorPrefix string = "28-"

const wireSensorDriverName string = "wire"

type Wire struct {
	CheckBounds        bool
	BoundMinimumMillis int
	BoundMaximumMillis int

	sensors []TemperatureSensor
	ready   bool
}

func (w1 *Wire) getSensorPathSlice() (pathSlice map[TemperatureSensor]string, err error) {
	pathSlice = make(map[TemperatureSensor]string)
	for _, s := range w1.sensors {
		var intBase int
		var numId int64
		stringId := strings.ToLower(s.GetId())
		if strings.HasPrefix(stringId, "0x") {
			stringId = strings.TrimLeft(stringId, "0x")
			intBase = 16
		} else {
			intBase = 10
		}
		numId, err = strconv.ParseInt(stringId, intBase, 64)
		if err != nil {
			err = errors.Wrapf(err, "failed to convert string id: %s to int", stringId)
			return
		}
		folderName := fmt.Sprintf("%s%012x", wireSensorPrefix, numId)
		pathSlice[s] = path.Join(wireSystemPath, folderName, "temperature")
	}

	return
}

func (w1 *Wire) Setup(tempSensors []TemperatureSensor) (err error) {
	_, err = ioutil.ReadDir(wireSystemPath)
	if err != nil {
		err = errors.Wrapf(err, "failed to init Wire sensor driver: error reading dir (%s):", wireSystemPath)
		return
	}

	w1.sensors = tempSensors

	pathSlice, err := w1.getSensorPathSlice()
	if err != nil {
		err = errors.Wrapf(err, "failed to init wire sensor driver, got error from generating sensor system path slice")
		return
	}
	for _, filePath := range pathSlice {
		_, err = os.ReadFile(filePath)
		if err != nil {
			err = errors.Wrapf(err, "failed to init wire sensor driver, cannot read file %s", filePath)
			return
		}
	}

	w1.ready = err == nil
	return
}

func (w1 *Wire) Close() error {
	return nil
}

func (w1 *Wire) IsReady() bool {
	return w1.ready
}

func (w1 *Wire) Name() string {
	return wireSensorDriverName
}

func (w1 *Wire) checkBounds(readount int) bool {
	if readount < w1.BoundMinimumMillis || readount > w1.BoundMaximumMillis {
		return false
	}
	return true
}

func (w1 *Wire) Sync() error {

	pathSlice, _ := w1.getSensorPathSlice()
	for sensor, filePath := range pathSlice {
		temperatureStringBytes, err := os.ReadFile(filePath)
		if err != nil {
			return errors.Wrapf(err, "failed reading file for sensor id: %s, folder name: %s", sensor.GetId(), filePath)
		}
		temperatureString := strings.TrimSpace(string(temperatureStringBytes))
		temperatureString = strings.Trim(temperatureString, "\n")
		temperatureString = strings.Trim(temperatureString, "\t")
		milliCelsiuses, err := strconv.ParseInt(string(temperatureString), 10, 32)
		if err != nil {
			return errors.Wrapf(err, "failed converting temperature string (bytes): %s to milli •C int value, for sensor id: %s", temperatureString, sensor.GetId())
		}
		if w1.CheckBounds && !w1.checkBounds(int(milliCelsiuses)) {
			return errors.Errorf("wire sensor out of bound check enabled and failed, value: %d m°C for sensor %s", milliCelsiuses, sensor.GetId())
		}
		sensor.SetValue(float64(milliCelsiuses) / 1000)
	}

	return nil
}

func (w1 *Wire) FindTemperatureSensor(id string) (TemperatureSensor, error) {
	for _, s := range w1.sensors {
		if strings.EqualFold(id, s.GetId()) {
			return s, nil
		}
	}
	return nil, errors.Errorf("sensor %s was not found in driver %s", id, w1.Name())
}
