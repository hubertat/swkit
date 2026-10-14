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

// erroringAnalogOutput is an AnalogOutput whose state cannot be read.
type erroringAnalogOutput struct{}

func (erroringAnalogOutput) GetMinMax() (int, int)  { return 0, 100 }
func (erroringAnalogOutput) GetState() (int, error) { return 0, errors.New("state is stale") }
func (erroringAnalogOutput) Set(int) error          { return nil }
func (erroringAnalogOutput) String() string         { return "test|a_out|err" }
func (erroringAnalogOutput) IsHealthy() bool        { return true }

// An analog-only dimmable light derives on/off from its analog output, so an
// unreadable analog output must surface as StateError, not as "off".
func TestGetStateReportsUnreadableAnalogOnlyDimmable(t *testing.T) {
	logger := log.New(nil)
	sw := &SwKit{Name: "t"}
	sw.dimmableLights = []*DimmableLight{
		NewDimmableLight(DimmableLightConfig{Name: "Dim"}, nil, erroringAnalogOutput{}, logger),
	}

	state := NewStateProvider(sw).GetState()
	if len(state.Devices) != 1 {
		t.Fatalf("got %d devices, want 1", len(state.Devices))
	}
	if d := state.Devices[0]; d.StateError == "" {
		t.Errorf("%s: StateError empty, want the analog read error", d.Name)
	}
}
