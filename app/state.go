package app

import (
	"encoding/json"
	"strconv"
	"time"
)

// AppState represents a snapshot of the entire application state
type AppState struct {
	Name      string
	Timestamp time.Time
	Drivers   []DriverState
	Devices   []DeviceState
	HomeKit   HomeKitState
	IoDebug   []IoPointDebugState
}

// IoPointDebugState represents the state of a single IO point for debug display
type IoPointDebugState struct {
	DriverName   string
	Index        int
	Name         string // e.g. "M1:DI3[2]"
	Type         string // "input" or "output"
	State        bool
	Healthy      bool
	LastChanged  time.Time // zero if never changed since startup
	LastEvent    time.Time // zero if no explicit event received (e.g. button press)
	ConfiguredAs string    // name of device that uses this IO point, empty if unconfigured
	CustomName   string    // user-assigned label; empty if not set
}

// IoNamesManager is implemented by providers that support persistent, shared IO point naming.
type IoNamesManager interface {
	SetIoName(key, name string)    // set or clear (empty = delete)
	GetIoName(key string) string   // single lookup
	GetIoNames() map[string]string // returns a copy of all names
	LoadIoNames(path string) error
	SaveIoNames(path string) error
}

// IoDebugKey returns the canonical map key for a named IO point.
// Format: "driverName|ioType|index" e.g. "shelly|input|0"
func IoDebugKey(driverName, ioType string, index int) string {
	return driverName + "|" + ioType + "|" + strconv.Itoa(index)
}

// DriverState represents the status of an IO driver
type DriverState struct {
	Name       string
	Ready      bool
	StatusInfo string          // Additional status info from DriverStatusProvider
	Details    json.RawMessage // Structured JSON details, driver-specific
}

// DeviceType indicates the category of device
type DeviceType string

const (
	DeviceTypeLight      DeviceType = "light"
	DeviceTypeColorLight DeviceType = "color_light"
	DeviceTypeOutlet     DeviceType = "outlet"
	DeviceTypeButton     DeviceType = "button"
)

// DeviceState represents the status of a device
type DeviceState struct {
	Name           string
	Type           DeviceType
	IsOn           bool
	IsHealthy      bool
	IsFaulty       bool
	HomeKitEnabled bool

	OutputIoId       string                  // lights, color lights, outlets
	RgbwIoId         string                  // color lights only
	EventInputId     string                  // buttons only
	ControlRelations []ButtonControlRelation // buttons only

	LastEventType string    // buttons only: last push event type, e.g. "single_press"
	LastEventTime time.Time // buttons only: when last push event occurred; zero if never
}

// ButtonControlRelation describes a button's control mapping
type ButtonControlRelation struct {
	EventType  string // "single_press", "double_press", etc.
	Action     string // "toggle", "on", "off"
	DeviceName string
}

// HomeKitState represents the HomeKit bridge status
type HomeKitState struct {
	Enabled     bool
	Pin         string
	Address     string
	DeviceCount int
}

// Summary provides counts for quick dashboard display
func (s *AppState) Summary() StateSummary {
	summary := StateSummary{}

	for _, d := range s.Drivers {
		summary.DriversTotal++
		if d.Ready {
			summary.DriversReady++
		}
	}

	for _, d := range s.Devices {
		switch d.Type {
		case DeviceTypeLight:
			summary.LightsCount++
		case DeviceTypeColorLight:
			summary.ColorLightsCount++
		case DeviceTypeOutlet:
			summary.OutletsCount++
		case DeviceTypeButton:
			summary.ButtonsCount++
		}
	}

	summary.HomeKitEnabled = s.HomeKit.Enabled
	summary.HomeKitDevices = s.HomeKit.DeviceCount

	return summary
}

// StateSummary provides aggregated counts for dashboard
type StateSummary struct {
	DriversTotal     int
	DriversReady     int
	LightsCount      int
	ColorLightsCount int
	OutletsCount     int
	ButtonsCount     int
	HomeKitEnabled   bool
	HomeKitDevices   int
}

// ControlResult represents the result of a device control operation
type ControlResult struct {
	DeviceName string
	Action     string // "toggle", "on", "off"
	NewState   bool
	Error      error
}
