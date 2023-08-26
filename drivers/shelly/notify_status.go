package shelly

import (
	"encoding/json"
	"errors"

	"github.com/hubertat/swkit/drivers/shelly/components"
)

type NotifyStatus struct {
	Switch0 json.RawMessage `json:"switch:0"`
	Switch1 json.RawMessage `json:"switch:1"`
	Switch2 json.RawMessage `json:"switch:2"`
	Switch3 json.RawMessage `json:"switch:3"`
	Input0  json.RawMessage `json:"input:0"`
	Input1  json.RawMessage `json:"input:1"`
	Input2  json.RawMessage `json:"input:2"`
	Input3  json.RawMessage `json:"input:3"`
}

func (ns *NotifyStatus) rawSwitchSlice() [][]byte {
	return [][]byte{ns.Switch0, ns.Switch1, ns.Switch2, ns.Switch3}
}

func (ns *NotifyStatus) FillSwitches(switches []components.Switch) error {
	for _, sw := range switches {
		swId := sw.Status.ID
		if swId < 0 || swId > 3 {
			return errors.New("switch id out of range [0, 3]")
		}
		rawSwitch := ns.rawSwitchSlice()[swId]
		if len(rawSwitch) > 0 {
			err := json.Unmarshal(rawSwitch, &sw)
			if err != nil {
				return errors.Join(errors.New("failed to unmarshal switch"), err)
			}
		}
	}

	return nil
}
