package agent

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/hubertat/swkit/app"
)

// registerDeviceTools registers device control tools
func registerDeviceTools(registry *ToolRegistry, controller app.DeviceController) {
	registry.Register(Tool{
		Name:        "toggle_device",
		Description: "Toggle a device on/off. Returns the new state.",
		InputSchema: anthropic.ToolInputSchemaParam{
			Properties: map[string]any{
				"name": map[string]any{
					"type":        "string",
					"description": "The name of the device to toggle",
				},
			},
			Required: []string{"name"},
		},
		Execute: makeToggleDevice(controller),
	})

	registry.Register(Tool{
		Name:        "set_device",
		Description: "Set a device to a specific state (on or off).",
		InputSchema: anthropic.ToolInputSchemaParam{
			Properties: map[string]any{
				"name": map[string]any{
					"type":        "string",
					"description": "The name of the device to set",
				},
				"state": map[string]any{
					"type":        "boolean",
					"description": "The desired state: true for on, false for off",
				},
			},
			Required: []string{"name", "state"},
		},
		Execute: makeSetDevice(controller),
	})

	registry.Register(Tool{
		Name:        "get_device_state",
		Description: "Get the current state of a device including on/off status and health.",
		InputSchema: anthropic.ToolInputSchemaParam{
			Properties: map[string]any{
				"name": map[string]any{
					"type":        "string",
					"description": "The name of the device to query",
				},
			},
			Required: []string{"name"},
		},
		Execute: makeGetDeviceState(controller),
	})
}

type toggleInput struct {
	Name string `json:"name"`
}

type setDeviceInput struct {
	Name  string `json:"name"`
	State bool   `json:"state"`
}

type getDeviceInput struct {
	Name string `json:"name"`
}

func makeToggleDevice(controller app.DeviceController) func(json.RawMessage) (string, error) {
	return func(input json.RawMessage) (string, error) {
		var in toggleInput
		if err := json.Unmarshal(input, &in); err != nil {
			return "", fmt.Errorf("invalid input: %w", err)
		}

		// Find device by name
		idx, device, err := findDeviceByName(controller, in.Name)
		if err != nil {
			return "", err
		}

		// Toggle the device
		result := controller.ToggleDevice(idx)
		if result.Error != nil {
			return "", result.Error
		}

		stateStr := "off"
		if result.NewState {
			stateStr = "on"
		}
		return fmt.Sprintf("%s (%s) is now %s", device.Name, device.Type, stateStr), nil
	}
}

func makeSetDevice(controller app.DeviceController) func(json.RawMessage) (string, error) {
	return func(input json.RawMessage) (string, error) {
		var in setDeviceInput
		if err := json.Unmarshal(input, &in); err != nil {
			return "", fmt.Errorf("invalid input: %w", err)
		}

		// Find device by name
		idx, device, err := findDeviceByName(controller, in.Name)
		if err != nil {
			return "", err
		}

		// Set the device state
		result := controller.SetDevice(idx, in.State)
		if result.Error != nil {
			return "", result.Error
		}

		stateStr := "off"
		if result.NewState {
			stateStr = "on"
		}
		return fmt.Sprintf("%s (%s) set to %s", device.Name, device.Type, stateStr), nil
	}
}

func makeGetDeviceState(controller app.DeviceController) func(json.RawMessage) (string, error) {
	return func(input json.RawMessage) (string, error) {
		var in getDeviceInput
		if err := json.Unmarshal(input, &in); err != nil {
			return "", fmt.Errorf("invalid input: %w", err)
		}

		// Find device by name
		_, device, err := findDeviceByName(controller, in.Name)
		if err != nil {
			return "", err
		}

		state := map[string]any{
			"name":            device.Name,
			"type":            device.Type,
			"is_on":           device.IsOn,
			"is_healthy":      device.IsHealthy,
			"is_faulty":       device.IsFaulty,
			"homekit_enabled": device.HomeKitEnabled,
		}

		result, err := json.Marshal(state)
		if err != nil {
			return "", fmt.Errorf("failed to encode state: %w", err)
		}
		return string(result), nil
	}
}

// findDeviceByName finds a device by name (case-insensitive partial match)
func findDeviceByName(controller app.DeviceController, name string) (int, app.DeviceState, error) {
	state := controller.GetState()
	nameLower := strings.ToLower(name)

	// First try exact match (case-insensitive)
	for i, device := range state.Devices {
		if strings.ToLower(device.Name) == nameLower {
			return i, device, nil
		}
	}

	// Then try partial match
	var matches []app.DeviceState
	var matchIdx int
	for i, device := range state.Devices {
		if strings.Contains(strings.ToLower(device.Name), nameLower) {
			matches = append(matches, device)
			matchIdx = i
		}
	}

	switch len(matches) {
	case 0:
		return -1, app.DeviceState{}, fmt.Errorf("no device found matching %q", name)
	case 1:
		return matchIdx, matches[0], nil
	default:
		names := make([]string, len(matches))
		for i, m := range matches {
			names[i] = m.Name
		}
		return -1, app.DeviceState{}, fmt.Errorf("multiple devices match %q: %s", name, strings.Join(names, ", "))
	}
}
