package app

import "testing"

func TestIoPointToIdShellyInputUsesNumericPort(t *testing.T) {
	pt := IoPointDebugState{
		DriverName: "shelly",
		Type:       "input",
		Name:       "shellyi4g3-e4b063d3f118:input1",
	}

	got := IoPointToId(pt)
	want := "shelly|d_in|shellyi4g3-e4b063d3f118:1"
	if got != want {
		t.Fatalf("unexpected io id: got %q, want %q", got, want)
	}
}

func TestIoPointToIdShellyOutputUsesNumericPort(t *testing.T) {
	pt := IoPointDebugState{
		DriverName: "shelly",
		Type:       "output",
		Name:       "shelly1pm-ABCDEF123456:switch0",
	}

	got := IoPointToId(pt)
	want := "shelly|d_out|shelly1pm-ABCDEF123456:0"
	if got != want {
		t.Fatalf("unexpected io id: got %q, want %q", got, want)
	}
}

func TestIoPointToIdShellyLightUsesLightForm(t *testing.T) {
	pt := IoPointDebugState{
		DriverName: "shelly",
		Type:       "output",
		Name:       "shellydimmer-ABCDEF123456:light0",
	}

	got := IoPointToId(pt)
	want := "shelly|d_out|shellydimmer-ABCDEF123456:light:0"
	if got != want {
		t.Fatalf("unexpected io id: got %q, want %q", got, want)
	}
}

func TestIoPointToIdShellyUnrecognizedSuffixFallsBackToRawName(t *testing.T) {
	pt := IoPointDebugState{
		DriverName: "shelly",
		Type:       "input",
		Name:       "shelly1pm-ABCDEF123456:weird",
	}

	got := IoPointToId(pt)
	want := "shelly|d_in|shelly1pm-ABCDEF123456:weird"
	if got != want {
		t.Fatalf("unexpected io id: got %q, want %q", got, want)
	}
}

func TestIoPointToIdShellyMissingColonFallsBackToRawName(t *testing.T) {
	pt := IoPointDebugState{
		DriverName: "shelly",
		Type:       "input",
		Name:       "malformed-name-no-colon",
	}

	got := IoPointToId(pt)
	want := "shelly|d_in|malformed-name-no-colon"
	if got != want {
		t.Fatalf("unexpected io id: got %q, want %q", got, want)
	}
}

func TestIoPointToIdWagoUsesGlobalIndex(t *testing.T) {
	pt := IoPointDebugState{
		DriverName: "wago",
		Type:       "input",
		Index:      5,
		Name:       "M2:DI1",
	}

	got := IoPointToId(pt)
	want := "wago|d_in|5"
	if got != want {
		t.Fatalf("unexpected io id: got %q, want %q", got, want)
	}
}

func TestIoPointToIdWagoOutputUsesGlobalIndex(t *testing.T) {
	pt := IoPointDebugState{
		DriverName: "wago",
		Type:       "output",
		Index:      12,
		Name:       "M3:DO4",
	}

	got := IoPointToId(pt)
	want := "wago|d_out|12"
	if got != want {
		t.Fatalf("unexpected io id: got %q, want %q", got, want)
	}
}

func TestIoPointToIdDefaultDriverUsesRawName(t *testing.T) {
	pt := IoPointDebugState{
		DriverName: "gpio",
		Type:       "output",
		Name:       "5",
	}

	got := IoPointToId(pt)
	want := "gpio|d_out|5"
	if got != want {
		t.Fatalf("unexpected io id: got %q, want %q", got, want)
	}
}

func TestIoPointToIdAnalogOutputMapsToAOut(t *testing.T) {
	pt := IoPointDebugState{
		DriverName: "wago",
		Type:       "analog_output",
		Index:      2,
	}

	got := IoPointToId(pt)
	want := "wago|a_out|2"
	if got != want {
		t.Fatalf("unexpected io id: got %q, want %q", got, want)
	}
}

func TestIoPointToIdWithTypePushEventOverridesInputType(t *testing.T) {
	pt := IoPointDebugState{
		DriverName: "shelly",
		Type:       "input",
		Name:       "shellyi4g3-e4b063d3f118:input1",
	}

	got := IoPointToIdWithType(pt, "push_event")
	want := "shelly|push_event|shellyi4g3-e4b063d3f118:1"
	if got != want {
		t.Fatalf("unexpected io id: got %q, want %q", got, want)
	}
}
