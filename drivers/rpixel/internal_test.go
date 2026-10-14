package rpixel

import (
	"net"
	"testing"
	"time"

	"github.com/hubertat/swkit/logging"
)

func TestDefaultRetryScheduleIsBounded(t *testing.T) {
	var total time.Duration
	for _, w := range defaultTiming().retryWaits {
		total += w
	}
	// Bounded, and well inside the device's 5 s replay window.
	if total != 1750*time.Millisecond {
		t.Fatalf("default retry schedule totals %s, want 1.75s", total)
	}
}

func TestOfflineThreshold(t *testing.T) {
	for _, c := range []struct {
		pollMs uint
		want   time.Duration
	}{
		{0, 6 * time.Second},     // default 2 s poll: 3x = 6 s
		{1000, 6 * time.Second},  // floor
		{5000, 15 * time.Second}, // 3x poll
	} {
		d := &Driver{PollIntervalMs: c.pollMs, timing: defaultTiming()}
		if err := d.buildDevices(); err != nil {
			t.Fatalf("buildDevices: %v", err)
		}
		if d.offlineAfter != c.want {
			t.Errorf("poll %d ms: offline after %s, want %s", c.pollMs, d.offlineAfter, c.want)
		}
	}
}

func newTestTransport(t *testing.T) *transport {
	t.Helper()
	tr, err := newTransport(logging.NewLogger(logging.PrefixRpixel), testTiming().retryWaits)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tr.close() })
	return tr
}

func TestTransportDeliverMatchesIdAndSource(t *testing.T) {
	tr := newTestTransport(t)
	want := net.IPv4(10, 0, 0, 1)
	p := &pendingRequest{ip: want, ch: make(chan reply, 1)}
	tr.mu.Lock()
	tr.pending[5] = p
	tr.mu.Unlock()

	ack := func(id uint16) Frame {
		f, _ := DecodeFrame(EncodeAck(Ack{ReqId: id}))
		return f
	}

	tr.deliver(ack(5), &net.UDPAddr{IP: net.IPv4(10, 0, 0, 2), Port: DefaultPort})
	tr.deliver(ack(6), &net.UDPAddr{IP: want, Port: DefaultPort})
	select {
	case r := <-p.ch:
		t.Fatalf("delivered a reply from the wrong source or id: %+v", r)
	default:
	}

	tr.deliver(ack(5), &net.UDPAddr{IP: want, Port: DefaultPort})
	// A duplicate must not block the reader even though nobody reads.
	done := make(chan struct{})
	go func() {
		tr.deliver(ack(5), &net.UDPAddr{IP: want, Port: DefaultPort})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("deliver blocked on a full reply channel")
	}
	if r := <-p.ch; r.frame.Type != TypeAck {
		t.Fatalf("delivered %+v", r)
	}
}

func TestTransportRequestIdsWrapAndSkipPending(t *testing.T) {
	tr := newTestTransport(t)
	tr.mu.Lock()
	defer tr.mu.Unlock()
	tr.nextId = 0xfffe
	tr.pending[0] = &pendingRequest{}
	for _, want := range []uint16{0xffff, 1, 2} {
		got, err := tr.allocIdLocked()
		if err != nil || got != want {
			t.Fatalf("allocId = %d, %v; want %d", got, err, want)
		}
	}
}

func TestTransportSeenFromStatus(t *testing.T) {
	tr := newTestTransport(t)
	id := DeviceId{1, 2, 3, 4, 5, 6, 7, 8}
	from := &net.UDPAddr{IP: net.IPv4(192, 168, 1, 50), Port: DefaultPort}

	got := make(chan *net.UDPAddr, 1)
	go func() { got <- tr.waitSeen(t.Context(), id, time.Second) }()

	raw := EncodeStatus(Status{ReqId: 1, ProtocolVersion: 1, DeviceId: id})
	tr.handleDatagram(raw, from)

	select {
	case a := <-got:
		if a == nil || !a.IP.Equal(from.IP) || a.Port != from.Port {
			t.Fatalf("waitSeen = %v, want %v", a, from)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("waitSeen did not return")
	}
	if a := tr.lookupSeen(id); a == nil || !a.IP.Equal(from.IP) {
		t.Fatalf("lookupSeen = %v", a)
	}
	if a := tr.waitSeen(t.Context(), DeviceId{9}, 20*time.Millisecond); a != nil {
		t.Fatalf("waitSeen for an unseen id = %v", a)
	}
}
