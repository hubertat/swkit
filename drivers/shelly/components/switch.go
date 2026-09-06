package components

var InitialStatesAvailable = []string{"off", "on", "restore_last", "match_input"}
var InModesAvailable = []string{"momentary", "follow", "flip", "detached"}

type Switch struct {
	Config SwitchConfig
	Status SwitchStatus
}

type SwitchConfig struct {
	ID           int     `json:"id"`
	Name         *string `json:"name,omitempty"`
	InMode       string  `json:"in_mode"`
	InitialState string  `json:"initial_state"`
	AutoOn       bool    `json:"auto_on"`
	AutoOnDelay  float64 `json:"auto_on_delay"`
	AutoOff      bool    `json:"auto_off"`
	AutoOffDelay float64 `json:"auto_off_delay"`

	AutorecoverVoltageErrors *bool    `json:"autorecover_voltage_errors,omitempty"`
	PowerLimit               *float64 `json:"power_limit,omitempty"`
	VoltageLimit             *float64 `json:"voltage_limit,omitempty"`
	UndervoltageLimit        *float64 `json:"undervoltage_limit,omitempty"`
	CurrentLimit             *float64 `json:"current_limit,omitempty"`
}

type SwitchStatus struct {
	ID     int     `json:"-"`
	Source *string `json:"source,omitempty"`
	Output *bool   `json:"output,omitempty"`

	TimerStartedAt *int `json:"timer_started_at,omitempty"`
	TimerDuration  *int `json:"timer_duration,omitempty"`

	APower  *float64 `json:"apower,omitempty"`
	Voltage *float64 `json:"voltage,omitempty"`
	Current *float64 `json:"current,omitempty"`
	Pf      *float64 `json:"pf,omitempty"`
	Freq    *float64 `json:"freq,omitempty"`

	AEnergy     *EnergyStats `json:"aenergy,omitempty"`
	Temperature *Temperature `json:"temperature,omitempty"`

	Errors *[]string `json:"errors,omitempty"`
}

func (ss *SwitchStatus) Update(newS SwitchStatus) bool {
	changed := false

	if newS.Source != nil {
		ss.Source = newS.Source
		changed = true
	}

	if newS.Output != nil {
		ss.Output = newS.Output
		changed = true
	}

	if newS.TimerStartedAt != nil {
		ss.TimerStartedAt = newS.TimerStartedAt
		changed = true
	}

	if newS.TimerDuration != nil {
		ss.TimerDuration = newS.TimerDuration
		changed = true
	}

	if newS.APower != nil {
		ss.APower = newS.APower
		changed = true
	}

	if newS.Voltage != nil {
		ss.Voltage = newS.Voltage
		changed = true
	}

	if newS.Current != nil {
		ss.Current = newS.Current
		changed = true
	}

	if newS.Pf != nil {
		ss.Pf = newS.Pf
		changed = true
	}

	if newS.Freq != nil {
		ss.Freq = newS.Freq
		changed = true
	}

	if newS.AEnergy != nil {
		ss.AEnergy = newS.AEnergy
		changed = true
	}

	if newS.Temperature != nil {
		ss.Temperature = newS.Temperature
		changed = true
	}

	if newS.Errors != nil {
		ss.Errors = newS.Errors
		changed = true
	}

	return changed
}

type EnergyStats struct {
	Total    float64   `json:"total"`
	ByMinute []float64 `json:"by_minute"`
	MinuteTS int       `json:"minute_ts"`
}

type Temperature struct {
	TC *float64 `json:"tC,omitempty"`
	TF *float64 `json:"tF,omitempty"`
}
