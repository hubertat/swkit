package drivers

import "testing"

func assertBools(t testing.TB, got, want bool) {
	t.Helper()

	if got != want {
		t.Errorf("got %v want %v", got, want)
	}
}

func assertUint16Slices(t testing.TB, got, want []uint16) {
	t.Helper()

	if len(got) != len(want) {
		t.Errorf("len(got) = %d len(want) = %d", len(got), len(want))
		return
	}

	for key, val := range got {
		if want[key] != val {
			t.Errorf("for key [%d] got: %d want: %d", key, val, want[key])
		}
	}
}

func TestMockInputGetState(t *testing.T) {
	inEnabled := MockInput{State: true}
	inDisabled := MockInput{State: false}

	state, _ := inEnabled.GetState()
	if state != true {
		t.Error("MockInput GetState failed")
	}

	state, _ = inDisabled.GetState()
	if state != false {
		t.Error("MockInput GetState failed")
	}
}

func TestMockOutputGetState(t *testing.T) {
	outEnabled := MockOutput{state: true}
	outDisable := MockOutput{state: false}

	stateTrue, _ := outEnabled.GetState()
	stateFalse, _ := outDisable.GetState()

	if stateTrue != true || stateFalse != false {
		t.Error("MockOutput GetState failed")
	}
}

func TestMockOutputSetState(t *testing.T) {
	out := MockOutput{}

	want := true
	out.Set(want)
	got, _ := out.GetState()
	assertBools(t, got, want)

	want = false
	out.Set(want)
	got, _ = out.GetState()
	assertBools(t, got, want)

	want = true
	out.Set(want)
	got, _ = out.GetState()
	assertBools(t, got, want)
}

func TestMockIoSetup(t *testing.T) {
	md := MockIoDriver{}

	want := false
	got := md.IsReady()
	assertBools(t, got, want)

	md.Setup([]uint16{1, 3, 5}, []uint16{2, 4})
	want = true
	got = md.IsReady()
	assertBools(t, got, want)
}

func TestMockIoGetAllIo(t *testing.T) {
	md := MockIoDriver{}
	md.Setup([]uint16{1, 3, 5}, []uint16{2, 4})
	inputs, outputs := md.GetAllIo()
	assertUint16Slices(t, inputs, []uint16{1, 3, 5})
	assertUint16Slices(t, outputs, []uint16{2, 4})
}

func TestMockIoGetUniqueId(t *testing.T) {
	md := MockIoDriver{}

	got := md.GetUniqueId(3)
	want := uint64(0xABCDEF03)

	if got != want {
		t.Errorf("got: %X want: %X", got, want)
	}
}

func TestMockGetOutput(t *testing.T) {
	md := MockIoDriver{}
	md.Setup([]uint16{}, []uint16{3})
	output, err := md.GetOutput(3)
	if err != nil {
		t.Errorf("GetOutput returned err: %v", err)
	}

	want := true
	output.Set(want)
	got, _ := output.GetState()
	assertBools(t, got, want)

	anotherOut, _ := md.GetOutput(3)
	got, _ = anotherOut.GetState()
	assertBools(t, got, want)

	want = false
	output.Set(want)
	got, _ = output.GetState()
	assertBools(t, got, want)
}
