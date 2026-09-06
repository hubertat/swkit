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

		// Run agent loop until we get a final text response
		for {
			select {
			case <-ctx.Done():
				errCh <- ctx.Err()
				return
			default:
			}

			resp, err := a.sendRequest(ctx)
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
			toolResults := a.executeTools(toolUses)

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

// appendMessage appends a message to the conversation history under lock.
func (a *Agent) appendMessage(msg anthropic.MessageParam) {
	a.messagesMu.Lock()
	defer a.messagesMu.Unlock()
	a.messages = append(a.messages, msg)
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

// executeTools runs tool calls and returns results
func (a *Agent) executeTools(toolUses []anthropic.ToolUseBlock) []anthropic.ContentBlockParamUnion {
	results := make([]anthropic.ContentBlockParamUnion, 0, len(toolUses))

	for _, use := range toolUses {
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

		// Execute tool
		result, err := tool.Execute(inputJSON)
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
