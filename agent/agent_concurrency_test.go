package agent

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/hubertat/swkit/logging"
)

// fakeAnthropicServer answers every /v1/messages request with a fixed
// end_turn text response, so Chat can complete without ever calling the
// real API. It never inspects the request body, which is fine for these
// concurrency tests.
func fakeAnthropicServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{
			"id": "msg_test",
			"type": "message",
			"role": "assistant",
			"model": "test-model",
			"content": [{"type": "text", "text": "ok"}],
			"stop_reason": "end_turn",
			"stop_sequence": null,
			"usage": {"input_tokens": 1, "output_tokens": 1}
		}`)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// newTestAgent builds an Agent backed by fakeAnthropicServer, with a real
// controller and registry, ready to exercise Chat/Reset/MessageCount.
func newTestAgent(t *testing.T) *Agent {
	t.Helper()
	srv := fakeAnthropicServer(t)

	client := anthropic.NewClient(
		option.WithBaseURL(srv.URL),
		option.WithAPIKey("test-key"),
	)

	ctrl := newMockController()
	registry := NewToolRegistry()
	registerDeviceTools(registry, ctrl)

	return &Agent{
		client:     client,
		registry:   registry,
		config:     DefaultConfig(),
		controller: ctrl,
		logger:     logging.NewLogger(logging.PrefixAgent),
	}
}

// TestAgent_ConcurrentChatResetMessageCount hammers Chat, Reset and
// MessageCount concurrently on a single Agent to prove the messages lock
// prevents data races. Run with -race.
func TestAgent_ConcurrentChatResetMessageCount(t *testing.T) {
	a := newTestAgent(t)

	var wg sync.WaitGroup
	const workers = 8
	const iterations = 10

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				textCh, errCh := a.Chat(ctx, fmt.Sprintf("hello from %d/%d", id, j))
				for textCh != nil || errCh != nil {
					select {
					case _, ok := <-textCh:
						if !ok {
							textCh = nil
						}
					case err, ok := <-errCh:
						if !ok {
							errCh = nil
						} else if err != nil {
							t.Errorf("unexpected Chat error: %v", err)
						}
					}
				}
				cancel()
			}
		}(i)
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			a.Reset()
			_ = a.MessageCount()
		}
	}()

	wg.Wait()
}

// TestAgent_NewSession verifies that sessions spawned from NewSession have
// independent conversation histories, and that Reset on one leaves others
// untouched.
func TestAgent_NewSession(t *testing.T) {
	base := newTestAgent(t)

	sessionA := base.NewSession()
	sessionB := base.NewSession()

	if sessionA == nil || sessionB == nil {
		t.Fatal("expected non-nil sessions")
	}
	if sessionA == sessionB {
		t.Fatal("expected distinct Agent instances")
	}

	ctx := context.Background()
	drain := func(textCh <-chan string, errCh <-chan error) {
		for textCh != nil || errCh != nil {
			select {
			case _, ok := <-textCh:
				if !ok {
					textCh = nil
				}
			case err, ok := <-errCh:
				if !ok {
					errCh = nil
				} else if err != nil {
					t.Fatalf("unexpected Chat error: %v", err)
				}
			}
		}
	}

	drain(sessionA.Chat(ctx, "hi from A"))
	drain(sessionA.Chat(ctx, "hi again from A"))
	drain(sessionB.Chat(ctx, "hi from B"))

	if got := sessionA.MessageCount(); got != 4 {
		t.Errorf("sessionA: expected 4 messages (2 user + 2 assistant), got %d", got)
	}
	if got := sessionB.MessageCount(); got != 2 {
		t.Errorf("sessionB: expected 2 messages (1 user + 1 assistant), got %d", got)
	}

	sessionB.Reset()
	if got := sessionB.MessageCount(); got != 0 {
		t.Errorf("sessionB: expected 0 messages after Reset, got %d", got)
	}
	if got := sessionA.MessageCount(); got != 4 {
		t.Errorf("sessionA: Reset on sessionB should not affect sessionA, got %d messages", got)
	}
}

// TestAgent_NewSession_NilReceiver verifies the nil-agent path used when no
// API key is configured keeps working.
func TestAgent_NewSession_NilReceiver(t *testing.T) {
	var a *Agent
	if got := a.NewSession(); got != nil {
		t.Errorf("expected nil session from nil Agent, got %v", got)
	}
}
