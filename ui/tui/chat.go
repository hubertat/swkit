package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/hubertat/swkit/agent"
)

// maxChatMessages caps how many messages ChatView keeps in scrollback.
// Beyond this, the oldest messages are discarded first. A few hundred is
// plenty for a terminal session and keeps updateViewportContent's
// per-render work bounded.
const maxChatMessages = 300

// ChatMessage represents a message in the chat
type ChatMessage struct {
	Role    string // "user", "assistant", or "system"
	Content string
	Time    time.Time

	// rendered caches this message's fully-formatted block (role prefix,
	// markdown-rendered body, trailing spacing) as produced by
	// renderMessageBlock, so updateViewportContent need not re-render
	// unchanged history on every update. renderedWidth records the
	// viewport width the cache was built for; renderMessageBlock output is
	// width-dependent (word wrap), so a width change invalidates it.
	rendered      string
	renderedWidth int
	renderedValid bool
}

// appendCapped appends msg to messages, discarding the oldest entries first
// so the list never grows past maxChatMessages.
func appendCapped(messages []ChatMessage, msg ChatMessage) []ChatMessage {
	messages = append(messages, msg)
	if len(messages) > maxChatMessages {
		messages = messages[len(messages)-maxChatMessages:]
	}
	return messages
}

// ChatResponseMsg is sent when an agent response arrives
type ChatResponseMsg struct {
	Content string
	Done    bool
	Error   error
}

// ChatView is the chat UI component
type ChatView struct {
	// ctx bounds outgoing agent.Chat requests to the model's lifetime, so an
	// SSH disconnect (or any other session teardown) cancels an in-flight
	// request instead of leaving it, and the command goroutine reading its
	// channels, stuck until the request finishes on its own.
	ctx        context.Context
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
	mdRenderer *glamour.TermRenderer
}

// NewChatView creates a new chat view. ctx bounds the lifetime of requests
// sent to the agent (a nil ctx behaves as context.Background).
func NewChatView(ctx context.Context, ag *agent.Agent, theme Theme) ChatView {
	if ctx == nil {
		ctx = context.Background()
	}
	ti := textinput.New()
	ti.Placeholder = "Type a message..."
	ti.CharLimit = 500
	ti.Width = 60

	vp := viewport.New(80, 20)
	vp.SetContent("")

	// Create markdown renderer with dark style and word wrapping
	mdRenderer, _ := glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(76),
	)

	cv := ChatView{
		ctx:        ctx,
		agent:      ag,
		messages:   make([]ChatMessage, 0),
		input:      ti,
		viewport:   vp,
		theme:      theme,
		focused:    false,
		mdRenderer: mdRenderer,
	}

	// Add welcome message
	cv.messages = append(cv.messages, ChatMessage{
		Role:    "system",
		Content: "Welcome! Ask me to control devices or get system status.",
		Time:    time.Now(),
	})

	// Show warning if agent is not configured
	if ag == nil {
		cv.messages = append(cv.messages, ChatMessage{
			Role:    "system",
			Content: "Agent not configured (ANTHROPIC_API_KEY not set). Chat is disabled.",
			Time:    time.Now(),
		})
	}

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

	// Recreate markdown renderer with updated width
	mdWidth := width - 8 // Account for borders and padding
	if mdWidth < 40 {
		mdWidth = 40
	}
	c.mdRenderer, _ = glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(mdWidth),
	)

	c.updateViewportContent()
	c.ready = true
}

// Update handles messages for the chat view
func (c ChatView) Update(msg tea.Msg) (ChatView, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case ChatResponseMsg:
		if msg.Error != nil {
			c.messages = appendCapped(c.messages, ChatMessage{
				Role:    "system",
				Content: fmt.Sprintf("Error: %v", msg.Error),
				Time:    time.Now(),
			})
			c.streaming = false
		} else if msg.Done {
			// Use msg.Content if provided, otherwise use accumulated currentMsg
			responseContent := msg.Content
			if responseContent == "" && c.currentMsg.Len() > 0 {
				responseContent = c.currentMsg.String()
			}
			if responseContent != "" {
				c.messages = appendCapped(c.messages, ChatMessage{
					Role:    "assistant",
					Content: responseContent,
					Time:    time.Now(),
				})
			}
			c.currentMsg.Reset()
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
			c.messages = appendCapped(c.messages, ChatMessage{
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
			c.messages = appendCapped(c.messages, ChatMessage{
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
	ctx := c.ctx
	agentRef := c.agent
	return func() tea.Msg {
		if agentRef == nil {
			return ChatResponseMsg{
				Error: fmt.Errorf("agent not configured (ANTHROPIC_API_KEY not set)"),
			}
		}

		textCh, errCh := agentRef.Chat(ctx, text)

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

	// Calculate max width for text wrapping (account for prefix and some padding)
	maxWidth := c.viewport.Width - 2
	if maxWidth < 20 {
		maxWidth = 20
	}

	// Reuse each message's cached rendered block instead of re-running
	// glamour over the entire history on every update. The cache is keyed
	// by the width it was rendered at, so a width change (via SetSize)
	// transparently invalidates and re-renders just that message.
	for i := range c.messages {
		msg := &c.messages[i]
		if !msg.renderedValid || msg.renderedWidth != maxWidth {
			msg.rendered = c.renderMessageBlock(msg.Role, msg.Content, maxWidth)
			msg.renderedWidth = maxWidth
			msg.renderedValid = true
		}
		content.WriteString(msg.rendered)
	}

	// Show streaming content
	if c.streaming && c.currentMsg.Len() > 0 {
		content.WriteString(c.theme.Secondary.Render("AI:"))
		content.WriteString("\n")
		if c.mdRenderer != nil {
			rendered, err := c.mdRenderer.Render(c.currentMsg.String())
			if err == nil {
				content.WriteString(strings.TrimSpace(rendered))
			} else {
				wrappedStyle := c.theme.Secondary.Width(maxWidth)
				content.WriteString(wrappedStyle.Render(c.currentMsg.String()))
			}
		} else {
			wrappedStyle := c.theme.Secondary.Width(maxWidth)
			content.WriteString(wrappedStyle.Render(c.currentMsg.String()))
		}
		content.WriteString(c.theme.Muted.Render("..."))
		content.WriteString("\n")
	} else if c.streaming {
		content.WriteString(c.theme.Muted.Render("Thinking..."))
		content.WriteString("\n")
	}

	c.viewport.SetContent(content.String())
}

// renderMessageBlock renders a single message's full display block (role
// prefix, markdown-rendered body for assistant messages, trailing blank
// line) at the given width. It is pure with respect to c.theme/c.mdRenderer
// and maxWidth, which is what makes the per-message cache in
// updateViewportContent safe.
func (c *ChatView) renderMessageBlock(role, text string, maxWidth int) string {
	var b strings.Builder

	switch role {
	case "user":
		wrappedStyle := c.theme.Primary.Width(maxWidth)
		b.WriteString(wrappedStyle.Render("You: " + text))
		b.WriteString("\n\n")

	case "assistant":
		// Render markdown for assistant messages
		b.WriteString(c.theme.Secondary.Render("AI:"))
		b.WriteString("\n")
		if c.mdRenderer != nil {
			rendered, err := c.mdRenderer.Render(text)
			if err == nil {
				// Trim extra newlines that glamour adds
				b.WriteString(strings.TrimSpace(rendered))
			} else {
				// Fallback to plain text if rendering fails
				wrappedStyle := c.theme.Secondary.Width(maxWidth)
				b.WriteString(wrappedStyle.Render(text))
			}
		} else {
			wrappedStyle := c.theme.Secondary.Width(maxWidth)
			b.WriteString(wrappedStyle.Render(text))
		}
		b.WriteString("\n\n")

	case "system":
		wrappedStyle := c.theme.Muted.Width(maxWidth)
		b.WriteString(wrappedStyle.Render(text))
		b.WriteString("\n\n")
	}

	return b.String()
}

// View renders the chat view
func (c ChatView) View() string {
	if !c.ready {
		return "Loading..."
	}

	var b strings.Builder

	// Chat messages viewport with scrollbar
	viewportContent := c.viewport.View()
	scrollbar := c.renderScrollbar()

	// Combine viewport box and scrollbar horizontally
	chatBox := c.theme.Box.Width(c.width - 4).Render(viewportContent)
	chatWithScrollbar := lipgloss.JoinHorizontal(lipgloss.Top, chatBox, scrollbar)
	b.WriteString(chatWithScrollbar)
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

// renderScrollbar creates a visual scrollbar indicator
func (c ChatView) renderScrollbar() string {
	totalLines := c.viewport.TotalLineCount()
	visibleLines := c.viewport.Height
	scrollPos := c.viewport.YOffset

	// No scrollbar needed if content fits
	if totalLines <= visibleLines {
		// Return empty space to maintain alignment
		var sb strings.Builder
		for i := 0; i < visibleLines+2; i++ { // +2 for box borders
			sb.WriteString(" \n")
		}
		return sb.String()
	}

	// Calculate scrollbar dimensions (account for box borders)
	barHeight := visibleLines
	if barHeight < 3 {
		barHeight = 3
	}

	// Calculate thumb size (minimum 1 line)
	thumbSize := (visibleLines * barHeight) / totalLines
	if thumbSize < 1 {
		thumbSize = 1
	}

	// Calculate thumb position
	scrollRange := totalLines - visibleLines
	thumbRange := barHeight - thumbSize
	thumbPos := 0
	if scrollRange > 0 {
		thumbPos = (scrollPos * thumbRange) / scrollRange
	}

	// Build scrollbar with top/bottom spacing for box borders
	var sb strings.Builder
	sb.WriteString(" \n") // Align with box border top

	for i := 0; i < barHeight; i++ {
		if i >= thumbPos && i < thumbPos+thumbSize {
			sb.WriteString(c.theme.Primary.Render("┃"))
		} else {
			sb.WriteString(c.theme.Muted.Render("│"))
		}
		sb.WriteString("\n")
	}

	sb.WriteString(" ") // Align with box border bottom

	return sb.String()
}

// HasAgent returns true if an agent is configured
func (c ChatView) HasAgent() bool {
	return c.agent != nil
}
