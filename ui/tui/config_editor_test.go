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

