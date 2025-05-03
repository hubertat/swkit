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

// ReadConfigPacket(packet Packet, override bool) error reads config packet, compares it with internal config
// when it is a match, returns nil, otherwise returns error or (if override is set to true) replaces internal config
func (a *ArduinoPro) ReadConfigPacket(packet Packet, override bool) error {
	if packet.tag != PACKET_TYPE_ARDUINOPRO_CONFIG_NOTICE {
		return errors.New("incorrect packet type (tag), expected: " + PACKET_TYPE_ARDUINOPRO_CONFIG_NOTICE.String() + ", got: " + packet.tag.String())
	}

	if packet.Len() < 3*3 {
		return errors.New("incorrect packet length")
	}

	type config struct {
		InPins       []Pin
		OutPins      []Pin
		CheckInputs  bool
		CheckOutputs bool
		CheckPullups bool
	}
	cfg := config{}

	data := packet.Data()
	blockPos := 0
	for blockPos+2 < packet.Len() {
		blockID := string([]byte{data[blockPos], data[blockPos+1], 0x00})
		blockLen := int(data[blockPos+2])
		if blockPos+2+blockLen >= packet.Len() {
			return fmt.Errorf("unexpected packet length when reading block: %s, read len=%d", blockID, blockLen)
		}

		switch blockID {
		case "IN":
			inB := data[blockPos+2 : blockPos+2+blockLen]
			inPins := make([]Pin, blockLen)
			for ix, b := range inB {
				inPins[ix] = Pin{
					number:  b,
					isInput: true,
				}
			}
			cfg.InPins = inPins
			cfg.CheckInputs = true
		case "OU":
			outB := data[blockPos+2 : blockPos+2+blockLen]
			outPins := make([]Pin, blockLen)
			for ix, b := range outB {
				outPins[ix] = Pin{
					number: b,
				}
			}
			cfg.OutPins = outPins
			cfg.CheckOutputs = true
		case "PU":
			pullB := data[blockPos+2 : blockPos+2+blockLen]
			inPins := cfg.InPins
			for _, b := range pullB {
				for ix, inPin := range inPins {
					if inPin.number == b {
						inPin.pullup = true
						inPins[ix] = inPin
					}
				}
			}
			cfg.InPins = inPins
			cfg.CheckPullups = true
		default:
			// unexpected block type/id
			// error?
		}
		blockPos = blockPos + 3 + blockLen
	}

	if !cfg.CheckInputs || !cfg.CheckOutputs || !cfg.CheckPullups {
		return fmt.Errorf("finished reading config with missing blocks, blocks checked out: [input, output, pullup] : %v", []bool{cfg.CheckInputs, cfg.CheckOutputs, cfg.CheckPullups})
	}

	mismatch := false

	internalPins := mapPins(append(a.inPins, a.outPins...))
	cfgPins := mapPins(append(cfg.InPins, cfg.OutPins...))

	for pinNo, pin := range internalPins {
		cfgPin, found := cfgPins[pinNo]
		if found {
			if !pin.ConfigEquals(cfgPin) {
				mismatch = true
				break
			}
		} else {
			mismatch = true
			break
		}
	}

	if !mismatch {
		return nil
	}

	if !override {
		return errors.New("received config does not match internal config, and override is not set")
	}

	a.inPins = cfg.InPins
	a.outPins = cfg.OutPins

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
