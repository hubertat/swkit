package arduino

import "fmt"

type PinState uint8

const (
	PinStateUnknown PinState = 0
	PinStateLow     PinState = 1
	PinStateHigh    PinState = 2
)

func (ps PinState) String() string {
	switch ps {
	case PinStateLow:
		return "LO"
	case PinStateHigh:
		return "HI"
	default:
		return "NA"
	}
}

type Pin struct {
	number  uint8
	isInput bool
	pullup  bool
	state   PinState
}

func NewInputPin(number uint8, pullup bool) Pin {
	return Pin{
		number:  number,
		isInput: true,
		pullup:  pullup,
		state:   PinStateUnknown,
	}
}

func NewOutputPin(number uint8) Pin {
	return Pin{
		number:  number,
		isInput: false,
		state:   PinStateUnknown,
	}
}

func (pi Pin) ConfigEquals(other Pin) bool {
	if pi.isInput {
		return pi.isInput == other.isInput && pi.pullup == other.pullup
	}

	return pi.isInput == other.isInput
}

func (pi Pin) String() string {
	dirString := ""
	if pi.isInput {
		dirString = "IN "
		if pi.pullup {
			dirString += "PULLUP"
		} else {
			dirString += "______"
		}
	} else {
		dirString = "OUT" + "______"
	}

	return fmt.Sprintf("pin [no: %2d] [%s] %s", pi.number, dirString, pi.state.String())
}

func mapPins(pins []Pin) map[byte]Pin {
	out := make(map[byte]Pin)
	for _, p := range pins {
		out[p.number] = p
	}
	return out
}
