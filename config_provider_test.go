package swkit

import (
	"strings"
	"testing"
)

func TestMarshalWithoutNilIndentOmitsNilFields(t *testing.T) {
	sw := SwKit{
		Name:    "test",
		Lights:  []LightConfig{},
		Buttons: nil,
		Gpio:    nil,
	}

	data, err := marshalWithoutNilIndent(sw)
	if err != nil {
		t.Fatalf("marshalWithoutNilIndent returned error: %v", err)
	}

	jsonOut := string(data)
	if strings.Contains(jsonOut, `"Buttons": null`) {
		t.Fatalf("expected nil slice to be omitted, got: %s", jsonOut)
	}
	if strings.Contains(jsonOut, `"Gpio": null`) {
		t.Fatalf("expected nil pointer to be omitted, got: %s", jsonOut)
	}
	if !strings.Contains(jsonOut, `"Lights": []`) {
		t.Fatalf("expected non-nil empty slice to be preserved as [], got: %s", jsonOut)
	}
}

func TestMarshalWithoutNilIndentKeepsFalseAndZeroValues(t *testing.T) {
	sw := SwKit{
		Name:        "test",
		HkDebug:     false,
		ColorLights: []ColorLightConfig{},
	}

	data, err := marshalWithoutNilIndent(sw)
	if err != nil {
		t.Fatalf("marshalWithoutNilIndent returned error: %v", err)
	}

	jsonOut := string(data)
	if !strings.Contains(jsonOut, `"HkDebug": false`) {
		t.Fatalf("expected false bool to be preserved, got: %s", jsonOut)
	}
}

func TestNormalizeButtonEventInputIoTypeMigratesDigitalInput(t *testing.T) {
	got := normalizeButtonEventInputIoType("shelly|d_in|dev:1")
	want := "shelly|push_event|dev:1"
	if got != want {
		t.Fatalf("unexpected migrated io id: got %q, want %q", got, want)
	}
}

func TestNormalizeButtonEventInputIoTypeKeepsPushEvent(t *testing.T) {
	input := "shelly|push_event|dev:1"
	got := normalizeButtonEventInputIoType(input)
	if got != input {
		t.Fatalf("expected push_event io id unchanged: got %q, want %q", got, input)
	}
}

func TestNormalizeButtonEventInputIoTypeKeepsInvalidValue(t *testing.T) {
	input := "invalid-value"
	got := normalizeButtonEventInputIoType(input)
	if got != input {
		t.Fatalf("expected invalid value unchanged: got %q, want %q", got, input)
	}
}

// TestSwKitConfigProviderSwapReflectsInEditableConfig proves that Swap
// actually retargets the provider: GetEditableConfig must reflect the
// swapped-in SwKit's config, not the one the provider was constructed with.
// This is what performReload relies on after a hot reload swaps the state
// provider - without it, saves after a reload would read/write through the
// stale, torn-down SwKit.
func TestSwKitConfigProviderSwapReflectsInEditableConfig(t *testing.T) {
	oldSw := &SwKit{
		Name:   "old",
		Lights: []LightConfig{{Name: "Old Light", DigitalOutName: "gpio|d_out|1"}},
	}
	provider := NewConfigProvider(oldSw, "unused-config-path.json")

	before := provider.GetEditableConfig()
	if len(before.Lights) != 1 || before.Lights[0].Name != "Old Light" {
		t.Fatalf("before swap: config = %+v, want one light named Old Light", before)
	}

	newSw := &SwKit{
		Name:   "new",
		Lights: []LightConfig{{Name: "New Light", DigitalOutName: "gpio|d_out|2"}},
	}
	provider.Swap(newSw)

	after := provider.GetEditableConfig()
	if len(after.Lights) != 1 || after.Lights[0].Name != "New Light" {
		t.Fatalf("after swap: config = %+v, want one light named New Light", after)
	}
}
