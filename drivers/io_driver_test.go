package drivers

import "testing"

func TestGetIoDriverByName(t *testing.T) {
	t.Run("McpIO", func(t *testing.T) {
		mcp := McpIO{}
		got := mcp.String()
		want := "mcpio"

		if got != want {
			t.Errorf("got %s want %s", got, want)
		}
	})

	t.Run("McpIO", func(t *testing.T) {
		gp := GpIO{}
		got := gp.String()
		want := "gpio"

		if got != want {
			t.Errorf("got %s want %s", got, want)
		}
	})
}
