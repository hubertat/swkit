package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/hubertat/swkit/logging"
)

const maxLogLines = 500

// LogLineMsg is sent when a new log line arrives from the broadcaster.
type LogLineMsg struct {
	Line []byte
}

// LogsView displays live log output in a scrollable viewport.
type LogsView struct {
	viewport    viewport.Model
	lines       []string
	broadcaster *logging.Broadcaster
	unsub       func()
	ch          <-chan []byte
	theme       Theme
	ready       bool
	autoScroll  bool
	width       int
	height      int
}

// NewLogsView creates a new logs view. If bc is nil, logs are unavailable.
func NewLogsView(bc *logging.Broadcaster, theme Theme) *LogsView {
	vp := viewport.New(80, 20)
	vp.SetContent("")

	lv := &LogsView{
		viewport:    vp,
		lines:       make([]string, 0, maxLogLines),
		broadcaster: bc,
		theme:       theme,
		autoScroll:  true,
	}

	if bc != nil {
		ch, unsub := bc.Subscribe(logging.FormatANSI)
		lv.ch = ch
		lv.unsub = unsub
	}

	return lv
}

// WaitForLogLine returns a tea.Cmd that blocks until a log line arrives.
func (lv *LogsView) WaitForLogLine() tea.Cmd {
	if lv.ch == nil {
		return nil
	}
	ch := lv.ch
	return func() tea.Msg {
		line, ok := <-ch
		if !ok {
			return nil
		}
		return LogLineMsg{Line: line}
	}
}

// SetSize updates the viewport dimensions.
func (lv *LogsView) SetSize(width, height int) {
	lv.width = width
	lv.height = height

	viewportHeight := height - 3
	if viewportHeight < 5 {
		viewportHeight = 5
	}

	lv.viewport.Width = width - 4
	lv.viewport.Height = viewportHeight
	lv.updateContent()
	lv.ready = true
}

// ScrollUp scrolls the viewport up by n lines and disables auto-scroll.
func (lv *LogsView) ScrollUp(n int) {
	lv.viewport.LineUp(n)
	lv.autoScroll = false
}

// ScrollDown scrolls the viewport down by n lines. Re-enables auto-scroll when at bottom.
func (lv *LogsView) ScrollDown(n int) {
	lv.viewport.LineDown(n)
	if lv.viewport.AtBottom() {
		lv.autoScroll = true
	}
}

// ToggleAutoScroll flips the auto-scroll flag and jumps to bottom when enabling.
func (lv *LogsView) ToggleAutoScroll() {
	lv.autoScroll = !lv.autoScroll
	if lv.autoScroll {
		lv.viewport.GotoBottom()
	}
}

// ClearLines discards all buffered log lines.
func (lv *LogsView) ClearLines() {
	lv.lines = lv.lines[:0]
	lv.updateContent()
}

// Update handles a log line message and returns a command to wait for the next one.
func (lv *LogsView) Update(msg LogLineMsg) tea.Cmd {
	// Trim trailing newline for display.
	line := strings.TrimRight(string(msg.Line), "\n")
	lv.lines = append(lv.lines, line)
	if len(lv.lines) > maxLogLines {
		lv.lines = lv.lines[len(lv.lines)-maxLogLines:]
	}
	lv.updateContent()
	if lv.autoScroll {
		lv.viewport.GotoBottom()
	}
	return lv.WaitForLogLine()
}

// View renders the logs view.
func (lv LogsView) View() string {
	if lv.broadcaster == nil {
		return lv.theme.Muted.Render("Log broadcasting not available")
	}
	if !lv.ready {
		return "Loading logs..."
	}

	content := lv.viewport.View()
	box := lv.theme.Box.Render(content)

	status := lv.theme.Muted.Render("  Lines: " + itoa(len(lv.lines)) + "/" + itoa(maxLogLines))
	if lv.autoScroll {
		status += lv.theme.Success.Render("  auto-scroll")
	}

	return box + "\n" + status
}

// Close unsubscribes from the broadcaster.
func (lv *LogsView) Close() {
	if lv.unsub != nil {
		lv.unsub()
		lv.unsub = nil
	}
}

func (lv *LogsView) updateContent() {
	lv.viewport.SetContent(strings.Join(lv.lines, "\n"))
}
