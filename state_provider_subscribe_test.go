package swkit

import (
	"context"
	"testing"
	"time"
)

// TestStateProviderSubscribeClosesOnContextCancel guards against finding 2's
// state-poller leak: an abandoned subscriber must not keep its goroutine
// calling GetState forever once its context is cancelled — the channel must
// close so the reader can detect it.
func TestStateProviderSubscribeClosesOnContextCancel(t *testing.T) {
	sw, _ := newDimmableTestSwKit(t)
	p := NewStateProvider(sw)

	ctx, cancel := context.WithCancel(context.Background())
	ch := p.Subscribe(ctx, 5*time.Millisecond)

	// Consume the guaranteed initial send.
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("did not receive initial state")
	}

	cancel()

	deadline := time.After(time.Second)
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return // channel closed as expected
			}
		case <-deadline:
			t.Fatal("state channel did not close after context cancellation")
		}
	}
}
