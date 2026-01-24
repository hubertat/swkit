package tui

import "github.com/charmbracelet/lipgloss"

// Theme contains all styles for the TUI
type Theme struct {
	// Base styles
	App    lipgloss.Style
	Header lipgloss.Style

	// Tab styles
	TabActive   lipgloss.Style
	TabInactive lipgloss.Style
	TabBar      lipgloss.Style

	// Box styles
	Box      lipgloss.Style
	BoxTitle lipgloss.Style

	// Text styles
	Primary   lipgloss.Style
	Secondary lipgloss.Style
	Success   lipgloss.Style
	Error     lipgloss.Style
	Muted     lipgloss.Style

	// Status styles
	Ready    lipgloss.Style
	NotReady lipgloss.Style
	Healthy  lipgloss.Style
	Faulty   lipgloss.Style
	On       lipgloss.Style
	Off      lipgloss.Style

	// List styles
	ListItem         lipgloss.Style
	ListItemSelected lipgloss.Style

	// Help styles
	Help     lipgloss.Style
	HelpKey  lipgloss.Style
	HelpDesc lipgloss.Style
}

// Colors matching PrintIoStatus in swkit.go:377-399
var (
	ColorPrimary   = lipgloss.Color("39")  // Bright blue - driver names
	ColorSuccess   = lipgloss.Color("42")  // Green - ready/healthy
	ColorHeader    = lipgloss.Color("86")  // Cyan - headers
	ColorError     = lipgloss.Color("196") // Red - not ready/faulty
	ColorBorder    = lipgloss.Color("240") // Gray - borders
	ColorSecondary = lipgloss.Color("245") // Light gray - subtle/secondary
	ColorMuted     = lipgloss.Color("241") // Darker gray
	ColorOn        = lipgloss.Color("220") // Yellow - on state
	ColorOff       = lipgloss.Color("244") // Gray - off state
)

// DefaultTheme creates the default TUI theme
func DefaultTheme() Theme {
	return Theme{
		App: lipgloss.NewStyle().
			Padding(1, 2),

		Header: lipgloss.NewStyle().
			Bold(true).
			Foreground(ColorHeader).
			MarginBottom(1),

		TabActive: lipgloss.NewStyle().
			Bold(true).
			Foreground(ColorHeader).
			Background(lipgloss.Color("236")).
			Padding(0, 2),

		TabInactive: lipgloss.NewStyle().
			Foreground(ColorSecondary).
			Padding(0, 2),

		TabBar: lipgloss.NewStyle().
			MarginBottom(1),

		Box: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(ColorBorder).
			Padding(0, 1),

		BoxTitle: lipgloss.NewStyle().
			Bold(true).
			Foreground(ColorHeader),

		Primary: lipgloss.NewStyle().
			Bold(true).
			Foreground(ColorPrimary),

		Secondary: lipgloss.NewStyle().
			Foreground(ColorSecondary),

		Success: lipgloss.NewStyle().
			Foreground(ColorSuccess).
			Bold(true),

		Error: lipgloss.NewStyle().
			Foreground(ColorError).
			Bold(true),

		Muted: lipgloss.NewStyle().
			Foreground(ColorMuted),

		Ready: lipgloss.NewStyle().
			Foreground(ColorSuccess).
			Bold(true),

		NotReady: lipgloss.NewStyle().
			Foreground(ColorError).
			Bold(true),

		Healthy: lipgloss.NewStyle().
			Foreground(ColorSuccess),

		Faulty: lipgloss.NewStyle().
			Foreground(ColorError),

		On: lipgloss.NewStyle().
			Foreground(ColorOn).
			Bold(true),

		Off: lipgloss.NewStyle().
			Foreground(ColorOff),

		ListItem: lipgloss.NewStyle().
			PaddingLeft(2),

		ListItemSelected: lipgloss.NewStyle().
			PaddingLeft(1).
			Foreground(ColorHeader).
			Bold(true),

		Help: lipgloss.NewStyle().
			Foreground(ColorMuted).
			MarginTop(1),

		HelpKey: lipgloss.NewStyle().
			Foreground(ColorSecondary).
			Bold(true),

		HelpDesc: lipgloss.NewStyle().
			Foreground(ColorMuted),
	}
}

// Icons/emoji matching logging/logger.go
const (
	IconHome       = "🏠"
	IconLight      = "💡"
	IconColorLight = "🌈"
	IconOutlet     = "🔌"
	IconButton     = "👆"
	IconDriver     = "⚡"
	IconHomeKit    = "🍎"
	IconHealthy    = "✓"
	IconFaulty     = "✗"
	IconOn         = "●"
	IconOff        = "○"
)
