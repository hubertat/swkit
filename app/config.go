package app

// ConfigProvider provides access to editable configuration
type ConfigProvider interface {
	// GetEditableConfig returns the current editable config snapshot
	GetEditableConfig() EditableConfig
	// SaveConfig persists the edited config, backing up the old file
	SaveConfig(config EditableConfig) error
}

// EditableConfig represents the editable portion of the configuration
type EditableConfig struct {
	Lights         []LightEditConfig
	DimmableLights []DimmableLightEditConfig
	Outlets        []OutletEditConfig
	Buttons        []ButtonEditConfig
	Scenes         []SceneEditConfig
	// OutputDeviceNames provides available target device names for button control relations
	OutputDeviceNames []string
}

// LightEditConfig represents an editable Light configuration
type LightEditConfig struct {
	Name           string
	DigitalOutName string
	DisableHomekit bool
}

// OutletEditConfig represents an editable Outlet configuration
type OutletEditConfig struct {
	Name           string
	DigitalOutName string
	DisableHomekit bool
}

// DimmableLightEditConfig represents an editable DimmableLight configuration
type DimmableLightEditConfig struct {
	Name           string
	DigitalOutName string
	AnalogOutName  string
	// DefaultSetpoint is the startup brightness (0-100, HomeKit %). 0 = do not initialise.
	DefaultSetpoint int
	DisableHomekit  bool
}

// ButtonEditConfig represents an editable Button configuration
type ButtonEditConfig struct {
	Name           string
	EventInputName string
	ControlDevices []ControlDeviceEdit
	DisableHomekit bool
}

// SceneEditConfig represents an editable Scene configuration. Scene actions use
// the scene grammar (on|off|toggle:<device>, brightness:<pct>:<device>) and are
// kept as raw strings here, since that grammar differs from the button control
// grammar.
type SceneEditConfig struct {
	Name   string
	States []SceneStateEditConfig
}

// SceneStateEditConfig represents one state of a scene.
type SceneStateEditConfig struct {
	Name    string
	Actions []string
}

// ControlDeviceEdit represents a parsed control device mapping
type ControlDeviceEdit struct {
	EventType  string // "single_press", "double_press", "triple_press", "long_press"
	Action     string // action verb, see AllActionVerbs()
	Level      int    // brightness level/step; only used by brightness-family verbs
	DeviceName string // name of target output device
}

// FormatControlDeviceString converts a ControlDeviceEdit to the config string
// format: event: followed by the Action grammar (event:verb:[level:]device).
func (c ControlDeviceEdit) FormatControlDeviceString() string {
	if c.Action == "" || c.Action == "toggle" {
		return c.EventType + ":" + c.DeviceName
	}
	act := Action{Verb: c.Action, Level: c.Level, Device: c.DeviceName}
	return c.EventType + ":" + act.String()
}

// AllEventTypes returns all available push event type strings
func AllEventTypes() []string {
	return []string{"single_press", "double_press", "triple_press", "long_press"}
}
