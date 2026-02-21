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
