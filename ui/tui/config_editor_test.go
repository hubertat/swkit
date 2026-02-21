package tui

import (
	"testing"

	"github.com/hubertat/swkit/app"
)

func TestConfigEditorIoPickerDisplayNameUsesCustomName(t *testing.T) {
	ce := NewConfigEditor(nil, DefaultTheme())
	ce.SetIoDisplayNames(map[string]string{
		"wago|output|7": "Kitchen Light",
	})

	pt := app.IoPointDebugState{
		DriverName: "wago",
		Type:       "output",
		Index:      7,
		Name:       "M1:DO7",
	}

	got := ce.ioPickerDisplayName(pt)
	want := "Kitchen Light [M1:DO7]"
	if got != want {
		t.Fatalf("unexpected display name: got %q, want %q", got, want)
	}
}

func TestConfigEditorIoPickerDisplayNameFallsBackToHardwareName(t *testing.T) {
	ce := NewConfigEditor(nil, DefaultTheme())

	pt := app.IoPointDebugState{
		DriverName: "wago",
		Type:       "input",
		Index:      2,
		Name:       "M1:DI2",
	}

	got := ce.ioPickerDisplayName(pt)
	want := "M1:DI2"
	if got != want {
		t.Fatalf("unexpected fallback display name: got %q, want %q", got, want)
	}
}

func TestConfigEditorSetIoDisplayNamesCopiesInputMap(t *testing.T) {
	ce := NewConfigEditor(nil, DefaultTheme())
	original := map[string]string{
		"wago|output|1": "Initial Name",
	}

	ce.SetIoDisplayNames(original)
	original["wago|output|1"] = "Changed Externally"

	pt := app.IoPointDebugState{
		DriverName: "wago",
		Type:       "output",
		Index:      1,
		Name:       "M1:DO1",
	}

	got := ce.ioPickerDisplayName(pt)
	want := "Initial Name [M1:DO1]"
	if got != want {
		t.Fatalf("display name changed via external map mutation: got %q, want %q", got, want)
	}
}

func TestIoPointToIdShellyInputUsesNumericPort(t *testing.T) {
	pt := app.IoPointDebugState{
		DriverName: "shelly",
		Type:       "input",
		Name:       "shellyi4g3-e4b063d3f118:input1",
	}

	got := ioPointToId(pt)
	want := "shelly|d_in|shellyi4g3-e4b063d3f118:1"
	if got != want {
		t.Fatalf("unexpected io id: got %q, want %q", got, want)
	}
}

func TestIoPointToIdShellyOutputUsesNumericPort(t *testing.T) {
	pt := app.IoPointDebugState{
		DriverName: "shelly",
		Type:       "output",
		Name:       "shelly1pm-ABCDEF123456:switch0",
	}

	got := ioPointToId(pt)
	want := "shelly|d_out|shelly1pm-ABCDEF123456:0"
	if got != want {
		t.Fatalf("unexpected io id: got %q, want %q", got, want)
	}
}

func TestIoPointToIdWagoUsesGlobalIndex(t *testing.T) {
	pt := app.IoPointDebugState{
		DriverName: "wago",
		Type:       "input",
		Index:      5,
		Name:       "M2:DI1",
	}

	got := ioPointToId(pt)
	want := "wago|d_in|5"
	if got != want {
		t.Fatalf("unexpected io id: got %q, want %q", got, want)
	}
}

func TestIoPointToIdForCurrentFieldButtonUsesPushEvent(t *testing.T) {
	ce := NewConfigEditor(nil, DefaultTheme())
	ce.items = []configListItem{
		{itemType: configItemButton, index: 0},
	}
	ce.cursor = 0
	ce.ioPickerField = 1

	pt := app.IoPointDebugState{
		DriverName: "shelly",
		Type:       "input",
		Name:       "shellyi4g3-e4b063d3f118:input1",
	}

	got := ce.ioPointToIdForCurrentField(pt)
	want := "shelly|push_event|shellyi4g3-e4b063d3f118:1"
	if got != want {
		t.Fatalf("unexpected io id for button picker field: got %q, want %q", got, want)
	}
}

func TestIoPointToIdForCurrentFieldLightUsesDigitalOutput(t *testing.T) {
	ce := NewConfigEditor(nil, DefaultTheme())
	ce.items = []configListItem{
		{itemType: configItemLight, index: 0},
	}
	ce.cursor = 0
	ce.ioPickerField = 1

	pt := app.IoPointDebugState{
		DriverName: "shelly",
		Type:       "output",
		Name:       "shelly1pm-ABCDEF123456:switch0",
	}

	got := ce.ioPointToIdForCurrentField(pt)
	want := "shelly|d_out|shelly1pm-ABCDEF123456:0"
	if got != want {
		t.Fatalf("unexpected io id for light picker field: got %q, want %q", got, want)
	}
}
