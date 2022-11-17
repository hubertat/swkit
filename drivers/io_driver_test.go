package drivers

import "testing"

func TestGetUniqueId(t *testing.T) {

	t.Run("McpIO", func(t *testing.T) {
		mcp := McpIO{BusNo: 3, DevNo: 5}
		got := mcp.GetUniqueId(0xa1)
		want := uint64(0x02000000000305a1)

		if got != want {
			t.Errorf("got %x want %x", got, want)
		}
	})

	t.Run("GpIO", func(t *testing.T) {

		gpio := GpIO{}
		got := gpio.GetUniqueId(0xa1)
		want := uint64(0x01000000000000a1)

		if got != want {
			t.Errorf("got %x want %x", got, want)
		}
	})
}

func TestGetIoDriverByName(t *testing.T) {
	t.Run("McpIO", func(t *testing.T) {
		mcp := McpIO{}
		got := mcp.NameId()
		want := "mcpio"

		if got != want {
			t.Errorf("got %s want %s", got, want)
		}
	})

	t.Run("McpIO", func(t *testing.T) {
		gp := GpIO{}
		got := gp.NameId()
		want := "gpio"

		if got != want {
			t.Errorf("got %s want %s", got, want)
		}
	})
}
