package components

type InputType string // "switch", "button", "analog"
var InputTypeSwitch InputType = "switch"
var InputTypeButton InputType = "button"
var InputTypeAnalog InputType = "analog"
var InputTypesAvailable = []InputType{InputTypeSwitch, InputTypeButton, InputTypeAnalog}

type Input struct {
	Config InputConfig
	Status InputStatus
}

type InputConfig struct {
	ID           int       `json:"id"`
	Name         *string   `json:"name,omitempty"`
	Type         InputType `json:"type"`
	Invert       bool      `json:"invert"`
	FactoryReset *bool     `json:"factory_reset,omitempty"`
	ReportThr    *float64  `json:"report_thr,omitempty"`
	RangeMap     *[]int    `json:"range_map,omitempty"`
}

type InputStatus struct {
	ID      int       `json:"id"`
	State   *bool     `json:"state,omitempty"`
	Percent *int      `json:"percent,omitempty"`
	Errors  *[]string `json:"errors,omitempty"`
}
