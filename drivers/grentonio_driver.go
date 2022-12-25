package drivers

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pkg/errors"
)

const grentonioDriverName = "grenton"
const grentonNetClientTimeout = 4500 * time.Millisecond
const grentonSetStateWaitForCheck = 900 * time.Millisecond
const grentonObjectFreshness = 20 * time.Second

type GrentonOutput struct {
	Grenton *GrentonIO

	state       bool
	refreshedAt time.Time
	id          uint16
}

func (gro *GrentonOutput) checkFreshness() (time.Duration, error) {
	if gro.refreshedAt.IsZero() {
		return 0, errors.Errorf("output was not yet refreshed")
	}

	return time.Since(gro.refreshedAt), nil
}

func (gro *GrentonOutput) GetState() (bool, error) {
	if time.Since(gro.refreshedAt) > grentonObjectFreshness {
		err := gro.Grenton.updateState()
		if err != nil {
			return false, errors.Wrap(err, "failed to refresh state")
		}
	}
	return gro.state, nil
}

func (gro *GrentonOutput) Set(state bool) error {
	currentState, err := gro.GetState()
	if err != nil {
		return errors.Wrap(err, "received error when getting current state")
	}

	if currentState == state {
		return nil
	}

	err = gro.Grenton.setState(state, gro)
	if err != nil {
		return errors.Wrap(err, "grenton setState returned error")
	}

	// go func() {
	// 	time.Sleep(grentonSetStateWaitForCheck)

	// 	err := gro.Grenton.updateState()
	// 	if err != nil {
	// 		err = errors.Wrap(err, "grenton returned error during refresh after setting new state")
	// 	}

	// }()
	// if gro.state != state {
	// 	return errors.Errorf("state mismatch after setting new state (want: %v, got: %v)", state, gro.state)
	// }

	return nil
}

type GrentonIO struct {
	GateAddress string
	CluId       uint32

	ObjectFreshnessDuration string

	setUrl          *url.URL
	getUrl          *url.URL
	ready           bool
	outputs         []*GrentonOutput
	gateLock        *sync.Mutex
	objectFreshness time.Duration
}

func (gio *GrentonIO) getCluString() string {
	return fmt.Sprintf("CLU_%08x", gio.CluId)
}

func (gio *GrentonIO) getQueryBody() (b []byte) {
	type GrentonObject struct {
		Kind string
		Clu  string
		Id   string
	}

	grentonSet := []GrentonObject{}

	for _, out := range gio.outputs {
		grentonSet = append(grentonSet, GrentonObject{"Light", gio.getCluString(), fmt.Sprintf("DOU%04d", out.id)})
	}

	b, _ = json.Marshal(grentonSet)
	return
}

func (gio *GrentonIO) getSetBody(state bool, id uint16) (b []byte) {
	type GrentonObject struct {
		Kind  string
		Clu   string
		Id    string
		Cmd   string
		Light struct {
			State bool
		}
	}

	objSet := GrentonObject{"Light", gio.getCluString(), fmt.Sprintf("DOU%04d", id), "SET", struct{ State bool }{state}}

	b, _ = json.Marshal(objSet)
	return
}

func (gio *GrentonIO) updateState() (err error) {
	gio.gateLock.Lock()
	defer gio.gateLock.Unlock()

	var netClient = &http.Client{
		Timeout: grentonNetClientTimeout,
	}

	bodyReader := strings.NewReader(string(gio.getQueryBody()))
	req, err := http.NewRequest("POST", gio.getUrl.String(), bodyReader)
	if err != nil {
		err = errors.Wrap(err, "preparing request failed")
		return
	}

	req.Header.Set("Content-Type", "application/json")

	response, err := netClient.Do(req)
	if err != nil {
		err = errors.Wrap(err, "sending request failed")
		return
	}
	defer response.Body.Close()

	if response.StatusCode > 200 {
		respBody, _ := io.ReadAll(response.Body)
		err = errors.Errorf("grenton gate returned non success status code (%d),\n query:\n%s\nresponse:\n%s", response.StatusCode, gio.getQueryBody(), respBody)
		return
	}

	type GrentonObject struct {
		Kind  string
		Clu   string
		Id    string
		Light struct {
			State bool
		}
	}

	statusResponse := []GrentonObject{}

	decoder := json.NewDecoder(response.Body)
	err = decoder.Decode(&statusResponse)
	if err != nil {
		err = errors.Wrap(err, "failed to decode json response")
		return
	}

	for _, obj := range statusResponse {
		idInt, convertErr := strconv.ParseInt(strings.TrimLeft(obj.Id, "DOU"), 10, 16)
		if convertErr != nil {
			err = errors.Wrapf(convertErr, "failed to convert grenton object id (%s) to int", obj.Id)
			return
		}
		for _, out := range gio.outputs {
			if out.id == uint16(idInt) {
				out.state = obj.Light.State
				out.refreshedAt = time.Now()
			}
		}
	}

	for _, out := range gio.outputs {
		_, refreshedErr := out.checkFreshness()
		if refreshedErr != nil {
			err = errors.Errorf("output %d wasn't refreshed during setup", out.id)
			return
		}
	}
	return
}

func (gio *GrentonIO) setState(state bool, output *GrentonOutput) (err error) {
	gio.gateLock.Lock()
	defer gio.gateLock.Unlock()

	var netClient = &http.Client{
		Timeout: grentonNetClientTimeout,
	}

	bodyReader := strings.NewReader(string(gio.getSetBody(state, output.id)))
	req, err := http.NewRequest("POST", gio.setUrl.String(), bodyReader)
	if err != nil {
		err = errors.Wrap(err, "preparing request failed")
		return
	}

	req.Header.Set("Content-Type", "application/json")

	_, err = netClient.Do(req)
	if err != nil {
		err = errors.Wrap(err, "sending request failed")
		return
	}

	return
}

func (gio *GrentonIO) Setup(inputs []uint16, outputs []uint16) (err error) {
	gio.ready = false
	gio.gateLock = &sync.Mutex{}

	gio.objectFreshness = grentonObjectFreshness
	if len(gio.ObjectFreshnessDuration) > 0 {
		d, dErr := time.ParseDuration(gio.ObjectFreshnessDuration)
		if dErr == nil {
			gio.objectFreshness = d
		}
	}

	gateUrl, err := url.Parse(gio.GateAddress)
	if err != nil {
		err = errors.Wrapf(err, "parsing url error")
		return
	}

	gio.setUrl, err = gateUrl.Parse("/homebridge")
	if err != nil {
		err = errors.Wrapf(err, "parsing url error")
		return
	}
	gio.getUrl, err = gateUrl.Parse("/multi/read/")
	if err != nil {
		err = errors.Wrapf(err, "parsing url error")
		return
	}

	if len(inputs) > 0 {
		err = errors.Errorf("received inputs slice, grenton io not supports inputs")
		return
	}

	if len(outputs) == 0 {
		err = errors.Errorf("received 0 length output slice, nothing to setup")
		return
	}

	gio.outputs = []*GrentonOutput{}

	for _, outId := range outputs {
		gio.outputs = append(gio.outputs, &GrentonOutput{id: outId, Grenton: gio})
	}

	err = gio.updateState()
	if err != nil {
		err = errors.Wrap(err, "error when updating grenton states")
		return
	}

	gio.ready = true

	return
}

func (gio *GrentonIO) Close() error {
	return nil
}

func (gio *GrentonIO) NameId() string {
	return grentonioDriverName
}

func (gio *GrentonIO) IsReady() bool {
	return gio.ready
}

func (gio *GrentonIO) GetInput(pin uint16) (DigitalInput, error) {
	return nil, errors.Errorf("grenton io not supports inputs")
}

func (gio *GrentonIO) GetOutput(pin uint16) (DigitalOutput, error) {
	for _, out := range gio.outputs {
		if out.id == pin {
			return out, nil
		}
	}
	return nil, errors.Errorf("output id %d not found", pin)
}

func (gio *GrentonIO) GetAllIo() (inputs []uint16, outputs []uint16) {
	for _, out := range gio.outputs {
		outputs = append(outputs, out.id)
	}

	return
}
