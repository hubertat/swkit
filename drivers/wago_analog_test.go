package drivers

import "testing"

func TestWago750550ModuleSpec(t *testing.T) {
	spec, ok := wagoModuleSpecs["750-550"]
	if !ok {
		t.Fatal("750-550 not registered in wagoModuleSpecs")
	}
	if spec.ao != 2 || spec.di != 0 || spec.do != 0 {
		t.Errorf("750-550 spec = %+v, want ao:2 di:0 do:0", spec)
	}
}

func TestWagoGetAoGlobalIndex(t *testing.T) {
	wio := &WagoIO{
		modules: []wagoModuleSpec{
			{do: 8, doOrder: []int{1, 3, 5, 7, 2, 4, 6, 8}, description: "750-530"},
			{ao: 2, description: "750-550"},
			{ao: 2, description: "750-550"},
		},
		totalDO: 8,
		totalAO: 4,
	}

	tests := []struct {
		moduleNo int
		rel      int
		global   int
	}{
		{moduleNo: 2, rel: 1, global: 0},
		{moduleNo: 2, rel: 2, global: 1},
		{moduleNo: 3, rel: 1, global: 2},
		{moduleNo: 3, rel: 2, global: 3},
	}
	for _, tc := range tests {
		got := wio.getAoGlobalIndex(tc.moduleNo, tc.rel)
		if got != tc.global {
			t.Errorf("getAoGlobalIndex(%d,%d) = %d, want %d", tc.moduleNo, tc.rel, got, tc.global)
		}
	}

	// digital-only module has no analog channels
	if got := wio.getAoGlobalIndex(1, 1); got != -1 {
		t.Errorf("getAoGlobalIndex on digital module = %d, want -1", got)
	}
}

func TestWagoAoPhysicalToProcessMap(t *testing.T) {
	wio := &WagoIO{
		modules: []wagoModuleSpec{
			{ao: 2, description: "750-550"},
			{ao: 2, aoOrder: []int{2, 1}, description: "750-550 reversed"},
		},
		totalAO: 4,
	}
	m := wio.buildPhysicalToProcessMap(wagoChannelAO)
	want := []int{0, 1, 3, 2} // second module reversed
	if len(m) != len(want) {
		t.Fatalf("map len = %d, want %d", len(m), len(want))
	}
	for i := range want {
		if m[i] != want[i] {
			t.Errorf("aoMap[%d] = %d, want %d", i, m[i], want[i])
		}
	}
}

func TestWagoAOGetMinMax(t *testing.T) {
	wao := &WagoAO{driver: &WagoIO{}, index: 0}
	min, max := wao.GetMinMax()
	if min != 0 || max != 32767 {
		t.Errorf("GetMinMax = (%d,%d), want (0,32767)", min, max)
	}
}

func TestWagoAOGetStateGuards(t *testing.T) {
	// not ready
	wao := &WagoAO{driver: &WagoIO{isReady: false}, index: 0}
	if _, err := wao.GetState(); err == nil {
		t.Error("GetState should error when driver not ready")
	}

	// ready but stale (lastPollOk zero => stale)
	wio := &WagoIO{isReady: true, analogOutputStates: []uint16{16000}}
	wao = &WagoAO{driver: wio, index: 0}
	if _, err := wao.GetState(); err == nil {
		t.Error("GetState should error when state is stale")
	}
}
