package tui

import (
	"context"
	"fmt"
	"testing"
)

// TestAppendCapped_DiscardsOldestFirst verifies that once the message list
// reaches maxChatMessages, appending further messages drops the oldest
// entries first and never grows past the cap.
func TestAppendCapped_DiscardsOldestFirst(t *testing.T) {
	var messages []ChatMessage

	total := maxChatMessages + 50
	for i := 0; i < total; i++ {
		messages = appendCapped(messages, ChatMessage{
			Role:    "user",
			Content: fmt.Sprintf("message-%d", i),
		})
	}

	if len(messages) != maxChatMessages {
		t.Fatalf("expected len %d, got %d", maxChatMessages, len(messages))
	}

	// The oldest 50 messages (0..49) should have been discarded; the
	// retained window should be message-50 .. message-(total-1), in order.
	wantFirst := fmt.Sprintf("message-%d", total-maxChatMessages)
	wantLast := fmt.Sprintf("message-%d", total-1)

	if got := messages[0].Content; got != wantFirst {
		t.Errorf("expected oldest retained message %q, got %q", wantFirst, got)
	}
	if got := messages[len(messages)-1].Content; got != wantLast {
		t.Errorf("expected newest message %q, got %q", wantLast, got)
	}
}

// TestAppendCapped_NoOpUnderCap verifies appendCapped does not trim
// anything while under the cap.
func TestAppendCapped_NoOpUnderCap(t *testing.T) {
	var messages []ChatMessage
	for i := 0; i < 5; i++ {
		messages = appendCapped(messages, ChatMessage{Role: "user", Content: fmt.Sprintf("m%d", i)})
	}
	if len(messages) != 5 {
		t.Fatalf("expected 5 messages, got %d", len(messages))
	}
}

// TestChatView_RenderCacheReusedWhenWidthUnchanged proves
// updateViewportContent reuses a message's cached rendered block instead of
// re-rendering it when the viewport width hasn't changed. We mutate the
// cached field with a sentinel value directly (white-box, same package) and
// confirm it survives a second render pass at the same width.
func TestChatView_RenderCacheReusedWhenWidthUnchanged(t *testing.T) {
	cv := NewChatView(context.Background(), nil, DefaultTheme())
	cv.messages = appendCapped(cv.messages, ChatMessage{
		Role:    "assistant",
		Content: "hello world",
	})

	cv.SetSize(100, 30)

	idx := len(cv.messages) - 1
	if !cv.messages[idx].renderedValid {
		t.Fatal("expected message render cache to be populated after SetSize")
	}
	widthAfterFirstRender := cv.messages[idx].renderedWidth

	const sentinel = "SENTINEL-CACHED-VALUE"
	cv.messages[idx].rendered = sentinel

	// Re-render at the same width: the cache should be reused verbatim.
	cv.updateViewportContent()

	if cv.messages[idx].rendered != sentinel {
		t.Errorf("expected cached rendered content to be reused unchanged, got %q", cv.messages[idx].rendered)
	}
	if cv.messages[idx].renderedWidth != widthAfterFirstRender {
		t.Errorf("expected renderedWidth to stay %d, got %d", widthAfterFirstRender, cv.messages[idx].renderedWidth)
	}
}

// TestChatView_RenderCacheInvalidatedOnWidthChange proves that resizing the
// chat view (SetSize, which rebuilds the width-dependent markdown renderer)
// invalidates the per-message render cache so the message is re-rendered at
// the new width rather than reusing stale wrapped output.
func TestChatView_RenderCacheInvalidatedOnWidthChange(t *testing.T) {
	cv := NewChatView(context.Background(), nil, DefaultTheme())
	cv.messages = appendCapped(cv.messages, ChatMessage{
		Role:    "assistant",
		Content: "hello world",
	})

	cv.SetSize(100, 30)
	idx := len(cv.messages) - 1
	firstWidth := cv.messages[idx].renderedWidth

	const sentinel = "SENTINEL-CACHED-VALUE"
	cv.messages[idx].rendered = sentinel

	// Resize to a narrower width: the cache must be invalidated and
	// recomputed, not reused.
	cv.SetSize(40, 30)

	if cv.messages[idx].renderedWidth == firstWidth {
		t.Fatalf("expected renderedWidth to change after resize, stayed at %d", firstWidth)
	}
	if cv.messages[idx].rendered == sentinel {
		t.Error("expected render cache to be invalidated and recomputed after width change, sentinel survived")
	}
	if !cv.messages[idx].renderedValid {
		t.Error("expected render cache to be valid (recomputed) after resize")
	}
}
