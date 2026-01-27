package tui

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/hubertat/swkit/agent"
	"github.com/hubertat/swkit/app"
)

// Tab represents a navigation tab
type Tab int

const (
	TabDashboard Tab = iota
	TabDrivers
	TabDevices
	TabHomeKit
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
	case TabHomeKit:
		return "HomeKit"
	case TabChat:
		return "Chat"
	default:
		return "Unknown"
	}
}

// AllTabs returns all available tabs
func AllTabs() []Tab {
	return []Tab{TabDashboard, TabDrivers, TabDevices, TabHomeKit, TabChat}
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
	ctx           context.Context
	cancel        context.CancelFunc
	chat          ChatView
	agent         *agent.Agent
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
	return NewModelWithAgent(provider, nil)
}

// NewModelWithAgent creates a new TUI model with an optional agent
func NewModelWithAgent(provider app.StateProvider, ag *agent.Agent) Model {
	ctx, cancel := context.WithCancel(context.Background())
	theme := DefaultTheme()
	return Model{
		provider:  provider,
		state:     provider.GetState(),
		activeTab: TabDashboard,
		keys:      DefaultKeyMap(),
		theme:     theme,
		ctx:       ctx,
		cancel:    cancel,
		agent:     ag,
		chat:      NewChatView(ag, theme),
	}
}

// NewModelWithRenderer creates a new TUI model with a custom renderer.
// This is needed for SSH sessions where each connection has its own renderer.
func NewModelWithRenderer(provider app.StateProvider, renderer *lipgloss.Renderer) Model {
	return NewModelWithRendererAndAgent(provider, renderer, nil)
}

// NewModelWithRendererAndAgent creates a new TUI model with a custom renderer and optional agent.
func NewModelWithRendererAndAgent(provider app.StateProvider, renderer *lipgloss.Renderer, ag *agent.Agent) Model {
	ctx, cancel := context.WithCancel(context.Background())
	theme := ThemeWithRenderer(renderer)
	return Model{
		provider:  provider,
		state:     provider.GetState(),
		activeTab: TabDashboard,
		keys:      DefaultKeyMap(),
		theme:     theme,
		ctx:       ctx,
		cancel:    cancel,
		agent:     ag,
		chat:      NewChatView(ag, theme),
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
	case tea.KeyMsg:
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

		case key.Matches(msg, m.keys.Enter):
			if m.activeTab == TabDevices {
				return m, m.toggleSelectedDevice()
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

// getMaxItems returns the max navigable items for current tab
func (m Model) getMaxItems() int {
	switch m.activeTab {
	case TabDrivers:
		return len(m.state.Drivers)
	case TabDevices:
		return len(m.state.Devices)
	default:
		return 0
	}
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
	case TabHomeKit:
		b.WriteString(m.renderHomeKit())
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
		prefix := "  "
		style := m.theme.ListItem
		if i == m.cursor {
			prefix = "> "
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

// renderDevices renders the devices view
func (m Model) renderDevices() string {
	if len(m.state.Devices) == 0 {
		return m.theme.Muted.Render("No devices configured")
	}

	var lines []string
	for i, device := range m.state.Devices {
		prefix := "  "
		style := m.theme.ListItem
		if i == m.cursor {
			prefix = "> "
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

	content := strings.Join(lines, "\n")
	return m.theme.Box.Render(content)
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

// renderHelp renders the help bar
func (m Model) renderHelp() string {
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

func formatPin(pin string) string {
	if len(pin) == 8 {
		return pin[:3] + "-" + pin[3:5] + "-" + pin[5:]
	}
	return pin
}
