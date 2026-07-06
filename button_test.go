package swkit

import (
	"testing"

	"github.com/charmbracelet/log"
	drivers "github.com/hubertat/swkit/drivers"
)

func TestParseControlDeviceString(t *testing.T) {
	cases := []struct {
		in       string
		wantEvt  drivers.PushEvent
		wantVerb string
		wantLvl  int
		wantDev  string
	}{
		{"single_press:Kitchen", drivers.PushEventSinglePress, "toggle", 0, "Kitchen"},
		{"double_press:on:Kitchen", drivers.PushEventDoublePress, "on", 0, "Kitchen"},
		{"long_press:off:Living Room", drivers.PushEventLongPress, "off", 0, "Living Room"},
		{"double_press:brightness_up:10:Desk Lamp", drivers.PushEventDoublePress, "brightness_up", 10, "Desk Lamp"},
		{"single_press:brightness:75:Hall", drivers.PushEventSinglePress, "brightness", 75, "Hall"},
	}
	for _, c := range cases {
		e, act, err := ParseControlDeviceString(c.in)
		if err != nil {
			t.Errorf("ParseControlDeviceString(%q): unexpected error %v", c.in, err)
			continue
		}
		if e != c.wantEvt || act.Verb != c.wantVerb || act.Level != c.wantLvl || act.Device != c.wantDev {
			t.Errorf("ParseControlDeviceString(%q) = (%v, %q, %d, %q), want (%v, %q, %d, %q)",
				c.in, e, act.Verb, act.Level, act.Device, c.wantEvt, c.wantVerb, c.wantLvl, c.wantDev)
		}
	}
}

func TestParseControlDeviceStringErrors(t *testing.T) {
	bad := []string{
		"",
		"single_press",                         // no device
		"not_an_event:Kitchen",                 // bad event
		"single_press:brightness:Kitchen",      // brightness missing level
		"single_press:a:b:c:d",                 // too many parts
		"single_press:brightness_up:x:Kitchen", // non-numeric step
	}
	for _, in := range bad {
		if _, _, err := ParseControlDeviceString(in); err == nil {
			t.Errorf("ParseControlDeviceString(%q): expected error", in)
		}
	}
}

func TestButtonPressFiresRelativeBrightness(t *testing.T) {
	dl, aOut := newTestDimmableLight(t, 0, 100) // identity range
	dl.SetBrightness(40)

	emitter := &fakeEmitter{}
	ctrl := []ControlDevice{{dev: dl, e: drivers.PushEventDoublePress, verb: "brightness_up", level: 15}}
	NewButton(ButtonConfig{Name: "B", DisableHomekit: true}, emitter, ctrl, log.New(nil))

	emitter.emit(drivers.PushEventDoublePress)
	if got, _ := aOut.GetState(); got != 55 {
		t.Errorf("double press brightness_up:15 from 40, native = %d, want 55", got)
	}
}
