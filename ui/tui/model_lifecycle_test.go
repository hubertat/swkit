package tui

import (
	"context"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/hubertat/swkit/app"
	"github.com/hubertat/swkit/logging"
)

// fakeStateProvider is a minimal app.StateProvider whose Subscribe mirrors
// the real contract (documented on app.StateProvider.Subscribe): it delivers
// an initial state, then periodic updates, and closes its channel once ctx
// is cancelled.
type fakeStateProvider struct {
	state app.AppState
}

func (f *fakeStateProvider) GetState() app.AppState { return f.state }

func (f *fakeStateProvider) Subscribe(ctx context.Context, interval time.Duration) <-chan app.AppState {
	ch := make(chan app.AppState, 1)
	go func() {
		defer close(ch)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		select {
		case ch <- f.state:
		case <-ctx.Done():
			return
		}
		for {
			select {
			case <-ticker.C:
				select {
				case ch <- f.state:
				default:
				}
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch
}

// waitUntil polls cond every 5ms until it is true or the deadline passes,
// failing the test on timeout.
func waitUntil(t *testing.T, deadline time.Duration, cond func() bool, msg string) {
	t.Helper()
	end := time.Now().Add(deadline)
	for time.Now().Before(end) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !cond() {
		t.Fatal(msg)
	}
}

// TestModelCloseCancelsContextAndClosesLogSubscription covers finding 2: once
// Close runs (standing in for the ctx-watcher goroutine reacting to session
// teardown), both the log subscription and the state subscription must be
// torn down.
func TestModelCloseCancelsContextAndClosesLogSubscription(t *testing.T) {
	bc := logging.NewBroadcaster(io.Discard, 16)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	m := NewModelFromOptions(Options{Provider: &fakeStateProvider{}, Broadcaster: bc, Ctx: ctx})

	// Simulate having viewed the Logs tab, which subscribes lazily.
	if cmd := m.logs.Start(); cmd == nil {
		t.Fatal("Start should return a wait command once subscribed")
	}

	m.Close()

	waitUntil(t, time.Second, func() bool {
		return m.logs.WaitForLogLine() == nil
	}, "log subscription was not released after Close")

	waitUntil(t, time.Second, func() bool {
		select {
		case _, ok := <-m.stateCh:
			return !ok
		default:
			return false
		}
	}, "state channel did not close after Close")
}

// TestModelCloseIdempotentAndConcurrent covers finding 2's requirement that
// Close be safe to call repeatedly and concurrently (it races the
// ctx-watcher goroutine in real use). Run with -race.
func TestModelCloseIdempotentAndConcurrent(t *testing.T) {
	bc := logging.NewBroadcaster(io.Discard, 16)
	m := NewModelFromOptions(Options{Provider: &fakeStateProvider{}, Broadcaster: bc})
	m.logs.Start()

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m.Close()
		}()
	}
	wg.Wait()

	if m.logs.WaitForLogLine() != nil {
		t.Error("expected log subscription released after concurrent Close calls")
	}
}

// TestModelLogsTabLazySubscription covers finding 5: no log subscription
// should exist until the Logs tab is actually viewed, and leaving it should
// release the subscription again.
func TestModelLogsTabLazySubscription(t *testing.T) {
	bc := logging.NewBroadcaster(io.Discard, 16)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	m := NewModelFromOptions(Options{Provider: &fakeStateProvider{}, Broadcaster: bc, Ctx: ctx})
	defer m.Close()

	if cmd := m.Init(); cmd == nil {
		t.Fatal("Init should return at least the state wait command")
	}
	if m.logs.WaitForLogLine() != nil {
		t.Error("logs should not be subscribed before the Logs tab is viewed")
	}

	if cmd := m.setActiveTab(TabLogs); cmd == nil {
		t.Fatal("entering the Logs tab should start the subscription")
	}
	if m.logs.WaitForLogLine() == nil {
		t.Error("logs should be subscribed once the Logs tab is active")
	}

	m.setActiveTab(TabDashboard)
	if m.logs.WaitForLogLine() != nil {
		t.Error("logs should be unsubscribed after leaving the Logs tab")
	}
}
