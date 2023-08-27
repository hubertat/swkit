package shelly

import (
	"encoding/json"

	"github.com/hubertat/swkit/drivers/shelly/components"
)

type GetStatus struct {
	Ethernet json.RawMessage `json:"eth"`
	Wifi     json.RawMessage `json:"wifi"`
	Profiles json.RawMessage `json:"profiles"`

	Switch0 json.RawMessage `json:"switch:0"`
	Switch1 json.RawMessage `json:"switch:1"`
	Switch2 json.RawMessage `json:"switch:2"`
	Switch3 json.RawMessage `json:"switch:3"`
	Input0  json.RawMessage `json:"input:0"`
	Input1  json.RawMessage `json:"input:1"`
	Input2  json.RawMessage `json:"input:2"`
	Input3  json.RawMessage `json:"input:3"`
}

func (gs *GetStatus) rawSwitchSlice() [][]byte {
	return [][]byte{gs.Switch0, gs.Switch1, gs.Switch2, gs.Switch3}
}

func (gs *GetStatus) rawInputSlice() [][]byte {
	return [][]byte{gs.Input0, gs.Input1, gs.Input2, gs.Input3}
}

func (gs *GetStatus) GetSwitches() (switches []components.SwitchStatus) {
	for _, rawSwitch := range gs.rawSwitchSlice() {
		if len(rawSwitch) > 0 {
			var sw components.SwitchStatus
			if json.Unmarshal(rawSwitch, &sw) == nil {
				switches = append(switches, sw)
			}
		}
	}

	return
}

func (gs *GetStatus) GetInputs() (inputs []components.InputStatus) {
	for _, rawInput := range gs.rawInputSlice() {
		if len(rawInput) > 0 {
			var in components.InputStatus
			if json.Unmarshal(rawInput, &in) == nil {
				inputs = append(inputs, in)
			}
		}
	}

	return
}

func (gs *GetStatus) GetEthernet() *components.EthernetStatus {
	eth := components.EthernetStatus{}
	if json.Unmarshal(gs.Ethernet, &eth) == nil {
		return &eth
	}
	return nil

}

func (gs *GetStatus) GetWifi() *components.WifiStatus {
	wifi := components.WifiStatus{}
	if json.Unmarshal(gs.Wifi, &wifi) == nil {
		return &wifi
	}
	return nil
}

func (gs *GetStatus) GetProfiles() []string {
	var profiles []string
	if json.Unmarshal(gs.Profiles, &profiles) == nil {
		return profiles
	}
	return nil
}
