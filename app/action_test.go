package app

import (
	"reflect"
	"testing"
)

func TestParseAndFormatActionRoundTrip(t *testing.T) {
	cases := []string{
		"on:Kitchen",
		"off:Kitchen",
		"toggle:Living Room",
		"brightness:50:Hallway",
		"brightness_up:10:Hallway",
		"brightness_down:5:Hallway",
	}
	for _, in := range cases {
		act, err := ParseAction(in)
		if err != nil {
			t.Errorf("ParseAction(%q): unexpected error %v", in, err)
			continue
		}
		if got := act.String(); got != in {
			t.Errorf("round trip %q -> %q", in, got)
		}
	}
}

func TestParseActionValues(t *testing.T) {
	act, err := ParseAction("brightness_up:15:Desk Lamp")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := Action{Verb: "brightness_up", Level: 15, Device: "Desk Lamp"}
	if !reflect.DeepEqual(act, want) {
		t.Errorf("got %+v, want %+v", act, want)
	}
}

func TestParseActionErrors(t *testing.T) {
	bad := []string{
		"",
		"on",
		"on:a:b",
		"brightness:Hallway",      // missing level
		"brightness:x:Hallway",    // non-numeric level
		"brightness_up:Hallway",   // missing step
		"brightness_up:x:Hallway", // non-numeric step
		"frob:Kitchen",
	}
	for _, in := range bad {
		if _, err := ParseAction(in); err == nil {
			t.Errorf("ParseAction(%q): expected error", in)
		}
	}
}

func TestAllActionVerbs(t *testing.T) {
	want := []string{"on", "off", "toggle", "brightness", "brightness_up", "brightness_down"}
	if got := AllActionVerbs(); !reflect.DeepEqual(got, want) {
		t.Errorf("AllActionVerbs() = %v, want %v", got, want)
	}
}
