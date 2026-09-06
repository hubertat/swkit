package swkit

import (
	"testing"
)

func TestApplyVerbRelativeBrightness(t *testing.T) {
	dl, aOut := newTestDimmableLight(t, 0, 100) // identity range: native == pct

	dl.SetBrightness(40)
	if err := applyVerb(dl, "brightness_up", 10); err != nil {
		t.Fatalf("brightness_up: %v", err)
	}
	if got, _ := aOut.GetState(); got != 50 {
		t.Errorf("brightness_up 10 from 40 = %d, want 50", got)
	}

	if err := applyVerb(dl, "brightness_down", 10); err != nil {
		t.Fatalf("brightness_down: %v", err)
	}
	if got, _ := aOut.GetState(); got != 40 {
		t.Errorf("brightness_down 10 from 50 = %d, want 40", got)
	}
}

func TestApplyVerbBrightnessClampsAtBounds(t *testing.T) {
	dl, aOut := newTestDimmableLight(t, 0, 100)

	dl.SetBrightness(95)
	if err := applyVerb(dl, "brightness_up", 20); err != nil {
		t.Fatalf("brightness_up: %v", err)
	}
	if got, _ := aOut.GetState(); got != 100 {
		t.Errorf("brightness_up past max should clamp to 100, got %d", got)
	}

	dl.SetBrightness(10)
	if err := applyVerb(dl, "brightness_down", 30); err != nil {
		t.Fatalf("brightness_down: %v", err)
	}
	if got, _ := aOut.GetState(); got != 0 {
		t.Errorf("brightness_down past min should clamp to 0, got %d", got)
	}
}

func TestApplyVerbBrightnessOnNonDimmableErrors(t *testing.T) {
	li := newTestLight("L", false) // not Dimmable
	if err := applyVerb(li, "brightness_up", 10); err == nil {
		t.Error("expected error applying brightness_up to a non-Dimmable device")
	}
}

func TestApplyVerbSimpleVerbs(t *testing.T) {
	li := newTestLight("L", false)

	if err := applyVerb(li, "on", 0); err != nil {
		t.Fatalf("on: %v", err)
	}
	if on, _ := li.GetState(); !on {
		t.Error("on should switch the device on")
	}

	if err := applyVerb(li, "toggle", 0); err != nil {
		t.Fatalf("toggle: %v", err)
	}
	if on, _ := li.GetState(); on {
		t.Error("toggle should switch the device off")
	}

	if err := applyVerb(li, "frob", 0); err == nil {
		t.Error("unknown verb should error")
	}
}
