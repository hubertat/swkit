package agent

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/hubertat/swkit/app"
)

// mockController implements DeviceController for testing
type mockController struct {
	state app.AppState
}

func newMockController() *mockController {
	return &mockController{
		state: app.AppState{
			Name: "Test Home",
			Drivers: []app.DriverState{
				{Name: "gpio", Ready: true},
				{Name: "shelly", Ready: true, StatusInfo: "2 devices"},
			},
			Devices: []app.DeviceState{
				{Name: "Living Room Light", Type: app.DeviceTypeLight, IsOn: false, IsHealthy: true},
				{Name: "Kitchen Light", Type: app.DeviceTypeLight, IsOn: true, IsHealthy: true},
				{Name: "Bedroom Outlet", Type: app.DeviceTypeOutlet, IsOn: false, IsHealthy: true},
			},
			HomeKit: app.HomeKitState{
				Enabled:     true,
				Pin:         "12345678",
				DeviceCount: 3,
			},
		},
	}
}

func (m *mockController) GetState() app.AppState {
	return m.state
}

func (m *mockController) Subscribe(ctx context.Context, interval time.Duration) <-chan app.AppState {
	ch := make(chan app.AppState)
	go func() {
		defer close(ch)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				ch <- m.state
			}
		}
	}()
	return ch
}

func (m *mockController) ToggleDevice(index int) app.ControlResult {
	if index < 0 || index >= len(m.state.Devices) {
		return app.ControlResult{Error: nil}
	}
	m.state.Devices[index].IsOn = !m.state.Devices[index].IsOn
	return app.ControlResult{
		DeviceName: m.state.Devices[index].Name,
		Action:     "toggle",
		NewState:   m.state.Devices[index].IsOn,
	}
}

func (m *mockController) SetDevice(index int, state bool) app.ControlResult {
	if index < 0 || index >= len(m.state.Devices) {
		return app.ControlResult{Error: nil}
	}
	m.state.Devices[index].IsOn = state
	return app.ControlResult{
		DeviceName: m.state.Devices[index].Name,
		Action:     "set",
		NewState:   m.state.Devices[index].IsOn,
	}
}

func (m *mockController) SetDeviceBrightness(index int, pct int) app.ControlResult {
	if index < 0 || index >= len(m.state.Devices) {
		return app.ControlResult{Error: nil}
	}
	m.state.Devices[index].Brightness = pct
	return app.ControlResult{
		DeviceName: m.state.Devices[index].Name,
		Action:     "brightness",
		NewState:   m.state.Devices[index].IsOn,
	}
}

func (m *mockController) AdjustDeviceBrightness(index int, delta int) app.ControlResult {
	if index < 0 || index >= len(m.state.Devices) {
		return app.ControlResult{Error: nil}
	}
	pct := m.state.Devices[index].Brightness + delta
	if pct < 0 {
		pct = 0
	} else if pct > 100 {
		pct = 100
	}
	m.state.Devices[index].Brightness = pct
	return app.ControlResult{
		DeviceName: m.state.Devices[index].Name,
		Action:     "brightness",
		NewState:   m.state.Devices[index].IsOn,
	}
}

func (m *mockController) SetDeviceValueFor(index int, state bool, seconds int) app.ControlResult {
	if index < 0 || index >= len(m.state.Devices) {
		return app.ControlResult{Error: nil}
	}
	m.state.Devices[index].IsOn = state
	return app.ControlResult{
		DeviceName: m.state.Devices[index].Name,
		Action:     "set_for",
		NewState:   state,
	}
}

func TestNewAgent_NoAPIKey(t *testing.T) {
	cfg := Config{
		APIKey: "",
		Model:  DefaultModel,
	}
	_, err := NewAgent(cfg, newMockController())
	if err != ErrNoAPIKey {
		t.Errorf("expected ErrNoAPIKey, got %v", err)
	}
}

func TestNewAgent_NilController(t *testing.T) {
	cfg := Config{
		APIKey: "test-key",
		Model:  DefaultModel,
	}
	_, err := NewAgent(cfg, nil)
	if err != ErrNilController {
		t.Errorf("expected ErrNilController, got %v", err)
	}
}

func TestToolRegistry(t *testing.T) {
	registry := NewToolRegistry()

	tool := Tool{
		Name:        "test_tool",
		Description: "A test tool",
		Execute: func(ctx context.Context, input json.RawMessage) (string, error) {
			return "executed", nil
		},
	}

	registry.Register(tool)

	got, ok := registry.Get("test_tool")
	if !ok {
		t.Fatal("expected tool to be found")
	}
	if got.Name != "test_tool" {
		t.Errorf("expected name 'test_tool', got %q", got.Name)
	}

	_, ok = registry.Get("nonexistent")
	if ok {
		t.Error("expected nonexistent tool to not be found")
	}

	names := registry.List()
	if len(names) != 1 || names[0] != "test_tool" {
		t.Errorf("expected ['test_tool'], got %v", names)
	}
}

func TestFindDeviceByName(t *testing.T) {
	ctrl := newMockController()

	tests := []struct {
		name    string
		wantIdx int
		wantErr bool
	}{
		{"Living Room Light", 0, false},
		{"living room light", 0, false}, // case insensitive
		{"Kitchen", 1, false},           // partial match
		{"bedroom", 2, false},           // partial match
		{"nonexistent", -1, true},
		{"Light", -1, true}, // ambiguous - matches multiple
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			idx, _, err := findDeviceByName(ctrl, tt.name)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				if idx != tt.wantIdx {
					t.Errorf("expected index %d, got %d", tt.wantIdx, idx)
				}
			}
		})
	}
}

func TestDeviceTools(t *testing.T) {
	ctrl := newMockController()
	registry := NewToolRegistry()
	registerDeviceTools(registry, ctrl)

	// Test list of registered tools
	tools := registry.List()
	expected := map[string]bool{"toggle_device": true, "set_device": true, "adjust_brightness": true, "get_device_state": true}
	for _, name := range tools {
		if !expected[name] {
			t.Errorf("unexpected tool: %s", name)
		}
		delete(expected, name)
	}
	if len(expected) > 0 {
		t.Errorf("missing tools: %v", expected)
	}

	// Test get_device_state
	getTool, _ := registry.Get("get_device_state")
	result, err := getTool.Execute(context.Background(), json.RawMessage(`{"name": "Kitchen Light"}`))
	if err != nil {
		t.Fatalf("get_device_state failed: %v", err)
	}

	var state map[string]any
	if err := json.Unmarshal([]byte(result), &state); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}
	if state["name"] != "Kitchen Light" {
		t.Errorf("expected name 'Kitchen Light', got %v", state["name"])
	}
	if state["is_on"] != true {
		t.Errorf("expected is_on true, got %v", state["is_on"])
	}

	// Test adjust_brightness against a dimmable light.
	ctrl.state.Devices = append(ctrl.state.Devices, app.DeviceState{
		Name: "Desk Lamp", Type: app.DeviceTypeDimmableLight, IsOn: true, IsHealthy: true, Brightness: 40,
	})
	adjTool, _ := registry.Get("adjust_brightness")
	if _, err := adjTool.Execute(context.Background(), json.RawMessage(`{"name": "Desk Lamp", "delta": 15}`)); err != nil {
		t.Fatalf("adjust_brightness failed: %v", err)
	}
	if got := ctrl.state.Devices[len(ctrl.state.Devices)-1].Brightness; got != 55 {
		t.Errorf("expected brightness 55 after +15, got %d", got)
	}
	// Clamp at 0 when dimming past the floor.
	if _, err := adjTool.Execute(context.Background(), json.RawMessage(`{"name": "Desk Lamp", "delta": -100}`)); err != nil {
		t.Fatalf("adjust_brightness failed: %v", err)
	}
	if got := ctrl.state.Devices[len(ctrl.state.Devices)-1].Brightness; got != 0 {
		t.Errorf("expected brightness clamped to 0, got %d", got)
	}

	// get_device_state should include brightness for dimmable lights.
	result, err = getTool.Execute(context.Background(), json.RawMessage(`{"name": "Desk Lamp"}`))
	if err != nil {
		t.Fatalf("get_device_state failed: %v", err)
	}
	state = nil
	if err := json.Unmarshal([]byte(result), &state); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}
	if _, ok := state["brightness"]; !ok {
		t.Errorf("expected brightness key for dimmable light, got %v", state)
	}
}

func TestInfoTools(t *testing.T) {
	ctrl := newMockController()
	registry := NewToolRegistry()
	registerInfoTools(registry, ctrl)

	// Test list_devices
	listTool, _ := registry.Get("list_devices")
	result, err := listTool.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("list_devices failed: %v", err)
	}

	var devices []map[string]any
	if err := json.Unmarshal([]byte(result), &devices); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}
	if len(devices) != 3 {
		t.Errorf("expected 3 devices, got %d", len(devices))
	}

	// Test list_devices with type filter
	result, err = listTool.Execute(context.Background(), json.RawMessage(`{"type": "light"}`))
	if err != nil {
		t.Fatalf("list_devices with filter failed: %v", err)
	}
	if err := json.Unmarshal([]byte(result), &devices); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}
	if len(devices) != 2 {
		t.Errorf("expected 2 lights, got %d", len(devices))
	}

	// Test get_system_status
	statusTool, _ := registry.Get("get_system_status")
	result, err = statusTool.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("get_system_status failed: %v", err)
	}

	var status map[string]any
	if err := json.Unmarshal([]byte(result), &status); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}
	if status["name"] != "Test Home" {
		t.Errorf("expected name 'Test Home', got %v", status["name"])
	}

	// Test get_homekit_status
	hkTool, _ := registry.Get("get_homekit_status")
	result, err = hkTool.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("get_homekit_status failed: %v", err)
	}

	var hk map[string]any
	if err := json.Unmarshal([]byte(result), &hk); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}
	if hk["enabled"] != true {
		t.Errorf("expected enabled true, got %v", hk["enabled"])
	}
}
