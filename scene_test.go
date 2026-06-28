package swkit

import (
	"strings"
	"testing"

	"github.com/charmbracelet/log"
)

// resolverFrom builds a name->Controllable resolver from the given devices.
func resolverFrom(devs ...Controllable) func(string) (Controllable, bool) {
	m := map[string]Controllable{}
	for _, d := range devs {
		m[d.Name()] = d
	}
	return func(name string) (Controllable, bool) {
		d, ok := m[name]
		return d, ok
	}
}

func TestParseSceneAction(t *testing.T) {
	cases := []struct {
		in         string
		wantAction string
		wantLevel  int
		wantDev    string
		wantErr    bool
	}{
		{"on:Kitchen", "on", 0, "Kitchen", false},
		{"off:Kitchen", "off", 0, "Kitchen", false},
		{"toggle:Kitchen", "toggle", 0, "Kitchen", false},
		{"brightness:50:Hallway", "brightness", 50, "Hallway", false},
		{"", "", 0, "", true},
		{"on", "", 0, "", true},
		{"on:a:b", "", 0, "", true},
		{"brightness:Hallway", "", 0, "", true},
		{"brightness:notnum:Hallway", "", 0, "", true},
		{"frobnicate:Kitchen", "", 0, "", true},
	}
	for _, c := range cases {
		action, level, dev, err := parseSceneAction(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("parseSceneAction(%q): expected error", c.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseSceneAction(%q): unexpected error %v", c.in, err)
			continue
		}
		if action != c.wantAction || level != c.wantLevel || dev != c.wantDev {
			t.Errorf("parseSceneAction(%q) = (%q,%d,%q), want (%q,%d,%q)",
				c.in, action, level, dev, c.wantAction, c.wantLevel, c.wantDev)
		}
	}
}

func TestSceneToggleCyclesStates(t *testing.T) {
	a := newTestLight("A", false)
	b := newTestLight("B", false)

	sc, err := NewScene(SceneConfig{
		Name: "Living",
		States: []SceneStateConfig{
			{Name: "off", Actions: []string{"off:A", "off:B"}},
			{Name: "a-on", Actions: []string{"on:A", "off:B"}},
			{Name: "b-on", Actions: []string{"off:A", "on:B"}},
		},
	}, resolverFrom(a, b), log.New(nil))
	if err != nil {
		t.Fatalf("NewScene: %v", err)
	}

	assertState := func(label string, wantA, wantB bool) {
		t.Helper()
		gotA, _ := a.GetState()
		gotB, _ := b.GetState()
		if gotA != wantA || gotB != wantB {
			t.Errorf("%s: A=%v B=%v, want A=%v B=%v", label, gotA, gotB, wantA, wantB)
		}
	}

	sc.Toggle() // 0 -> 1
	if sc.CurrentState() != 1 {
		t.Errorf("after first toggle currentState = %d, want 1", sc.CurrentState())
	}
	assertState("state 1", true, false)

	sc.Toggle() // 1 -> 2
	assertState("state 2", false, true)

	sc.Toggle() // 2 -> 0 (wrap)
	if sc.CurrentState() != 0 {
		t.Errorf("after wrap currentState = %d, want 0", sc.CurrentState())
	}
	assertState("state 0", false, false)
}

func TestSceneSetValue(t *testing.T) {
	a := newTestLight("A", false)

	sc, err := NewScene(SceneConfig{
		Name: "S",
		States: []SceneStateConfig{
			{Name: "off", Actions: []string{"off:A"}},
			{Name: "on", Actions: []string{"on:A"}},
		},
	}, resolverFrom(a), log.New(nil))
	if err != nil {
		t.Fatalf("NewScene: %v", err)
	}

	sc.SetValue(true)
	if on, _ := a.GetState(); !on {
		t.Error("SetValue(true) should activate first non-off state")
	}
	if sc.CurrentState() != 1 {
		t.Errorf("currentState = %d, want 1", sc.CurrentState())
	}

	sc.SetValue(false)
	if on, _ := a.GetState(); on {
		t.Error("SetValue(false) should activate off state")
	}
	if sc.CurrentState() != 0 {
		t.Errorf("currentState = %d, want 0", sc.CurrentState())
	}
}

func TestSceneBrightnessAction(t *testing.T) {
	dl, aOut := newTestDimmableLight(t, 0, 100)

	sc, err := NewScene(SceneConfig{
		Name: "Dim",
		States: []SceneStateConfig{
			{Name: "off", Actions: []string{"off:Test Dim"}},
			{Name: "dim", Actions: []string{"on:Test Dim", "brightness:75:Test Dim"}},
		},
	}, resolverFrom(dl), log.New(nil))
	if err != nil {
		t.Fatalf("NewScene: %v", err)
	}

	if err := sc.Activate(1); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	if on, _ := dl.GetState(); !on {
		t.Error("dimmable light should be on")
	}
	if got, _ := aOut.GetState(); got != 75 {
		t.Errorf("brightness native = %d, want 75", got)
	}
}

func TestSceneActivatePartialFailureAggregates(t *testing.T) {
	a := newTestLight("A", false) // not Dimmable
	b := newTestLight("B", false)

	sc, err := NewScene(SceneConfig{
		Name: "P",
		States: []SceneStateConfig{
			{Name: "off", Actions: []string{"off:A"}},
			// brightness on a plain Light fails at apply; on:B must still run.
			{Name: "mixed", Actions: []string{"brightness:50:A", "on:B"}},
		},
	}, resolverFrom(a, b), log.New(nil))
	if err != nil {
		t.Fatalf("NewScene: %v", err)
	}

	actErr := sc.Activate(1)
	if actErr == nil || !strings.Contains(actErr.Error(), "brightness") {
		t.Errorf("expected aggregated brightness error, got %v", actErr)
	}
	if on, _ := b.GetState(); !on {
		t.Error("on:B should still apply despite earlier failure")
	}
	if sc.CurrentState() != 1 {
		t.Errorf("currentState = %d, want 1 (recorded even on partial failure)", sc.CurrentState())
	}
}

func TestSceneUnknownDeviceFailsConstruction(t *testing.T) {
	_, err := NewScene(SceneConfig{
		Name:   "Bad",
		States: []SceneStateConfig{{Name: "s", Actions: []string{"on:Nope"}}},
	}, resolverFrom(), log.New(nil))
	if err == nil {
		t.Fatal("expected error for unknown device reference")
	}
}

func TestSceneControlsScene(t *testing.T) {
	a := newTestLight("A", false)
	inner, err := NewScene(SceneConfig{
		Name: "Inner",
		States: []SceneStateConfig{
			{Name: "off", Actions: []string{"off:A"}},
			{Name: "on", Actions: []string{"on:A"}},
		},
	}, resolverFrom(a), log.New(nil))
	if err != nil {
		t.Fatalf("NewScene inner: %v", err)
	}

	// A scene resolves and drives another scene as a Controllable.
	outer, err := NewScene(SceneConfig{
		Name: "Outer",
		States: []SceneStateConfig{
			{Name: "off", Actions: []string{"off:Inner"}},
			{Name: "on", Actions: []string{"on:Inner"}},
		},
	}, resolverFrom(inner), log.New(nil))
	if err != nil {
		t.Fatalf("NewScene outer: %v", err)
	}

	outer.Activate(1)
	if on, _ := a.GetState(); !on {
		t.Error("outer scene should drive inner scene which turns A on")
	}
}
