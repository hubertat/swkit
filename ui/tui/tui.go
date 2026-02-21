package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/hubertat/swkit/agent"
	"github.com/hubertat/swkit/app"
)

// IoDebugFilter controls which IO points are shown
type IoDebugFilter int

const (
	IoFilterAll IoDebugFilter = iota
	IoFilterInputs
	IoFilterOutputs
)

func (f IoDebugFilter) String() string {
	switch f {
	case IoFilterInputs:
		return "Inputs"
	case IoFilterOutputs:
		return "Outputs"
	default:
		return "All"
	}
}

// Tab represents a navigation tab
type Tab int

const (
	TabDashboard Tab = iota
	TabDrivers
	TabDevices
	TabConfig
	TabHomeKit
	TabIoDebug
	TabChat
)

func (t Tab) String() string {
	switch t {
	case TabDashboard:
		return "Dashboard"
	case TabDrivers:
		return "Drivers"
	case TabDevices:
		return "Devices"
	case TabConfig:
		return "Config"
	case TabHomeKit:
		return "HomeKit"
	case TabIoDebug:
		return "IO Debug"
	case TabChat:
		return "Chat"
	default:
		return "Unknown"
	}
}

// AllTabs returns all available tabs
func AllTabs() []Tab {
	return []Tab{TabDashboard, TabDrivers, TabDevices, TabConfig, TabHomeKit, TabIoDebug, TabChat}
}

// Model is the main TUI model
type Model struct {
	provider      app.StateProvider
	state         app.AppState
	activeTab     Tab
	keys          KeyMap
	theme         Theme
	width, height int
	showHelp      bool
	cursor        int // For list navigation within tabs
	ioDebugFilter IoDebugFilter
	ioNames        map[string]string    // session-only custom names, key: "driver|type|index"
	ioPrevStates   map[string]bool      // last seen State per IO point for TUI-side change detection
	ioStateChanged map[string]time.Time // when TUI last observed a state change for an IO point
	ioNaming      bool              // true when text input is active for naming
	ioNameInput   textinput.Model
	ioNameTarget  string // key of the IO point being named
	ioExportMsg   string // transient status message after export
	ctx           context.Context
	cancel        context.CancelFunc
	chat          ChatView
	agent         *agent.Agent
	configEditor  ConfigEditor
}

// StateUpdateMsg is sent when state is updated
type StateUpdateMsg struct {
	State app.AppState
}

// ControlResultMsg is sent when a device control operation completes
type ControlResultMsg struct {
	Result app.ControlResult
}

// NewModel creates a new TUI model using the default renderer
func NewModel(provider app.StateProvider) Model {
	return NewModelWithOptions(provider, nil, nil, nil)
}

// NewModelWithAgent creates a new TUI model with an optional agent
func NewModelWithAgent(provider app.StateProvider, ag *agent.Agent) Model {
	return NewModelWithOptions(provider, nil, ag, nil)
}

// NewModelWithRenderer creates a new TUI model with a custom renderer.
// This is needed for SSH sessions where each connection has its own renderer.
func NewModelWithRenderer(provider app.StateProvider, renderer *lipgloss.Renderer) Model {
	return NewModelWithOptions(provider, nil, nil, renderer)
}

// NewModelWithRendererAndAgent creates a new TUI model with a custom renderer and optional agent.
func NewModelWithRendererAndAgent(provider app.StateProvider, renderer *lipgloss.Renderer, ag *agent.Agent) Model {
	return NewModelWithOptions(provider, nil, ag, renderer)
}

// NewModelWithOptions creates a new TUI model with all optional dependencies.
func NewModelWithOptions(provider app.StateProvider, configProvider app.ConfigProvider, ag *agent.Agent, renderer *lipgloss.Renderer) Model {
	ctx, cancel := context.WithCancel(context.Background())
	var theme Theme
	if renderer != nil {
		theme = ThemeWithRenderer(renderer)
	} else {
		theme = DefaultTheme()
	}
	return Model{
		provider:       provider,
		state:          provider.GetState(),
		activeTab:      TabDashboard,
		keys:           DefaultKeyMap(),
		theme:          theme,
		ioNames:        make(map[string]string),
		ioPrevStates:   make(map[string]bool),
		ioStateChanged: make(map[string]time.Time),
		ioNameInput:    newIoNameInput(),
		ctx:            ctx,
		cancel:         cancel,
		agent:          ag,
		chat:           NewChatView(ag, theme),
		configEditor:   NewConfigEditor(configProvider, theme),
	}
}

// Init initializes the model
func (m Model) Init() tea.Cmd {
	return m.subscribeToState()
}

// subscribeToState starts the state subscription
func (m Model) subscribeToState() tea.Cmd {
	return func() tea.Msg {
		ch := m.provider.Subscribe(m.ctx, 500*time.Millisecond)
		state, ok := <-ch
		if !ok {
			return nil
		}
		return StateUpdateMsg{State: state}
	}
}

// waitForNextState waits for the next state update
func (m Model) waitForNextState() tea.Cmd {
	return func() tea.Msg {
		ch := m.provider.Subscribe(m.ctx, 500*time.Millisecond)
		state, ok := <-ch
		if !ok {
			return nil
		}
		return StateUpdateMsg{State: state}
	}
}

// Update handles messages
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case ioExportClearMsg:
		m.ioExportMsg = ""
		return m, nil

	case ConfigSaveMsg:
		if msg.Error != nil {
			m.configEditor.statusMsg = "Save error: " + msg.Error.Error()
		} else {
			m.configEditor.statusMsg = "Config saved"
			m.configEditor.dirty = false
		}
		return m, tea.Tick(3*time.Second, func(time.Time) tea.Msg {
			return configSaveClearMsg{}
		})

	case configSaveClearMsg:
		m.configEditor.statusMsg = ""
		return m, nil

	case tea.KeyMsg:
		// Handle IO naming mode - intercept all keys
		if m.ioNaming {
			switch msg.Type {
			case tea.KeyEnter:
				name := strings.TrimSpace(m.ioNameInput.Value())
				if name != "" {
					m.ioNames[m.ioNameTarget] = name
				} else {
					delete(m.ioNames, m.ioNameTarget)
				}
				m.ioNaming = false
				m.ioNameInput.Blur()
				return m, nil
			case tea.KeyEsc:
				m.ioNaming = false
				m.ioNameInput.Blur()
				return m, nil
			default:
				var cmd tea.Cmd
				m.ioNameInput, cmd = m.ioNameInput.Update(msg)
				return m, cmd
			}
		}

		// Global Ctrl+S for config save
		if msg.String() == "ctrl+s" && m.configEditor.IsDirty() {
			return m, m.configEditor.Save()
		}

		// Handle config tab - forward most keys to config editor
		if m.activeTab == TabConfig {
			// Only capture tab switching and quit from config
			switch {
			case key.Matches(msg, m.keys.Tab):
				m.activeTab = (m.activeTab + 1) % Tab(len(AllTabs()))
				m.cursor = 0
				if m.activeTab == TabChat {
					m.chat.Focus()
				}
				return m, nil
			case key.Matches(msg, m.keys.ShiftTab):
				if m.activeTab == 0 {
					m.activeTab = Tab(len(AllTabs()) - 1)
				} else {
					m.activeTab--
				}
				m.cursor = 0
				if m.activeTab == TabChat {
					m.chat.Focus()
				}
				return m, nil
			case key.Matches(msg, m.keys.Quit):
				m.cancel()
				return m, tea.Quit
			case key.Matches(msg, m.keys.Help):
				m.showHelp = !m.showHelp
				return m, nil
			default:
				cmd := m.configEditor.Update(msg)
				return m, cmd
			}
		}

		// Handle chat tab specially - forward most keys to chat view
		if m.activeTab == TabChat && m.chat.IsFocused() {
			// Only capture tab switching from chat (let all other keys through including 'q')
			switch {
			case key.Matches(msg, m.keys.Tab):
				m.chat.Blur()
				m.activeTab = (m.activeTab + 1) % Tab(len(AllTabs()))
				m.cursor = 0
				return m, nil
			case key.Matches(msg, m.keys.ShiftTab):
				m.chat.Blur()
				if m.activeTab == 0 {
					m.activeTab = Tab(len(AllTabs()) - 1)
				} else {
					m.activeTab--
				}
				m.cursor = 0
				return m, nil
			case msg.String() == "esc":
				m.chat.Blur()
				return m, nil
			default:
				// Forward to chat view
				var cmd tea.Cmd
				m.chat, cmd = m.chat.Update(msg)
				return m, cmd
			}
		}

		switch {
		case key.Matches(msg, m.keys.Quit):
			m.cancel()
			return m, tea.Quit

		case key.Matches(msg, m.keys.Tab):
			m.activeTab = (m.activeTab + 1) % Tab(len(AllTabs()))
			m.cursor = 0
			if m.activeTab == TabChat {
				m.chat.Focus()
			}
			return m, nil

		case key.Matches(msg, m.keys.ShiftTab):
			if m.activeTab == 0 {
				m.activeTab = Tab(len(AllTabs()) - 1)
			} else {
				m.activeTab--
			}
			m.cursor = 0
			if m.activeTab == TabChat {
				m.chat.Focus()
			}
			return m, nil

		case key.Matches(msg, m.keys.Up):
			if m.cursor > 0 {
				m.cursor--
			}
			return m, nil

		case key.Matches(msg, m.keys.Down):
			maxItems := m.getMaxItems()
			if m.cursor < maxItems-1 {
				m.cursor++
			}
			return m, nil

		case key.Matches(msg, m.keys.Help):
			m.showHelp = !m.showHelp
			return m, nil

		case key.Matches(msg, m.keys.Refresh):
			m.state = m.provider.GetState()
			return m, nil

		case key.Matches(msg, m.keys.Filter):
			if m.activeTab == TabIoDebug {
				m.ioDebugFilter = (m.ioDebugFilter + 1) % 3
				m.cursor = 0
				return m, nil
			}

		case key.Matches(msg, m.keys.Name):
			if m.activeTab == TabIoDebug && m.ioDebugFilter != IoFilterAll {
				points := m.ioDebugByType(m.ioDebugFilter)
				if m.cursor >= 0 && m.cursor < len(points) {
					pt := points[m.cursor]
					k := ioPointKey(pt)
					m.ioNameTarget = k
					m.ioNameInput.SetValue(m.ioNames[k])
					m.ioNameInput.Focus()
					m.ioNaming = true
					return m, textinput.Blink
				}
			}

		case key.Matches(msg, m.keys.Export):
			if m.activeTab == TabIoDebug && len(m.ioNames) > 0 {
				exportMsg, cmd := m.doExportIoNames()
				m.ioExportMsg = exportMsg
				return m, cmd
			}

		case key.Matches(msg, m.keys.Enter):
			if m.activeTab == TabDevices {
				return m, m.toggleSelectedDevice()
			}
			if m.activeTab == TabIoDebug && m.ioDebugFilter == IoFilterOutputs {
				return m, m.toggleSelectedIoOutput()
			}
			if m.activeTab == TabChat && !m.chat.IsFocused() {
				m.chat.Focus()
				return m, nil
			}
			return m, nil
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		// Update chat view size (account for header, tabs, help)
		chatHeight := m.height - 10
		if chatHeight < 10 {
			chatHeight = 10
		}
		m.chat.SetSize(m.width-4, chatHeight)
		return m, nil

	case ChatResponseMsg:
		var cmd tea.Cmd
		m.chat, cmd = m.chat.Update(msg)
		cmds = append(cmds, cmd)
		return m, tea.Batch(cmds...)

	case StateUpdateMsg:
		m.detectIoStateChanges(msg.State.IoDebug)
		m.state = msg.State
		return m, m.waitForNextState()

	case ControlResultMsg:
		// Control operation completed - state will update on next poll
		// Could add error feedback here in the future
		return m, nil
	}

	return m, nil
}

// toggleSelectedDevice sends a toggle command for the currently selected device
func (m Model) toggleSelectedDevice() tea.Cmd {
	controller, ok := m.provider.(app.DeviceController)
	if !ok {
		return nil
	}
	return func() tea.Msg {
		return ControlResultMsg{Result: controller.ToggleDevice(m.cursor)}
	}
}

// toggleSelectedIoOutput sends a toggle command for the selected IO output
func (m Model) toggleSelectedIoOutput() tea.Cmd {
	controller, ok := m.provider.(app.IoOutputController)
	if !ok {
		return nil
	}
	outputs := m.ioDebugByType(IoFilterOutputs)
	if m.cursor < 0 || m.cursor >= len(outputs) {
		return nil
	}
	pt := outputs[m.cursor]
	return func() tea.Msg {
		_ = controller.ToggleIoOutput(pt.DriverName, pt.Index)
		return nil
	}
}

// doExportIoNames writes named IO points to a JSON file, returns status message and clear cmd
func (m Model) doExportIoNames() (string, tea.Cmd) {
	type namedPoint struct {
		Driver string `json:"driver"`
		Type   string `json:"type"`
		Index  int    `json:"index"`
		HwName string `json:"hw_name"`
		Name   string `json:"name"`
	}

	var points []namedPoint
	for _, pt := range m.state.IoDebug {
		k := ioPointKey(pt)
		if name, ok := m.ioNames[k]; ok {
			points = append(points, namedPoint{
				Driver: pt.DriverName,
				Type:   pt.Type,
				Index:  pt.Index,
				HwName: pt.Name,
				Name:   name,
			})
		}
	}

	data, err := json.MarshalIndent(points, "", "  ")
	if err != nil {
		return "Export error: " + err.Error(), clearExportMsg()
	}

	filename := "io_names.json"
	if err := os.WriteFile(filename, data, 0644); err != nil {
		return "Export error: " + err.Error(), clearExportMsg()
	}

	return fmt.Sprintf("Exported %d names to %s", len(points), filename), clearExportMsg()
}

func clearExportMsg() tea.Cmd {
	return tea.Tick(3*time.Second, func(time.Time) tea.Msg {
		return ioExportClearMsg{}
	})
}

// getMaxItems returns the max navigable items for current tab
func (m Model) getMaxItems() int {
	switch m.activeTab {
	case TabDrivers:
		return len(m.state.Drivers)
	case TabDevices:
		return len(m.state.Devices)
	case TabIoDebug:
		if m.ioDebugFilter == IoFilterAll {
			return 0 // side-by-side view, no cursor
		}
		return len(m.ioDebugByType(m.ioDebugFilter))
	default:
		return 0
	}
}

// detectIoStateChanges compares incoming IO point states against the last seen values.
// When a state change is detected, it records the current time in ioStateChanged.
// Both maps are reference types so mutations persist across BubbleTea model copies.
func (m Model) detectIoStateChanges(pts []app.IoPointDebugState) {
	now := time.Now()
	for _, pt := range pts {
		k := ioPointKey(pt)
		if prev, ok := m.ioPrevStates[k]; ok && prev != pt.State {
			m.ioStateChanged[k] = now
		}
		m.ioPrevStates[k] = pt.State
	}
}

// ioDebugByType returns IO debug points filtered to a specific type
func (m Model) ioDebugByType(filter IoDebugFilter) []app.IoPointDebugState {
	filterType := "input"
	if filter == IoFilterOutputs {
		filterType = "output"
	}
	var filtered []app.IoPointDebugState
	for _, pt := range m.state.IoDebug {
		if pt.Type == filterType {
			filtered = append(filtered, pt)
		}
	}
	return filtered
}

// View renders the UI
func (m Model) View() string {
	var b strings.Builder

	// Header
	header := m.theme.Header.Render(IconHome + " swkit - " + m.state.Name)
	b.WriteString(header)
	b.WriteString("\n\n")

	// Tab bar
	b.WriteString(m.renderTabBar())
	b.WriteString("\n\n")

	// Content based on active tab
	switch m.activeTab {
	case TabDashboard:
		b.WriteString(m.renderDashboard())
	case TabDrivers:
		b.WriteString(m.renderDrivers())
	case TabDevices:
		b.WriteString(m.renderDevices())
	case TabConfig:
		b.WriteString(m.configEditor.View(m.theme))
	case TabHomeKit:
		b.WriteString(m.renderHomeKit())
	case TabIoDebug:
		b.WriteString(m.renderIoDebug())
	case TabChat:
		b.WriteString(m.chat.View())
	}

	// Help
	b.WriteString("\n")
	b.WriteString(m.renderHelp())

	return m.theme.App.Render(b.String())
}

// renderTabBar renders the navigation tabs
func (m Model) renderTabBar() string {
	var tabs []string
	for _, tab := range AllTabs() {
		style := m.theme.TabInactive
		if tab == m.activeTab {
			style = m.theme.TabActive
		}
		tabs = append(tabs, style.Render(tab.String()))
	}
	return m.theme.TabBar.Render(lipgloss.JoinHorizontal(lipgloss.Top, tabs...))
}

// renderDashboard renders the dashboard view
func (m Model) renderDashboard() string {
	summary := m.state.Summary()

	// Drivers box
	driversContent := m.theme.Primary.Render(IconDriver+" Drivers") + "\n"
	driversStatus := m.theme.Success.Render("Ready")
	if summary.DriversReady < summary.DriversTotal {
		driversStatus = m.theme.Error.Render("Issues")
	}
	driversContent += driversStatus + " " + m.theme.Secondary.Render(
		strings.Repeat("●", summary.DriversReady)+
			strings.Repeat("○", summary.DriversTotal-summary.DriversReady))
	driversBox := m.theme.Box.Width(24).Render(driversContent)

	// Devices box
	devicesContent := m.theme.Primary.Render(IconHome+" Devices") + "\n"
	devicesContent += IconLight + " Lights: " + m.theme.Secondary.Render(itoa(summary.LightsCount)) + "\n"
	if summary.ColorLightsCount > 0 {
		devicesContent += IconColorLight + " Color: " + m.theme.Secondary.Render(itoa(summary.ColorLightsCount)) + "\n"
	}
	devicesContent += IconOutlet + " Outlets: " + m.theme.Secondary.Render(itoa(summary.OutletsCount)) + "\n"
	devicesContent += IconButton + " Buttons: " + m.theme.Secondary.Render(itoa(summary.ButtonsCount))
	devicesBox := m.theme.Box.Width(24).Render(devicesContent)

	// HomeKit box
	hkContent := m.theme.Primary.Render(IconHomeKit+" HomeKit") + "\n"
	if summary.HomeKitEnabled {
		hkContent += m.theme.Success.Render("Enabled") + "\n"
		hkContent += m.theme.Secondary.Render(itoa(summary.HomeKitDevices) + " accessories")
	} else {
		hkContent += m.theme.Muted.Render("Disabled")
	}
	hkBox := m.theme.Box.Width(24).Render(hkContent)

	// Layout boxes
	leftColumn := lipgloss.JoinVertical(lipgloss.Left, driversBox, hkBox)
	return lipgloss.JoinHorizontal(lipgloss.Top, leftColumn, "  ", devicesBox)
}

// renderDrivers renders the drivers view
func (m Model) renderDrivers() string {
	if len(m.state.Drivers) == 0 {
		return m.theme.Muted.Render("No drivers configured")
	}

	var lines []string
	for i, driver := range m.state.Drivers {
		prefix := "   "
		style := m.theme.ListItem
		if i == m.cursor {
			prefix = " > "
			style = m.theme.ListItemSelected
		}

		statusText := m.theme.Ready.Render("Ready")
		if !driver.Ready {
			statusText = m.theme.NotReady.Render("Not Ready")
		}

		line := prefix + m.theme.Primary.Render(padRight(driver.Name, 12)) + " " + statusText
		if driver.StatusInfo != "" {
			line += "  " + m.theme.Secondary.Render(driver.StatusInfo)
		}

		lines = append(lines, style.Render(line))
	}

	content := strings.Join(lines, "\n")
	return m.theme.Box.Render(content)
}

// renderDevices renders the devices view with list and detail panel
func (m Model) renderDevices() string {
	if len(m.state.Devices) == 0 {
		return m.theme.Muted.Render("No devices configured")
	}

	listBox := m.theme.Box.Render(m.renderDeviceList())
	detailBox := m.theme.Box.Width(40).Render(m.renderDeviceDetail())

	return lipgloss.JoinHorizontal(lipgloss.Top, listBox, " ", detailBox)
}

// renderDeviceList renders the device list for the left column
func (m Model) renderDeviceList() string {
	var lines []string
	for i, device := range m.state.Devices {
		prefix := "   "
		style := m.theme.ListItem
		if i == m.cursor {
			prefix = " > "
			style = m.theme.ListItemSelected
		}

		icon := deviceIcon(device.Type)

		// State indicator
		stateText := m.theme.Off.Render(IconOff)
		if device.Type != app.DeviceTypeButton && device.IsOn {
			stateText = m.theme.On.Render(IconOn)
		}
		if device.Type == app.DeviceTypeButton {
			stateText = m.theme.Secondary.Render("-")
		}

		// Health indicator
		healthText := ""
		if device.IsFaulty {
			healthText = m.theme.Faulty.Render(" " + IconFaulty)
		}

		// HomeKit indicator
		hkText := ""
		if !device.HomeKitEnabled {
			hkText = m.theme.Muted.Render(" (no HK)")
		}

		line := prefix + icon + " " + m.theme.Primary.Render(padRight(device.Name, 20)) +
			" " + stateText + healthText + hkText

		lines = append(lines, style.Render(line))
	}

	return strings.Join(lines, "\n")
}

// renderDeviceDetail renders the detail panel for the selected device
func (m Model) renderDeviceDetail() string {
	if m.cursor < 0 || m.cursor >= len(m.state.Devices) {
		return m.theme.Muted.Render("No device selected")
	}

	device := m.state.Devices[m.cursor]
	icon := deviceIcon(device.Type)

	// Title
	title := m.theme.Primary.Render(icon + " " + device.Name)

	// Type
	typeName := string(device.Type)
	typeLabel := m.theme.Secondary.Render("Type: ") + typeName

	// HomeKit status
	hkLabel := m.theme.Secondary.Render("HomeKit: ")
	if device.HomeKitEnabled {
		hkLabel += m.theme.Success.Render("enabled")
	} else {
		hkLabel += m.theme.Muted.Render("disabled")
	}

	// Health status
	healthLabel := m.theme.Secondary.Render("Health: ")
	if device.IsFaulty {
		healthLabel += m.theme.Faulty.Render("faulty")
	} else if device.IsHealthy {
		healthLabel += m.theme.Healthy.Render("healthy")
	} else {
		healthLabel += m.theme.Muted.Render("unknown")
	}

	lines := []string{title, "", typeLabel, hkLabel, healthLabel}

	// IO Config section
	hasIo := device.OutputIoId != "" || device.RgbwIoId != "" || device.EventInputId != ""
	if hasIo {
		lines = append(lines, "", m.theme.BoxTitle.Render("IO Config"))
		if device.OutputIoId != "" {
			lines = append(lines, m.theme.Secondary.Render("Output: ")+device.OutputIoId)
		}
		if device.RgbwIoId != "" {
			lines = append(lines, m.theme.Secondary.Render("RGBW:   ")+device.RgbwIoId)
		}
		if device.EventInputId != "" {
			lines = append(lines, m.theme.Secondary.Render("Input:  ")+device.EventInputId)
		}
	}

	// Controls section (buttons only)
	if len(device.ControlRelations) > 0 {
		lines = append(lines, "", m.theme.BoxTitle.Render("Controls"))
		for _, rel := range device.ControlRelations {
			lines = append(lines, m.theme.Secondary.Render(rel.EventType)+" "+
				m.theme.On.Render(rel.Action)+" "+
				m.theme.Primary.Render(rel.DeviceName))
		}
	}

	return strings.Join(lines, "\n")
}

// renderHomeKit renders the HomeKit view
func (m Model) renderHomeKit() string {
	hk := m.state.HomeKit

	var content string
	if hk.Enabled {
		content = m.theme.Success.Render("HomeKit Enabled") + "\n\n"
		content += m.theme.Secondary.Render("PIN: ") + m.theme.Primary.Render(formatPin(hk.Pin)) + "\n"
		if hk.Address != "" {
			content += m.theme.Secondary.Render("Address: ") + m.theme.Primary.Render(hk.Address) + "\n"
		}
		content += m.theme.Secondary.Render("Accessories: ") + m.theme.Primary.Render(itoa(hk.DeviceCount))
	} else {
		content = m.theme.Muted.Render("HomeKit is not configured\n")
		content += m.theme.Secondary.Render("Set HkPin in config.json to enable")
	}

	return m.theme.Box.Width(40).Render(content)
}

// renderIoDebug renders the IO debug view
func (m Model) renderIoDebug() string {
	if len(m.state.IoDebug) == 0 {
		return m.theme.Muted.Render("No IO debug data available")
	}

	// Filter header
	header := m.theme.Secondary.Render("View: ")
	header += m.theme.Primary.Render(m.ioDebugFilter.String())
	header += m.theme.Muted.Render("  (f to cycle)")
	if m.ioDebugFilter != IoFilterAll {
		header += m.theme.Muted.Render("  n: name  w: export")
	}

	var result string
	if m.ioDebugFilter == IoFilterAll {
		result = header + "\n" + m.renderIoDebugColumns()
	} else {
		points := m.ioDebugByType(m.ioDebugFilter)
		if len(points) == 0 {
			result = header + "\n" + m.theme.Muted.Render("No matching IO points")
		} else {
			content := m.renderIoDebugList(points, true)
			result = header + "\n" + m.theme.Box.Render(content)
		}
	}

	// Text input for naming
	if m.ioNaming {
		result += "\n" + m.theme.Secondary.Render("Name: ") + m.ioNameInput.View()
	}

	// Export status message
	if m.ioExportMsg != "" {
		result += "\n" + m.theme.Success.Render(m.ioExportMsg)
	}

	return result
}

// renderIoDebugColumns renders inputs and outputs side by side
func (m Model) renderIoDebugColumns() string {
	inputs := m.ioDebugByType(IoFilterInputs)
	outputs := m.ioDebugByType(IoFilterOutputs)

	inputTitle := m.theme.BoxTitle.Render("Inputs (" + itoa(len(inputs)) + ")")
	outputTitle := m.theme.BoxTitle.Render("Outputs (" + itoa(len(outputs)) + ")")

	inputContent := m.theme.Muted.Render("none")
	if len(inputs) > 0 {
		inputContent = m.renderIoDebugList(inputs, false)
	}

	outputContent := m.theme.Muted.Render("none")
	if len(outputs) > 0 {
		outputContent = m.renderIoDebugList(outputs, false)
	}

	inputBox := m.theme.Box.Render(inputTitle + "\n" + inputContent)
	outputBox := m.theme.Box.Render(outputTitle + "\n" + outputContent)

	return lipgloss.JoinHorizontal(lipgloss.Top, inputBox, "  ", outputBox)
}

// renderIoDebugList renders a list of IO points
func (m Model) renderIoDebugList(points []app.IoPointDebugState, withCursor bool) string {
	now := m.state.Timestamp
	nameWidth := 8
	for _, pt := range points {
		if len(pt.Name) > nameWidth {
			nameWidth = len(pt.Name)
		}
	}
	var lines []string
	for i, pt := range points {
		prefix := "   "
		style := m.theme.ListItem
		if withCursor && i == m.cursor {
			prefix = " > "
			style = m.theme.ListItemSelected
		}

		// State indicator — orange for 2s after an explicit event, then back to normal
		stateText := m.theme.Off.Render(IconOff)
		if pt.State {
			stateText = m.theme.On.Render(IconOn)
		}
		if !pt.LastEvent.IsZero() && now.Sub(pt.LastEvent) < 2*time.Second {
			stateText = m.theme.Event.Render(IconOn)
		}

		// Health indicator
		healthText := m.theme.Healthy.Render(IconHealthy)
		if !pt.Healthy {
			healthText = m.theme.Faulty.Render(IconFaulty)
		}

		// Activity timer: counts up (seconds since last state change or event), visible for 200s
		changedText := ""
		activityTime := pt.LastChanged
		if !pt.LastEvent.IsZero() && pt.LastEvent.After(activityTime) {
			activityTime = pt.LastEvent
		}
		if tuiChange, ok := m.ioStateChanged[ioPointKey(pt)]; ok && tuiChange.After(activityTime) {
			activityTime = tuiChange
		}
		if !activityTime.IsZero() {
			const timerDuration = 200 * time.Second
			ago := now.Sub(activityTime)
			if ago < timerDuration {
				changedText = " " + m.theme.On.Render(fmt.Sprintf("%ds", int(ago.Seconds())))
			}
		}

		// Custom name
		nameText := ""
		if customName, ok := m.ioNames[ioPointKey(pt)]; ok {
			nameText = " " + m.theme.Secondary.Render("["+customName+"]")
		}

		line := prefix +
			m.theme.Primary.Render(padRight(pt.Name, nameWidth)) + " " +
			stateText + " " +
			healthText +
			changedText +
			nameText

		lines = append(lines, style.Render(line))
	}

	return strings.Join(lines, "\n")
}

// renderHelp renders the help bar
func (m Model) renderHelp() string {
	if m.activeTab == TabConfig {
		configHelp := m.configEditor.ConfigHelpKeys(m.theme)
		return m.theme.Help.Render(configHelp)
	}
	bindings := m.keys.ShortHelp()
	var parts []string
	for _, b := range bindings {
		parts = append(parts, m.theme.HelpKey.Render(b.Help().Key)+" "+m.theme.HelpDesc.Render(b.Help().Desc))
	}
	return m.theme.Help.Render(strings.Join(parts, "  "))
}

// Helper functions

func deviceIcon(t app.DeviceType) string {
	switch t {
	case app.DeviceTypeLight:
		return IconLight
	case app.DeviceTypeColorLight:
		return IconColorLight
	case app.DeviceTypeOutlet:
		return IconOutlet
	case app.DeviceTypeButton:
		return IconButton
	default:
		return "?"
	}
}

func itoa(n int) string {
	return strconv.Itoa(n)
}

func padRight(s string, n int) string {
	if len(s) >= n {
		return s[:n]
	}
	return s + strings.Repeat(" ", n-len(s))
}

// ioExportClearMsg clears the export status message after a delay
type ioExportClearMsg struct{}

// configSaveClearMsg clears the config save status message after a delay
type configSaveClearMsg struct{}

func newIoNameInput() textinput.Model {
	ti := textinput.New()
	ti.Placeholder = "Enter name..."
	ti.CharLimit = 40
	ti.Width = 30
	return ti
}

// ioPointKey returns a unique key for an IO point (used as map key for names)
func ioPointKey(pt app.IoPointDebugState) string {
	return pt.DriverName + "|" + pt.Type + "|" + itoa(pt.Index)
}

func formatPin(pin string) string {
	if len(pin) == 8 {
		return pin[:3] + "-" + pin[3:5] + "-" + pin[5:]
	}
	return pin
}
