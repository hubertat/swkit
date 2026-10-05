package swkit

import (
	"errors"
	"testing"

	"github.com/charmbracelet/log"
)

// erroringOutput is a DigitalOutput whose state cannot be read.
type erroringOutput struct{}

func (erroringOutput) GetState() (bool, error)           { return false, errors.New("state is stale") }
func (erroringOutput) Set(bool) error                    { return nil }
func (erroringOutput) String() string                    { return "test|d_out|err" }
func (erroringOutput) SetOnStateUpdate(func(bool)) error { return errors.New("unsupported") }
func (erroringOutput) IsHealthy() bool                   { return true }

func TestGetStateReportsUnreadableOutput(t *testing.T) {
	logger := log.New(nil)
	sw := &SwKit{Name: "t"}
	sw.lights = []*Light{NewLight(LightConfig{Name: "Hall"}, erroringOutput{}, logger)}
	sw.outlets = []*Outlet{NewOutlet(OutletConfig{Name: "Plug"}, erroringOutput{}, logger)}

	state := NewStateProvider(sw).GetState()
	if len(state.Devices) != 2 {
		t.Fatalf("got %d devices, want 2", len(state.Devices))
	}
	for _, d := range state.Devices {
		if d.StateError == "" {
			t.Errorf("%s: StateError empty, want the read error", d.Name)
		}
		if d.IsOn {
			t.Errorf("%s: IsOn = true for an unreadable output", d.Name)
		}
	}
}
