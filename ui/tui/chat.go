package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/hubertat/swkit/agent"
)

// ChatMessage represents a message in the chat
type ChatMessage struct {
	Role    string // "user", "assistant", or "system"
	Content string
	Time    time.Time
}

// ChatResponseMsg is sent when an agent response arrives
type ChatResponseMsg struct {
	Content string
	Done    bool
	Error   error
}

// ChatView is the chat UI component
type ChatView struct {
	agent      *agent.Agent
	messages   []ChatMessage
	input      textinput.Model
	viewport   viewport.Model
	width      int
	height     int
	ready      bool
	streaming  bool
	currentMsg strings.Builder
	theme      Theme
	focused    bool
}

// NewChatView creates a new chat view
func NewChatView(ag *agent.Agent, theme Theme) ChatView {
	ti := textinput.New()
	ti.Placeholder = "Type a message..."
	ti.CharLimit = 500
	ti.Width = 60

	vp := viewport.New(80, 20)
	vp.SetContent("")

	cv := ChatView{
		agent:    ag,
		messages: make([]ChatMessage, 0),
		input:    ti,
		viewport: vp,
		theme:    theme,
		focused:  false,
	}

	// Add welcome message
	cv.messages = append(cv.messages, ChatMessage{
		Role:    "system",
		Content: "Welcome! Ask me to control devices or get system status.",
		Time:    time.Now(),
	})

	return cv
}

// Focus enables input focus
func (c *ChatView) Focus() {
	c.focused = true
	c.input.Focus()
}

// Blur disables input focus
func (c *ChatView) Blur() {
	c.focused = false
	c.input.Blur()
}

// IsFocused returns whether the input is focused
func (c ChatView) IsFocused() bool {
	return c.focused
}

// SetSize updates the viewport size
func (c *ChatView) SetSize(width, height int) {
	c.width = width
	c.height = height

	// Reserve space for input (3 lines) and some padding
	viewportHeight := height - 5
	if viewportHeight < 5 {
		viewportHeight = 5
	}

	c.viewport.Width = width - 4 // Account for box borders
	c.viewport.Height = viewportHeight
	c.input.Width = width - 6

	c.updateViewportContent()
	c.ready = true
}

// Update handles messages for the chat view
func (c ChatView) Update(msg tea.Msg) (ChatView, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case ChatResponseMsg:
		if msg.Error != nil {
			c.messages = append(c.messages, ChatMessage{
				Role:    "system",
				Content: fmt.Sprintf("Error: %v", msg.Error),
				Time:    time.Now(),
			})
			c.streaming = false
		} else if msg.Done {
			if c.currentMsg.Len() > 0 {
				c.messages = append(c.messages, ChatMessage{
					Role:    "assistant",
					Content: c.currentMsg.String(),
					Time:    time.Now(),
				})
				c.currentMsg.Reset()
			}
			c.streaming = false
		} else {
			c.currentMsg.WriteString(msg.Content)
		}
		c.updateViewportContent()
		c.viewport.GotoBottom()
		return c, nil

	case tea.KeyMsg:
		if !c.focused {
			return c, nil
		}

		switch msg.String() {
		case "enter":
			if c.streaming {
				return c, nil
			}
			text := strings.TrimSpace(c.input.Value())
			if text == "" {
				return c, nil
			}

			// Add user message
			c.messages = append(c.messages, ChatMessage{
				Role:    "user",
				Content: text,
				Time:    time.Now(),
			})
			c.input.SetValue("")
			c.streaming = true
			c.updateViewportContent()
			c.viewport.GotoBottom()

			// Send to agent
			return c, c.sendMessage(text)

		case "ctrl+l":
			// Clear chat history
			c.messages = c.messages[:0]
			c.messages = append(c.messages, ChatMessage{
				Role:    "system",
				Content: "Chat cleared.",
				Time:    time.Now(),
			})
			if c.agent != nil {
				c.agent.Reset()
			}
			c.updateViewportContent()
			return c, nil
		}
	}

	// Update text input
	if c.focused {
		var cmd tea.Cmd
		c.input, cmd = c.input.Update(msg)
		cmds = append(cmds, cmd)
	}

	// Update viewport for scrolling
	var cmd tea.Cmd
	c.viewport, cmd = c.viewport.Update(msg)
	cmds = append(cmds, cmd)

	return c, tea.Batch(cmds...)
}

// sendMessage sends a message to the agent
func (c *ChatView) sendMessage(text string) tea.Cmd {
	return func() tea.Msg {
		if c.agent == nil {
			return ChatResponseMsg{
				Error: fmt.Errorf("agent not configured (ANTHROPIC_API_KEY not set)"),
			}
		}

		ctx := context.Background()
		textCh, errCh := c.agent.Chat(ctx, text)

		// Read response
		var response strings.Builder
		for text := range textCh {
			response.WriteString(text)
		}

		// Check for errors
		if err := <-errCh; err != nil {
			return ChatResponseMsg{Error: err}
		}

		return ChatResponseMsg{
			Content: response.String(),
			Done:    true,
		}
	}
}

// updateViewportContent rebuilds the viewport content from messages
func (c *ChatView) updateViewportContent() {
	var content strings.Builder

	for _, msg := range c.messages {
		var prefix string
		var style lipgloss.Style

		switch msg.Role {
		case "user":
			prefix = "You: "
			style = c.theme.Primary
		case "assistant":
			prefix = "AI: "
			style = c.theme.Secondary
		case "system":
			prefix = ""
			style = c.theme.Muted
		}

		line := style.Render(prefix + msg.Content)
		content.WriteString(line)
		content.WriteString("\n\n")
	}

	// Show streaming content
	if c.streaming && c.currentMsg.Len() > 0 {
		content.WriteString(c.theme.Secondary.Render("AI: " + c.currentMsg.String()))
		content.WriteString(c.theme.Muted.Render("..."))
		content.WriteString("\n")
	} else if c.streaming {
		content.WriteString(c.theme.Muted.Render("Thinking..."))
		content.WriteString("\n")
	}

	c.viewport.SetContent(content.String())
}

// View renders the chat view
func (c ChatView) View() string {
	if !c.ready {
		return "Loading..."
	}

	var b strings.Builder

	// Chat messages viewport
	b.WriteString(c.theme.Box.Width(c.width - 2).Render(c.viewport.View()))
	b.WriteString("\n")

	// Input area
	inputStyle := c.theme.Box.Width(c.width - 2)
	if c.focused {
		inputStyle = inputStyle.BorderForeground(ColorHeader)
	}

	statusText := ""
	if c.streaming {
		statusText = c.theme.Muted.Render(" (processing...)")
	}

	inputContent := c.input.View() + statusText
	b.WriteString(inputStyle.Render(inputContent))

	return b.String()
}

// HasAgent returns true if an agent is configured
func (c ChatView) HasAgent() bool {
	return c.agent != nil
}
