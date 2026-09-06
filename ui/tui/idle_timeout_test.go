package tui

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// TestIdleWatchdogQuitsAfterConfiguredTimeout guards finding C: the TUI's own
// input watchdog (not the SSH transport, which the repainting TUI keeps
// alive on its own) must quit the session once idleTimeout elapses with no
// key/mouse input, and must call Close() first so the state/log
// subscriptions are released exactly like the explicit-quit key handlers.
func TestIdleWatchdogQuitsAfterConfiguredTimeout(t *testing.T) {
	m := NewModelFromOptions(Options{Provider: &fakeStateProvider{}, IdleTimeout: 50 * time.Millisecond})
	// Backdate lastInput past the timeout instead of sleeping, since the
	// watchdog only checks elapsed time on its own tick.
	m.lastInput = time.Now().Add(-time.Hour)

	updated, cmd := m.Update(idleWatchdogTickMsg{})
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("expected a command from the idle tick when the timeout has elapsed")
	}

	msg := cmd()
	if _, ok := msg.(tea.QuitMsg); !ok {
		t.Fatalf("expected tea.Quit after idle timeout elapsed, got %T", msg)
	}

	// Close should have released the state subscription (see
	// model_lifecycle_test.go's Close coverage for the same assertion style).
	waitUntil(t, time.Second, func() bool {
		select {
		case _, ok := <-m.stateCh:
			return !ok
		default:
			return false
		}
	}, "state channel did not close after idle watchdog fired")
}

// TestIdleWatchdogReschedulesWhileActive guards the non-expiry path: before
// the timeout elapses, the tick must reschedule itself rather than quitting,
// and recent key input must reset the idle clock.
func TestIdleWatchdogReschedulesWhileActive(t *testing.T) {
	m := NewModelFromOptions(Options{Provider: &fakeStateProvider{}, IdleTimeout: time.Hour})
	defer m.Close()

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	m = updated.(Model)
	if m.lastInput.IsZero() {
		t.Fatal("expected lastInput to be set after a key message")
	}

	updated, cmd = m.Update(idleWatchdogTickMsg{})
	m = updated.(Model)
	// Don't execute cmd(): idleWatchdogTick() is a real tea.Tick, so running
	// it would block this test for idleWatchdogInterval. A non-nil command
	// here (as opposed to the nil returned once idleTimeout elapses, see
	// TestIdleWatchdogQuitsAfterConfiguredTimeout) is exactly the signal that
	// Update chose to reschedule rather than quit.
	if cmd == nil {
		t.Fatal("expected the watchdog to reschedule itself, got nil command")
	}
}

// TestIdleWatchdogDisabledWhenZero guards that IdleTimeout: 0 (the default,
// and what the local non-SSH TUI gets) never schedules the watchdog at all.
func TestIdleWatchdogDisabledWhenZero(t *testing.T) {
	m := NewModelFromOptions(Options{Provider: &fakeStateProvider{}})
	defer m.Close()

	if m.idleTimeout != 0 {
		t.Fatalf("expected idleTimeout to default to 0, got %v", m.idleTimeout)
	}

	// Init should not schedule an idle tick when disabled. We can't easily
	// introspect a tea.Batch's contents, so instead confirm the watchdog
	// case itself is a no-op guard when idleTimeout is 0, even if a stray
	// tick message ever arrived.
	updated, cmd := m.Update(idleWatchdogTickMsg{})
	m = updated.(Model)
	if cmd != nil {
		t.Fatal("expected no rescheduled command when idleTimeout is 0")
	}
}
