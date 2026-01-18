package wago

type wago611 struct {
}

// String returns a string representation of the Wago 750-611 module
// 230 V AC Power
// Supply/Fuse/Diagn.
// 750-611
func (w *wago611) String() string {
	return "750-611"
}

// DiCount returns the number of digital inputs
func (w *wago611) DiCount() int {
	return 2
}

// DoCount returns the number of digital outputs
func (w *wago611) DoCount() int {
	return 0
}
