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
	Event    lipgloss.Style // brief flash on input event

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
	ColorEvent     = lipgloss.Color("208") // Orange - event flash
)

// DefaultTheme creates the default TUI theme using the default renderer
func DefaultTheme() Theme {
	return ThemeWithRenderer(lipgloss.DefaultRenderer())
}

// ThemeWithRenderer creates a TUI theme using the specified renderer.
// This is needed for SSH sessions where each connection needs its own renderer.
func ThemeWithRenderer(r *lipgloss.Renderer) Theme {
	return Theme{
		App: r.NewStyle().
			Padding(1, 2),

		Header: r.NewStyle().
			Bold(true).
			Foreground(ColorHeader).
			MarginBottom(1),

		TabActive: r.NewStyle().
			Bold(true).
			Foreground(ColorHeader).
			Background(lipgloss.Color("236")).
			Padding(0, 2),

		TabInactive: r.NewStyle().
			Foreground(ColorSecondary).
			Padding(0, 2),

		TabBar: r.NewStyle().
			MarginBottom(1),

		Box: r.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(ColorBorder).
			Padding(0, 1),

		BoxTitle: r.NewStyle().
			Bold(true).
			Foreground(ColorHeader),

		Primary: r.NewStyle().
			Bold(true).
			Foreground(ColorPrimary),

		Secondary: r.NewStyle().
			Foreground(ColorSecondary),

		Success: r.NewStyle().
			Foreground(ColorSuccess).
			Bold(true),

		Error: r.NewStyle().
			Foreground(ColorError).
			Bold(true),

		Muted: r.NewStyle().
			Foreground(ColorMuted),

		Ready: r.NewStyle().
			Foreground(ColorSuccess).
			Bold(true),

		NotReady: r.NewStyle().
			Foreground(ColorError).
			Bold(true),

		Healthy: r.NewStyle().
			Foreground(ColorSuccess),

		Faulty: r.NewStyle().
			Foreground(ColorError),

		On: r.NewStyle().
			Foreground(ColorOn).
			Bold(true),

		Off: r.NewStyle().
			Foreground(ColorOff),

		Event: r.NewStyle().
			Foreground(ColorEvent).
			Bold(true),

		ListItem: r.NewStyle().
			PaddingLeft(0),

		ListItemSelected: r.NewStyle().
			PaddingLeft(0).
			Foreground(lipgloss.AdaptiveColor{Light: "19", Dark: "86"}).
			Bold(true),

		Help: r.NewStyle().
			Foreground(ColorMuted).
			MarginTop(1),

		HelpKey: r.NewStyle().
			Foreground(ColorSecondary).
			Bold(true),

		HelpDesc: r.NewStyle().
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
	IconChat       = "💬"
	IconIoDebug    = "🔍"
	IconHealthy    = "✓"
	IconFaulty     = "✗"
	IconOn         = "●"
	IconOff        = "○"
)
