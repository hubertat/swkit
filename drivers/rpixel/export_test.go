package rpixel

import (
	"context"
	"time"
)

// testTiming shrinks every delay so the suite runs fast: a request gives up
// after 280 ms, and a device polled every 50 ms goes offline after 400 ms.
func testTiming() *timing {
	return &timing{
		retryWaits:     []time.Duration{40 * time.Millisecond, 80 * time.Millisecond, 160 * time.Millisecond},
		offlineMin:     400 * time.Millisecond,
		resolveEvery:   time.Minute,
		resolveTimeout: time.Second,
		broadcastEvery: 100 * time.Millisecond,
		discoveryWait:  300 * time.Millisecond,
		initialWait:    2 * time.Second,
	}
}

// SetupForTest runs Setup with testTiming.
func SetupForTest(d *Driver, ios []string) error {
	d.timing = testTiming()
	return d.Setup(context.Background(), ios)
}

// DisplayMixed reports whether device name's cached display is mixed.
func DisplayMixed(d *Driver, name string) bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.byName[name].disp.mixed
}

// CmdGen returns device name's command generation.
func CmdGen(d *Driver, name string) uint64 {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.byName[name].cmdGen
}

// SetCmdInFlight marks a command to device name in flight, or not.
func SetCmdInFlight(d *Driver, name string, inFlight bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.byName[name].cmdInFlight = inFlight
}

// ApplyStatus applies st to device name as the reply to a G sent at gen.
func ApplyStatus(d *Driver, name string, st Status, gen uint64) {
	d.applyStatus(d.byName[name], st, gen)
}
