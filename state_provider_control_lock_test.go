package swkit

import (
	"testing"
	"time"

	"github.com/charmbracelet/log"
	"github.com/hubertat/swkit/drivers"
)

// blockingDigitalOutput is a minimal drivers.DigitalOutput whose Set() blocks
// until its release channel is closed. It stands in for a wedged real driver
// output (e.g. a hung MQTT round trip) so tests can exercise the control path
// (ToggleDevice, SetDevice, ...) while a driver call is stuck in-flight.
type blockingDigitalOutput struct {
	release chan struct{}
	state   bool
}

func newBlockingDigitalOutput() *blockingDigitalOutput {
	return &blockingDigitalOutput{release: make(chan struct{})}
}

func (o *blockingDigitalOutput) GetState() (bool, error) { return o.state, nil }
func (o *blockingDigitalOutput) Set(state bool) error {
	<-o.release
	o.state = state
	return nil
}
func (o *blockingDigitalOutput) String() string                    { return "blocking-output" }
func (o *blockingDigitalOutput) SetOnStateUpdate(func(bool)) error { return nil }
func (o *blockingDigitalOutput) IsHealthy() bool                   { return true }

// TestToggleDeviceDoesNotHoldLockAcrossDriverCall guards finding 1: the control
// path (ToggleDevice here, representative of SetDevice/SetDeviceBrightness/
// AdjustDeviceBrightness/SetDeviceValueFor/ToggleIoOutput/SetIoAnalogOutput,
// all fixed the same way) must only hold p.mu long enough to resolve the
// device from p.sw, not across the call into the device/driver. We assert
// this the same way state_provider_cancel_test.go does for GetState: start a
// control call that blocks inside a driver call, then confirm Reload (which
// needs p.mu.Lock()) completes promptly instead of waiting for the blocked call.
func TestToggleDeviceDoesNotHoldLockAcrossDriverCall(t *testing.T) {
	logger := log.New(nil)
	out := newBlockingDigitalOutput()

	sw := &SwKit{Name: "t"}
	sw.lights = []*Light{NewLight(LightConfig{Name: "L1"}, out, logger)}
	sw.ioDrivers = map[string]drivers.IoDriver{}

	p := NewStateProvider(sw)

	done := make(chan struct{})
	go func() {
		_ = p.ToggleDevice(0)
		close(done)
	}()

	// Let ToggleDevice reach the blocked driver call inside output.Set().
	time.Sleep(20 * time.Millisecond)

	reloadDone := make(chan struct{})
	go func() {
		sw2 := &SwKit{Name: "t2"}
		sw2.lights = []*Light{NewLight(LightConfig{Name: "L1"}, drivers.NewMockOutput("l1"), logger)}
		sw2.ioDrivers = map[string]drivers.IoDriver{}
		p.Reload(sw2)
		close(reloadDone)
	}()

	select {
	case <-reloadDone:
	case <-time.After(time.Second):
		t.Fatal("Reload did not complete promptly; ToggleDevice appears to hold p.mu across the blocked driver call")
	}

	close(out.release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("blocked ToggleDevice never returned after driver unblocked")
	}
}
