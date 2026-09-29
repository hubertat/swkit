package rpixel

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/log"
	"github.com/hubertat/swkit/drivers"
	"github.com/hubertat/swkit/logging"
)

// DriverName is the driver segment of rpixel IO ids: rpixel|<type>|<device name>.
const DriverName = "rpixel"

const (
	defaultPollIntervalMs   = 2000
	defaultBroadcastAddress = "255.255.255.255"
	defaultFadeMs           = 500
	defaultWipeMs           = 2400 // matches the firmware's own HTTP on/off wipe
	firmwareBrightness      = 60   // brightness the firmware boots with
)

// defaultOnColour is used for "on" until a colour has been seen or requested:
// white 255, the colour the firmware boots to and its HTTP /set/on uses.
var defaultOnColour = Colour{W: 255}

// timing holds every delay the driver uses. Tests shrink it; production uses
// defaultTiming.
type timing struct {
	retryWaits     []time.Duration // reply wait after each send of one request
	offlineMin     time.Duration   // lower bound of the offline threshold
	resolveEvery   time.Duration   // re-resolve a healthy device this often
	resolveTimeout time.Duration   // bound on one DNS lookup
	broadcastEvery time.Duration   // at most one discovery broadcast per this, per driver
	discoveryWait  time.Duration   // how long a poll waits for a broadcast reply
	initialWait    time.Duration   // bound on Setup's initial status round
}

func defaultTiming() *timing {
	return &timing{
		// Sends at 0, 250 ms and 750 ms; gives up at 1.75 s, well inside the
		// device's 5 s replay window, so a retried A replays its result.
		retryWaits:     []time.Duration{250 * time.Millisecond, 500 * time.Millisecond, time.Second},
		offlineMin:     6 * time.Second,
		resolveEvery:   60 * time.Second,
		resolveTimeout: 2 * time.Second,
		broadcastEvery: 5 * time.Second,
		discoveryWait:  time.Second,
		initialWait:    2 * time.Second,
	}
}

// Driver implements drivers.IoDriver for rpixels rings. Each configured device
// is one ring, exposed as up to three views onto the same state:
//
//	rpixel|d_out|<name>     on/off (Light, Outlet, ...)
//	rpixel|a_out|<name>     strip brightness 0..255 (DimmableLight)
//	rpixel|rgbw_out|<name>  colour (ColorLight)
//
// Exported fields are the JSON config; Setup never writes them, so a config
// save round-trips exactly what the user wrote.
type Driver struct {
	Devices          []DeviceConfig
	PollIntervalMs   uint   `json:",omitempty"` // default 2000
	BroadcastAddress string `json:",omitempty"` // default "255.255.255.255"; host or host:port, used only for DeviceId discovery

	logger *log.Logger
	timing *timing // nil until Setup, unless a test set it

	// Immutable after Setup.
	tr            *transport
	devices       []*device
	byName        map[string]*device
	pollInterval  time.Duration
	offlineAfter  time.Duration
	broadcastAddr *net.UDPAddr
	ctx           context.Context
	cancel        context.CancelFunc
	wg            sync.WaitGroup
	closeOnce     sync.Once

	// mu guards isReady, lastBroadcast and every device's mutable state. It is
	// never held across network I/O or a callback.
	mu            sync.RWMutex
	isReady       bool
	lastBroadcast time.Time
}

// DeviceConfig configures one ring.
type DeviceConfig struct {
	// Name is the IO name: rpixel|d_out|<Name>. Required and unique. It may
	// not contain ':' (reserved for a future ring-segment suffix) or '|'.
	Name string
	// Address is a host name, IP, or host:port (default port 8888), for
	// example "rpxl-e6614103e7452d2f.local". Resolved with the system
	// resolver, so .local names work where nss-mdns is installed.
	Address string `json:",omitempty"`
	// DeviceId is the optional 16 hex digit board ID. When set, every S must
	// carry it. Required when Address is empty: the device is then found by
	// broadcasting G to BroadcastAddress.
	DeviceId string `json:",omitempty"`
	// Effect is how on/off is shown: "fade" (default), "wipe" or "immediate".
	Effect string `json:",omitempty"`
	// TransitionMs is the on/off (and, for fade, brightness/colour) duration.
	// 0 means the effect's default: fade 500, wipe 2400. Ignored for immediate.
	TransitionMs uint `json:",omitempty"`
	// DefaultBrightness is the "on" brightness until one is known. 0 means 60,
	// the firmware default.
	DefaultBrightness uint8 `json:",omitempty"`
}

type effect int

const (
	effectFade effect = iota
	effectWipe
	effectImmediate
)

func parseEffect(s string) (effect, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "fade":
		return effectFade, nil
	case "wipe":
		return effectWipe, nil
	case "immediate":
		return effectImmediate, nil
	default:
		return 0, fmt.Errorf("unknown effect %q, want fade, wipe or immediate", s)
	}
}

// display is the settled state of one ring, as last reported by S or
// optimistically set by an accepted command.
type display struct {
	on         bool
	brightness uint8
	colour     Colour // zero when off or mixed
	mixed      bool
}

// device is the runtime state of one configured ring.
type device struct {
	index int
	name  string
	cfg   DeviceConfig

	// Resolved config, immutable after Setup.
	host         string // empty when found by DeviceId only
	port         int
	wantId       *DeviceId
	effect       effect
	transitionMs uint32
	defaultBri   uint8

	// cmdMu serialises commands to this device. It is held across the
	// request, but never together with Driver.mu.
	cmdMu sync.Mutex

	// notifyMu serialises on-state callbacks so they always end on the
	// latest state; lastNotified is guarded by it.
	notifyMu     sync.Mutex
	notified     bool
	lastNotified bool

	// Guarded by Driver.mu.
	addr          *net.UDPAddr
	lastResolve   time.Time
	lastSeen      time.Time // last accepted S; zero when never seen
	reportedId    *DeviceId
	rejected      string // why the last S was rejected; empty when it was accepted
	pollFailing   bool
	disp          display
	onColour      Colour // colour memory for "on"; zero means defaultOnColour
	briMemory     uint8  // last non-zero brightness; zero means defaultBri
	onChanged     time.Time
	briChanged    time.Time
	cmdGen        uint64 // bumped by every command
	cmdInFlight   bool
	onStateUpdate func(bool)

	dOut *digitalOut
	aOut *analogOut
	rgbw *rgbwOut
}

var (
	_ drivers.IoDriver             = (*Driver)(nil)
	_ drivers.IoDebugProvider      = (*Driver)(nil)
	_ drivers.IoOutputToggler      = (*Driver)(nil)
	_ drivers.IoAnalogOutputSetter = (*Driver)(nil)
)

func (d *Driver) String() string {
	return DriverName
}

func (d *Driver) IsReady() bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.isReady
}

// Setup validates the config and the requested ios, opens the socket, starts
// one poll goroutine per device and waits (bounded) for a first status round.
// Only config errors and a socket failure fail Setup: an unreachable ring is a
// warning, so one offline ring cannot stop swkit.
func (d *Driver) Setup(ctx context.Context, ios []string) error {
	d.logger = logging.NewLogger(logging.PrefixRpixel)
	if d.timing == nil {
		d.timing = defaultTiming()
	}

	if err := d.buildDevices(); err != nil {
		return err
	}
	if err := d.checkIos(ios); err != nil {
		return err
	}

	tr, err := newTransport(d.logger, d.timing.retryWaits)
	if err != nil {
		return errors.Join(err, errors.New("rpixel driver: failed to open udp socket"))
	}
	d.tr = tr
	d.ctx, d.cancel = context.WithCancel(ctx)

	d.logger.Debug("setup starting", "devices", len(d.devices), "pollInterval", d.pollInterval, "local", tr.conn.LocalAddr())

	firstRound := make([]chan struct{}, len(d.devices))
	for i, dev := range d.devices {
		firstRound[i] = make(chan struct{})
		d.wg.Add(1)
		go d.pollLoop(dev, firstRound[i])
	}

	d.mu.Lock()
	d.isReady = true
	d.mu.Unlock()

	// Wait for every device's first poll so the first Sync already has state,
	// but never longer than initialWait in total.
	deadline := time.NewTimer(d.timing.initialWait)
	defer deadline.Stop()
wait:
	for _, done := range firstRound {
		select {
		case <-done:
		case <-deadline.C:
			break wait
		case <-d.ctx.Done():
			break wait
		}
	}

	online := 0
	for _, dev := range d.devices {
		if _, err := dev.dOut.GetState(); err != nil {
			d.logger.Warn("device not available yet, will keep polling", "device", dev.name, "address", dev.cfg.Address, "deviceId", dev.cfg.DeviceId, "err", err)
		} else {
			online++
		}
	}
	d.logger.Info("setup complete", "devices", len(d.devices), "online", online)
	return nil
}

// buildDevices validates the config and fills the immutable runtime fields.
func (d *Driver) buildDevices() error {
	d.pollInterval = time.Duration(d.PollIntervalMs) * time.Millisecond
	if d.PollIntervalMs == 0 {
		d.pollInterval = defaultPollIntervalMs * time.Millisecond
	}
	d.offlineAfter = max(3*d.pollInterval, d.timing.offlineMin)

	needBroadcast := false
	d.byName = make(map[string]*device, len(d.Devices))
	d.devices = nil
	for i, cfg := range d.Devices {
		dev, err := newDevice(i, cfg)
		if err != nil {
			return errors.Join(err, fmt.Errorf("rpixel driver: invalid device #%d (%q)", i, cfg.Name))
		}
		if _, dup := d.byName[dev.name]; dup {
			return fmt.Errorf("rpixel driver: duplicate device name %q", dev.name)
		}
		dev.dOut = &digitalOut{d: d, dev: dev}
		dev.aOut = &analogOut{d: d, dev: dev}
		dev.rgbw = &rgbwOut{d: d, dev: dev}
		d.byName[dev.name] = dev
		d.devices = append(d.devices, dev)
		needBroadcast = needBroadcast || dev.host == ""
	}

	if needBroadcast {
		addr, err := parseBroadcastAddress(d.BroadcastAddress)
		if err != nil {
			return errors.Join(err, fmt.Errorf("rpixel driver: invalid BroadcastAddress %q", d.BroadcastAddress))
		}
		d.broadcastAddr = addr
	}
	return nil
}

func newDevice(index int, cfg DeviceConfig) (*device, error) {
	dev := &device{index: index, name: cfg.Name, cfg: cfg}

	if cfg.Name == "" {
		return nil, errors.New("name is required")
	}
	if strings.ContainsAny(cfg.Name, ":|") {
		return nil, errors.New("name may not contain ':' or '|'")
	}

	if cfg.DeviceId != "" {
		id, err := ParseDeviceId(cfg.DeviceId)
		if err != nil {
			return nil, err
		}
		dev.wantId = &id
	}

	if cfg.Address == "" {
		if dev.wantId == nil {
			return nil, errors.New("either Address or DeviceId is required")
		}
	} else {
		host, port, err := splitHostPort(cfg.Address, DefaultPort)
		if err != nil {
			return nil, err
		}
		dev.host, dev.port = host, port
	}

	eff, err := parseEffect(cfg.Effect)
	if err != nil {
		return nil, err
	}
	dev.effect = eff
	if cfg.TransitionMs > MaxStageDurationMs {
		return nil, fmt.Errorf("TransitionMs %d exceeds %d", cfg.TransitionMs, MaxStageDurationMs)
	}
	switch {
	case eff == effectImmediate:
		dev.transitionMs = 0
	case cfg.TransitionMs != 0:
		dev.transitionMs = uint32(cfg.TransitionMs)
	case eff == effectWipe:
		dev.transitionMs = defaultWipeMs
	default:
		dev.transitionMs = defaultFadeMs
	}

	dev.defaultBri = cfg.DefaultBrightness
	if dev.defaultBri == 0 {
		dev.defaultBri = firmwareBrightness
	}
	return dev, nil
}

// splitHostPort accepts "host" or "host:port". A bare IPv6 literal is left
// for the resolver to reject, since the socket is IPv4 only.
func splitHostPort(s string, defaultPort int) (string, int, error) {
	host, portStr, err := net.SplitHostPort(s)
	if err != nil {
		return s, defaultPort, nil
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		return "", 0, fmt.Errorf("invalid port in address %q", s)
	}
	if host == "" {
		return "", 0, fmt.Errorf("missing host in address %q", s)
	}
	return host, port, nil
}

func parseBroadcastAddress(s string) (*net.UDPAddr, error) {
	if s == "" {
		s = defaultBroadcastAddress
	}
	host, port, err := splitHostPort(s, DefaultPort)
	if err != nil {
		return nil, err
	}
	ip, err := netip.ParseAddr(host)
	if err != nil || !ip.Unmap().Is4() {
		return nil, errors.New("broadcast address must be an IPv4 address")
	}
	return &net.UDPAddr{IP: ip.Unmap().AsSlice(), Port: port}, nil
}

// checkIos verifies every io id handed to Setup names a configured device
// with a supported type.
func (d *Driver) checkIos(ios []string) error {
	for _, io := range ios {
		driver, ioType, name, err := drivers.ResolveIoIdString(io)
		if err != nil {
			return errors.Join(err, fmt.Errorf("rpixel driver: invalid io id format: %s", io))
		}
		if !strings.EqualFold(driver, d.String()) {
			return fmt.Errorf("rpixel driver: driver name mismatch, expected %s, got %s", d.String(), driver)
		}
		switch ioType {
		case drivers.IoTypeDigitalOutput, drivers.IoTypeAnalogOutput, drivers.IoTypeRgbwOutput:
		default:
			return fmt.Errorf("rpixel driver: unsupported io type %s in %s (supported: d_out, a_out, rgbw_out)", ioType.String(), io)
		}
		if _, ok := d.byName[name]; !ok {
			return fmt.Errorf("rpixel driver: io %s names unknown device %q", io, name)
		}
	}
	return nil
}

// Close stops polling, closes the socket and waits for every goroutine. It
// sends nothing to the devices: a ring keeps showing what it shows.
func (d *Driver) Close() error {
	var err error
	d.closeOnce.Do(func() {
		d.mu.Lock()
		d.isReady = false
		d.mu.Unlock()

		if d.cancel != nil {
			d.cancel()
		}
		if d.tr != nil {
			err = d.tr.close()
		}
		d.wg.Wait()
	})
	return err
}

// ---- health ----

// stateErrLocked returns nil when dev's cached state can be trusted.
// Callers hold d.mu.
func (d *Driver) stateErrLocked(dev *device, now time.Time) error {
	switch {
	case !d.isReady:
		return errors.New("rpixel driver: not ready")
	case dev.rejected != "":
		return fmt.Errorf("rpixel %s: status rejected: %s", dev.name, dev.rejected)
	case dev.lastSeen.IsZero():
		return fmt.Errorf("rpixel %s: device not seen yet", dev.name)
	case now.Sub(dev.lastSeen) >= d.offlineAfter:
		return fmt.Errorf("rpixel %s: device offline, last seen %s ago", dev.name, now.Sub(dev.lastSeen).Round(time.Millisecond))
	}
	return nil
}

func (d *Driver) healthy(dev *device) bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.stateErrLocked(dev, time.Now()) == nil
}

// ---- polling ----

func (d *Driver) pollLoop(dev *device, firstDone chan<- struct{}) {
	defer d.wg.Done()

	d.pollOnce(dev)
	close(firstDone)

	ticker := time.NewTicker(d.pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-d.ctx.Done():
			return
		case <-ticker.C:
			d.pollOnce(dev)
		}
	}
}

// pollOnce resolves dev when needed, sends G and applies the S.
func (d *Driver) pollOnce(dev *device) {
	ctx := d.ctx
	err := func() error {
		addr, err := d.resolve(ctx, dev)
		if err != nil {
			return err
		}
		// Capture the command generation before G goes out: an S answering
		// a G sent before a later command is stale.
		d.mu.RLock()
		gen := dev.cmdGen
		d.mu.RUnlock()

		f, err := d.tr.request(ctx, addr, func(id uint16) ([]byte, error) {
			return EncodeStatusRequest(id), nil
		})
		if err != nil {
			return err
		}
		switch f.Type {
		case TypeStatus:
			st, err := ParseStatus(f.Payload)
			if err != nil {
				return err
			}
			d.applyStatus(dev, st, gen)
			d.notify(dev)
			return nil
		default:
			ack, err := ParseAck(f.Payload)
			if err != nil {
				return err
			}
			return fmt.Errorf("status request answered with R %s (detail %d)", ack.Status, ack.Detail)
		}
	}()
	if ctx.Err() != nil {
		return // shutting down; nothing worth logging
	}

	d.mu.Lock()
	wasFailing := dev.pollFailing
	dev.pollFailing = err != nil
	d.mu.Unlock()
	switch {
	case err != nil && !wasFailing:
		d.logger.Warn("device poll failed", "device", dev.name, "err", err)
	case err != nil:
		d.logger.Debug("device poll failed", "device", dev.name, "err", err)
	case wasFailing:
		d.logger.Info("device poll recovered", "device", dev.name)
	}
}

// resolve returns dev's address, looking it up again when it is unknown, the
// device is unhealthy, or the last lookup is older than resolveEvery. A failed
// re-resolve keeps the previous address.
func (d *Driver) resolve(ctx context.Context, dev *device) (*net.UDPAddr, error) {
	now := time.Now()
	d.mu.RLock()
	addr := dev.addr
	fresh := addr != nil && now.Sub(dev.lastResolve) < d.timing.resolveEvery
	healthy := d.stateErrLocked(dev, now) == nil
	d.mu.RUnlock()
	if fresh && healthy {
		return addr, nil
	}

	var found *net.UDPAddr
	var err error
	if dev.host != "" {
		found, err = d.lookupHost(ctx, dev)
	} else {
		found, err = d.discover(ctx, dev, healthy)
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	dev.lastResolve = now
	if err != nil {
		if dev.addr != nil {
			d.logger.Debug("re-resolve failed, keeping previous address", "device", dev.name, "address", dev.addr, "err", err)
			return copyAddr(dev.addr), nil
		}
		return nil, err
	}
	if dev.addr == nil || !dev.addr.IP.Equal(found.IP) || dev.addr.Port != found.Port {
		d.logger.Info("device address resolved", "device", dev.name, "address", found)
	}
	dev.addr = found
	return copyAddr(found), nil
}

func (d *Driver) lookupHost(ctx context.Context, dev *device) (*net.UDPAddr, error) {
	if ip, err := netip.ParseAddr(dev.host); err == nil {
		if !ip.Unmap().Is4() {
			return nil, fmt.Errorf("rpixel %s: %s is not an IPv4 address", dev.name, dev.host)
		}
		return &net.UDPAddr{IP: ip.Unmap().AsSlice(), Port: dev.port}, nil
	}
	ctx, cancel := context.WithTimeout(ctx, d.timing.resolveTimeout)
	defer cancel()
	ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip4", dev.host)
	if err != nil {
		return nil, fmt.Errorf("rpixel %s: resolve %s: %w", dev.name, dev.host, err)
	}
	for _, ip := range ips {
		if ip.Unmap().Is4() {
			return &net.UDPAddr{IP: ip.Unmap().AsSlice(), Port: dev.port}, nil
		}
	}
	return nil, fmt.Errorf("rpixel %s: %s has no IPv4 address", dev.name, dev.host)
}

// discover finds a device configured only by DeviceId among the sources of S
// replies seen so far, broadcasting G (rate-limited per driver) when it has not
// been seen or is unhealthy, and waiting briefly for an answer.
func (d *Driver) discover(ctx context.Context, dev *device, healthy bool) (*net.UDPAddr, error) {
	addr := d.tr.lookupSeen(*dev.wantId)
	if addr == nil || !healthy {
		d.maybeBroadcast()
	}
	if addr == nil {
		addr = d.tr.waitSeen(ctx, *dev.wantId, d.timing.discoveryWait)
	}
	if addr == nil {
		return nil, fmt.Errorf("rpixel %s: device %s not discovered yet (broadcast to %s)", dev.name, dev.wantId, d.broadcastAddr)
	}
	return addr, nil
}

func (d *Driver) maybeBroadcast() {
	d.mu.Lock()
	if !d.lastBroadcast.IsZero() && time.Since(d.lastBroadcast) < d.timing.broadcastEvery {
		d.mu.Unlock()
		return
	}
	d.lastBroadcast = time.Now()
	d.mu.Unlock()

	if err := d.tr.broadcastStatusRequest(d.broadcastAddr); err != nil {
		d.logger.Debug("discovery broadcast failed", "to", d.broadcastAddr, "err", err)
	}
}

// applyStatus updates dev from an S answering a G sent at command generation gen.
func (d *Driver) applyStatus(dev *device, st Status, gen uint64) {
	d.mu.Lock()
	defer d.mu.Unlock()

	reason := ""
	switch {
	case st.ProtocolVersion != ProtocolVersion:
		reason = fmt.Sprintf("protocol version %d, want %d", st.ProtocolVersion, ProtocolVersion)
	case dev.wantId != nil && st.DeviceId != *dev.wantId:
		reason = fmt.Sprintf("device id %s, want %s", st.DeviceId, dev.wantId)
	}
	if reason != "" {
		if dev.rejected != reason {
			d.logger.Warn("rejecting device status, marking device unhealthy", "device", dev.name, "reason", reason)
		}
		dev.rejected = reason
		return
	}
	if dev.rejected != "" {
		d.logger.Info("device status accepted again", "device", dev.name)
		dev.rejected = ""
	}

	now := time.Now()
	dev.lastSeen = now
	id := st.DeviceId
	dev.reportedId = &id

	// The device is alive either way, but a stale S must not undo a command's
	// optimistic state: drop it while a command is in flight, or when its G
	// went out before the latest command.
	if dev.cmdInFlight || gen != dev.cmdGen {
		d.logger.Debug("dropping stale status", "device", dev.name, "inFlight", dev.cmdInFlight, "gen", gen, "cmdGen", dev.cmdGen)
		return
	}

	d.applyDisplayLocked(dev, display{
		on:         st.On(),
		brightness: st.Brightness,
		colour:     st.Colour,
		mixed:      st.Mixed(),
	}, now)
}

// applyDisplayLocked sets dev's settled state and updates the brightness and
// colour memories. A mixed or zero colour leaves the colour memory alone, per
// the spec's client guidance. Callers hold d.mu.
func (d *Driver) applyDisplayLocked(dev *device, next display, now time.Time) {
	if next.on != dev.disp.on {
		dev.onChanged = now
	}
	if next.brightness != dev.disp.brightness {
		dev.briChanged = now
	}
	dev.disp = next
	if next.brightness != 0 {
		dev.briMemory = next.brightness
	}
	if !next.mixed && !next.colour.IsZero() {
		dev.onColour = next.colour
	}
}

// notify fires dev's on-state callback when the on state differs from the
// last one reported. It runs outside d.mu, and notifyMu keeps concurrent
// notifications ordered so the last callback always carries the latest state.
func (d *Driver) notify(dev *device) {
	dev.notifyMu.Lock()
	defer dev.notifyMu.Unlock()

	d.mu.RLock()
	on, cb := dev.disp.on, dev.onStateUpdate
	d.mu.RUnlock()
	if cb == nil || (dev.notified && dev.lastNotified == on) {
		return
	}
	dev.notified, dev.lastNotified = true, on
	cb(on)
}

// ---- commands ----

// onBrightnessLocked is the brightness "on" uses: the last non-zero one, else
// the configured default.
func (dev *device) onBrightnessLocked() uint8 {
	if dev.briMemory != 0 {
		return dev.briMemory
	}
	return dev.defaultBri
}

// onColourLocked is the colour "on" uses: the last non-zero unmixed one, else
// white.
func (dev *device) onColourLocked() Colour {
	if dev.onColour.IsZero() {
		return defaultOnColour
	}
	return dev.onColour
}

// fade is a one-stage full-ring fade, used for brightness and colour changes.
// It takes TransitionMs for the fade effect and is immediate otherwise.
func (dev *device) fade(c Colour, bri uint8) Animation {
	var ms uint32
	if dev.effect == effectFade {
		ms = dev.transitionMs
	}
	return Animation{
		Kind:            KindFade,
		Target:          TargetFullRing,
		StageDurationMs: ms,
		Stages:          []Stage{{Colour: c, Brightness: bri}},
	}
}

// onOff is the animation for turning the ring on (to c) or off (c zero).
func (dev *device) onOff(on bool, c Colour, bri uint8) Animation {
	a := Animation{
		Kind:            KindFade,
		Target:          TargetFullRing,
		StageDurationMs: dev.transitionMs, // 0 for immediate
		Stages:          []Stage{{Colour: c, Brightness: bri}},
	}
	if dev.effect == effectWipe {
		// Forward for on and reverse for off, like the firmware's HTTP on/off.
		a.Kind = KindCircularWipe
		a.DirMode = DirModeFixed
		a.Dir = DirForward
		if !on {
			a.Dir = DirReverse
		}
	}
	return a
}

func (d *Driver) setOn(dev *device, on bool) error {
	return d.command(dev, "set on", func() (*Animation, display) {
		// Off keeps the brightness and only fades the colour to zero, so an
		// on/off cycle never loses the brightness.
		bri := dev.onBrightnessLocked()
		var c Colour
		if on {
			c = dev.onColourLocked()
		}
		a := dev.onOff(on, c, bri)
		return &a, display{on: on, brightness: bri, colour: c}
	})
}

func (d *Driver) setBrightness(dev *device, value int) error {
	bri := uint8(min(max(value, 0), 255))
	return d.command(dev, "set brightness", func() (*Animation, display) {
		on := dev.disp.on
		var c Colour
		if on {
			c = dev.onColourLocked()
		}
		a := dev.fade(c, bri)
		return &a, display{on: on && bri != 0, brightness: bri, colour: c}
	})
}

func (d *Driver) setColour(dev *device, c Colour) error {
	return d.command(dev, "set colour", func() (*Animation, display) {
		bri := dev.disp.brightness
		if c.IsZero() {
			// Zero means off: send it, keep the colour memory.
			a := dev.fade(Colour{}, bri)
			return &a, display{on: false, brightness: bri, colour: Colour{}}
		}
		dev.onColour = c
		if !dev.disp.on {
			return nil, dev.disp // only remembered, shown by the next "on"
		}
		a := dev.fade(c, bri)
		return &a, display{on: true, brightness: bri, colour: c}
	})
}

// command runs one command against dev. prepare runs under d.mu and returns
// the animation to send (nil to send nothing) and the settled state to assume
// once the device accepts it.
func (d *Driver) command(dev *device, what string, prepare func() (*Animation, display)) error {
	err := d.commandSerialised(dev, what, prepare)
	d.notify(dev)
	return err
}

func (d *Driver) commandSerialised(dev *device, what string, prepare func() (*Animation, display)) error {
	dev.cmdMu.Lock()
	defer dev.cmdMu.Unlock()

	d.mu.Lock()
	if !d.isReady {
		d.mu.Unlock()
		return errors.New("rpixel driver: not ready")
	}
	anim, next := prepare()
	if anim == nil {
		d.applyDisplayLocked(dev, next, time.Now())
		d.mu.Unlock()
		return nil
	}
	if dev.addr == nil {
		d.mu.Unlock()
		return fmt.Errorf("rpixel %s: %s: device address not known yet", dev.name, what)
	}
	// Never drive a ring that failed verification: with a DeviceId mismatch
	// the address now belongs to some other device.
	if dev.rejected != "" {
		reason := dev.rejected
		d.mu.Unlock()
		return fmt.Errorf("rpixel %s: %s: refusing to send, status rejected: %s", dev.name, what, reason)
	}
	if dev.wantId != nil && dev.reportedId == nil {
		d.mu.Unlock()
		return fmt.Errorf("rpixel %s: %s: device id %s not verified yet", dev.name, what, dev.wantId)
	}
	addr := copyAddr(dev.addr)
	dev.cmdGen++
	dev.cmdInFlight = true
	d.mu.Unlock()

	d.logger.Debug("sending animation", "device", dev.name, "cmd", what, "kind", anim.Kind, "dir", anim.Dir, "ms", anim.StageDurationMs, "stage", anim.Stages[0])
	ack, err := d.sendAnimation(addr, *anim)

	d.mu.Lock()
	defer d.mu.Unlock()
	dev.cmdInFlight = false
	if err != nil {
		return errors.Join(err, fmt.Errorf("rpixel %s: %s failed", dev.name, what))
	}
	switch ack.Status {
	case AckAccepted, AckCompleted:
		d.applyDisplayLocked(dev, next, time.Now())
	case AckCancelled:
		// Only seen when the accepted R was lost and the retry replayed a
		// later result: the command ran but something replaced it. The next
		// poll reports what is shown now.
		d.logger.Debug("command superseded before it was confirmed", "device", dev.name, "cmd", what)
	}
	return nil
}

// sendAnimation sends an A and waits for its first R. accepted, completed and
// cancelled are returned as success; rejected, unsupported or no reply are
// errors.
func (d *Driver) sendAnimation(addr *net.UDPAddr, a Animation) (Ack, error) {
	f, err := d.tr.request(d.ctx, addr, func(id uint16) ([]byte, error) {
		return EncodeAnimation(id, a)
	})
	if err != nil {
		return Ack{}, err
	}
	if f.Type != TypeAck {
		return Ack{}, fmt.Errorf("rpixel: animation answered with %s, want R", f.Type)
	}
	ack, err := ParseAck(f.Payload)
	if err != nil {
		return Ack{}, err
	}
	switch ack.Status {
	case AckAccepted, AckCompleted, AckCancelled:
		return ack, nil
	default:
		return ack, fmt.Errorf("rpixel: animation %s (detail %d)", ack.Status, ack.Detail)
	}
}

// ---- drivers.IoDriver getters ----

func (d *Driver) device(name string) (*device, error) {
	dev, ok := d.byName[name]
	if !ok {
		return nil, fmt.Errorf("rpixel driver: device %q not configured", name)
	}
	return dev, nil
}

func (d *Driver) GetDigitalOutput(id string) (drivers.DigitalOutput, error) {
	dev, err := d.device(id)
	if err != nil {
		return nil, err
	}
	return dev.dOut, nil
}

func (d *Driver) GetAnalogOutput(id string) (drivers.AnalogOutput, error) {
	dev, err := d.device(id)
	if err != nil {
		return nil, err
	}
	return dev.aOut, nil
}

func (d *Driver) GetRgbwOutput(id string) (drivers.RgbwOutput, error) {
	dev, err := d.device(id)
	if err != nil {
		return nil, err
	}
	return dev.rgbw, nil
}

func (d *Driver) GetDigitalInput(id string) (drivers.DigitalInput, error) {
	return nil, errors.New("rpixel driver: digital inputs not supported")
}

func (d *Driver) GetPushEventEmitter(id string) (drivers.PushEventEmitter, error) {
	return nil, errors.New("rpixel driver: push event emitters not supported")
}

// ---- optional interfaces ----

// GetIoDebugSnapshot returns, per device i, a d_out point (on/off) and an
// a_out point (brightness 0..255), both with Index i and the device name.
// Satisfies drivers.IoDebugProvider.
func (d *Driver) GetIoDebugSnapshot() drivers.IoDebugSnapshot {
	d.mu.RLock()
	defer d.mu.RUnlock()
	now := time.Now()
	points := make([]drivers.IoPointState, 0, 2*len(d.devices))
	for _, dev := range d.devices {
		healthy := d.stateErrLocked(dev, now) == nil
		points = append(points,
			drivers.IoPointState{
				Index:       dev.index,
				Name:        dev.name,
				Type:        drivers.IoTypeDigitalOutput,
				State:       dev.disp.on,
				Healthy:     healthy,
				LastChanged: dev.onChanged,
			},
			drivers.IoPointState{
				Index:       dev.index,
				Name:        dev.name,
				Type:        drivers.IoTypeAnalogOutput,
				Healthy:     healthy,
				LastChanged: dev.briChanged,
				Value:       int(dev.disp.brightness),
				Min:         0,
				Max:         255,
			},
		)
	}
	return drivers.IoDebugSnapshot{Points: points}
}

func (d *Driver) deviceByIndex(index int) (*device, error) {
	if index < 0 || index >= len(d.devices) {
		return nil, fmt.Errorf("rpixel driver: device index %d out of range (0-%d)", index, len(d.devices)-1)
	}
	return d.devices[index], nil
}

// ToggleOutput toggles device index's on state. Satisfies drivers.IoOutputToggler.
func (d *Driver) ToggleOutput(index int) error {
	dev, err := d.deviceByIndex(index)
	if err != nil {
		return err
	}
	on, err := dev.dOut.GetState()
	if err != nil {
		return err
	}
	return d.setOn(dev, !on)
}

// SetAnalogOutput sets device index's brightness (0..255). Satisfies
// drivers.IoAnalogOutputSetter.
func (d *Driver) SetAnalogOutput(index int, value int) error {
	dev, err := d.deviceByIndex(index)
	if err != nil {
		return err
	}
	return d.setBrightness(dev, value)
}

// Status returns a one-line summary. Satisfies swkit.DriverStatusProvider.
func (d *Driver) Status() string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	now := time.Now()
	online := 0
	for _, dev := range d.devices {
		if d.stateErrLocked(dev, now) == nil {
			online++
		}
	}
	return fmt.Sprintf("%d devices, %d online", len(d.devices), online)
}

// Details is the structured driver detail shown in the web UI.
type Details struct {
	Rings []DeviceDetails `json:"rings"`
}

// DeviceDetails describes one configured ring.
type DeviceDetails struct {
	Name       string     `json:"name"`
	Address    string     `json:"address,omitempty"`  // as configured
	Resolved   string     `json:"resolved,omitempty"` // ip:port in use
	DeviceId   string     `json:"device_id,omitempty"`
	Online     bool       `json:"online"`
	LastSeen   *time.Time `json:"last_seen,omitempty"`
	On         bool       `json:"on"`
	Brightness uint8      `json:"brightness"`
	Problem    string     `json:"problem,omitempty"`
}

// DriverDetails returns per-ring details. Satisfies swkit.DriverDetailProvider.
func (d *Driver) DriverDetails() interface{} {
	d.mu.RLock()
	defer d.mu.RUnlock()
	now := time.Now()
	details := Details{Rings: make([]DeviceDetails, 0, len(d.devices))}
	for _, dev := range d.devices {
		dd := DeviceDetails{
			Name:       dev.name,
			Address:    dev.cfg.Address,
			DeviceId:   dev.cfg.DeviceId,
			On:         dev.disp.on,
			Brightness: dev.disp.brightness,
		}
		if dev.reportedId != nil {
			dd.DeviceId = dev.reportedId.String()
		}
		if dev.addr != nil {
			dd.Resolved = dev.addr.String()
		}
		if !dev.lastSeen.IsZero() {
			seen := dev.lastSeen
			dd.LastSeen = &seen
		}
		if err := d.stateErrLocked(dev, now); err != nil {
			dd.Problem = err.Error()
		} else {
			dd.Online = true
		}
		details.Rings = append(details.Rings, dd)
	}
	return details
}
