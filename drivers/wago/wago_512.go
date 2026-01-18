package wago

type wago512 struct {
}

// String returns a string representation of the Wago 750-512 module
// 2DO 230V AC 2.0A/ Relay 2NO
// 2-Channel Relay Output Module 230 VAC,
// Non-floating; 2 Make Contacts
func (w *wago512) String() string {
	return "750-512"
}

// DiCount returns the number of digital inputs
func (w *wago512) DiCount() int {
	return 0
}

// DoCount returns the number of digital outputs
func (w *wago512) DoCount() int {
	return 2
}
