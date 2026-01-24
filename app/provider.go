package app

import (
	"context"
	"time"
)

// StateProvider provides application state for UIs
type StateProvider interface {
	// GetState returns the current application state snapshot
	GetState() AppState

	// Subscribe returns a channel that receives state updates
	// The channel is closed when the context is cancelled
	Subscribe(ctx context.Context, interval time.Duration) <-chan AppState
}
