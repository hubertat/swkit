package tui

import (
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/hubertat/swkit/app"
)

// shutterTuiProvider is a DeviceController + WindowShutterController +
// IoOutputController recording the commands it receives.
type shutterTuiProvider struct {
	fakeStateProvider
	mu      sync.Mutex
	calls   []string
	toggled []int
}

func (p *shutterTuiProvider) record(s string) app.ControlResult {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, s)
	return app.ControlResult{DeviceName: "Sh"}
}
func (p *shutterTuiProvider) ToggleDevice(i int) app.ControlResult { return p.record("toggle") }
func (p *shutterTuiProvider) SetDevice(i int, v bool) app.ControlResult {
	return p.record("set")
}
func (p *shutterTuiProvider) SetDeviceBrightness(i, v int) app.ControlResult {
	return p.record("brightness")
}
func (p *shutterTuiProvider) AdjustDeviceBrightness(i, v int) app.ControlResult {
	return p.record("adjust")
}
func (p *shutterTuiProvider) SetDeviceValueFor(i int, v bool, s int) app.ControlResult {
	return p.record("set_for")
}
func (p *shutterTuiProvider) OpenDevice(i int) app.ControlResult  { return p.record("open") }
func (p *shutterTuiProvider) CloseDevice(i int) app.ControlResult { return p.record("close") }
func (p *shutterTuiProvider) StopDevice(i int) app.ControlResult  { return p.record("stop") }
func (p *shutterTuiProvider) SetDevicePosition(i, pct int) app.ControlResult {
	return p.record("position:" + itoa(pct))
}
func (p *shutterTuiProvider) ToggleIoOutput(driver string, index int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.toggled = append(p.toggled, index)
	return nil
}

func shutterTuiState(known bool) app.AppState {
	return app.AppState{
		Timestamp: time.Now(),
		Devices: []app.DeviceState{
			{Name: "L1", Type: app.DeviceTypeLight, IsHealthy: true},
			{Name: "Sh", Type: app.DeviceTypeWindowShutter, IsHealthy: true, WindowShutter: &app.WindowShutterState{
				Motion: "stopped", TargetPosition: 40, CurrentPosition: 40, PositionKnown: known,
				OpenIoId: "wago|d_out|4", CloseIoId: "wago|d_out|5", OpenDurationSeconds: 20, CloseDurationSeconds: 20,
			}},
		},
		IoDebug: []app.IoPointDebugState{
			{DriverName: "wago", Index: 4, Name: "M1:DO5", Type: "output", ConfiguredAs: "Sh (open)"},
			{DriverName: "wago", Index: 6, Name: "M1:DO7", Type: "output"},
		},
	}
}

func runCmd(t *testing.T, m Model, cmd tea.Cmd) Model {
	t.Helper()
	if cmd == nil {
		return m
	}
	msg := cmd()
	if msg == nil {
		return m
	}
	next, _ := m.Update(msg)
	return next.(Model)
}

func pressKey(t *testing.T, m Model, k string) (Model, tea.Cmd) {
	t.Helper()
	var km tea.KeyMsg
	switch k {
	case "enter":
		km = tea.KeyMsg{Type: tea.KeyEnter}
	default:
		km = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
	}
	next, cmd := m.Update(km)
	return next.(Model), cmd
}

func newShutterModel(known bool) (Model, *shutterTuiProvider) {
	p := &shutterTuiProvider{}
	p.state = shutterTuiState(known)
	m := NewModel(p)
	m.state = p.state
	m.activeTab = TabDevices
	m.cursor = 1
	return m, p
}

func TestTuiShutterKeys(t *testing.T) {
	m, p := newShutterModel(true)
	for _, k := range []string{"o", "c", "s", "+", "-", "enter"} {
		var cmd tea.Cmd
		m, cmd = pressKey(t, m, k)
		m = runCmd(t, m, cmd)
	}
	want := []string{"open", "close", "stop", "position:45", "position:35", "toggle"}
	if !slices.Equal(p.calls, want) {
		t.Fatalf("calls = %v, want %v", p.calls, want)
	}
	// p opens the position prompt; enter commits.
	var cmd tea.Cmd
	m, cmd = pressKey(t, m, "p")
	if !m.posPrompt {
		t.Fatal("p must open the position prompt")
	}
	m, _ = pressKey(t, m, "7")
	m, _ = pressKey(t, m, "0")
	m, cmd = pressKey(t, m, "enter")
	m = runCmd(t, m, cmd)
	if p.calls[len(p.calls)-1] != "position:70" {
		t.Fatalf("prompt position: %v", p.calls)
	}
	// Timed control is rejected for shutters, without a call.
	n := len(p.calls)
	m, cmd = pressKey(t, m, "f")
	m = runCmd(t, m, cmd)
	if len(p.calls) != n || !strings.Contains(m.deviceMsg, "not available") {
		t.Fatalf("f on shutter: calls %v msg %q", p.calls, m.deviceMsg)
	}
	if !strings.Contains(m.renderHelp(), "open/close/stop") {
		t.Fatal("help must show shutter keys")
	}
	if !strings.Contains(m.renderDeviceDetail(), "40% open (estimated)") {
		t.Fatalf("detail: %s", m.renderDeviceDetail())
	}
}

func TestTuiShutterUnknownRejectsSteps(t *testing.T) {
	m, p := newShutterModel(false)
	var cmd tea.Cmd
	m, cmd = pressKey(t, m, "+")
	m = runCmd(t, m, cmd)
	m, cmd = pressKey(t, m, "p")
	m = runCmd(t, m, cmd)
	if len(p.calls) != 0 || m.posPrompt || !strings.Contains(m.deviceMsg, "Position unknown") {
		t.Fatalf("unknown position must reject stepping: calls %v prompt %v msg %q", p.calls, m.posPrompt, m.deviceMsg)
	}
	// Open still works from unknown.
	m, cmd = pressKey(t, m, "o")
	runCmd(t, m, cmd)
	if !slices.Equal(p.calls, []string{"open"}) {
		t.Fatalf("calls %v", p.calls)
	}
}

func TestTuiShutterKeysScopedToShutter(t *testing.T) {
	m, p := newShutterModel(true)
	m.cursor = 0 // a light: o/c/s/p do nothing shutter-related
	for _, k := range []string{"o", "s", "p"} {
		var cmd tea.Cmd
		m, cmd = pressKey(t, m, k)
		m = runCmd(t, m, cmd)
	}
	if len(p.calls) != 0 || m.posPrompt {
		t.Fatalf("shutter keys must not act on a light: %v", p.calls)
	}
}

func TestTuiShutterOwnedIoOutputLocked(t *testing.T) {
	m, p := newShutterModel(true)
	m.activeTab = TabIoDebug
	m.ioDebugFilter = IoFilterOutputs
	m.cursor = 0 // wago 4 = shutter open output
	var cmd tea.Cmd
	m, cmd = pressKey(t, m, "enter")
	m = runCmd(t, m, cmd)
	if len(p.toggled) != 0 || !strings.Contains(m.ioExportMsg, "window shutter Sh (open)") {
		t.Fatalf("owned output must not toggle: %v %q", p.toggled, m.ioExportMsg)
	}
	m.cursor = 1
	m, cmd = pressKey(t, m, "enter")
	runCmd(t, m, cmd)
	if !slices.Equal(p.toggled, []int{6}) {
		t.Fatalf("free output must toggle: %v", p.toggled)
	}
}
