package tui

import (
	"context"
	"strings"
	"sync"

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
//
// The broadcaster subscription is opened lazily (Start) only while the Logs
// tab is active, and closed (Stop) when the user navigates away, so idle
// sessions parked on another tab neither consume nor render every log line
// the process emits. ch/unsub are guarded by mu because they are touched both
// from the Bubble Tea update goroutine (tab switches, WaitForLogLine) and
// from the ctx-watcher goroutine started in NewModelFromOptions, which calls
// Close on session teardown.
type LogsView struct {
	mu          sync.Mutex
	broadcaster *logging.Broadcaster
	unsub       func()
	ch          <-chan []byte
	// ctx bounds the session; subCtx bounds the current subscription and is
	// cancelled by Close/Stop. Subscriber channels are never closed by unsub
	// (the broadcaster just drops the map entry), so WaitForLogLine must also
	// select on a context — otherwise a command left in flight across a
	// Stop/Close blocks on that channel receive forever. It selects on subCtx
	// rather than ctx so leaving the Logs tab releases the waiting goroutine
	// (and the unsubscribed channel's buffered replay lines) immediately,
	// instead of accumulating one orphan per tab visit until the session ends.
	ctx       context.Context
	subCtx    context.Context
	subCancel context.CancelFunc

	viewport   viewport.Model
	lines      []string
	theme      Theme
	ready      bool
	autoScroll bool
	width      int
	height     int
}

// NewLogsView creates a new logs view. If bc is nil, logs are unavailable.
// It does not subscribe to the broadcaster; call Start when the Logs tab
// becomes active. ctx bounds the view's lifetime (a nil ctx behaves as
// context.Background, i.e. WaitForLogLine only returns when a line arrives).
func NewLogsView(ctx context.Context, bc *logging.Broadcaster, theme Theme) *LogsView {
	if ctx == nil {
		ctx = context.Background()
	}
	vp := viewport.New(80, 20)
	vp.SetContent("")

	return &LogsView{
		viewport:    vp,
		lines:       make([]string, 0, maxLogLines),
		broadcaster: bc,
		theme:       theme,
		autoScroll:  true,
		ctx:         ctx,
	}
}

// Start subscribes to the broadcaster if not already subscribed, and returns
// a command that waits for the next log line. Safe to call repeatedly (e.g.
// every time the Logs tab is entered): if already subscribed it just returns
// a fresh wait command. The broadcaster replays its ring buffer to new
// subscribers, so re-entering the tab after Stop still shows recent history.
func (lv *LogsView) Start() tea.Cmd {
	lv.mu.Lock()
	if lv.broadcaster != nil && lv.ch == nil {
		ch, unsub := lv.broadcaster.Subscribe(logging.FormatANSI)
		lv.ch = ch
		lv.unsub = unsub
		lv.subCtx, lv.subCancel = context.WithCancel(lv.ctx)
	}
	lv.mu.Unlock()
	return lv.WaitForLogLine()
}

// Stop unsubscribes from the broadcaster. It is equivalent to Close, named
// for the Start/Stop pairing used when the Logs tab loses focus; a later
// Start resubscribes (with ring-buffer replay).
func (lv *LogsView) Stop() {
	lv.Close()
}

// WaitForLogLine returns a tea.Cmd that blocks until a log line arrives, or
// nil if there is no active subscription.
func (lv *LogsView) WaitForLogLine() tea.Cmd {
	lv.mu.Lock()
	ch := lv.ch
	ctx := lv.subCtx
	lv.mu.Unlock()
	if ch == nil || ctx == nil {
		return nil
	}
	return func() tea.Msg {
		select {
		case line, ok := <-ch:
			if !ok {
				return nil
			}
			return LogLineMsg{Line: line}
		case <-ctx.Done():
			return nil
		}
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

// Update handles a log line message, draining any additional lines already
// buffered in the channel (non-blocking, bounded to the ring size) so a burst
// of log activity costs one viewport rebuild and one repaint instead of one
// per line. Returns a command to wait for the next line.
func (lv *LogsView) Update(msg LogLineMsg) tea.Cmd {
	lv.mu.Lock()
	ch := lv.ch
	lv.mu.Unlock()

	lv.appendLine(msg.Line)
	for i := 0; i < logging.DefaultRingSize && ch != nil; i++ {
		select {
		case line, ok := <-ch:
			if !ok {
				ch = nil
				break
			}
			lv.appendLine(line)
		default:
			ch = nil
		}
	}

	lv.updateContent()
	if lv.autoScroll {
		lv.viewport.GotoBottom()
	}
	return lv.WaitForLogLine()
}

// appendLine trims the trailing newline and appends a line to the buffer,
// trimming the buffer to maxLogLines. It does not refresh the viewport
// content — callers batch that after appending one or more lines.
func (lv *LogsView) appendLine(raw []byte) {
	line := strings.TrimRight(string(raw), "\n")
	lv.lines = append(lv.lines, line)
	if len(lv.lines) > maxLogLines {
		lv.lines = lv.lines[len(lv.lines)-maxLogLines:]
	}
}

// View renders the logs view.
func (lv *LogsView) View() string {
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

// Close unsubscribes from the broadcaster. Safe to call repeatedly and
// concurrently (e.g. from both an explicit quit and the ctx-watcher
// goroutine racing to tear the session down).
func (lv *LogsView) Close() {
	lv.mu.Lock()
	unsub := lv.unsub
	cancel := lv.subCancel
	lv.unsub = nil
	lv.subCancel = nil
	lv.subCtx = nil
	lv.ch = nil
	lv.mu.Unlock()
	if unsub != nil {
		unsub()
	}
	// Release any WaitForLogLine command still parked on the now-unsubscribed
	// channel, which would otherwise never receive again.
	if cancel != nil {
		cancel()
	}
}

func (lv *LogsView) updateContent() {
	lv.viewport.SetContent(strings.Join(lv.lines, "\n"))
}
