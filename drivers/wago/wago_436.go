package wago

// Wago 750-436 module implementation, implements WagoModule interface
// - 8 channel digital input module
// - 24 VDC
// - 3 ms filter
// - low-side switching
// - 1-wire connection
type wago436 struct {
}

// String returns a string representation of the module
func (w *wago436) String() string {
	return "750-436"
}

// DiCount returns the number of digital inputs
func (w *wago436) DiCount() int {
	return 8
}

// DoCount returns the number of digital outputs
func (w *wago436) DoCount() int {
	return 0
}
