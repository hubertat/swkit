package agent

import (
	"context"
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
		Description: "Toggle a device on/off. On a window shutter this is the movement toggle: start if stopped (opposite of the last direction), stop if moving. Returns the new state.",
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
		Description: "Set a device to a specific state (on or off). On a window shutter, true opens and false closes; prefer open_shutter/close_shutter/stop_shutter/set_shutter_position for shutters.",
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
		Name:        "adjust_brightness",
		Description: "Change a dimmable light's brightness by a relative amount (percentage points). Use a positive delta to brighten, negative to dim. The result is clamped to 0-100.",
		InputSchema: anthropic.ToolInputSchemaParam{
			Properties: map[string]any{
				"name": map[string]any{
					"type":        "string",
					"description": "The name of the dimmable light to adjust",
				},
				"delta": map[string]any{
					"type":        "integer",
					"description": "Relative change in percentage points, e.g. 10 to brighten by 10%, -20 to dim by 20%",
				},
			},
			Required: []string{"name", "delta"},
		},
		Execute: makeAdjustBrightness(controller),
	})

	registry.Register(Tool{
		Name:        "get_device_state",
		Description: "Get the current state of a device including on/off status and health. For window shutters it includes motion and the position estimate (estimated from travel time, never measured; unknown until a full open or close).",
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

	// Window shutter tools, only when the controller supports them.
	if sc, ok := controller.(app.WindowShutterController); ok {
		shutterTool := func(name, desc string, run func(int) app.ControlResult) {
			registry.Register(Tool{
				Name:        name,
				Description: desc,
				InputSchema: anthropic.ToolInputSchemaParam{
					Properties: map[string]any{
						"name": map[string]any{"type": "string", "description": "The name of the window shutter"},
					},
					Required: []string{"name"},
				},
				Execute: makeShutterCommand(controller, func(idx int, _ shutterInput) app.ControlResult { return run(idx) }),
			})
		}
		shutterTool("open_shutter", "Start opening a window shutter fully. Movement is only started; the shutter stops by itself at the end of its travel time.", sc.OpenDevice)
		shutterTool("close_shutter", "Start closing a window shutter fully. Movement is only started; the shutter stops by itself at the end of its travel time.", sc.CloseDevice)
		shutterTool("stop_shutter", "Stop a moving window shutter.", sc.StopDevice)
		registry.Register(Tool{
			Name:        "set_shutter_position",
			Description: "Move a window shutter to a position in percent open (0 = closed, 100 = open). Intermediate positions need a known position estimate (after one full open or close); 0 and 100 always work and calibrate the estimate.",
			InputSchema: anthropic.ToolInputSchemaParam{
				Properties: map[string]any{
					"name":     map[string]any{"type": "string", "description": "The name of the window shutter"},
					"position": map[string]any{"type": "integer", "minimum": 0, "maximum": 100, "description": "Target position, percent open"},
				},
				Required: []string{"name", "position"},
			},
			Execute: makeShutterCommand(controller, func(idx int, in shutterInput) app.ControlResult {
				return sc.SetDevicePosition(idx, *in.Position)
			}),
		})
	}
}

type shutterInput struct {
	Name     string `json:"name"`
	Position *int   `json:"position"`
}

// makeShutterCommand resolves the shutter by name, validates the input and
// reports the accepted command's state (from the result, not a re-read).
func makeShutterCommand(controller app.DeviceController, run func(int, shutterInput) app.ControlResult) func(context.Context, json.RawMessage) (string, error) {
	return func(ctx context.Context, input json.RawMessage) (string, error) {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		var in shutterInput
		if err := json.Unmarshal(input, &in); err != nil {
			return "", fmt.Errorf("invalid input: %w", err)
		}
		if in.Position != nil && (*in.Position < 0 || *in.Position > 100) {
			return "", fmt.Errorf("position must be between 0 and 100, got %d", *in.Position)
		}
		idx, device, err := findDeviceByName(controller, in.Name)
		if err != nil {
			return "", err
		}
		if device.Type != app.DeviceTypeWindowShutter {
			return "", fmt.Errorf("%s is a %s, not a window shutter", device.Name, device.Type)
		}
		if err := ctx.Err(); err != nil {
			return "", err
		}
		result := run(idx, in)
		if result.Error != nil {
			return "", result.Error
		}
		return device.Name + ": " + describeShutter(result.WindowShutter), nil
	}
}

// describeShutter renders shutter state in words, always calling the
// position an estimate.
func describeShutter(ws *app.WindowShutterState) string {
	if ws == nil {
		return "command accepted"
	}
	pos := "position unknown (needs a full open or close to calibrate)"
	if ws.PositionKnown {
		pos = fmt.Sprintf("estimated %d%% open", ws.CurrentPosition)
	}
	switch {
	case ws.PendingStart:
		return fmt.Sprintf("waiting to reverse, then moving toward %d%%; %s", ws.TargetPosition, pos)
	case ws.Motion != "stopped":
		return fmt.Sprintf("%s toward %d%%, about %.0f s until it stops; %s", ws.Motion, ws.TargetPosition, float64(ws.RemainingTravelMilliseconds)/1000, pos)
	default:
		return "stopped, " + pos
	}
}

type toggleInput struct {
	Name string `json:"name"`
}

type setDeviceInput struct {
	Name  string `json:"name"`
	State bool   `json:"state"`
}

type adjustBrightnessInput struct {
	Name  string `json:"name"`
	Delta int    `json:"delta"`
}

type getDeviceInput struct {
	Name string `json:"name"`
}

func makeToggleDevice(controller app.DeviceController) func(context.Context, json.RawMessage) (string, error) {
	return func(ctx context.Context, input json.RawMessage) (string, error) {
		if err := ctx.Err(); err != nil {
			return "", err
		}

		var in toggleInput
		if err := json.Unmarshal(input, &in); err != nil {
			return "", fmt.Errorf("invalid input: %w", err)
		}

		// Find device by name
		idx, device, err := findDeviceByName(controller, in.Name)
		if err != nil {
			return "", err
		}

		if err := ctx.Err(); err != nil {
			return "", err
		}

		// Toggle the device
		result := controller.ToggleDevice(idx)
		if result.Error != nil {
			return "", result.Error
		}

		if result.WindowShutter != nil {
			return device.Name + " (window_shutter): " + describeShutter(result.WindowShutter), nil
		}
		stateStr := "off"
		if result.NewState {
			stateStr = "on"
		}
		return fmt.Sprintf("%s (%s) is now %s", device.Name, device.Type, stateStr), nil
	}
}

func makeSetDevice(controller app.DeviceController) func(context.Context, json.RawMessage) (string, error) {
	return func(ctx context.Context, input json.RawMessage) (string, error) {
		if err := ctx.Err(); err != nil {
			return "", err
		}

		var in setDeviceInput
		if err := json.Unmarshal(input, &in); err != nil {
			return "", fmt.Errorf("invalid input: %w", err)
		}

		// Find device by name
		idx, device, err := findDeviceByName(controller, in.Name)
		if err != nil {
			return "", err
		}

		if err := ctx.Err(); err != nil {
			return "", err
		}

		// Set the device state
		result := controller.SetDevice(idx, in.State)
		if result.Error != nil {
			return "", result.Error
		}

		if result.WindowShutter != nil {
			return device.Name + " (window_shutter): " + describeShutter(result.WindowShutter), nil
		}
		stateStr := "off"
		if result.NewState {
			stateStr = "on"
		}
		return fmt.Sprintf("%s (%s) set to %s", device.Name, device.Type, stateStr), nil
	}
}

func makeAdjustBrightness(controller app.DeviceController) func(context.Context, json.RawMessage) (string, error) {
	return func(ctx context.Context, input json.RawMessage) (string, error) {
		if err := ctx.Err(); err != nil {
			return "", err
		}

		var in adjustBrightnessInput
		if err := json.Unmarshal(input, &in); err != nil {
			return "", fmt.Errorf("invalid input: %w", err)
		}

		idx, device, err := findDeviceByName(controller, in.Name)
		if err != nil {
			return "", err
		}

		if err := ctx.Err(); err != nil {
			return "", err
		}

		result := controller.AdjustDeviceBrightness(idx, in.Delta)
		if result.Error != nil {
			return "", result.Error
		}

		// Re-read the device to report the resulting brightness.
		brightness := device.Brightness
		if _, updated, err := findDeviceByName(controller, in.Name); err == nil {
			brightness = updated.Brightness
		}
		return fmt.Sprintf("%s (%s) brightness is now %d%%", device.Name, device.Type, brightness), nil
	}
}

func makeGetDeviceState(controller app.DeviceController) func(context.Context, json.RawMessage) (string, error) {
	return func(ctx context.Context, input json.RawMessage) (string, error) {
		if err := ctx.Err(); err != nil {
			return "", err
		}

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
		if device.Type == app.DeviceTypeDimmableLight {
			state["brightness"] = device.Brightness
		}
		if ws := shutterInfo(device.WindowShutter); ws != nil {
			state["window_shutter"] = ws
			state["summary"] = describeShutter(device.WindowShutter)
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

// shutterInfo is the agent-facing shutter state (nil for other devices).
// The position is labelled as an estimate.
func shutterInfo(ws *app.WindowShutterState) map[string]any {
	if ws == nil {
		return nil
	}
	info := map[string]any{
		"motion":                 ws.Motion,
		"position_known":         ws.PositionKnown,
		"pending_start":          ws.PendingStart,
		"open_duration_seconds":  ws.OpenDurationSeconds,
		"close_duration_seconds": ws.CloseDurationSeconds,
		"remaining_travel_ms":    ws.RemainingTravelMilliseconds,
	}
	if ws.PositionKnown {
		info["estimated_position_percent_open"] = ws.CurrentPosition
	}
	if ws.TargetPosition >= 0 {
		info["target_position_percent_open"] = ws.TargetPosition
	}
	return info
}
