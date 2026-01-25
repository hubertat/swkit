package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/charmbracelet/log"
	"github.com/hubertat/swkit/app"
)

var (
	ErrNoAPIKey      = errors.New("ANTHROPIC_API_KEY not set")
	ErrNilController = errors.New("controller is nil")
)

// Agent manages conversations with Claude
type Agent struct {
	client     anthropic.Client
	registry   *ToolRegistry
	messages   []anthropic.MessageParam
	config     Config
	controller app.DeviceController
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
		a.messages = append(a.messages, anthropic.MessageParam{
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
			a.messages = append(a.messages, anthropic.MessageParam{
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
			a.messages = append(a.messages, anthropic.MessageParam{
				Role:    anthropic.MessageParamRoleUser,
				Content: toolResults,
			})

			// Continue loop to get next response
		}
	}()

	return textCh, errCh
}

// sendRequest sends a message request to the API
func (a *Agent) sendRequest(ctx context.Context) (*anthropic.Message, error) {
	log.Debug("sending request to API", "model", a.config.Model, "messages", len(a.messages))

	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(a.config.Model),
		MaxTokens: 4096,
		System: []anthropic.TextBlockParam{
			{Text: a.config.SystemPrompt},
		},
		Messages: a.messages,
	}

	// Add tools if registered
	if tools := a.registry.AsToolParams(); len(tools) > 0 {
		params.Tools = tools
	}

	resp, err := a.client.Messages.New(ctx, params)
	if err != nil {
		return nil, err
	}

	log.Debug("received response", "stop_reason", resp.StopReason)
	return resp, nil
}

// executeTools runs tool calls and returns results
func (a *Agent) executeTools(toolUses []anthropic.ToolUseBlock) []anthropic.ContentBlockParamUnion {
	results := make([]anthropic.ContentBlockParamUnion, 0, len(toolUses))

	for _, use := range toolUses {
		log.Debug("executing tool", "name", use.Name, "id", use.ID)

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
			log.Debug("tool execution failed", "name", use.Name, "error", err)
			results = append(results, anthropic.NewToolResultBlock(
				use.ID,
				fmt.Sprintf("error: %v", err),
				true,
			))
			continue
		}

		log.Debug("tool execution succeeded", "name", use.Name)
		results = append(results, anthropic.NewToolResultBlock(use.ID, result, false))
	}

	return results
}

// Reset clears conversation history
func (a *Agent) Reset() {
	a.messages = make([]anthropic.MessageParam, 0)
	log.Debug("conversation history cleared")
}

// MessageCount returns the number of messages in history
func (a *Agent) MessageCount() int {
	return len(a.messages)
}
