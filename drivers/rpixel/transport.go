package rpixel

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/charmbracelet/log"
)

// ErrNoReply is returned when a request got no matching R or S after every retry.
var ErrNoReply = errors.New("rpixel: no reply")

// errTransportClosed is returned to requests still waiting when the socket closes.
var errTransportClosed = errors.New("rpixel: transport closed")

// reply is one frame delivered to a waiting request.
type reply struct {
	frame Frame
	from  *net.UDPAddr
}

// pendingRequest is registered while a request waits for its reply. Its
// channel is buffered and private to the request: the reader delivers into it
// without blocking and never closes it, so a reply that arrives after the
// requester gave up can never block the reader or panic on a closed channel
// (see CLAUDE.md "Accepted Open Issues #2").
type pendingRequest struct {
	ip net.IP
	ch chan reply
}

// transport owns the driver's single UDP socket. All requests to all devices
// go out through it, so request IDs come from one driver-wide counter and
// replies are matched by ID and source IP. One reader goroutine runs until the
// socket is closed.
type transport struct {
	conn       *net.UDPConn
	logger     *log.Logger
	retryWaits []time.Duration // time to wait for a reply after each send

	mu          sync.Mutex
	nextId      uint16
	pending     map[uint16]*pendingRequest
	seen        map[string]*net.UDPAddr // device id (hex) -> source of its last S
	seenChanged chan struct{}           // closed and replaced whenever seen changes

	readerDone chan struct{} // closed when the reader goroutine exits
}

// newTransport opens a UDP socket on an ephemeral port. Go enables
// SO_BROADCAST on UDP sockets, so the same socket serves discovery broadcasts.
func newTransport(logger *log.Logger, retryWaits []time.Duration) (*transport, error) {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{})
	if err != nil {
		return nil, err
	}
	t := &transport{
		conn:        conn,
		logger:      logger,
		retryWaits:  retryWaits,
		pending:     make(map[uint16]*pendingRequest),
		seen:        make(map[string]*net.UDPAddr),
		seenChanged: make(chan struct{}),
		readerDone:  make(chan struct{}),
	}
	go t.readLoop()
	return t, nil
}

// close closes the socket and waits for the reader to exit. Requests still
// waiting return errTransportClosed.
func (t *transport) close() error {
	err := t.conn.Close()
	<-t.readerDone
	return err
}

func (t *transport) readLoop() {
	defer close(t.readerDone)
	// One byte over the maximum, so an oversized datagram is detected rather
	// than silently truncated to a valid-looking length.
	buf := make([]byte, MaxFrameLen+1)
	for {
		n, from, err := t.conn.ReadFromUDP(buf)
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			// Transient errors (e.g. an ICMP-induced error on some platforms)
			// must not end the reader; back off briefly so a persistent error
			// cannot spin.
			t.logger.Debug("udp read error", "err", err)
			time.Sleep(10 * time.Millisecond)
			continue
		}
		t.handleDatagram(buf[:n], from)
	}
}

func (t *transport) handleDatagram(b []byte, from *net.UDPAddr) {
	f, err := DecodeFrame(b)
	if err != nil {
		t.logger.Debug("dropping invalid frame", "from", from, "len", len(b), "err", err)
		return
	}
	if f.Type != TypeAck && f.Type != TypeStatus {
		t.logger.Debug("ignoring unexpected packet type", "from", from, "type", f.Type)
		return
	}
	// buf is reused for the next datagram, so the payload must be copied
	// before it leaves the reader.
	f.Payload = append([]byte(nil), f.Payload...)
	from = copyAddr(from)

	if f.Type == TypeStatus {
		if st, err := ParseStatus(f.Payload); err == nil {
			t.noteSeen(st.DeviceId, from)
		}
	}
	t.deliver(f, from)
}

// deliver hands a reply to the request waiting for its ID, provided it comes
// from the IP the request was sent to. It never blocks.
func (t *transport) deliver(f Frame, from *net.UDPAddr) {
	id, ok := f.RequestId()
	if !ok {
		t.logger.Debug("dropping reply without request id", "from", from, "type", f.Type)
		return
	}
	t.mu.Lock()
	p := t.pending[id]
	t.mu.Unlock()
	if p == nil || !p.ip.Equal(from.IP) {
		// A late reply (e.g. the terminal "completed" for an A), a reply to a
		// discovery broadcast, or a reply from an unexpected host.
		t.logger.Debug("dropping unmatched reply", "from", from, "type", f.Type, "reqId", id)
		return
	}
	select {
	case p.ch <- reply{frame: f, from: from}:
	default:
		t.logger.Debug("dropping duplicate reply", "from", from, "type", f.Type, "reqId", id)
	}
}

func (t *transport) noteSeen(id DeviceId, from *net.UDPAddr) {
	key := id.String()
	t.mu.Lock()
	defer t.mu.Unlock()
	if old := t.seen[key]; old != nil && old.IP.Equal(from.IP) && old.Port == from.Port {
		return
	}
	t.seen[key] = from
	close(t.seenChanged)
	t.seenChanged = make(chan struct{})
}

// lookupSeen returns the source address of the last S from id, or nil.
func (t *transport) lookupSeen(id DeviceId) *net.UDPAddr {
	t.mu.Lock()
	defer t.mu.Unlock()
	return copyAddr(t.seen[id.String()])
}

// waitSeen waits up to timeout for an S from id to have been seen.
func (t *transport) waitSeen(ctx context.Context, id DeviceId, timeout time.Duration) *net.UDPAddr {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	key := id.String()
	for {
		t.mu.Lock()
		addr, changed := t.seen[key], t.seenChanged
		t.mu.Unlock()
		if addr != nil {
			return copyAddr(addr)
		}
		select {
		case <-changed:
		case <-timer.C:
			return nil
		case <-ctx.Done():
			return nil
		case <-t.readerDone:
			return nil
		}
	}
}

// allocIdLocked returns the next request ID not currently waiting for a reply.
// The counter is driver-wide and wraps; at our request rate an ID cannot come
// round again within the device's 5 s replay window. Callers hold t.mu.
func (t *transport) allocIdLocked() (uint16, error) {
	for i := 0; i <= 0xffff; i++ {
		t.nextId++
		if _, busy := t.pending[t.nextId]; !busy {
			return t.nextId, nil
		}
	}
	return 0, errors.New("rpixel: no free request id")
}

// request sends the frame built for a fresh request ID to addr and waits for an
// R or S carrying that ID from addr's IP. With no reply, the identical frame
// (same ID, so the device replays rather than re-runs an A) is resent after
// each retry wait; the whole exchange is bounded by the sum of the waits, and
// ctx or closing the transport aborts it early.
func (t *transport) request(ctx context.Context, addr *net.UDPAddr, build func(reqId uint16) ([]byte, error)) (Frame, error) {
	p := &pendingRequest{ip: addr.IP, ch: make(chan reply, 1)}
	t.mu.Lock()
	id, err := t.allocIdLocked()
	if err == nil {
		t.pending[id] = p
	}
	t.mu.Unlock()
	if err != nil {
		return Frame{}, err
	}
	defer func() {
		t.mu.Lock()
		delete(t.pending, id)
		t.mu.Unlock()
	}()

	b, err := build(id)
	if err != nil {
		return Frame{}, err
	}

	for attempt, wait := range t.retryWaits {
		if attempt > 0 {
			t.logger.Debug("no reply, resending", "to", addr, "type", PacketType(b[2]), "reqId", id, "attempt", attempt+1)
		}
		if _, err := t.conn.WriteToUDP(b, addr); err != nil {
			return Frame{}, fmt.Errorf("rpixel: send to %s: %w", addr, err)
		}
		timer := time.NewTimer(wait)
		select {
		case r := <-p.ch:
			timer.Stop()
			return r.frame, nil
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return Frame{}, ctx.Err()
		case <-t.readerDone:
			timer.Stop()
			return Frame{}, errTransportClosed
		}
	}
	return Frame{}, fmt.Errorf("%w from %s after %d attempts", ErrNoReply, addr, len(t.retryWaits))
}

// broadcastStatusRequest sends one G to addr (normally a broadcast address).
// It registers nothing: every S it provokes only updates seen.
func (t *transport) broadcastStatusRequest(addr *net.UDPAddr) error {
	t.mu.Lock()
	id, err := t.allocIdLocked()
	t.mu.Unlock()
	if err != nil {
		return err
	}
	_, err = t.conn.WriteToUDP(EncodeStatusRequest(id), addr)
	return err
}

func copyAddr(a *net.UDPAddr) *net.UDPAddr {
	if a == nil {
		return nil
	}
	return &net.UDPAddr{IP: append(net.IP(nil), a.IP...), Port: a.Port, Zone: a.Zone}
}
