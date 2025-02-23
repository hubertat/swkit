package arduino

import (
	"errors"
	"fmt"
	"net/netip"
	"sort"
	"time"
)

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

func (a *ArduinoPro) ConfigPacket() Packet {
	inPins := a.GetInputPins()
	outPins := a.GetOutputPins()

	pullupPins := []Pin{}

	cfg := []byte{}

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

	return NewPacket(PACKET_TYPE_ARDUINOPRO_CONFIG, cfg)
}

func (a *ArduinoPro) ReadStatusPacket(packet Packet) error {
	// status packet data: in count (1)+ out count (1) + input states + output states
	if packet.tag != PACKET_TYPE_ARDUINOPRO_STATUS {
		return errors.New("incorrect packet type (tag), expected: " + PACKET_TYPE_ARDUINOPRO_STATUS.String() + ", got: " + packet.tag.String())
	}

	if len(packet.Data()) < 2 {
		return fmt.Errorf("packet too short, length is: %d", len(packet.Data()))
	}

	inLen := int(packet.Data()[0])
	outLen := int(packet.Data()[1])

	states := packet.Data()[2:]

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
