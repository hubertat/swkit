package agent

import (
	"os"
	"time"
)

const (
	DefaultModel = "claude-sonnet-4-20250514"

	DefaultSystemPrompt = `You are a home automation assistant for swkit. You can:
- Control lights and outlets (turn on/off, toggle)
- Check device states and system health
- List available devices and drivers

Be concise in responses. When controlling devices, confirm the action taken.
If a device name is ambiguous, list matching devices and ask for clarification.`

	// DefaultRequestTimeout bounds a single API request so a hung request
	// cannot pin a session forever.
	DefaultRequestTimeout = 120 * time.Second

	// DefaultToolTimeout bounds a single tool handler invocation. A blocked
	// device-control or get_state call is abandoned after this long so it
	// cannot stall the agent loop; see Agent.runToolWithTimeout.
	DefaultToolTimeout = 30 * time.Second

	// DefaultMaxHistoryMessages caps the number of messages kept in a
	// session's conversation history. Trimming happens in whole
	// conversation turns (see Agent.trimHistoryLocked) so the retained
	// history always stays valid to send to the API.
	DefaultMaxHistoryMessages = 200

	// DefaultMaxToolIterations caps how many times a single Chat turn may
	// send a request and get back a tool_use before it is aborted. A
	// home-automation assistant's real tool-use chains are short (look up a
	// device, call one control tool), so 25 comfortably covers legitimate
	// use while still bounding a model that keeps requesting tools forever.
	DefaultMaxToolIterations = 25

	// DefaultTurnDeadline bounds the total wall-clock time a single Chat
	// turn (from the user message to a final text reply) may take. It must
	// stay comfortably larger than DefaultRequestTimeout, or it would just
	// pre-empt individual requests instead of bounding the whole turn.
	DefaultTurnDeadline = 5 * time.Minute
)

// Config holds agent configuration
type Config struct {
	APIKey             string        // from env ANTHROPIC_API_KEY
	Model              string        // default: claude-sonnet-4-5-20250514
	SystemPrompt       string        // optional custom prompt
	RequestTimeout     time.Duration // default: 120s, applied per API request
	ToolTimeout        time.Duration // default: 30s, applied per tool call
	MaxHistoryMessages int           // default: 200, conversation history cap
	MaxToolIterations  int           // default: 25, cap on tool-use round trips within one turn
	TurnDeadline       time.Duration // default: 5m, total wall-clock cap on one turn
}

// DefaultConfig returns configuration with defaults
func DefaultConfig() Config {
	return Config{
		APIKey:             os.Getenv("ANTHROPIC_API_KEY"),
		Model:              DefaultModel,
		SystemPrompt:       DefaultSystemPrompt,
		RequestTimeout:     DefaultRequestTimeout,
		ToolTimeout:        DefaultToolTimeout,
		MaxHistoryMessages: DefaultMaxHistoryMessages,
		MaxToolIterations:  DefaultMaxToolIterations,
		TurnDeadline:       DefaultTurnDeadline,
	}
}

// WithRequestTimeout sets the per-request timeout.
func (c Config) WithRequestTimeout(d time.Duration) Config {
	if d > 0 {
		c.RequestTimeout = d
	}
	return c
}

// requestTimeout returns the effective request timeout, applying the
// default when unset.
func (c Config) requestTimeout() time.Duration {
	if c.RequestTimeout <= 0 {
		return DefaultRequestTimeout
	}
	return c.RequestTimeout
}

// WithToolTimeout sets the per-tool-call timeout.
func (c Config) WithToolTimeout(d time.Duration) Config {
	if d > 0 {
		c.ToolTimeout = d
	}
	return c
}

// toolTimeout returns the effective tool timeout, applying the default
// when unset.
func (c Config) toolTimeout() time.Duration {
	if c.ToolTimeout <= 0 {
		return DefaultToolTimeout
	}
	return c.ToolTimeout
}

// WithMaxHistoryMessages sets the conversation history cap.
func (c Config) WithMaxHistoryMessages(n int) Config {
	if n > 0 {
		c.MaxHistoryMessages = n
	}
	return c
}

// maxHistoryMessages returns the effective history cap, applying the
// default when unset.
func (c Config) maxHistoryMessages() int {
	if c.MaxHistoryMessages <= 0 {
		return DefaultMaxHistoryMessages
	}
	return c.MaxHistoryMessages
}

// WithMaxToolIterations sets the cap on tool-use round trips within one turn.
func (c Config) WithMaxToolIterations(n int) Config {
	if n > 0 {
		c.MaxToolIterations = n
	}
	return c
}

// maxToolIterations returns the effective tool-iteration cap, applying the
// default when unset.
func (c Config) maxToolIterations() int {
	if c.MaxToolIterations <= 0 {
		return DefaultMaxToolIterations
	}
	return c.MaxToolIterations
}

// WithTurnDeadline sets the total wall-clock cap on one turn.
func (c Config) WithTurnDeadline(d time.Duration) Config {
	if d > 0 {
		c.TurnDeadline = d
	}
	return c
}

// turnDeadline returns the effective turn deadline, applying the default
// when unset.
func (c Config) turnDeadline() time.Duration {
	if c.TurnDeadline <= 0 {
		return DefaultTurnDeadline
	}
	return c.TurnDeadline
}

// WithAPIKey sets the API key
func (c Config) WithAPIKey(key string) Config {
	c.APIKey = key
	return c
}

// WithModel sets the model
func (c Config) WithModel(model string) Config {
	if model != "" {
		c.Model = model
	}
	return c
}

// WithSystemPrompt sets a custom system prompt
func (c Config) WithSystemPrompt(prompt string) Config {
	if prompt != "" {
		c.SystemPrompt = prompt
	}
	return c
}

// Valid returns true if the configuration has required fields
func (c Config) Valid() bool {
	return c.APIKey != ""
}
