package app

import "errors"

// ErrConfigRevisionMismatch is returned by SaveConfigIfRevision when the
// caller's expectedRevision no longer matches the provider's current
// Revision(): something else (a concurrent editor session, or an
// out-of-band config reload) changed the config first. The caller must not
// treat this as a generic save failure - the correct recovery is to reload
// the config and let the user re-apply their edits, not retry the same
// write.
var ErrConfigRevisionMismatch = errors.New("config revision mismatch: config changed since it was loaded")

// ConfigProvider provides access to editable configuration
type ConfigProvider interface {
	// GetEditableConfig returns the current editable config snapshot
	GetEditableConfig() EditableConfig
	// Revision returns an opaque, stable identifier for the current
	// persisted editable config content (the same fields SaveConfig
	// writes). It changes if and only if that content changes, so a caller
	// can pair it with a GetEditableConfig snapshot and later detect,
	// via SaveConfigIfRevision, whether the config changed underneath it
	// (a lost-update / stale-save race).
	Revision() string
	// SaveConfig persists the edited config, backing up the old file
	SaveConfig(config EditableConfig) error
	// SaveConfigIfRevision persists config only if the provider's current
	// Revision() still equals expectedRevision, checking and saving
	// atomically with respect to other callers of this provider. It
	// returns ErrConfigRevisionMismatch, without modifying anything, if the
	// revision no longer matches.
	SaveConfigIfRevision(config EditableConfig, expectedRevision string) error
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
	// DisableHomekit is reserved; scenes have no HomeKit representation yet.
	// It is carried through here purely so a config save does not silently
	// erase the value already set in config.json (mirrors SceneConfig).
	DisableHomekit bool
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
