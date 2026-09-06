package logging

import (
	"bytes"
	"io"
	"sync"

	"github.com/charmbracelet/x/ansi"
)

// DefaultRingSize is the default number of log lines kept in the ring buffer.
const DefaultRingSize = 500

// LogFormat selects how log lines are delivered to subscribers.
type LogFormat int

const (
	// FormatANSI delivers raw bytes including ANSI escape sequences (for TUI).
	FormatANSI LogFormat = iota
	// FormatPlain strips ANSI escape sequences (for web UI / plain text).
	FormatPlain
)

// subscriber holds a channel and its desired format.
type subscriber struct {
	ch     chan []byte
	format LogFormat
}

// Broadcaster is a fan-out io.Writer that writes to a primary writer
// and distributes log lines to dynamic subscribers via channels.
// It maintains a ring buffer of recent log lines for replay on subscribe.
type Broadcaster struct {
	primary io.Writer

	mu          sync.Mutex
	ring        [][]byte
	ringSize    int
	ringPos     int
	ringFull    bool
	subscribers map[*subscriber]struct{}

	// partial accumulates bytes until a newline is seen
	partial bytes.Buffer
}

// NewBroadcaster creates a Broadcaster that always writes to primary
// and keeps up to ringSize recent log lines for subscriber replay.
func NewBroadcaster(primary io.Writer, ringSize int) *Broadcaster {
	if ringSize <= 0 {
		ringSize = DefaultRingSize
	}
	return &Broadcaster{
		primary:     primary,
		ring:        make([][]byte, ringSize),
		ringSize:    ringSize,
		subscribers: make(map[*subscriber]struct{}),
	}
}

// Write implements io.Writer. It writes to the primary writer unconditionally,
// then splits the data on newlines and dispatches complete lines to subscribers.
func (b *Broadcaster) Write(p []byte) (int, error) {
	n, err := b.primary.Write(p)

	// Split on newlines and dispatch complete lines.
	b.mu.Lock()
	b.partial.Write(p[:n])
	for {
		line, readErr := b.partial.ReadBytes('\n')
		if readErr != nil {
			// Incomplete line — put it back for next Write.
			b.partial.Write(line)
			break
		}
		// Store copy in ring buffer (with trailing newline).
		cp := make([]byte, len(line))
		copy(cp, line)
		b.ring[b.ringPos] = cp
		b.ringPos = (b.ringPos + 1) % b.ringSize
		if b.ringPos == 0 {
			b.ringFull = true
		}
		// Dispatch to subscribers.
		b.dispatch(cp)
	}
	b.mu.Unlock()

	return n, err
}

// Subscribe registers a new subscriber that receives log lines in the given format.
// It returns a channel of log lines and an unsubscribe function.
// The subscriber immediately receives any buffered log lines (replay).
func (b *Broadcaster) Subscribe(format LogFormat) (<-chan []byte, func()) {
	sub := &subscriber{
		ch:     make(chan []byte, 256),
		format: format,
	}

	b.mu.Lock()
	// Replay ring buffer.
	b.replayTo(sub)
	b.subscribers[sub] = struct{}{}
	b.mu.Unlock()

	unsub := func() {
		b.mu.Lock()
		delete(b.subscribers, sub)
		b.mu.Unlock()
	}
	return sub.ch, unsub
}

// replayTo sends all buffered lines to sub. Caller must hold b.mu.
func (b *Broadcaster) replayTo(sub *subscriber) {
	start := 0
	count := b.ringPos
	if b.ringFull {
		start = b.ringPos
		count = b.ringSize
	}
	for i := 0; i < count; i++ {
		idx := (start + i) % b.ringSize
		line := b.ring[idx]
		if line == nil {
			continue
		}
		formatted := formatLine(line, sub.format)
		select {
		case sub.ch <- formatted:
		default:
			// Slow subscriber during replay — skip.
		}
	}
}

// dispatch sends a line to all current subscribers. Caller must hold b.mu.
func (b *Broadcaster) dispatch(line []byte) {
	for sub := range b.subscribers {
		formatted := formatLine(line, sub.format)
		select {
		case sub.ch <- formatted:
		default:
			// Non-blocking: slow subscriber drops the line.
		}
	}
}

// formatLine applies the requested format to a raw log line.
func formatLine(line []byte, format LogFormat) []byte {
	if format == FormatPlain {
		return []byte(ansi.Strip(string(line)))
	}
	return line
}
