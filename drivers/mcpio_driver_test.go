package drivers

import "testing"

func newTestMcpIOWithPushers(pins ...uint8) *McpIO {
	mcp := &McpIO{}
	for _, pin := range pins {
		mcp.addPusher(pin)
	}
	return mcp
}

func TestMcpIO_GetPushEventEmitter_ReturnsDetectorForConfiguredPin(t *testing.T) {
	mcp := newTestMcpIOWithPushers(3, 7)

	emitter, err := mcp.GetPushEventEmitter("7")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if emitter.String() != "7" {
		t.Errorf("expected emitter for pin 7, got %q", emitter.String())
	}
}

func TestMcpIO_GetPushEventEmitter_UnknownPinFails(t *testing.T) {
	mcp := newTestMcpIOWithPushers(3)

	if _, err := mcp.GetPushEventEmitter("4"); err == nil {
		t.Error("expected error for pin without push event emitter")
	}
}

func TestMcpIO_GetPushEventEmitter_InvalidIdFails(t *testing.T) {
	mcp := newTestMcpIOWithPushers(3)

	for _, id := range []string{"a3", "256", "-1"} {
		if _, err := mcp.GetPushEventEmitter(id); err == nil {
			t.Errorf("expected error for invalid id %q", id)
		}
	}
}

func TestMcpIO_AddPusher_ExposesPinAsInput(t *testing.T) {
	mcp := newTestMcpIOWithPushers(5)

	if _, err := mcp.GetDigitalInput("5"); err != nil {
		t.Errorf("expected push event pin to be readable as digital input: %v", err)
	}
}
