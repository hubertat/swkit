// Package rpixel implements the swkit IO driver for rpixels RGBW NeoPixel ring
// controllers, which speak the binary UDP protocol described in
// docs/rpixel/protocol.md (protocol version 1).
//
// This file holds the wire format only: framing, packet builders and parsers.
// It does no I/O, so both the driver and the fake device in rpixeltest share it.
package rpixel

import (
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"hash/crc32"
)

// ProtocolVersion is the protocol version this package speaks. Devices report
// theirs in S; a device reporting any other version is not used.
const ProtocolVersion = 1

// DefaultPort is the UDP port every device listens on.
const DefaultPort = 8888

// Frame size bounds, including the length field and the checksum.
const (
	MinFrameLen = 7
	MaxFrameLen = 512
)

// frameOverhead is length (2) + type (1) + CRC-32 (4).
const frameOverhead = 7

// MaxStages is the largest stage_count an A may carry.
const MaxStages = 16

// MaxStageDurationMs is the largest supported stage_duration_ms (2^31 - 1).
const MaxStageDurationMs = 1<<31 - 1

// Payload sizes fixed by the spec.
const (
	setPayloadLen           = 6
	animationHeaderLen      = 11
	stageLen                = 5
	stopPayloadLen          = 3
	ackPayloadLen           = 4
	statusRequestPayloadLen = 2
	statusPayloadMinLen     = 23
	requestIdLen            = 2
)

// S flag bits.
const (
	statusFlagAnimating uint8 = 1 << 0
	statusFlagOn        uint8 = 1 << 1
	statusFlagMixed     uint8 = 1 << 2
)

// PacketType is the ASCII type byte of a frame.
type PacketType byte

const (
	TypeSet           PacketType = 'P' // immediate RGBW display state, no reply
	TypeAnimation     PacketType = 'A' // start an animation
	TypeStop          PacketType = 'X' // stop the active animation
	TypeAck           PacketType = 'R' // acknowledgement / result
	TypeStatusRequest PacketType = 'G' // request identity, capabilities and state
	TypeStatus        PacketType = 'S' // identity, capabilities and state
)

func (pt PacketType) String() string {
	if pt >= 0x20 && pt < 0x7f {
		return string(rune(pt))
	}
	return fmt.Sprintf("0x%02x", byte(pt))
}

// Set modes for P.
const (
	SetModeWipeLeft  byte = 'L' // 2400 ms forward/left wipe to the colour
	SetModeWipeRight byte = 'R' // 2400 ms reverse/right wipe to the colour
	SetModeImmediate byte = 'I' // colour applied at once
	// Any other mode byte is a 200 ms reverse wipe to off.
)

// Frame decoding errors. A device silently drops such frames; so does the driver.
var (
	ErrFrameTooShort    = errors.New("rpixel: frame shorter than 7 bytes")
	ErrFrameTooLong     = errors.New("rpixel: frame longer than 512 bytes")
	ErrFrameLenMismatch = errors.New("rpixel: declared frame length does not match actual length")
	ErrFrameBadChecksum = errors.New("rpixel: frame CRC-32 mismatch")
	ErrInvalidPayload   = errors.New("rpixel: payload does not match packet type")
	ErrInvalidAnimation = errors.New("rpixel: invalid animation")
	ErrPayloadTooLong   = errors.New("rpixel: payload does not fit in a frame")
	ErrInvalidDeviceId  = errors.New("rpixel: device id must be 16 hex digits")
)

// Frame is one decoded datagram.
type Frame struct {
	Type    PacketType
	Payload []byte
}

// EncodeFrame builds `u16 BE length | type | payload | CRC-32 IEEE BE`.
func EncodeFrame(t PacketType, payload []byte) ([]byte, error) {
	total := frameOverhead + len(payload)
	if total > MaxFrameLen {
		return nil, ErrPayloadTooLong
	}
	b := make([]byte, 0, total)
	b = binary.BigEndian.AppendUint16(b, uint16(total))
	b = append(b, byte(t))
	b = append(b, payload...)
	b = binary.BigEndian.AppendUint32(b, crc32.ChecksumIEEE(b))
	return b, nil
}

// mustEncodeFrame is for builders whose payload size is fixed and small.
func mustEncodeFrame(t PacketType, payload []byte) []byte {
	b, err := EncodeFrame(t, payload)
	if err != nil {
		panic(err) // unreachable: fixed-size payloads always fit
	}
	return b
}

// DecodeFrame validates length, declared length and CRC. The returned payload
// aliases b.
func DecodeFrame(b []byte) (Frame, error) {
	if len(b) < MinFrameLen {
		return Frame{}, ErrFrameTooShort
	}
	if len(b) > MaxFrameLen {
		return Frame{}, ErrFrameTooLong
	}
	if int(binary.BigEndian.Uint16(b[0:2])) != len(b) {
		return Frame{}, ErrFrameLenMismatch
	}
	crcAt := len(b) - 4
	if crc32.ChecksumIEEE(b[:crcAt]) != binary.BigEndian.Uint32(b[crcAt:]) {
		return Frame{}, ErrFrameBadChecksum
	}
	return Frame{Type: PacketType(b[2]), Payload: b[3:crcAt]}, nil
}

// RequestId returns the request ID every request (and R/S reply) starts its
// payload with, or false when the payload is too short to carry one.
func (f Frame) RequestId() (uint16, bool) {
	if len(f.Payload) < requestIdLen {
		return 0, false
	}
	return binary.BigEndian.Uint16(f.Payload), true
}

// ---- enums ----

// AnimationKind selects the effect of an A.
type AnimationKind uint8

const (
	KindCircularWipe AnimationKind = 1
	KindFade         AnimationKind = 2
)

func (k AnimationKind) String() string {
	switch k {
	case KindCircularWipe:
		return "circular_wipe"
	case KindFade:
		return "fade"
	default:
		return fmt.Sprintf("kind(%d)", uint8(k))
	}
}

func (k AnimationKind) valid() bool { return k == KindCircularWipe || k == KindFade }

// Target selects which pixels an A animates.
type Target uint8

const (
	TargetFullRing  Target = 1
	TargetInnerRing Target = 2
	TargetOuterRing Target = 3
)

func (t Target) String() string {
	switch t {
	case TargetFullRing:
		return "full_ring"
	case TargetInnerRing:
		return "inner_ring"
	case TargetOuterRing:
		return "outer_ring"
	default:
		return fmt.Sprintf("target(%d)", uint8(t))
	}
}

func (t Target) valid() bool { return t >= TargetFullRing && t <= TargetOuterRing }

// DirectionMode says how a wipe's direction evolves across stages.
type DirectionMode uint8

const (
	DirModeNone        DirectionMode = 0
	DirModeFixed       DirectionMode = 1
	DirModeAlternating DirectionMode = 2
)

func (m DirectionMode) String() string {
	switch m {
	case DirModeNone:
		return "none"
	case DirModeFixed:
		return "fixed"
	case DirModeAlternating:
		return "alternating"
	default:
		return fmt.Sprintf("dir_mode(%d)", uint8(m))
	}
}

func (m DirectionMode) valid() bool { return m <= DirModeAlternating }

// Direction is a wipe's (initial) walking direction.
type Direction uint8

const (
	DirNone    Direction = 0
	DirForward Direction = 1 // forward/left
	DirReverse Direction = 2 // reverse/right
)

func (d Direction) String() string {
	switch d {
	case DirNone:
		return "none"
	case DirForward:
		return "forward"
	case DirReverse:
		return "reverse"
	default:
		return fmt.Sprintf("dir(%d)", uint8(d))
	}
}

func (d Direction) valid() bool { return d <= DirReverse }

// AckStatus is the status byte of an R.
type AckStatus uint8

const (
	AckAccepted    AckStatus = 0
	AckRejected    AckStatus = 1
	AckUnsupported AckStatus = 2
	AckCompleted   AckStatus = 3
	AckCancelled   AckStatus = 4
)

func (s AckStatus) String() string {
	switch s {
	case AckAccepted:
		return "accepted"
	case AckRejected:
		return "rejected"
	case AckUnsupported:
		return "unsupported"
	case AckCompleted:
		return "completed"
	case AckCancelled:
		return "cancelled"
	default:
		return fmt.Sprintf("status(%d)", uint8(s))
	}
}

// Detail codes carried in R (SwkitError values).
const (
	DetailNone          uint8 = 0
	DetailInvalidId     uint8 = 4  // with unsupported
	DetailInvalidPacket uint8 = 12 // with rejected
)

// ---- value types ----

// Colour is one RGBW colour, each channel 0..255.
type Colour struct {
	R, G, B, W uint8
}

// IsZero reports whether every channel is zero (which the device treats as off).
func (c Colour) IsZero() bool { return c == Colour{} }

// Stage is one animation step: a target colour and the strip-global brightness.
type Stage struct {
	Colour
	Brightness uint8
}

// Animation is the content of an A, without its request ID.
type Animation struct {
	Kind            AnimationKind
	Target          Target
	DirMode         DirectionMode
	Dir             Direction
	StageDurationMs uint32
	Stages          []Stage
}

// Validate applies the device's rejection rules, so a client never sends an A
// the device would reject.
func (a Animation) Validate() error {
	switch {
	case len(a.Stages) == 0 || len(a.Stages) > MaxStages:
		return fmt.Errorf("%w: stage count %d, want 1..%d", ErrInvalidAnimation, len(a.Stages), MaxStages)
	case !a.Kind.valid():
		return fmt.Errorf("%w: animation kind %s", ErrInvalidAnimation, a.Kind)
	case !a.Target.valid():
		return fmt.Errorf("%w: target %s", ErrInvalidAnimation, a.Target)
	case !a.DirMode.valid():
		return fmt.Errorf("%w: direction mode %s", ErrInvalidAnimation, a.DirMode)
	case !a.Dir.valid():
		return fmt.Errorf("%w: direction %s", ErrInvalidAnimation, a.Dir)
	case a.StageDurationMs > MaxStageDurationMs:
		return fmt.Errorf("%w: stage duration %d ms exceeds %d", ErrInvalidAnimation, a.StageDurationMs, uint32(MaxStageDurationMs))
	}
	return nil
}

// DeviceId is the 8-byte board unique ID a device reports in S.
type DeviceId [8]byte

// String returns the 16 lowercase hex digit text form, as used in the
// device's hostname (rpxl-<id>.local).
func (id DeviceId) String() string { return hex.EncodeToString(id[:]) }

// ParseDeviceId parses the 16 hex digit text form (either case).
func ParseDeviceId(s string) (DeviceId, error) {
	var id DeviceId
	if len(s) != 2*len(id) {
		return id, ErrInvalidDeviceId
	}
	if _, err := hex.Decode(id[:], []byte(s)); err != nil {
		return id, ErrInvalidDeviceId
	}
	return id, nil
}

// Ack is a parsed R.
type Ack struct {
	ReqId  uint16
	Status AckStatus
	Detail uint8
}

// Status is a parsed S. It describes the settled state: where the display is
// heading once a running animation finishes.
type Status struct {
	ReqId           uint16
	ProtocolVersion uint8
	DeviceId        DeviceId
	PixelCount      uint16
	InnerCount      uint8
	OuterCount      uint8
	MaxStages       uint8
	AnimationKinds  uint8 // bit (kind - 1) set for each supported kind
	Flags           uint8
	Brightness      uint8
	Colour          Colour // zero when Mixed
}

// Animating reports flag bit 0: an animation is running.
func (s Status) Animating() bool { return s.Flags&statusFlagAnimating != 0 }

// On reports flag bit 1: pixels are on once the display settles.
func (s Status) On() bool { return s.Flags&statusFlagOn != 0 }

// Mixed reports flag bit 2: pixels settle to different colours, and Colour is zero.
func (s Status) Mixed() bool { return s.Flags&statusFlagMixed != 0 }

// SupportsKind reports whether AnimationKinds advertises k.
func (s Status) SupportsKind(k AnimationKind) bool {
	return k >= 1 && k <= 8 && s.AnimationKinds&(1<<(k-1)) != 0
}

// ---- client-side builders ----

// EncodeSet builds a P. It never gets a reply; brightness 0 keeps the current one.
func EncodeSet(c Colour, mode byte, brightness uint8) []byte {
	return mustEncodeFrame(TypeSet, []byte{c.R, c.G, c.B, c.W, mode, brightness})
}

// EncodeAnimation builds an A after validating it.
func EncodeAnimation(reqId uint16, a Animation) ([]byte, error) {
	if err := a.Validate(); err != nil {
		return nil, err
	}
	p := make([]byte, 0, animationHeaderLen+stageLen*len(a.Stages))
	p = binary.BigEndian.AppendUint16(p, reqId)
	p = append(p, byte(a.Kind), byte(a.Target), byte(a.DirMode), byte(a.Dir))
	p = binary.BigEndian.AppendUint32(p, a.StageDurationMs)
	p = append(p, byte(len(a.Stages)))
	for _, s := range a.Stages {
		p = append(p, s.R, s.G, s.B, s.W, s.Brightness)
	}
	return EncodeFrame(TypeAnimation, p)
}

// EncodeStop builds an X. setOff additionally sets an immediate RGBW-zero state.
func EncodeStop(reqId uint16, setOff bool) []byte {
	var off byte
	if setOff {
		off = 1
	}
	p := binary.BigEndian.AppendUint16(nil, reqId)
	return mustEncodeFrame(TypeStop, append(p, off))
}

// EncodeStatusRequest builds a G.
func EncodeStatusRequest(reqId uint16) []byte {
	return mustEncodeFrame(TypeStatusRequest, binary.BigEndian.AppendUint16(nil, reqId))
}

// ParseAck parses an R payload, which must be exactly 4 bytes.
func ParseAck(payload []byte) (Ack, error) {
	if len(payload) != ackPayloadLen {
		return Ack{}, fmt.Errorf("%w: R payload is %d bytes, want %d", ErrInvalidPayload, len(payload), ackPayloadLen)
	}
	return Ack{
		ReqId:  binary.BigEndian.Uint16(payload),
		Status: AckStatus(payload[2]),
		Detail: payload[3],
	}, nil
}

// ParseStatus parses an S payload. Bytes after the known 23 are ignored, since
// later revisions may append fields without changing the protocol version.
func ParseStatus(payload []byte) (Status, error) {
	if len(payload) < statusPayloadMinLen {
		return Status{}, fmt.Errorf("%w: S payload is %d bytes, want at least %d", ErrInvalidPayload, len(payload), statusPayloadMinLen)
	}
	st := Status{
		ReqId:           binary.BigEndian.Uint16(payload[0:2]),
		ProtocolVersion: payload[2],
		PixelCount:      binary.BigEndian.Uint16(payload[11:13]),
		InnerCount:      payload[13],
		OuterCount:      payload[14],
		MaxStages:       payload[15],
		AnimationKinds:  payload[16],
		Flags:           payload[17],
		Brightness:      payload[18],
		Colour:          Colour{R: payload[19], G: payload[20], B: payload[21], W: payload[22]},
	}
	copy(st.DeviceId[:], payload[3:11])
	return st, nil
}

// ---- device-side codecs ----
//
// These mirror the builders above. The driver does not need them; the fake
// device in rpixeltest does, and they keep the round-trip tests honest.

// SetCommand is a parsed P.
type SetCommand struct {
	Colour     Colour
	Mode       byte
	Brightness uint8
}

// ParseSet parses a P payload, which must be exactly 6 bytes.
func ParseSet(payload []byte) (SetCommand, error) {
	if len(payload) != setPayloadLen {
		return SetCommand{}, fmt.Errorf("%w: P payload is %d bytes, want %d", ErrInvalidPayload, len(payload), setPayloadLen)
	}
	return SetCommand{
		Colour:     Colour{R: payload[0], G: payload[1], B: payload[2], W: payload[3]},
		Mode:       payload[4],
		Brightness: payload[5],
	}, nil
}

// ParseAnimation parses an A payload, applying the device's rejection rules.
// The request ID is returned whenever it can be read, even on error, so a
// device can address its R rejected.
func ParseAnimation(payload []byte) (uint16, Animation, error) {
	if len(payload) < requestIdLen {
		return 0, Animation{}, fmt.Errorf("%w: A payload has no request id", ErrInvalidPayload)
	}
	reqId := binary.BigEndian.Uint16(payload)
	if len(payload) < animationHeaderLen {
		return reqId, Animation{}, fmt.Errorf("%w: A payload is %d bytes, want at least %d", ErrInvalidPayload, len(payload), animationHeaderLen)
	}
	count := int(payload[10])
	if len(payload) != animationHeaderLen+stageLen*count {
		return reqId, Animation{}, fmt.Errorf("%w: A payload is %d bytes, want %d for %d stages", ErrInvalidPayload, len(payload), animationHeaderLen+stageLen*count, count)
	}
	a := Animation{
		Kind:            AnimationKind(payload[2]),
		Target:          Target(payload[3]),
		DirMode:         DirectionMode(payload[4]),
		Dir:             Direction(payload[5]),
		StageDurationMs: binary.BigEndian.Uint32(payload[6:10]),
		Stages:          make([]Stage, count),
	}
	for i := range a.Stages {
		s := payload[animationHeaderLen+stageLen*i:]
		a.Stages[i] = Stage{Colour: Colour{R: s[0], G: s[1], B: s[2], W: s[3]}, Brightness: s[4]}
	}
	if err := a.Validate(); err != nil {
		return reqId, Animation{}, err
	}
	return reqId, a, nil
}

// ParseStop parses an X payload, which must be exactly 3 bytes. As for
// ParseAnimation, the request ID is returned whenever it can be read.
func ParseStop(payload []byte) (reqId uint16, setOff bool, err error) {
	if len(payload) < requestIdLen {
		return 0, false, fmt.Errorf("%w: X payload has no request id", ErrInvalidPayload)
	}
	reqId = binary.BigEndian.Uint16(payload)
	if len(payload) != stopPayloadLen {
		return reqId, false, fmt.Errorf("%w: X payload is %d bytes, want %d", ErrInvalidPayload, len(payload), stopPayloadLen)
	}
	return reqId, payload[2] != 0, nil
}

// ParseStatusRequest parses a G payload, which must be exactly 2 bytes.
func ParseStatusRequest(payload []byte) (uint16, error) {
	if len(payload) != statusRequestPayloadLen {
		reqId, _ := Frame{Payload: payload}.RequestId()
		return reqId, fmt.Errorf("%w: G payload is %d bytes, want %d", ErrInvalidPayload, len(payload), statusRequestPayloadLen)
	}
	return binary.BigEndian.Uint16(payload), nil
}

// EncodeAck builds an R.
func EncodeAck(a Ack) []byte {
	p := binary.BigEndian.AppendUint16(nil, a.ReqId)
	return mustEncodeFrame(TypeAck, append(p, byte(a.Status), a.Detail))
}

// EncodeStatus builds an S from s. Flags are taken as given.
func EncodeStatus(s Status) []byte {
	p := make([]byte, 0, statusPayloadMinLen)
	p = binary.BigEndian.AppendUint16(p, s.ReqId)
	p = append(p, s.ProtocolVersion)
	p = append(p, s.DeviceId[:]...)
	p = binary.BigEndian.AppendUint16(p, s.PixelCount)
	p = append(p, s.InnerCount, s.OuterCount, s.MaxStages, s.AnimationKinds, s.Flags, s.Brightness)
	p = append(p, s.Colour.R, s.Colour.G, s.Colour.B, s.Colour.W)
	return mustEncodeFrame(TypeStatus, p)
}

// StatusFlags builds the S flags byte.
func StatusFlags(animating, on, mixed bool) uint8 {
	var f uint8
	if animating {
		f |= statusFlagAnimating
	}
	if on {
		f |= statusFlagOn
	}
	if mixed {
		f |= statusFlagMixed
	}
	return f
}
