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
)

// Config holds agent configuration
type Config struct {
	APIKey         string        // from env ANTHROPIC_API_KEY
	Model          string        // default: claude-sonnet-4-5-20250514
	SystemPrompt   string        // optional custom prompt
	RequestTimeout time.Duration // default: 120s, applied per API request
}

// DefaultConfig returns configuration with defaults
func DefaultConfig() Config {
	return Config{
		APIKey:         os.Getenv("ANTHROPIC_API_KEY"),
		Model:          DefaultModel,
		SystemPrompt:   DefaultSystemPrompt,
		RequestTimeout: DefaultRequestTimeout,
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
