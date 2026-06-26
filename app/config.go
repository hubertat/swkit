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
	DisableHomekit bool
}

// ButtonEditConfig represents an editable Button configuration
type ButtonEditConfig struct {
	Name           string
	EventInputName string
	ControlDevices []ControlDeviceEdit
	DisableHomekit bool
}

// ControlDeviceEdit represents a parsed control device mapping
type ControlDeviceEdit struct {
	EventType  string // "single_press", "double_press", "triple_press", "long_press"
	Action     string // "toggle", "on", "off"
	DeviceName string // name of target output device
}

// FormatControlDeviceString converts a ControlDeviceEdit to the config string format: event:action:device
func (c ControlDeviceEdit) FormatControlDeviceString() string {
	if c.Action == "" || c.Action == "toggle" {
		return c.EventType + ":" + c.DeviceName
	}
	return c.EventType + ":" + c.Action + ":" + c.DeviceName
}

// AllEventTypes returns all available push event type strings
func AllEventTypes() []string {
	return []string{"single_press", "double_press", "triple_press", "long_press"}
}

// AllActions returns all available control actions
func AllActions() []string {
	return []string{"toggle", "on", "off"}
}
