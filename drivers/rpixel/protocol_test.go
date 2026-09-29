package rpixel

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"hash/crc32"
	"reflect"
	"strings"
	"testing"
)

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(strings.ReplaceAll(s, " ", ""))
	if err != nil {
		t.Fatalf("bad hex %q: %v", s, err)
	}
	return b
}

// The three byte-for-byte vectors from docs/rpixel/protocol.md.

func TestVectorStatusRequest(t *testing.T) {
	want := mustHex(t, "00 09 47 00 01 b9 f8 13 44")
	if got := EncodeStatusRequest(1); !bytes.Equal(got, want) {
		t.Fatalf("G vector\n got %x\nwant %x", got, want)
	}
}

func TestVectorSet(t *testing.T) {
	want := mustHex(t, "00 0d 50 00 00 00 ff 49 80 2c 42 ab 49")
	if got := EncodeSet(Colour{W: 255}, SetModeImmediate, 128); !bytes.Equal(got, want) {
		t.Fatalf("P vector\n got %x\nwant %x", got, want)
	}
}

func TestVectorAnimation(t *testing.T) {
	want := mustHex(t, "00 17 41 00 02 02 01 00 00 00 00 00 00 01 00 00 00 ff 80 ee db 30 ea")
	got, err := EncodeAnimation(2, Animation{
		Kind:   KindFade,
		Target: TargetFullRing,
		Stages: []Stage{{Colour: Colour{W: 255}, Brightness: 128}},
	})
	if err != nil {
		t.Fatalf("EncodeAnimation: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("A vector\n got %x\nwant %x", got, want)
	}
}

func TestDecodeFrameVectors(t *testing.T) {
	f, err := DecodeFrame(mustHex(t, "00 09 47 00 01 b9 f8 13 44"))
	if err != nil {
		t.Fatalf("DecodeFrame: %v", err)
	}
	if f.Type != TypeStatusRequest {
		t.Fatalf("type = %s, want G", f.Type)
	}
	if id, ok := f.RequestId(); !ok || id != 1 {
		t.Fatalf("request id = %d/%v, want 1", id, ok)
	}
}

func TestFrameRoundTrip(t *testing.T) {
	for _, n := range []int{0, 1, 23, MaxFrameLen - frameOverhead} {
		payload := bytes.Repeat([]byte{0xa5}, n)
		b, err := EncodeFrame(TypeStatus, payload)
		if err != nil {
			t.Fatalf("EncodeFrame(%d): %v", n, err)
		}
		if len(b) != n+frameOverhead {
			t.Fatalf("frame len = %d, want %d", len(b), n+frameOverhead)
		}
		f, err := DecodeFrame(b)
		if err != nil {
			t.Fatalf("DecodeFrame(%d): %v", n, err)
		}
		if f.Type != TypeStatus || !bytes.Equal(f.Payload, payload) {
			t.Fatalf("round trip mismatch for %d bytes", n)
		}
	}
	if _, err := EncodeFrame(TypeStatus, make([]byte, MaxFrameLen-frameOverhead+1)); !errors.Is(err, ErrPayloadTooLong) {
		t.Fatalf("oversized payload: err = %v, want ErrPayloadTooLong", err)
	}
}

// withCRC recomputes the checksum so a test can isolate one rejection rule.
func withCRC(b []byte) []byte {
	b = append([]byte(nil), b...)
	binary.BigEndian.PutUint32(b[len(b)-4:], crc32.ChecksumIEEE(b[:len(b)-4]))
	return b
}

func TestDecodeFrameRejections(t *testing.T) {
	good := EncodeStatusRequest(7)

	tooLong := make([]byte, MaxFrameLen+1)
	binary.BigEndian.PutUint16(tooLong, uint16(len(tooLong)))
	tooLong = withCRC(tooLong)

	lenMismatch := append([]byte(nil), good...)
	lenMismatch[1]++
	lenMismatch = withCRC(lenMismatch)

	badCRC := append([]byte(nil), good...)
	badCRC[len(badCRC)-1] ^= 0xff

	badPayloadByte := append([]byte(nil), good...)
	badPayloadByte[4] ^= 0x01

	cases := []struct {
		name string
		in   []byte
		want error
	}{
		{"empty", nil, ErrFrameTooShort},
		{"six bytes", good[:6], ErrFrameTooShort},
		{"over 512", tooLong, ErrFrameTooLong},
		{"length mismatch", lenMismatch, ErrFrameLenMismatch},
		{"truncated", good[:len(good)-1], ErrFrameLenMismatch},
		{"bad crc", badCRC, ErrFrameBadChecksum},
		{"corrupted payload", badPayloadByte, ErrFrameBadChecksum},
	}
	for _, c := range cases {
		if _, err := DecodeFrame(c.in); !errors.Is(err, c.want) {
			t.Errorf("%s: err = %v, want %v", c.name, err, c.want)
		}
	}

	// A minimal 7-byte frame (no payload) is well formed.
	if _, err := DecodeFrame(mustEncodeFrame(TypeStatusRequest, nil)); err != nil {
		t.Errorf("7-byte frame rejected: %v", err)
	}
}

func TestAnimationRoundTrip(t *testing.T) {
	a := Animation{
		Kind:            KindCircularWipe,
		Target:          TargetOuterRing,
		DirMode:         DirModeAlternating,
		Dir:             DirReverse,
		StageDurationMs: 2400,
		Stages: []Stage{
			{Colour: Colour{R: 1, G: 2, B: 3, W: 4}, Brightness: 5},
			{Colour: Colour{}, Brightness: 0},
			{Colour: Colour{R: 255, G: 255, B: 255, W: 255}, Brightness: 255},
		},
	}
	b, err := EncodeAnimation(0xfffe, a)
	if err != nil {
		t.Fatalf("EncodeAnimation: %v", err)
	}
	f, err := DecodeFrame(b)
	if err != nil || f.Type != TypeAnimation {
		t.Fatalf("DecodeFrame: %v type %s", err, f.Type)
	}
	reqId, got, err := ParseAnimation(f.Payload)
	if err != nil {
		t.Fatalf("ParseAnimation: %v", err)
	}
	if reqId != 0xfffe || !reflect.DeepEqual(got, a) {
		t.Fatalf("round trip: id %d anim %+v, want %+v", reqId, got, a)
	}
}

func TestEncodeAnimationValidation(t *testing.T) {
	stage := []Stage{{Colour: Colour{W: 1}, Brightness: 1}}
	ok := Animation{Kind: KindFade, Target: TargetFullRing, Stages: stage}
	if _, err := EncodeAnimation(1, ok); err != nil {
		t.Fatalf("valid animation rejected: %v", err)
	}
	max := ok
	max.Stages = make([]Stage, MaxStages)
	if _, err := EncodeAnimation(1, max); err != nil {
		t.Fatalf("16 stages rejected: %v", err)
	}

	mutate := func(f func(*Animation)) Animation {
		a := ok
		f(&a)
		return a
	}
	cases := map[string]Animation{
		"no stages":     mutate(func(a *Animation) { a.Stages = nil }),
		"17 stages":     mutate(func(a *Animation) { a.Stages = make([]Stage, MaxStages+1) }),
		"kind 0":        mutate(func(a *Animation) { a.Kind = 0 }),
		"kind 3":        mutate(func(a *Animation) { a.Kind = 3 }),
		"target 0":      mutate(func(a *Animation) { a.Target = 0 }),
		"target 4":      mutate(func(a *Animation) { a.Target = 4 }),
		"dir mode 3":    mutate(func(a *Animation) { a.DirMode = 3 }),
		"dir 3":         mutate(func(a *Animation) { a.Dir = 3 }),
		"duration 2^31": mutate(func(a *Animation) { a.StageDurationMs = 1 << 31 }),
	}
	for name, a := range cases {
		if _, err := EncodeAnimation(1, a); !errors.Is(err, ErrInvalidAnimation) {
			t.Errorf("%s: err = %v, want ErrInvalidAnimation", name, err)
		}
	}
}

func TestParseAnimationRejections(t *testing.T) {
	good, _ := EncodeAnimation(9, Animation{Kind: KindFade, Target: TargetFullRing, Stages: []Stage{{Brightness: 1}}})
	f, _ := DecodeFrame(good)
	p := f.Payload

	if _, _, err := ParseAnimation(p[:1]); !errors.Is(err, ErrInvalidPayload) {
		t.Errorf("1-byte payload: err = %v", err)
	}
	// Every other rejection still yields the request id for the R rejected.
	short := p[:10]
	extra := append(append([]byte(nil), p...), 0)
	zeroStages := append([]byte(nil), p[:11]...)
	zeroStages[10] = 0
	badKind := append([]byte(nil), p...)
	badKind[2] = 9
	for name, payload := range map[string][]byte{"short": short, "extra byte": extra, "zero stages": zeroStages, "bad kind": badKind} {
		id, _, err := ParseAnimation(payload)
		if err == nil {
			t.Errorf("%s: accepted", name)
		}
		if id != 9 {
			t.Errorf("%s: request id = %d, want 9", name, id)
		}
	}
}

func TestParseAck(t *testing.T) {
	f, err := DecodeFrame(EncodeAck(Ack{ReqId: 0x1234, Status: AckRejected, Detail: DetailInvalidPacket}))
	if err != nil || f.Type != TypeAck {
		t.Fatalf("DecodeFrame: %v %s", err, f.Type)
	}
	a, err := ParseAck(f.Payload)
	if err != nil {
		t.Fatalf("ParseAck: %v", err)
	}
	if a != (Ack{ReqId: 0x1234, Status: AckRejected, Detail: 12}) {
		t.Fatalf("ack = %+v", a)
	}
	for _, n := range []int{0, 3, 5} {
		if _, err := ParseAck(make([]byte, n)); !errors.Is(err, ErrInvalidPayload) {
			t.Errorf("%d-byte R: err = %v, want ErrInvalidPayload", n, err)
		}
	}
}

func TestParseStatus(t *testing.T) {
	in := Status{
		ReqId:           42,
		ProtocolVersion: 1,
		DeviceId:        DeviceId{0xe6, 0x61, 0x41, 0x03, 0xe7, 0x45, 0x2d, 0x2f},
		PixelCount:      40,
		InnerCount:      16,
		OuterCount:      24,
		MaxStages:       16,
		AnimationKinds:  0x03,
		Flags:           StatusFlags(true, true, false),
		Brightness:      60,
		Colour:          Colour{R: 1, G: 2, B: 3, W: 255},
	}
	f, err := DecodeFrame(EncodeStatus(in))
	if err != nil || f.Type != TypeStatus {
		t.Fatalf("DecodeFrame: %v %s", err, f.Type)
	}
	if len(f.Payload) != 23 {
		t.Fatalf("S payload = %d bytes, want 23", len(f.Payload))
	}
	got, err := ParseStatus(f.Payload)
	if err != nil {
		t.Fatalf("ParseStatus: %v", err)
	}
	if got != in {
		t.Fatalf("status = %+v, want %+v", got, in)
	}
	if got.DeviceId.String() != "e6614103e7452d2f" {
		t.Fatalf("device id = %s", got.DeviceId)
	}
	if !got.Animating() || !got.On() || got.Mixed() {
		t.Fatalf("flags: animating %v on %v mixed %v", got.Animating(), got.On(), got.Mixed())
	}
	if !got.SupportsKind(KindFade) || !got.SupportsKind(KindCircularWipe) || got.SupportsKind(3) {
		t.Fatalf("SupportsKind wrong for 0x03")
	}

	// Appended fields are ignored.
	longer, err := ParseStatus(append(append([]byte(nil), f.Payload...), 0xde, 0xad))
	if err != nil || longer != in {
		t.Fatalf("S with trailing bytes: %+v, %v", longer, err)
	}
	// Short S is rejected.
	if _, err := ParseStatus(f.Payload[:22]); !errors.Is(err, ErrInvalidPayload) {
		t.Fatalf("22-byte S: err = %v", err)
	}
}

func TestParseSetStopStatusRequest(t *testing.T) {
	f, _ := DecodeFrame(EncodeSet(Colour{R: 9, W: 8}, SetModeWipeLeft, 0))
	sc, err := ParseSet(f.Payload)
	if err != nil || sc != (SetCommand{Colour: Colour{R: 9, W: 8}, Mode: 'L'}) {
		t.Fatalf("ParseSet = %+v, %v", sc, err)
	}
	if _, err := ParseSet(f.Payload[:5]); err == nil {
		t.Fatal("5-byte P accepted")
	}

	f, _ = DecodeFrame(EncodeStop(77, true))
	id, off, err := ParseStop(f.Payload)
	if err != nil || id != 77 || !off {
		t.Fatalf("ParseStop = %d %v %v", id, off, err)
	}
	if id, _, err := ParseStop(f.Payload[:2]); err == nil || id != 77 {
		t.Fatalf("2-byte X: id %d err %v", id, err)
	}
	f, _ = DecodeFrame(EncodeStop(78, false))
	if _, off, _ := ParseStop(f.Payload); off {
		t.Fatal("set_off=0 parsed as true")
	}

	f, _ = DecodeFrame(EncodeStatusRequest(0xabcd))
	if id, err := ParseStatusRequest(f.Payload); err != nil || id != 0xabcd {
		t.Fatalf("ParseStatusRequest = %d %v", id, err)
	}
	if id, err := ParseStatusRequest([]byte{0xab, 0xcd, 0}); err == nil || id != 0xabcd {
		t.Fatalf("3-byte G: id %d err %v", id, err)
	}
}

func TestParseDeviceId(t *testing.T) {
	id, err := ParseDeviceId("E6614103e7452d2f")
	if err != nil || id.String() != "e6614103e7452d2f" {
		t.Fatalf("ParseDeviceId = %s, %v", id, err)
	}
	for _, bad := range []string{"", "e6614103e7452d2", "e6614103e7452d2f0", "g6614103e7452d2f"} {
		if _, err := ParseDeviceId(bad); !errors.Is(err, ErrInvalidDeviceId) {
			t.Errorf("ParseDeviceId(%q): err = %v", bad, err)
		}
	}
}

func TestEnumStrings(t *testing.T) {
	checks := map[string]string{
		KindCircularWipe.String():   "circular_wipe",
		KindFade.String():           "fade",
		TargetInnerRing.String():    "inner_ring",
		DirModeAlternating.String(): "alternating",
		DirReverse.String():         "reverse",
		AckCancelled.String():       "cancelled",
		AckStatus(9).String():       "status(9)",
		TypeAnimation.String():      "A",
	}
	for got, want := range checks {
		if got != want {
			t.Errorf("String() = %q, want %q", got, want)
		}
	}
}
