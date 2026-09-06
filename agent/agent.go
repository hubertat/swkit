package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/charmbracelet/log"
	"github.com/hubertat/swkit/app"
	"github.com/hubertat/swkit/logging"
)

var (
	ErrNoAPIKey      = errors.New("ANTHROPIC_API_KEY not set")
	ErrNilController = errors.New("controller is nil")
)

// Agent manages conversations with Claude
type Agent struct {
	client     anthropic.Client
	registry   *ToolRegistry
	config     Config
	controller app.DeviceController
	logger     *log.Logger

	messagesMu sync.Mutex
	messages   []anthropic.MessageParam
}

// NewAgent creates a new agent instance
func NewAgent(cfg Config, controller app.DeviceController) (*Agent, error) {
	if !cfg.Valid() {
		return nil, ErrNoAPIKey
	}
	if controller == nil {
		return nil, ErrNilController
	}

	client := anthropic.NewClient()

	agent := &Agent{
		client:     client,
		registry:   NewToolRegistry(),
		messages:   make([]anthropic.MessageParam, 0),
		config:     cfg,
		controller: controller,
		logger:     logging.NewLogger(logging.PrefixAgent),
	}

	// Register tools
	registerDeviceTools(agent.registry, controller)
	registerInfoTools(agent.registry, controller)

	return agent, nil
}

// Chat sends a message and returns the response
// The response channel streams text chunks as they arrive
func (a *Agent) Chat(ctx context.Context, userMessage string) (<-chan string, <-chan error) {
	textCh := make(chan string, 100)
	errCh := make(chan error, 1)

	go func() {
		defer close(textCh)
		defer close(errCh)

		// Add user message to history
		a.appendMessage(anthropic.MessageParam{
			Role: anthropic.MessageParamRoleUser,
			Content: []anthropic.ContentBlockParamUnion{
				anthropic.NewTextBlock(userMessage),
			},
		})

		// turnCtx bounds the whole turn (this call to a final text reply) to
		// a.config.turnDeadline(), derived from the caller's ctx so that
		// both session cancellation and the turn deadline surface as
		// turnCtx.Done(), and so the deadline also bounds the requests and
		// tool calls made inside the turn (they are called with turnCtx,
		// not ctx, below).
		turnCtx, cancel := context.WithTimeout(ctx, a.config.turnDeadline())
		defer cancel()

		// Run agent loop until we get a final text response, a bound is
		// hit, or an error occurs.
		iterations := 0
		for {
			// Both new bounds (like the pre-existing ctx.Done() check they
			// replace) are enforced here, at the top of the loop, and
			// nowhere else. At this point in the loop the history always
			// ends either on a plain user message or on a complete
			// assistant tool_use plus its tool_result blocks (appended
			// together, at the bottom of the previous iteration) - never on
			// an assistant tool_use alone. That means it is always valid to
			// send as-is, so returning here can never leave the
			// API-rejecting gap of a tool_use with no matching tool_result.
			// The other early return below (sendRequest error) shares that
			// same property: it fires before the assistant message for
			// this iteration is appended. Nothing between an appendMessage
			// of an assistant message and the matching appendMessage of its
			// tool results may return early.
			select {
			case <-turnCtx.Done():
				if ctx.Err() != nil {
					// Session cancellation takes precedence in the
					// reported cause even if the turn deadline expired at
					// the same moment.
					errCh <- ctx.Err()
				} else {
					errCh <- fmt.Errorf("agent turn exceeded deadline of %s", a.config.turnDeadline())
				}
				return
			default:
			}

			iterations++
			if iterations > a.config.maxToolIterations() {
				errCh <- fmt.Errorf("agent turn exceeded max tool iterations (%d): assistant kept requesting tools", a.config.maxToolIterations())
				return
			}

			resp, err := a.sendRequest(turnCtx)
			if err != nil {
				errCh <- fmt.Errorf("API error: %w", err)
				return
			}

			// Process response content blocks
			var assistantContent []anthropic.ContentBlockParamUnion
			var toolUses []anthropic.ToolUseBlock
			var textContent string

			for _, block := range resp.Content {
				switch b := block.AsAny().(type) {
				case anthropic.TextBlock:
					textContent += b.Text
					assistantContent = append(assistantContent, anthropic.NewTextBlock(b.Text))
				case anthropic.ToolUseBlock:
					toolUses = append(toolUses, b)
					assistantContent = append(assistantContent, anthropic.NewToolUseBlock(b.ID, b.Input, b.Name))
				}
			}

			// Add assistant response to history
			a.appendMessage(anthropic.MessageParam{
				Role:    anthropic.MessageParamRoleAssistant,
				Content: assistantContent,
			})

			// If no tool calls, we're done - send the text
			if len(toolUses) == 0 {
				if textContent != "" {
					textCh <- textContent
				}
				return
			}

			// Execute tool calls
			toolResults := a.executeTools(turnCtx, toolUses)

			// Add tool results to history
			a.appendMessage(anthropic.MessageParam{
				Role:    anthropic.MessageParamRoleUser,
				Content: toolResults,
			})

			// Continue loop to get next response
		}
	}()

	return textCh, errCh
}

// appendMessage appends a message to the conversation history under lock,
// then trims the oldest whole turns if the history has grown past the
// configured cap.
func (a *Agent) appendMessage(msg anthropic.MessageParam) {
	a.messagesMu.Lock()
	defer a.messagesMu.Unlock()
	a.messages = append(a.messages, msg)
	a.trimHistoryLocked()
}

// isTurnStart reports whether msg begins a new conversation turn: a user
// message carrying plain (non tool_result) content. A tool_result is also
// sent with Role user, as a continuation of the turn that issued the
// matching tool_use, so it must not be mistaken for a turn boundary.
func isTurnStart(msg anthropic.MessageParam) bool {
	if msg.Role != anthropic.MessageParamRoleUser {
		return false
	}
	for _, block := range msg.Content {
		if block.OfToolResult != nil {
			return false
		}
	}
	return true
}

// trimHistoryLocked drops the oldest whole conversation turns until the
// history is at or under a.config.maxHistoryMessages(). Callers must hold
// messagesMu.
//
// The Anthropic API rejects a conversation whose first message is a
// tool_result, or one that contains a tool_result with no preceding
// tool_use. Slicing off the oldest N messages could easily produce exactly
// that (e.g. cutting between a tool_use and its tool_result), so instead we
// trim in whole turns: a turn starts at a user text message (see
// isTurnStart) and runs through every following message up to, but not
// including, the next such message. Dropping only whole turns keeps every
// tool_use paired with its tool_result and keeps the retained history
// starting on a plain user message.
//
// At least one turn (the most recent) is always kept, even if it alone
// exceeds the cap - there is nothing shorter to fall back to.
func (a *Agent) trimHistoryLocked() {
	max := a.config.maxHistoryMessages()
	if len(a.messages) <= max {
		return
	}

	var turnStarts []int
	for i, msg := range a.messages {
		if isTurnStart(msg) {
			turnStarts = append(turnStarts, i)
		}
	}
	if len(turnStarts) <= 1 {
		return
	}

	// Find the earliest turn boundary whose suffix already fits the cap;
	// dropping everything before it removes as many oldest turns as
	// possible while keeping as much recent history as the cap allows.
	keepFrom := turnStarts[len(turnStarts)-1] // always keep at least the last turn
	for i := 0; i < len(turnStarts)-1; i++ {
		boundary := turnStarts[i+1]
		if len(a.messages)-boundary <= max {
			keepFrom = boundary
			break
		}
	}

	if keepFrom == 0 {
		return
	}

	trimmed := make([]anthropic.MessageParam, len(a.messages)-keepFrom)
	copy(trimmed, a.messages[keepFrom:])
	a.messages = trimmed
}

// snapshotMessages returns a copy of the current conversation history so
// callers can send it to the API without racing further appends.
func (a *Agent) snapshotMessages() []anthropic.MessageParam {
	a.messagesMu.Lock()
	defer a.messagesMu.Unlock()
	snapshot := make([]anthropic.MessageParam, len(a.messages))
	copy(snapshot, a.messages)
	return snapshot
}

// sendRequest sends a message request to the API
func (a *Agent) sendRequest(ctx context.Context) (*anthropic.Message, error) {
	messages := a.snapshotMessages()

	a.logger.Debug("sending request to API", "model", a.config.Model, "messages", len(messages))

	reqCtx, cancel := context.WithTimeout(ctx, a.config.requestTimeout())
	defer cancel()

	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(a.config.Model),
		MaxTokens: 4096,
		System: []anthropic.TextBlockParam{
			{Text: a.config.SystemPrompt},
		},
		Messages: messages,
	}

	// Add tools if registered
	if tools := a.registry.AsToolParams(); len(tools) > 0 {
		params.Tools = tools
	}

	resp, err := a.client.Messages.New(reqCtx, params)
	if err != nil {
		return nil, err
	}

	a.logger.Debug("received response", "stop_reason", resp.StopReason)
	return resp, nil
}

// executeTools runs tool calls and returns results. It checks ctx before
// each call and bounds every handler with a.config.toolTimeout(), so a
// blocked handler cannot pin the agent loop (or the chat goroutine reading
// its channels) forever.
func (a *Agent) executeTools(ctx context.Context, toolUses []anthropic.ToolUseBlock) []anthropic.ContentBlockParamUnion {
	results := make([]anthropic.ContentBlockParamUnion, 0, len(toolUses))

	for _, use := range toolUses {
		if err := ctx.Err(); err != nil {
			results = append(results, anthropic.NewToolResultBlock(
				use.ID,
				fmt.Sprintf("error: cancelled before executing tool %q: %v", use.Name, err),
				true,
			))
			continue
		}

		a.logger.Debug("executing tool", "name", use.Name, "id", use.ID)

		tool, ok := a.registry.Get(use.Name)
		if !ok {
			results = append(results, anthropic.NewToolResultBlock(
				use.ID,
				fmt.Sprintf("error: unknown tool %q", use.Name),
				true,
			))
			continue
		}

		// Convert input to JSON
		inputJSON, err := json.Marshal(use.Input)
		if err != nil {
			results = append(results, anthropic.NewToolResultBlock(
				use.ID,
				fmt.Sprintf("error: failed to parse input: %v", err),
				true,
			))
			continue
		}

		// Execute tool, bounded by the per-tool timeout.
		result, err := a.runToolWithTimeout(ctx, tool, use.Name, inputJSON)
		if err != nil {
			a.logger.Debug("tool execution failed", "name", use.Name, "error", err)
			results = append(results, anthropic.NewToolResultBlock(
				use.ID,
				fmt.Sprintf("error: %v", err),
				true,
			))
			continue
		}

		a.logger.Debug("tool execution succeeded", "name", use.Name)
		results = append(results, anthropic.NewToolResultBlock(use.ID, result, false))
	}

	return results
}

// toolExecResult carries a tool handler's outcome back from the goroutine
// it runs in.
type toolExecResult struct {
	result string
	err    error
}

// runToolWithTimeout runs tool.Execute in its own goroutine, bounded by
// a.config.toolTimeout() and by ctx. If the handler doesn't return before
// the bound expires, the call is abandoned: this function returns a timeout
// error immediately so the agent loop, Chat, and the session all recover.
//
// Be honest about what this does and does not do: Go has no way to forcibly
// stop a running goroutine. A handler that ignores its context keeps
// running - and keeps its own goroutine alive - until it eventually returns
// on its own, however long that takes; that goroutine is simply leaked from
// this call's point of view. What this function guarantees is that nothing
// downstream ever blocks on it: resCh is buffered (size 1), so the
// abandoned goroutine can still deliver its eventual result and exit
// cleanly instead of leaking a blocked send, and no one is left reading
// from resCh by the time it does.
func (a *Agent) runToolWithTimeout(ctx context.Context, tool Tool, name string, input json.RawMessage) (string, error) {
	toolCtx, cancel := context.WithTimeout(ctx, a.config.toolTimeout())
	defer cancel()

	resCh := make(chan toolExecResult, 1)
	go func() {
		result, err := tool.Execute(toolCtx, input)
		resCh <- toolExecResult{result: result, err: err}
	}()

	select {
	case res := <-resCh:
		return res.result, res.err
	case <-toolCtx.Done():
		a.logger.Debug("tool execution timed out or cancelled", "name", name, "error", toolCtx.Err())
		return "", fmt.Errorf("tool %q timed out: %w", name, toolCtx.Err())
	}
}

// Reset clears conversation history
func (a *Agent) Reset() {
	a.messagesMu.Lock()
	a.messages = make([]anthropic.MessageParam, 0)
	a.messagesMu.Unlock()
	a.logger.Debug("conversation history cleared")
}

// MessageCount returns the number of messages in history
func (a *Agent) MessageCount() int {
	a.messagesMu.Lock()
	defer a.messagesMu.Unlock()
	return len(a.messages)
}

// NewSession returns a new Agent that shares the immutable/shared parts of a
// (the API client, tool registry, config, controller and logger) but has its
// own independent conversation history and its own lock. This lets each SSH
// or local TUI session hold its own conversation without racing others.
//
// A nil receiver returns nil, preserving the "no agent configured" path used
// when no API key is set.
func (a *Agent) NewSession() *Agent {
	if a == nil {
		return nil
	}
	return &Agent{
		client:     a.client,
		registry:   a.registry,
		config:     a.config,
		controller: a.controller,
		logger:     a.logger,
		messages:   make([]anthropic.MessageParam, 0),
	}
}
