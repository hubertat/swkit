// Package rpixeltest provides an in-process fake rpixels ring for tests. It
// listens on 127.0.0.1 and implements the device side of protocol v1 (P, A, X
// and G, answered with R and S) closely enough to drive the rpixel driver,
// with knobs to lose packets, go silent, and change state out of band.
//
// Request-ID replay is not implemented: a repeated A is handled again.
package rpixeltest

import (
	"errors"
	"net"
	"sync"
	"time"

	"github.com/hubertat/swkit/drivers/rpixel"
)

// DefaultDeviceId is the device ID a new Device reports.
var DefaultDeviceId = rpixel.DeviceId{0xe6, 0x61, 0x41, 0x03, 0xe7, 0x45, 0x2d, 0x2f}

// Boot state, as the firmware settles after start-up.
var (
	BootColour     = rpixel.Colour{W: 255}
	BootBrightness = uint8(60)
)

// AnimationRecord is one A the device received and accepted.
type AnimationRecord struct {
	ReqId     uint16
	Animation rpixel.Animation
	Raw       []byte // the whole frame, byte for byte
}

type activeAnim struct {
	reqId uint16
	to    *net.UDPAddr
	timer *time.Timer
}

// Device is a fake ring. All methods are safe for concurrent use.
type Device struct {
	conn *net.UDPConn
	done chan struct{}

	mu          sync.Mutex
	id          rpixel.DeviceId
	version     uint8
	colour      rpixel.Colour
	brightness  uint8
	mixed       bool
	anim        *activeAnim
	silent      bool
	drop        map[rpixel.PacketType]int
	statusDelay time.Duration
	received    map[rpixel.PacketType]int
	animations  []AnimationRecord
	sets        []rpixel.SetCommand
}

// New starts a fake device on 127.0.0.1 with an ephemeral port, in the boot
// state (white 255 at brightness 60).
func New() (*Device, error) {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		return nil, err
	}
	d := &Device{
		conn:       conn,
		done:       make(chan struct{}),
		id:         DefaultDeviceId,
		version:    rpixel.ProtocolVersion,
		colour:     BootColour,
		brightness: BootBrightness,
		drop:       make(map[rpixel.PacketType]int),
		received:   make(map[rpixel.PacketType]int),
	}
	go d.serve()
	return d, nil
}

// Addr returns the UDP address the device listens on.
func (d *Device) Addr() *net.UDPAddr {
	a := d.conn.LocalAddr().(*net.UDPAddr)
	return &net.UDPAddr{IP: append(net.IP(nil), a.IP...), Port: a.Port}
}

// Address returns Addr as "127.0.0.1:<port>", ready for DeviceConfig.Address.
func (d *Device) Address() string {
	return d.Addr().String()
}

// Close stops the device and waits for its reader to exit.
func (d *Device) Close() error {
	d.mu.Lock()
	if d.anim != nil {
		d.anim.timer.Stop()
		d.anim = nil
	}
	d.mu.Unlock()
	err := d.conn.Close()
	<-d.done
	return err
}

// SetDeviceId changes the ID reported in S.
func (d *Device) SetDeviceId(id rpixel.DeviceId) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.id = id
}

// SetProtocolVersion changes the version reported in S.
func (d *Device) SetProtocolVersion(v uint8) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.version = v
}

// DropNext makes the device ignore the next n packets of type t, as if they
// were lost on the way.
func (d *Device) DropNext(t rpixel.PacketType, n int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.drop[t] += n
}

// SetSilent makes the device ignore every packet (true) or resume (false),
// like a ring that lost power or Wi-Fi.
func (d *Device) SetSilent(silent bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.silent = silent
}

// SetStatusDelay delays every S by delay. The S still describes the state at
// the time its G was received.
func (d *Device) SetStatusDelay(delay time.Duration) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.statusDelay = delay
}

// SetState changes the display out of band, cancelling any running animation.
func (d *Device) SetState(c rpixel.Colour, brightness uint8) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.cancelAnimLocked()
	d.colour, d.brightness, d.mixed = c, brightness, false
}

// SetMixed makes the rings show different colours (S reports the mixed flag).
func (d *Device) SetMixed() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.cancelAnimLocked()
	d.colour, d.mixed = rpixel.Colour{}, true
}

// Toggle simulates HTTP GET /set/toggle: off when on, otherwise white 255 at
// the current brightness (60 when it is 0).
func (d *Device) Toggle() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.cancelAnimLocked()
	if d.onLocked() {
		d.colour, d.mixed = rpixel.Colour{}, false
		return
	}
	d.colour, d.mixed = rpixel.Colour{W: 255}, false
	if d.brightness == 0 {
		d.brightness = 60
	}
}

// State returns the settled colour, brightness and on flag.
func (d *Device) State() (c rpixel.Colour, brightness uint8, on bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.colour, d.brightness, d.onLocked()
}

// Received returns how many well-framed packets of type t arrived, including
// dropped ones.
func (d *Device) Received(t rpixel.PacketType) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.received[t]
}

// ReceivedTotal returns how many well-framed packets of any type arrived.
func (d *Device) ReceivedTotal() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	n := 0
	for _, c := range d.received {
		n += c
	}
	return n
}

// Animations returns every accepted A, oldest first.
func (d *Device) Animations() []AnimationRecord {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]AnimationRecord(nil), d.animations...)
}

// LastAnimation returns the most recent accepted A.
func (d *Device) LastAnimation() (AnimationRecord, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.animations) == 0 {
		return AnimationRecord{}, false
	}
	return d.animations[len(d.animations)-1], true
}

// Sets returns every accepted P, oldest first.
func (d *Device) Sets() []rpixel.SetCommand {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]rpixel.SetCommand(nil), d.sets...)
}

func (d *Device) serve() {
	defer close(d.done)
	buf := make([]byte, rpixel.MaxFrameLen+1)
	for {
		n, from, err := d.conn.ReadFromUDP(buf)
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			continue
		}
		f, err := rpixel.DecodeFrame(buf[:n])
		if err != nil {
			continue // dropped without any reply, like the firmware
		}
		raw := append([]byte(nil), buf[:n]...)
		f.Payload = append([]byte(nil), f.Payload...)
		d.handle(f, raw, from)
	}
}

func (d *Device) handle(f rpixel.Frame, raw []byte, from *net.UDPAddr) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.received[f.Type]++
	if d.silent {
		return
	}
	if d.drop[f.Type] > 0 {
		d.drop[f.Type]--
		return
	}

	switch f.Type {
	case rpixel.TypeSet:
		sc, err := rpixel.ParseSet(f.Payload)
		if err != nil {
			return
		}
		d.cancelAnimLocked()
		d.sets = append(d.sets, sc)
		switch sc.Mode {
		case rpixel.SetModeWipeLeft, rpixel.SetModeWipeRight, rpixel.SetModeImmediate:
			d.colour = sc.Colour
		default:
			d.colour = rpixel.Colour{}
		}
		d.mixed = false
		if sc.Brightness != 0 {
			d.brightness = sc.Brightness
		}

	case rpixel.TypeAnimation:
		reqId, a, err := rpixel.ParseAnimation(f.Payload)
		if err != nil {
			if _, ok := f.RequestId(); ok {
				d.replyLocked(from, rpixel.EncodeAck(rpixel.Ack{ReqId: reqId, Status: rpixel.AckRejected, Detail: rpixel.DetailInvalidPacket}))
			}
			return
		}
		d.cancelAnimLocked()
		d.animations = append(d.animations, AnimationRecord{ReqId: reqId, Animation: a, Raw: raw})
		// S describes the settled state, so apply the last stage right away.
		last := a.Stages[len(a.Stages)-1]
		d.brightness = last.Brightness
		if a.Target == rpixel.TargetFullRing {
			d.colour, d.mixed = last.Colour, false
		} else {
			d.colour, d.mixed = rpixel.Colour{}, true
		}
		d.replyLocked(from, rpixel.EncodeAck(rpixel.Ack{ReqId: reqId, Status: rpixel.AckAccepted}))
		total := time.Duration(a.StageDurationMs) * time.Duration(len(a.Stages)) * time.Millisecond
		if total == 0 {
			d.replyLocked(from, rpixel.EncodeAck(rpixel.Ack{ReqId: reqId, Status: rpixel.AckCompleted}))
			return
		}
		aa := &activeAnim{reqId: reqId, to: from}
		aa.timer = time.AfterFunc(total, func() { d.completeAnim(aa) })
		d.anim = aa

	case rpixel.TypeStop:
		reqId, setOff, err := rpixel.ParseStop(f.Payload)
		if err != nil {
			d.replyLocked(from, rpixel.EncodeAck(rpixel.Ack{ReqId: reqId, Status: rpixel.AckRejected, Detail: rpixel.DetailInvalidPacket}))
			return
		}
		d.cancelAnimLocked()
		if setOff {
			d.colour, d.mixed = rpixel.Colour{}, false
		}
		d.replyLocked(from, rpixel.EncodeAck(rpixel.Ack{ReqId: reqId, Status: rpixel.AckAccepted}))

	case rpixel.TypeStatusRequest:
		reqId, err := rpixel.ParseStatusRequest(f.Payload)
		if err != nil {
			d.replyLocked(from, rpixel.EncodeAck(rpixel.Ack{ReqId: reqId, Status: rpixel.AckRejected, Detail: rpixel.DetailInvalidPacket}))
			return
		}
		s := d.statusLocked(reqId)
		if d.statusDelay > 0 {
			time.AfterFunc(d.statusDelay, func() { _, _ = d.conn.WriteToUDP(s, from) })
			return
		}
		d.replyLocked(from, s)

	case rpixel.TypeAck, rpixel.TypeStatus:
		// Never answered, so two devices cannot bounce replies forever.

	default:
		if reqId, ok := f.RequestId(); ok {
			d.replyLocked(from, rpixel.EncodeAck(rpixel.Ack{ReqId: reqId, Status: rpixel.AckUnsupported, Detail: rpixel.DetailInvalidId}))
		}
	}
}

func (d *Device) completeAnim(aa *activeAnim) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.anim != aa {
		return
	}
	d.anim = nil
	d.replyLocked(aa.to, rpixel.EncodeAck(rpixel.Ack{ReqId: aa.reqId, Status: rpixel.AckCompleted}))
}

// cancelAnimLocked replaces the running animation, sending its terminal
// cancelled to whoever started it.
func (d *Device) cancelAnimLocked() {
	if d.anim == nil {
		return
	}
	aa := d.anim
	d.anim = nil
	if aa.timer.Stop() {
		d.replyLocked(aa.to, rpixel.EncodeAck(rpixel.Ack{ReqId: aa.reqId, Status: rpixel.AckCancelled}))
	}
}

func (d *Device) onLocked() bool {
	return (d.mixed || !d.colour.IsZero()) && d.brightness != 0
}

func (d *Device) statusLocked(reqId uint16) []byte {
	return rpixel.EncodeStatus(rpixel.Status{
		ReqId:           reqId,
		ProtocolVersion: d.version,
		DeviceId:        d.id,
		PixelCount:      40,
		InnerCount:      16,
		OuterCount:      24,
		MaxStages:       rpixel.MaxStages,
		AnimationKinds:  0x03,
		Flags:           rpixel.StatusFlags(d.anim != nil, d.onLocked(), d.mixed),
		Brightness:      d.brightness,
		Colour:          d.colour,
	})
}

func (d *Device) replyLocked(to *net.UDPAddr, b []byte) {
	_, _ = d.conn.WriteToUDP(b, to)
}
