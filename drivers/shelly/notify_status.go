package shelly

import (
	"encoding/json"
	"errors"

	"github.com/hubertat/swkit/drivers/shelly/components"
)

type NotifyStatus struct {
	Switch0 json.RawMessage `json:"switch:0,omitempty"`
	Switch1 json.RawMessage `json:"switch:1,omitempty"`
	Switch2 json.RawMessage `json:"switch:2,omitempty"`
	Switch3 json.RawMessage `json:"switch:3,omitempty"`
	Input0  json.RawMessage `json:"input:0,omitempty"`
	Input1  json.RawMessage `json:"input:1,omitempty"`
	Input2  json.RawMessage `json:"input:2,omitempty"`
	Input3  json.RawMessage `json:"input:3,omitempty"`
}

func (ns *NotifyStatus) rawSwitchSlice() [][]byte {
	return [][]byte{ns.Switch0, ns.Switch1, ns.Switch2, ns.Switch3}
}

func (ns *NotifyStatus) FillSwitches(switches []components.Switch) error {
	for ix, sw := range switches {
		swId := sw.Status.ID
		if swId < 0 || swId > 3 {
			return errors.New("switch id out of range [0, 3]")
		}
		rawSwitch := ns.rawSwitchSlice()[swId]
		if len(rawSwitch) > 0 {
			err := json.Unmarshal(rawSwitch, &sw.Status)
			if err != nil {
				return errors.Join(errors.New("failed to unmarshal switch"), err)
			}
			switches[ix] = sw
		}
	}

	return nil
}

func (ns *NotifyStatus) GetAllSwitches() (switches []components.Switch) {
	for _, rawSwitch := range ns.rawSwitchSlice() {
		if len(rawSwitch) > 0 {
			var sw components.Switch
			if json.Unmarshal(rawSwitch, &sw.Status) == nil {
				switches = append(switches, sw)
			}
		}
	}

	return
}
