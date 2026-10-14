package app

import (
	"testing"
	"time"
)

func TestParseSceneStepRoundTrip(t *testing.T) {
	cases := []string{
		"on:Kitchen",
		"brightness:40:Hallway",
		"after:5s:off:Kitchen",
		"after:1m30s:brightness:20:Hallway",
		"after:2h:brightness_down:10:Hallway",
		"after:500ms:toggle:Living Room",
		"after:24h:off:Porch",
	}
	for _, in := range cases {
		st, err := ParseSceneStep(in)
		if err != nil {
			t.Errorf("ParseSceneStep(%q): unexpected error %v", in, err)
			continue
		}
		if got := st.String(); got != in {
			t.Errorf("round trip %q -> %q", in, got)
		}
	}
}

func TestParseSceneStepValues(t *testing.T) {
	st, err := ParseSceneStep("after:1m30s:brightness:20:Desk Lamp")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if st.Delay != 90*time.Second || st.Verb != "brightness" || st.Level != 20 || st.Device != "Desk Lamp" {
		t.Errorf("got %+v", st)
	}
}

func TestParseSceneStepNormalizesDelay(t *testing.T) {
	cases := map[string]string{
		"after:90s:on:A":   "after:1m30s:on:A",
		"after:60m:on:A":   "after:1h:on:A",
		"after:0s:on:A":    "on:A",
		"AFTER:1.5s:off:A": "after:1s500ms:off:A",
	}
	for in, want := range cases {
		st, err := ParseSceneStep(in)
		if err != nil {
			t.Errorf("ParseSceneStep(%q): %v", in, err)
			continue
		}
		if got := st.String(); got != want {
			t.Errorf("ParseSceneStep(%q).String() = %q, want %q", in, got, want)
		}
	}
}

func TestParseSceneStepErrors(t *testing.T) {
	bad := []string{
		"",
		"after:5s",          // no action
		"after:soon:on:A",   // bad duration
		"after:-5s:on:A",    // negative
		"after:25h:on:A",    // over the cap
		"after:1500us:on:A", // sub-millisecond
		"after:5s:frob:A",   // bad action
	}
	for _, in := range bad {
		if _, err := ParseSceneStep(in); err == nil {
			t.Errorf("ParseSceneStep(%q): expected error", in)
		}
	}
}

// Button mappings use ParseAction, which must keep rejecting delays so a
// delay can never be silently dropped from a button relation.
func TestParseActionRejectsDelay(t *testing.T) {
	if _, err := ParseAction("after:5s:on:A"); err == nil {
		t.Error("ParseAction accepted a delayed step")
	}
}
