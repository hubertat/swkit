package drivers

import (
	"encoding/json"
	"net/http"
	"net/url"
	"time"

	"github.com/pkg/errors"
)

const remoteioDriverName = "remoteio"
const requiredRemoteIoStateAge = 5 * time.Second
const remoteIoNetClientTimeout = 2 * time.Second

type RemoteInput struct {
	pinNo uint8

	state    bool
	driver   *RemoteIO
	lastSync time.Time
}

func (ifr *RemoteInput) GetState() (state bool, err error) {
	state = ifr.state
	if time.Since(ifr.lastSync) > requiredRemoteIoStateAge {
		err = errors.Errorf("InputFromRemote state too old: %s", time.Since(ifr.lastSync).String())
	}
	return
}

type RemoteOutput struct {
	pinNo uint8

	state    bool
	driver   *RemoteIO
	lastSync time.Time
}

func (otr *RemoteOutput) GetState() (state bool, err error) {
	state = otr.state
	if time.Since(otr.lastSync) > requiredRemoteIoStateAge {
		err = errors.Errorf("OutputToRemote state too old: %s", time.Since(otr.lastSync).String())
	}
	return
}

func (otr *RemoteOutput) Set(bool) (err error) {
	return
}

type RemoteIO struct {
	Host       string
	Token      string
	DriverName string

	inputs  []*RemoteInput
	outputs []*RemoteOutput
	isReady bool
}

func (rio *RemoteIO) getRemoteResponse(path string) (response *http.Response, err error) {
	var netClient = &http.Client{
		Timeout: remoteIoNetClientTimeout,
	}

	reqUrl, err := url.Parse(rio.Host)
	if err != nil {
		err = errors.Wrap(err, "RemoteIO failed to parse Host url")
		return
	}
	reqUrl, err = reqUrl.Parse(path)
	if err != nil {
		err = errors.Wrapf(err, "RemoteIO error parsing url (%s)", path)
		return
	}
	req, err := http.NewRequest("GET", reqUrl.String(), nil)
	if err != nil {
		err = errors.Wrap(err, "RemoteIO error preparing request")
		return
	}
	req.Header.Add("remoteio-token", rio.Token)
	response, err = netClient.Do(req)
	return
}

func (rio *RemoteIO) Setup(inputs []uint8, outputs []uint8) error {
	response, err := rio.getRemoteResponse("config")
	if err != nil {
		return errors.Wrap(err, "RemoteIO Setup: preparing net client error")
	}
	defer response.Body.Close()

	if response.StatusCode >= 300 {
		return errors.Errorf("RemoteIO Setup failed (response code: %d)", response.StatusCode)
	}

	type RemoteConfig struct {
		Inputs  []uint8
		Outputs []uint8
	}
	remoteConfig := &RemoteConfig{}

	err = json.NewDecoder(response.Body).Decode(remoteConfig)
	if err != nil {
		return errors.Wrap(err, "RemoteIO Setup: decoding response failed")
	}

	if len(remoteConfig.Inputs) == 0 && len(remoteConfig.Outputs) == 0 {
		return errors.Errorf("RemoteIO Setup: received response with 0 inputs and 0 outpus - not ready")
	}

	for _, input := range inputs {
		found := false
		for _, inputAvailable := range remoteConfig.Inputs {
			if inputAvailable == input {
				found = true
				rio.inputs = append(rio.inputs, &RemoteInput{pinNo: input, driver: rio})
			}
		}
		if !found {
			return errors.Errorf("RemoteIO Setup: input %d not found on remote!", input)
		}
	}
	for _, output := range outputs {
		found := false
		for _, outputAvailable := range remoteConfig.Outputs {
			if outputAvailable == output {
				found = true
				rio.outputs = append(rio.outputs, &RemoteOutput{pinNo: output, driver: rio})
			}
		}
		if !found {
			return errors.Errorf("RemoteIO Setup: output %d not found on remote!", output)
		}
	}

	rio.isReady = true
	return nil
}

func (rio *RemoteIO) Close() (err error) {
	return
}

func (rio *RemoteIO) NameId() string {
	if len(rio.DriverName) > 0 {
		return rio.DriverName
	}
	return remoteioDriverName
}

func (rio *RemoteIO) GetUniqueId(ioPin uint8) (uid uint64) {
	baseId := uint64(3) << 56

	baseId += uint64(1) << 16 // TODO get last part of url string/ip
	baseId += uint64(2) << 8  // TODO get port of url (Host)
	return baseId + uint64(ioPin)
}

func (rio *RemoteIO) IsReady() bool {
	return rio.isReady
}

func (rio *RemoteIO) GetInput(pin uint8) (DigitalInput, error) {
	for _, input := range rio.inputs {
		if input.pinNo == pin {
			return input, nil
		}
	}
	return nil, errors.Errorf("RemoteIO GetInput input %d not found", pin)
}

func (rio *RemoteIO) GetOutput(pin uint8) (DigitalOutput, error) {
	for _, output := range rio.outputs {
		if output.pinNo == pin {
			return output, nil
		}
	}
	return nil, errors.Errorf("RemoteIO GetOutput output %d not found", pin)
}

func (rio *RemoteIO) GetAllIo() (inputs []uint8, outputs []uint8) {
	for _, input := range rio.inputs {
		inputs = append(inputs, input.pinNo)
	}
	for _, output := range rio.outputs {
		outputs = append(outputs, output.pinNo)
	}

	return
}
