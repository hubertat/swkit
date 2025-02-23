package arduino

import (
	"net/netip"
	"time"
)

type RPixel struct {
	address       netip.Addr
	lastRefreshed time.Time

	red        uint8
	green      uint8
	blue       uint8
	white      uint8
	brightness uint8
}

func NewRPixel(address netip.Addr) *RPixel {
	return &RPixel{
		address: address,
	}
}

func (rp *RPixel) SetColor(red, green, blue, white uint8) {
	rp.red = red
	rp.green = green
	rp.blue = blue
	rp.white = white
}

// SetColorRGB sets the color of the pixel using RGB values
func (rp *RPixel) SetColorRGB(red, green, blue uint8) {
	rp.SetColor(red, green, blue, 0)
}

// SetColorWhite sets the color of the pixel using white value
func (rp *RPixel) SetColorWhite(white uint8) {
	rp.SetColor(0, 0, 0, white)
}

// SetBrightness sets the brightness of the pixel
func (rp *RPixel) SetBrightness(brightness uint8) {
	rp.brightness = brightness
}

// GetSettingPacket() Packet return a packet with the current settings
func (rp *RPixel) GetSettingPacket(mode byte) Packet {
	dataBytes := []byte{rp.red, rp.green, rp.blue, rp.white, mode, rp.brightness}
	return NewPacket(PACKET_TYPE_RPIXEL_SET, dataBytes)
}
