package app

import "testing"

func TestParseAndFormatSceneActionRoundTrip(t *testing.T) {
	cases := []string{
		"on:Kitchen",
		"off:Kitchen",
		"toggle:Living Room",
		"brightness:50:Hallway",
	}
	for _, in := range cases {
		action, level, dev, err := ParseSceneAction(in)
		if err != nil {
			t.Errorf("ParseSceneAction(%q): unexpected error %v", in, err)
			continue
		}
		if got := FormatSceneAction(action, level, dev); got != in {
			t.Errorf("round trip %q -> %q", in, got)
		}
	}
}

func TestParseSceneActionErrors(t *testing.T) {
	bad := []string{"", "on", "on:a:b", "brightness:Hallway", "brightness:x:Hallway", "frob:Kitchen"}
	for _, in := range bad {
		if _, _, _, err := ParseSceneAction(in); err == nil {
			t.Errorf("ParseSceneAction(%q): expected error", in)
		}
	}
}
