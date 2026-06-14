package components

// Light models the Shelly Gen2+ "light" component (dimmable brightness output).
// It is controlled via the Light.Set RPC, where `on` and `brightness` can be
// set independently.
type Light struct {
	Config LightConfig
	Status LightStatus
}

type LightConfig struct {
	ID           int     `json:"id"`
	Name         *string `json:"name,omitempty"`
	InitialState string  `json:"initial_state,omitempty"`
	AutoOn       bool    `json:"auto_on,omitempty"`
	AutoOnDelay  float64 `json:"auto_on_delay,omitempty"`
	AutoOff      bool    `json:"auto_off,omitempty"`
	AutoOffDelay float64 `json:"auto_off_delay,omitempty"`
}

type LightStatus struct {
	ID         int      `json:"-"`
	Source     *string  `json:"source,omitempty"`
	Output     *bool    `json:"output,omitempty"`
	Brightness *float64 `json:"brightness,omitempty"`

	TimerStartedAt *int `json:"timer_started_at,omitempty"`
	TimerDuration  *int `json:"timer_duration,omitempty"`

	Errors *[]string `json:"errors,omitempty"`
}

// Update merges non-nil fields from newS into the receiver, returning whether
// anything changed. Mirrors SwitchStatus.Update so partial NotifyStatus updates
// preserve previously known values.
func (ls *LightStatus) Update(newS LightStatus) bool {
	changed := false

	if newS.Source != nil {
		ls.Source = newS.Source
		changed = true
	}

	if newS.Output != nil {
		ls.Output = newS.Output
		changed = true
	}

	if newS.Brightness != nil {
		ls.Brightness = newS.Brightness
		changed = true
	}

	if newS.TimerStartedAt != nil {
		ls.TimerStartedAt = newS.TimerStartedAt
		changed = true
	}

	if newS.TimerDuration != nil {
		ls.TimerDuration = newS.TimerDuration
		changed = true
	}

	if newS.Errors != nil {
		ls.Errors = newS.Errors
		changed = true
	}

	return changed
}
