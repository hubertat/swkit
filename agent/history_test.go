package agent

import (
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
)

// userText builds a plain user turn-start message.
func userText(text string) anthropic.MessageParam {
	return anthropic.MessageParam{
		Role: anthropic.MessageParamRoleUser,
		Content: []anthropic.ContentBlockParamUnion{
			anthropic.NewTextBlock(text),
		},
	}
}

// assistantText builds a plain assistant text message.
func assistantText(text string) anthropic.MessageParam {
	return anthropic.MessageParam{
		Role: anthropic.MessageParamRoleAssistant,
		Content: []anthropic.ContentBlockParamUnion{
			anthropic.NewTextBlock(text),
		},
	}
}

// assistantToolUse builds an assistant message containing a single tool_use
// block.
func assistantToolUse(id, name string) anthropic.MessageParam {
	return anthropic.MessageParam{
		Role: anthropic.MessageParamRoleAssistant,
		Content: []anthropic.ContentBlockParamUnion{
			anthropic.NewToolUseBlock(id, map[string]any{}, name),
		},
	}
}

// userToolResult builds a user-role message carrying a tool_result
// continuation (not a turn start).
func userToolResult(toolUseID, result string) anthropic.MessageParam {
	return anthropic.MessageParam{
		Role: anthropic.MessageParamRoleUser,
		Content: []anthropic.ContentBlockParamUnion{
			anthropic.NewToolResultBlock(toolUseID, result, false),
		},
	}
}

// buildConversation returns a conversation of n turns, each turn being:
// user text -> assistant tool_use -> user tool_result -> assistant text.
// This exercises the trap the finding calls out: naively slicing the
// oldest N messages off such a conversation can easily split a
// tool_use/tool_result pair or leave a tool_result at the front.
func buildConversation(turns int) []anthropic.MessageParam {
	var msgs []anthropic.MessageParam
	for i := 0; i < turns; i++ {
		id := "toolu_" + string(rune('a'+i))
		msgs = append(msgs,
			userText("question"),
			assistantToolUse(id, "get_device_state"),
			userToolResult(id, "result"),
			assistantText("answer"),
		)
	}
	return msgs
}

// assertHistoryAPIValid checks the two invariants the Anthropic API
// enforces: the conversation must not start with a tool_result, and every
// tool_result must have a preceding tool_use with a matching ID somewhere
// earlier in the retained history.
func assertHistoryAPIValid(t *testing.T, messages []anthropic.MessageParam) {
	t.Helper()
	if len(messages) == 0 {
		return
	}

	if !isTurnStart(messages[0]) {
		t.Fatalf("retained history must start with a plain user message, got role=%s content=%+v", messages[0].Role, messages[0].Content)
	}

	seenToolUse := make(map[string]bool)
	for i, msg := range messages {
		for _, block := range msg.Content {
			if block.OfToolUse != nil {
				seenToolUse[block.OfToolUse.ID] = true
			}
			if block.OfToolResult != nil {
				if !seenToolUse[block.OfToolResult.ToolUseID] {
					t.Fatalf("message %d: orphaned tool_result for tool_use_id %q with no preceding tool_use in retained history", i, block.OfToolResult.ToolUseID)
				}
			}
		}
	}
}

func TestTrimHistoryLocked_KeepsWholeTurns(t *testing.T) {
	a := &Agent{
		config: DefaultConfig().WithMaxHistoryMessages(6), // less than one turn's worth of history to force trimming across many turns
	}

	// 5 turns * 4 messages = 20 messages, cap is 6.
	a.messages = buildConversation(5)

	a.messagesMu.Lock()
	a.trimHistoryLocked()
	a.messagesMu.Unlock()

	if len(a.messages) == 0 {
		t.Fatal("expected some history to remain")
	}
	if len(a.messages)%4 != 0 {
		t.Errorf("expected a whole number of 4-message turns to remain, got %d messages", len(a.messages))
	}

	assertHistoryAPIValid(t, a.messages)

	// The most recent turn's content should be present.
	last := a.messages[len(a.messages)-1]
	if last.Role != anthropic.MessageParamRoleAssistant {
		t.Errorf("expected last retained message to be the final assistant answer, got role=%s", last.Role)
	}
}

func TestTrimHistoryLocked_AlwaysKeepsAtLeastOneTurn(t *testing.T) {
	a := &Agent{
		config: DefaultConfig().WithMaxHistoryMessages(1), // cap smaller than even a single turn
	}
	a.messages = buildConversation(3)

	a.messagesMu.Lock()
	a.trimHistoryLocked()
	a.messagesMu.Unlock()

	// Even though the cap (1) is smaller than one turn (4 messages), we
	// must not cut a turn in half - the last whole turn is kept regardless.
	if len(a.messages) != 4 {
		t.Fatalf("expected the single most recent turn (4 messages) to be kept despite the cap, got %d", len(a.messages))
	}
	assertHistoryAPIValid(t, a.messages)
}

func TestTrimHistoryLocked_NoOpUnderCap(t *testing.T) {
	a := &Agent{
		config: DefaultConfig().WithMaxHistoryMessages(1000),
	}
	original := buildConversation(2)
	a.messages = append([]anthropic.MessageParam(nil), original...)

	a.messagesMu.Lock()
	a.trimHistoryLocked()
	a.messagesMu.Unlock()

	if len(a.messages) != len(original) {
		t.Errorf("expected no trimming under cap, got %d messages (want %d)", len(a.messages), len(original))
	}
}

func TestAppendMessage_TrimsAcrossManyTurns(t *testing.T) {
	a := &Agent{
		config: DefaultConfig().WithMaxHistoryMessages(10),
	}

	for i := 0; i < 20; i++ {
		a.appendMessage(userText("q"))
		a.appendMessage(assistantToolUse("toolu_x", "get_device_state"))
		a.appendMessage(userToolResult("toolu_x", "r"))
		a.appendMessage(assistantText("a"))
	}

	if got := a.MessageCount(); got > 10+3 { // allow up to just under 2 turns depending on boundary alignment
		t.Errorf("expected history capped near 10 messages, got %d", got)
	}

	assertHistoryAPIValid(t, a.messages)
}

func TestNewSession_InheritsHistoryCap(t *testing.T) {
	base := &Agent{
		config: DefaultConfig().WithMaxHistoryMessages(4),
	}
	session := base.NewSession()

	if session.config.MaxHistoryMessages != 4 {
		t.Fatalf("expected session to inherit MaxHistoryMessages=4, got %d", session.config.MaxHistoryMessages)
	}

	session.appendMessage(userText("q1"))
	session.appendMessage(assistantText("a1"))
	session.appendMessage(userText("q2"))
	session.appendMessage(assistantText("a2"))
	session.appendMessage(userText("q3"))
	session.appendMessage(assistantText("a3"))

	// Cap is 4; turns here are 2 messages each (no tool calls), so we
	// expect trimming down to the most recent 2 turns (4 messages).
	if got := session.MessageCount(); got != 4 {
		t.Errorf("expected 4 messages retained after cap, got %d", got)
	}
	assertHistoryAPIValid(t, session.messages)
}
