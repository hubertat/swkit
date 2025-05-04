package swkit

// rgbToHsv converts color from rgb notation to hsv (hue, saturation, value/brightness)
// r, g, b are 0 to 0xFF range
func rgbToHsv(r, g, b uint8) (hue float64, saturation float64, value int) {
	// TODO
	return
}

// hsvToRgb converts color from hsv (hue, saturation value/brightness) to rgb notation
// hue and brightness are expressed in 0-360°, value is 0-100
func hsvToRgb(hue, brightness float64, value int) (r, g, b uint8) {
	// TODO
	return
}

// rgbToRgbw checks if rgb color is white
func rgbToRgbw(r, g, b uint8) (uint8, uint8, uint8, uint8) {
	if r == g && g == b {
		return 0x00, 0x00, 0x00, r
	}

	return r, g, b, 0x00
}

func rgbwToRgb(r, g, b, w uint8) (uint8, uint8, uint8) {
	if w > 0 {
		return w, w, w
	}
}

// convertIntRange converts input value with provided range to new range
func convertIntRange(value, inMin, inMax, outMin, outMax int) int {
	if inMin >= inMax || outMin >= outMax {
		return 0
	}

	if inMin == outMin && inMax == outMax {
		return value
	}

	if value < inMin {
		value = inMin
	}
	if value > inMax {
		value = inMax
	}

	inRange := inMax - inMin
	outRange := outMax - outMin

	fraction := float64(value-inMin) / float64(inRange)
	return int(fraction*float64(outRange)) + outMin
}
