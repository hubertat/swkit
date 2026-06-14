package agent

import (
	"encoding/json"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/hubertat/swkit/app"
)

// registerInfoTools registers information/query tools
func registerInfoTools(registry *ToolRegistry, controller app.DeviceController) {
	registry.Register(Tool{
		Name:        "list_devices",
		Description: "List all devices with their current states. Optionally filter by device type.",
		InputSchema: anthropic.ToolInputSchemaParam{
			Properties: map[string]any{
				"type": map[string]any{
					"type":        "string",
					"description": "Optional device type filter: 'light', 'color_light', 'outlet', or 'button'",
					"enum":        []string{"light", "color_light", "outlet", "button"},
				},
			},
		},
		Execute: makeListDevices(controller),
	})

	registry.Register(Tool{
		Name:        "list_drivers",
		Description: "List all IO drivers and their status.",
		InputSchema: anthropic.ToolInputSchemaParam{
			Properties: map[string]any{},
		},
		Execute: makeListDrivers(controller),
	})

	registry.Register(Tool{
		Name:        "get_system_status",
		Description: "Get overall system health and summary counts.",
		InputSchema: anthropic.ToolInputSchemaParam{
			Properties: map[string]any{},
		},
		Execute: makeGetSystemStatus(controller),
	})

	registry.Register(Tool{
		Name:        "get_homekit_status",
		Description: "Get HomeKit bridge configuration and status.",
		InputSchema: anthropic.ToolInputSchemaParam{
			Properties: map[string]any{},
		},
		Execute: makeGetHomeKitStatus(controller),
	})
}

type listDevicesInput struct {
	Type string `json:"type"`
}

func makeListDevices(controller app.DeviceController) func(json.RawMessage) (string, error) {
	return func(input json.RawMessage) (string, error) {
		var in listDevicesInput
		if err := json.Unmarshal(input, &in); err != nil {
			// Ignore parse error, treat as no filter
			in.Type = ""
		}

		state := controller.GetState()

		type deviceInfo struct {
			Name           string `json:"name"`
			Type           string `json:"type"`
			IsOn           bool   `json:"is_on"`
			IsHealthy      bool   `json:"is_healthy"`
			IsFaulty       bool   `json:"is_faulty"`
			HomeKitEnabled bool   `json:"homekit_enabled"`
		}

		var devices []deviceInfo
		for _, d := range state.Devices {
			// Filter by type if specified
			if in.Type != "" && string(d.Type) != in.Type {
				continue
			}
			devices = append(devices, deviceInfo{
				Name:           d.Name,
				Type:           string(d.Type),
				IsOn:           d.IsOn,
				IsHealthy:      d.IsHealthy,
				IsFaulty:       d.IsFaulty,
				HomeKitEnabled: d.HomeKitEnabled,
			})
		}

		result, err := json.Marshal(devices)
		if err != nil {
			return "", err
		}
		return string(result), nil
	}
}

func makeListDrivers(controller app.DeviceController) func(json.RawMessage) (string, error) {
	return func(_ json.RawMessage) (string, error) {
		state := controller.GetState()

		type driverInfo struct {
			Name       string `json:"name"`
			Ready      bool   `json:"ready"`
			StatusInfo string `json:"status_info,omitempty"`
		}

		drivers := make([]driverInfo, len(state.Drivers))
		for i, d := range state.Drivers {
			drivers[i] = driverInfo{
				Name:       d.Name,
				Ready:      d.Ready,
				StatusInfo: d.StatusInfo,
			}
		}

		result, err := json.Marshal(drivers)
		if err != nil {
			return "", err
		}
		return string(result), nil
	}
}

func makeGetSystemStatus(controller app.DeviceController) func(json.RawMessage) (string, error) {
	return func(_ json.RawMessage) (string, error) {
		state := controller.GetState()
		summary := state.Summary()

		status := map[string]any{
			"name":            state.Name,
			"drivers_total":   summary.DriversTotal,
			"drivers_ready":   summary.DriversReady,
			"lights":          summary.LightsCount,
			"color_lights":    summary.ColorLightsCount,
			"dimmable_lights": summary.DimmableLightsCount,
			"outlets":         summary.OutletsCount,
			"buttons":         summary.ButtonsCount,
			"homekit_enabled": summary.HomeKitEnabled,
			"homekit_devices": summary.HomeKitDevices,
		}

		// Check for faulty devices
		faultyCount := 0
		for _, d := range state.Devices {
			if d.IsFaulty {
				faultyCount++
			}
		}
		status["faulty_devices"] = faultyCount

		result, err := json.Marshal(status)
		if err != nil {
			return "", err
		}
		return string(result), nil
	}
}

func makeGetHomeKitStatus(controller app.DeviceController) func(json.RawMessage) (string, error) {
	return func(_ json.RawMessage) (string, error) {
		state := controller.GetState()

		hkStatus := map[string]any{
			"enabled":      state.HomeKit.Enabled,
			"pin":          state.HomeKit.Pin,
			"address":      state.HomeKit.Address,
			"device_count": state.HomeKit.DeviceCount,
		}

		result, err := json.Marshal(hkStatus)
		if err != nil {
			return "", err
		}
		return string(result), nil
	}
}
