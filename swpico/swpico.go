package swpico

import "errors"

type SwPico struct {
	name    string
	inputs  InputSlice
	outputs OutputSlice
}

func (sp *SwPico) Setup() error {
	err := sp.inputs.SetupPins()
	if err != nil {
		return errors.Join(err, errors.New("failed to setup input pins"))
	}

	err = sp.outputs.SetupPins()
	if err != nil {
		return errors.Join(err, errors.New("failed to setup output pins"))
	}

	return nil
}

func (sp *SwPico) Name() string {
	return sp.name
}

func (sp *SwPico) Inputs() InputSlice {
	return sp.inputs
}

func (sp *SwPico) Outputs() OutputSlice {
	return sp.outputs
}
