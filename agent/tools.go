package agent

import (
	"context"
	"encoding/json"

	"github.com/anthropics/anthropic-sdk-go"
)

// Tool defines a callable tool for the agent.
//
// Execute receives the per-call context so a handler can check for
// cancellation before doing hardware work. The agent loop also bounds each
// call with a timeout (see Config.ToolTimeout / executeTools) and abandons
// handlers that ignore ctx and run past it — see the comment on
// Agent.runToolWithTimeout for what that guarantees and what it does not.
type Tool struct {
	Name        string
	Description string
	InputSchema anthropic.ToolInputSchemaParam
	Execute     func(ctx context.Context, input json.RawMessage) (string, error)
}

// ToolRegistry manages available tools
type ToolRegistry struct {
	tools map[string]Tool
}

// NewToolRegistry creates an empty tool registry
func NewToolRegistry() *ToolRegistry {
	return &ToolRegistry{
		tools: make(map[string]Tool),
	}
}

// Register adds a tool to the registry
func (r *ToolRegistry) Register(tool Tool) {
	r.tools[tool.Name] = tool
}

// Get retrieves a tool by name
func (r *ToolRegistry) Get(name string) (Tool, bool) {
	tool, ok := r.tools[name]
	return tool, ok
}

// List returns all tool names
func (r *ToolRegistry) List() []string {
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	return names
}

// AsToolParams converts tools to Anthropic API format
func (r *ToolRegistry) AsToolParams() []anthropic.ToolUnionParam {
	params := make([]anthropic.ToolUnionParam, 0, len(r.tools))
	for _, tool := range r.tools {
		params = append(params, anthropic.ToolUnionParam{
			OfTool: &anthropic.ToolParam{
				Name:        tool.Name,
				Description: anthropic.String(tool.Description),
				InputSchema: tool.InputSchema,
			},
		})
	}
	return params
}
