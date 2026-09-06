package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/hubertat/swkit/logging"
)

// alwaysToolUseServer answers every /v1/messages request with a tool_use
// block for a tool named "noop", never an end_turn text reply. It stands in
// for a model that keeps requesting tools forever, so tests can exercise the
// iteration cap and turn deadline without ever calling the real API. When
// delay is non-zero, each response is held for that long before being
// written, so a test can force wall-clock time to pass without depending on
// an unbounded iteration count.
func alwaysToolUseServer(t *testing.T, delay time.Duration) *httptest.Server {
	t.Helper()
	var n int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if delay > 0 {
			time.Sleep(delay)
		}
		n++
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{
			"id": "msg_%d",
			"type": "message",
			"role": "assistant",
			"model": "test-model",
			"content": [{"type": "tool_use", "id": "tool_%d", "name": "noop", "input": {}}],
			"stop_reason": "tool_use",
			"stop_sequence": null,
			"usage": {"input_tokens": 1, "output_tokens": 1}
		}`, n, n)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// newLoopingTestAgent builds an Agent backed by alwaysToolUseServer, with a
// single registered "noop" tool that always succeeds instantly, and the
// given config (so tests can set small MaxToolIterations/TurnDeadline
// values).
func newLoopingTestAgent(t *testing.T, cfg Config, serverDelay time.Duration) *Agent {
	t.Helper()
	srv := alwaysToolUseServer(t, serverDelay)

	client := anthropic.NewClient(
		option.WithBaseURL(srv.URL),
		option.WithAPIKey("test-key"),
	)

	registry := NewToolRegistry()
	registry.Register(Tool{
		Name:        "noop",
		Description: "does nothing",
		Execute: func(ctx context.Context, input json.RawMessage) (string, error) {
			return "ok", nil
		},
	})

	return &Agent{
		client:     client,
		registry:   registry,
		config:     cfg,
		controller: newMockController(),
		logger:     logging.NewLogger(logging.PrefixAgent),
	}
}

// assertNoDanglingToolUse walks the conversation history and asserts that
// every assistant tool_use block is eventually matched by a tool_result
// block somewhere later in the history, i.e. none is left dangling at the
// end. This is the complement of the existing assertHistoryAPIValid (in
// history_test.go), which checks that every tool_result has a preceding
// tool_use but not the converse. Together they cover the exact invariant the
// Anthropic API enforces: a conversation containing an assistant tool_use
// with no matching tool_result is rejected. Both new bounds (iteration cap,
// turn deadline) must never violate it.
func assertNoDanglingToolUse(t *testing.T, messages []anthropic.MessageParam) {
	t.Helper()

	pending := make(map[string]bool)
	for _, msg := range messages {
		for _, block := range msg.Content {
			if block.OfToolUse != nil {
				pending[block.OfToolUse.ID] = true
			}
			if block.OfToolResult != nil {
				delete(pending, block.OfToolResult.ToolUseID)
			}
		}
	}
	if len(pending) > 0 {
		t.Fatalf("history ends with %d unmatched tool_use block(s); this would be rejected by the API", len(pending))
	}
}

// TestChatStopsAtMaxToolIterations guards finding 3's iteration cap: a model
// that keeps requesting tools forever must not keep a turn "processing"
// indefinitely. Chat must return an error naming the cause, and the history
// it leaves behind must still be valid to send to the API (no dangling
// tool_use).
func TestChatStopsAtMaxToolIterations(t *testing.T) {
	cfg := DefaultConfig().
		WithMaxToolIterations(3).
		WithTurnDeadline(time.Minute) // large, so only the iteration cap can fire
	a := newLoopingTestAgent(t, cfg, 0)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	textCh, errCh := a.Chat(ctx, "keep going forever")
	text, chatErr := drainChat(t, textCh, errCh)

	if text != "" {
		t.Errorf("expected no text reply, got %q", text)
	}
	if chatErr == nil {
		t.Fatal("expected an error when the model keeps requesting tools forever")
	}
	if !strings.Contains(chatErr.Error(), "max tool iterations") {
		t.Errorf("error %q does not name the max-iterations cause", chatErr.Error())
	}

	msgs := a.snapshotMessages()
	assertHistoryAPIValid(t, msgs)
	assertNoDanglingToolUse(t, msgs)
}

// TestChatStopsAtTurnDeadline guards finding 3's turn deadline: a turn must
// not run past a.config.turnDeadline() regardless of how many iterations it
// would otherwise take, and the resulting history must still be API-valid.
func TestChatStopsAtTurnDeadline(t *testing.T) {
	cfg := DefaultConfig().
		WithMaxToolIterations(1_000_000). // effectively unbounded; only the deadline should fire
		WithTurnDeadline(30 * time.Millisecond)
	// Each fake response takes 5ms, so the loop runs several iterations
	// before the 30ms deadline elapses, without depending on a huge
	// iteration count or real time.Sleep-based flakiness.
	a := newLoopingTestAgent(t, cfg, 5*time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	textCh, errCh := a.Chat(ctx, "keep going forever")
	text, chatErr := drainChat(t, textCh, errCh)

	if text != "" {
		t.Errorf("expected no text reply, got %q", text)
	}
	if chatErr == nil {
		t.Fatal("expected an error when the turn deadline elapses")
	}
	if !strings.Contains(chatErr.Error(), "deadline") {
		t.Errorf("error %q does not name the deadline cause", chatErr.Error())
	}

	msgs := a.snapshotMessages()
	assertHistoryAPIValid(t, msgs)
	assertNoDanglingToolUse(t, msgs)
}

// TestChatSessionCancellationWinsOverTurnDeadline guards the requirement
// that cancelling the caller's context is reported as the cause even when
// the turn deadline has also elapsed: the session is cancelled before Chat
// is even called, with a turn deadline so short it is already elapsed too,
// so the very first top-of-loop check in Chat sees both turnCtx.Done() (from
// either cause) and ctx.Err() != nil at once. The precedence rule in that
// check must pick ctx.Err() (context.Canceled), not a deadline error.
func TestChatSessionCancellationWinsOverTurnDeadline(t *testing.T) {
	cfg := DefaultConfig().
		WithMaxToolIterations(1_000_000).
		WithTurnDeadline(time.Nanosecond)
	a := newLoopingTestAgent(t, cfg, 0)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled before Chat starts

	textCh, errCh := a.Chat(ctx, "keep going forever")
	text, chatErr := drainChat(t, textCh, errCh)

	if text != "" {
		t.Errorf("expected no text reply, got %q", text)
	}
	if chatErr == nil {
		t.Fatal("expected an error after cancelling the session")
	}
	if !strings.Contains(chatErr.Error(), "context canceled") {
		t.Errorf("error %q does not report session cancellation", chatErr.Error())
	}
	if strings.Contains(chatErr.Error(), "deadline") {
		t.Errorf("error %q incorrectly reports the turn deadline instead of cancellation", chatErr.Error())
	}

	msgs := a.snapshotMessages()
	assertHistoryAPIValid(t, msgs)
	assertNoDanglingToolUse(t, msgs)
}
