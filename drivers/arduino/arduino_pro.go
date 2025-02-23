package arduino

import (
	"errors"
	"fmt"
	"hash/crc32"
	"net/netip"
	"sort"
	"strings"
	"time"
)

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

type ArduinoPro struct {
	inPins  []Pin
	outPins []Pin
	address netip.Addr

	lastRefreshed time.Time
}

func NewArduinoPro(address netip.Addr, pins []Pin) *ArduinoPro {

	inPins := []Pin{}
	outPins := []Pin{}

	pinNoMap := map[uint8]bool{}

	for _, pin := range pins {
		_, exists := pinNoMap[pin.number]

		if !exists {
			pinNoMap[pin.number] = true
			if pin.isInput {
				inPins = append(inPins, pin)
			} else {
				outPins = append(outPins, pin)
			}
		}
	}

	sort.Slice(inPins, func(i, j int) bool {
		return inPins[i].number < inPins[j].number
	})
	sort.Slice(outPins, func(i, j int) bool {
		return outPins[i].number < outPins[j].number
	})

	return &ArduinoPro{
		address: address,
		inPins:  inPins,
		outPins: outPins,
	}

}

func (a *ArduinoPro) GetPin(pinNumber uint8) (Pin, bool) {
	for _, pin := range a.inPins {
		if pin.number == pinNumber {
			return pin, true
		}
	}

	for _, pin := range a.outPins {
		if pin.number == pinNumber {
			return pin, true
		}
	}

	return Pin{}, false
}

func (a *ArduinoPro) GetInputPins() []Pin {
	return a.inPins
}

func (a *ArduinoPro) GetOutputPins() []Pin {
	return a.outPins
}

func (a *ArduinoPro) ConfigBytes() []byte {
	inPins := a.GetInputPins()
	outPins := a.GetOutputPins()
	// config prefix starts with SOH and CONFIG_
	cfg := []byte{0x01, 'C', 'O', 'N', 'F', 'I', 'G', '_', 0x00}

	pullupPins := []Pin{}

	// IN for input pins
	cfg = append(cfg, 'I', 'N', byte(len(inPins)))
	for _, pin := range inPins {
		cfg = append(cfg, pin.number)
		if pin.pullup {
			pullupPins = append(pullupPins, pin)
		}
	}
	// OU for output pins
	cfg = append(cfg, 'O', 'U', byte(len(outPins)))
	for _, pin := range outPins {
		cfg = append(cfg, pin.number)
	}
	// PU for pullup pins
	cfg = append(cfg, 'P', 'U', byte(len(pullupPins)))
	for _, pin := range pullupPins {
		cfg = append(cfg, pin.number)
	}

	// calculate CRC32 without starting SOH
	crc := crc32.ChecksumIEEE(cfg[1:])

	// append CRC32 to the end, big endian
	cfg = append(cfg, byte(crc>>24), byte(crc>>16), byte(crc>>8), byte(crc))
	// append ETX
	cfg = append(cfg, 0x03)

	return cfg
}

func (a *ArduinoPro) ReadStatusPacket(packet []byte) error {
	// status packet construction: "STATUS_\0" (8) + counts (2) + input states + output states + CRC (4)
	if len(packet) < 14 {
		return errors.New("packet too short")
	}

	if !strings.EqualFold(string(packet[:7]), "STATUS_") {
		return errors.New("invalid packet prefix")
	}

	calculatedCrc := crc32.ChecksumIEEE(packet[:len(packet)-4])
	receivedCrc := uint32(packet[len(packet)-4])<<24 | uint32(packet[len(packet)-3])<<16 | uint32(packet[len(packet)-2])<<8 | uint32(packet[len(packet)-1])
	if calculatedCrc != receivedCrc {
		return errors.New("invalid packet CRC")
	}

	inLen := int(packet[8])
	outLen := int(packet[9])
	states := packet[10 : len(packet)-4]

	if len(states) != inLen+outLen {
		return errors.New("invalid packet length")
	}

	if inLen != len(a.inPins) || outLen != len(a.outPins) {
		return errors.New("invalid pin count, status pin count and configured pin count do not match")
	}

	for i, pin := range a.inPins {
		newPinState := PinStateUnknown
		if states[i] == 0 {
			newPinState = PinStateLow
		} else {
			newPinState = PinStateHigh
		}
		if pin.state != newPinState {
			// pin state changed, maybe notify or fire an event
			pin.state = newPinState
			a.inPins[i] = pin
		}
	}

	for i, pin := range a.outPins {
		newPinState := PinStateUnknown
		if states[i+inLen] == 0 {
			newPinState = PinStateLow
		} else {
			newPinState = PinStateHigh
		}

		if pin.state != newPinState {
			// pin state changed, maybe notify or fire an event
			pin.state = newPinState
			a.outPins[i] = pin
		}
	}
	a.lastRefreshed = time.Now()

	return nil
}

// String() returns a verbose and nice string representation of the ArduinoPro
func (a *ArduinoPro) String() string {
	inPins := a.GetInputPins()
	outPins := a.GetOutputPins()

	str := fmt.Sprintf("ArduinoPro at %s\n", a.address.String())
	if a.lastRefreshed.IsZero() {
		str += "Last Refreshed: never\n"
	} else {
		str += fmt.Sprintf("Last Refreshed: %s ago\n", time.Since(a.lastRefreshed).String())
	}
	str += "Input Pins:\n"
	for _, pin := range inPins {
		str += pin.String() + "\n"
	}
	str += "Output Pins:\n"
	for _, pin := range outPins {
		str += pin.String() + "\n"
	}

	return str
}
