package swpico

import (
	"machine"
)

const (
	picoType1 string = "PicoType1"
)

func PicoType1() *SwPico {
	swp := &SwPico{
		name: picoType1,

		inputs: []Input{
			{pin: machine.GP16},
			{pin: machine.GP17},
			{pin: machine.GP18},
			{pin: machine.GP19},
			{pin: machine.GP20},
			{pin: machine.GP21},
			{pin: machine.GP22},
			{pin: machine.GP9},
			{pin: machine.GP8},
		},

		outputs: []Output{
			{pin: machine.GP2},
			{pin: machine.GP3},
			{pin: machine.GP4},
			{pin: machine.GP5},
			{pin: machine.GP6},
			{pin: machine.GP7},
			{pin: machine.GP10},
			{pin: machine.GP11},
			{pin: machine.GP12},
			{pin: machine.GP13},
			{pin: machine.GP14},
			{pin: machine.GP15},
		},
	}

	swp.inputs[0].AppendClickedEvent(NewEvent(ActionToggle, &swp.outputs[0]))
	swp.inputs[1].AppendClickedEvent(NewEvent(ActionToggle, &swp.outputs[1]))
	swp.inputs[2].AppendClickedEvent(NewEvent(ActionToggle, &swp.outputs[2]))
	swp.inputs[0].AppendClickedEvent(NewEvent(ActionToggle, &swp.outputs[4]))
	swp.inputs[0].AppendClickedEvent(NewEvent(ActionToggle, &swp.outputs[3]))

	return swp
}
