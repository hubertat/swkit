package tui

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hubertat/swkit/logging"
)

// TestLogsViewLazyStartStop covers finding 5: NewLogsView must not subscribe
// on its own; Start/Stop control the subscription lifecycle, and Stop
// followed by Start resubscribes (picking up ring-buffer replay).
func TestLogsViewLazyStartStop(t *testing.T) {
	bc := logging.NewBroadcaster(io.Discard, 16)
	lv := NewLogsView(context.Background(), bc, DefaultTheme())

	if cmd := lv.WaitForLogLine(); cmd != nil {
		t.Error("NewLogsView must not subscribe before Start")
	}

	if cmd := lv.Start(); cmd == nil {
		t.Error("Start should return a wait command")
	}
	if cmd := lv.WaitForLogLine(); cmd == nil {
		t.Error("expected an active subscription after Start")
	}

	lv.Stop()
	if cmd := lv.WaitForLogLine(); cmd != nil {
		t.Error("expected no subscription after Stop")
	}

	if cmd := lv.Start(); cmd == nil {
		t.Error("Start should resubscribe after Stop")
	}
}

// TestLogsViewCoalescesBufferedLines covers finding 5's second half: a burst
// of lines already queued in the channel must be drained in one Update call
// rather than requiring one Update round-trip per line.
func TestLogsViewCoalescesBufferedLines(t *testing.T) {
	bc := logging.NewBroadcaster(io.Discard, 512)
	lv := NewLogsView(context.Background(), bc, DefaultTheme())
	lv.SetSize(80, 24)

	cmd := lv.Start()
	if cmd == nil {
		t.Fatal("Start should return a wait command")
	}

	const burst = 20
	for i := 0; i < burst; i++ {
		if _, err := bc.Write([]byte(fmt.Sprintf("line-%d\n", i))); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}

	msg := cmd()
	lineMsg, ok := msg.(LogLineMsg)
	if !ok {
		t.Fatalf("cmd() = %#v, want LogLineMsg", msg)
	}

	nextCmd := lv.Update(lineMsg)
	if nextCmd == nil {
		t.Error("Update should return a command to wait for the next line")
	}

	if len(lv.lines) != burst {
		t.Errorf("len(lines) = %d, want %d (a single Update should drain the whole buffered burst)", len(lv.lines), burst)
	}
	for i := 0; i < burst; i++ {
		want := fmt.Sprintf("line-%d", i)
		if !strings.Contains(lv.lines[i], want) {
			t.Errorf("lines[%d] = %q, want to contain %q", i, lv.lines[i], want)
		}
	}
}

// TestLogsViewCloseIdempotentAndConcurrent covers finding 2's mutex
// requirement: Close must tolerate repeated and concurrent calls (it races
// the ctx-watcher goroutine and any in-flight Update/tab-switch in real use).
func TestLogsViewCloseIdempotentAndConcurrent(t *testing.T) {
	bc := logging.NewBroadcaster(io.Discard, 16)
	lv := NewLogsView(context.Background(), bc, DefaultTheme())
	lv.Start()

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			lv.Close()
		}()
	}
	wg.Wait()
	lv.Close() // sequential call after the concurrent ones must also be safe

	if cmd := lv.WaitForLogLine(); cmd != nil {
		t.Error("expected no subscription after Close")
	}
}

// TestLogsViewWaitForLogLineRespectsContext covers finding 2: a command left
// in flight when the session tears down must return instead of blocking
// forever, since unsubscribe does not close the channel.
func TestLogsViewWaitForLogLineRespectsContext(t *testing.T) {
	bc := logging.NewBroadcaster(io.Discard, 16)
	ctx, cancel := context.WithCancel(context.Background())
	lv := NewLogsView(ctx, bc, DefaultTheme())

	cmd := lv.Start()
	if cmd == nil {
		t.Fatal("Start should return a wait command")
	}

	cancel()

	done := make(chan struct{})
	go func() {
		cmd() // must return (nil), not block forever, once ctx is cancelled
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("WaitForLogLine command did not return after context cancellation")
	}
}

// TestLogsViewStopReleasesPendingWait covers the residual half of finding 2:
// leaving the Logs tab must release the wait command already in flight, not
// just the subscription. Unsubscribe does not close the channel, so a command
// still parked on it would otherwise survive until the whole session ends,
// pinning that channel and its buffered replay lines — one orphan per visit
// to the Logs tab.
func TestLogsViewStopReleasesPendingWait(t *testing.T) {
	bc := logging.NewBroadcaster(io.Discard, 16)
	lv := NewLogsView(context.Background(), bc, DefaultTheme())

	cmd := lv.Start()
	if cmd == nil {
		t.Fatal("Start should return a wait command")
	}

	done := make(chan struct{})
	go func() {
		cmd()
		close(done)
	}()

	// The command is parked on an empty subscription channel.
	select {
	case <-done:
		t.Fatal("wait command returned before any line arrived or Stop was called")
	case <-time.After(50 * time.Millisecond):
	}

	lv.Stop()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Stop did not release the pending WaitForLogLine command")
	}

	// The session context is still live, so a later visit to the tab works.
	if cmd := lv.Start(); cmd == nil {
		t.Error("Start should resubscribe after Stop")
	}
}
