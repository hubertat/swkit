package swkit

import (
	"context"
	"testing"
	"time"

	"github.com/hubertat/swkit/drivers"
)

// blockingDriver is a minimal drivers.IoDriver whose IsReady() blocks until
// its release channel is closed. It stands in for a wedged real driver (e.g.
// a hung MQTT round trip) so tests can exercise GetState/Subscribe while a
// driver call is stuck in-flight, then release it deterministically.
type blockingDriver struct {
	release chan struct{}
}

func newBlockingDriver() *blockingDriver {
	return &blockingDriver{release: make(chan struct{})}
}

func (d *blockingDriver) Setup(ctx context.Context, ios []string) error { return nil }
func (d *blockingDriver) Close() error                                  { return nil }
func (d *blockingDriver) String() string                                { return "blocking" }
func (d *blockingDriver) IsReady() bool {
	<-d.release
	return true
}
func (d *blockingDriver) GetDigitalInput(id string) (drivers.DigitalInput, error) {
	return nil, nil
}
func (d *blockingDriver) GetDigitalOutput(id string) (drivers.DigitalOutput, error) {
	return nil, nil
}
func (d *blockingDriver) GetAnalogOutput(id string) (drivers.AnalogOutput, error) {
	return nil, nil
}
func (d *blockingDriver) GetRgbwOutput(id string) (drivers.RgbwOutput, error) {
	return nil, nil
}
func (d *blockingDriver) GetPushEventEmitter(id string) (drivers.PushEventEmitter, error) {
	return nil, nil
}

// TestSubscribeCancelDuringBlockedDriverCall guards finding A: Subscribe must
// return promptly on context cancellation even while GetState is stuck
// inside a blocked driver call, and the abandoned GetState goroutine must be
// able to finish later (once the driver unblocks) without panicking on a
// send to the already-closed subscriber channel.
func TestSubscribeCancelDuringBlockedDriverCall(t *testing.T) {
	sw, _ := newDimmableTestSwKit(t)
	fake := newBlockingDriver()
	sw.ioDrivers["blocking"] = fake

	p := NewStateProvider(sw)

	ctx, cancel := context.WithCancel(context.Background())
	ch := p.Subscribe(ctx, 5*time.Millisecond)

	// The initial GetState is now stuck inside fake.IsReady(). Give the
	// Subscribe goroutine a moment to actually start it, then cancel.
	time.Sleep(20 * time.Millisecond)
	cancel()

	deadline := time.After(time.Second)
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				goto closed
			}
		case <-deadline:
			t.Fatal("Subscribe did not return/close ch promptly after cancellation while GetState was blocked")
		}
	}
closed:

	// Release the blocked driver call. The abandoned GetState goroutine will
	// try to deliver its result on its own dedicated result channel; this
	// must not touch ch (closed above) or panic the test process.
	close(fake.release)

	// Give the leaked goroutine a moment to finish; if it panics, the test
	// binary crashes regardless of this sleep.
	time.Sleep(50 * time.Millisecond)
}

// TestGetStateDoesNotHoldLockAcrossDriverCall guards finding A's first part:
// GetState must only hold p.mu long enough to read the *SwKit pointer, not
// for the whole snapshot. We assert this by starting a GetState that blocks
// inside a driver call, then confirming Reload (which needs p.mu.Lock())
// completes promptly instead of waiting for the blocked call.
func TestGetStateDoesNotHoldLockAcrossDriverCall(t *testing.T) {
	sw, _ := newDimmableTestSwKit(t)
	fake := newBlockingDriver()
	sw.ioDrivers["blocking"] = fake

	p := NewStateProvider(sw)

	done := make(chan struct{})
	go func() {
		_ = p.GetState()
		close(done)
	}()

	// Let GetState reach the blocked driver call.
	time.Sleep(20 * time.Millisecond)

	reloadDone := make(chan struct{})
	go func() {
		sw2, _ := newDimmableTestSwKit(t)
		p.Reload(sw2)
		close(reloadDone)
	}()

	select {
	case <-reloadDone:
	case <-time.After(time.Second):
		t.Fatal("Reload did not complete promptly; GetState appears to hold p.mu across the blocked driver call")
	}

	close(fake.release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("blocked GetState never returned after driver unblocked")
	}
}
