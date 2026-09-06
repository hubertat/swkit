package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/hubertat/swkit/logging"
)

// blockingToolInput is unused but kept for symmetry with other tool input
// structs in this package; the blocking tool ignores its input entirely.
type blockingToolInput struct{}

// registerBlockingTool registers a tool whose handler ignores its context
// and blocks until released, or forever if release is nil. started is
// closed once the handler has actually begun running, so tests can
// deterministically wait for it before asserting on timing behavior.
func registerBlockingTool(registry *ToolRegistry, started chan<- struct{}, release <-chan struct{}) {
	registry.Register(Tool{
		Name:        "block_forever",
		Description: "Test tool that blocks until released, ignoring ctx.",
		InputSchema: anthropic.ToolInputSchemaParam{
			Properties: map[string]any{},
		},
		Execute: func(ctx context.Context, input json.RawMessage) (string, error) {
			if started != nil {
				close(started)
			}
			if release == nil {
				select {} // block forever
			}
			<-release
			return "released", nil
		},
	})
}

// fakeAnthropicToolUseServer answers the first /v1/messages request with a
// tool_use block calling toolName, and every subsequent request with a
// fixed end_turn text response. This drives Chat through exactly one
// executeTools call per test.
func fakeAnthropicToolUseServer(t *testing.T, toolName string) *httptest.Server {
	t.Helper()
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if atomic.AddInt32(&calls, 1) == 1 {
			fmt.Fprintf(w, `{
				"id": "msg_tooluse",
				"type": "message",
				"role": "assistant",
				"model": "test-model",
				"content": [{"type": "tool_use", "id": "toolu_1", "name": %q, "input": {}}],
				"stop_reason": "tool_use",
				"stop_sequence": null,
				"usage": {"input_tokens": 1, "output_tokens": 1}
			}`, toolName)
			return
		}
		fmt.Fprintf(w, `{
			"id": "msg_final",
			"type": "message",
			"role": "assistant",
			"model": "test-model",
			"content": [{"type": "text", "text": "done"}],
			"stop_reason": "end_turn",
			"stop_sequence": null,
			"usage": {"input_tokens": 1, "output_tokens": 1}
		}`)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func newToolUseTestAgent(t *testing.T, toolName string, registerTool func(*ToolRegistry), cfg Config) *Agent {
	t.Helper()
	srv := fakeAnthropicToolUseServer(t, toolName)

	client := anthropic.NewClient(
		option.WithBaseURL(srv.URL),
		option.WithAPIKey("test-key"),
	)

	ctrl := newMockController()
	registry := NewToolRegistry()
	registerTool(registry)

	if cfg.RequestTimeout <= 0 {
		cfg = cfg.WithRequestTimeout(DefaultRequestTimeout)
	}

	return &Agent{
		client:     client,
		registry:   registry,
		config:     cfg,
		controller: ctrl,
		logger:     logging.NewLogger(logging.PrefixAgent),
	}
}

func drainChat(t *testing.T, textCh <-chan string, errCh <-chan error) (string, error) {
	t.Helper()
	var text string
	var chatErr error
	for textCh != nil || errCh != nil {
		select {
		case s, ok := <-textCh:
			if !ok {
				textCh = nil
				continue
			}
			text += s
		case err, ok := <-errCh:
			if !ok {
				errCh = nil
				continue
			}
			chatErr = err
		}
	}
	return text, chatErr
}

// TestExecuteTools_BlockedHandlerTimesOut proves that a tool handler which
// blocks forever (ignoring its context, as Go cannot forcibly interrupt a
// running goroutine) is abandoned after Config.ToolTimeout, and that Chat
// still completes normally afterward instead of hanging. A generous test
// deadline (via t.Deadline-independent explicit timeout below) ensures a
// regression here fails the test rather than hanging CI.
func TestExecuteTools_BlockedHandlerTimesOut(t *testing.T) {
	started := make(chan struct{})
	cfg := DefaultConfig().WithToolTimeout(200 * time.Millisecond)

	a := newToolUseTestAgent(t, "block_forever", func(r *ToolRegistry) {
		registerBlockingTool(r, started, nil) // never released
	}, cfg)

	done := make(chan struct{})
	var text string
	var chatErr error
	go func() {
		defer close(done)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		textCh, errCh := a.Chat(ctx, "please block")
		text, chatErr = drainChat(t, textCh, errCh)
	}()

	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("blocking tool handler never started")
	}

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Chat did not complete after tool timeout; agent loop is stuck")
	}

	if chatErr != nil {
		t.Fatalf("expected Chat to complete without error (the tool error is folded into history as a tool_result), got: %v", chatErr)
	}
	if text != "done" {
		t.Errorf("expected final text %q, got %q", "done", text)
	}

	// The abandoned handler goroutine is still blocked forever in this
	// test (release is nil) - that's the documented, unavoidable leak for
	// a handler that ignores ctx. It must not have been able to write
	// anywhere the loop reads after moving on; reaching here without a
	// deadlock or panic demonstrates that.
}

// TestExecuteTools_ContextCancelledMidTool verifies that cancelling the
// context passed to Chat while a tool handler is running causes Chat to
// return promptly (closing its channels) rather than waiting for the
// handler.
func TestExecuteTools_ContextCancelledMidTool(t *testing.T) {
	started := make(chan struct{})
	cfg := DefaultConfig().WithToolTimeout(30 * time.Second) // long enough that only cancellation triggers the return

	a := newToolUseTestAgent(t, "block_forever", func(r *ToolRegistry) {
		registerBlockingTool(r, started, nil) // never released
	}, cfg)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	var chatErr error
	go func() {
		defer close(done)
		textCh, errCh := a.Chat(ctx, "please block")
		_, chatErr = drainChat(t, textCh, errCh)
	}()

	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("blocking tool handler never started")
	}

	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Chat did not return after context cancellation; channels leaked")
	}

	if chatErr == nil {
		t.Error("expected a non-nil error from Chat after cancellation")
	}
}
