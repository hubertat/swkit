package drivers

import (
	"fmt"
	"testing"
	"time"
)

func TestWagoGetIoDebugSnapshot(t *testing.T) {
	wio := &WagoIO{
		modules: []wagoModuleSpec{
			{di: 8, do: 0, description: "8DI"},
			{di: 0, do: 2, description: "2DO relay"},
			{di: 2, do: 0, description: "2DI power supply"},
			{di: 0, do: 8, description: "8DO output"},
		},
		totalDI:           10,
		totalDO:           10,
		inputStates:       make([]bool, 10),
		outputStates:      make([]bool, 10),
		inputLastChanged:  make([]time.Time, 10),
		outputLastChanged: make([]time.Time, 10),
		isReady:           true,
		lastPollOk:        time.Now(),
	}

	// Set some states
	wio.inputStates[0] = true
	wio.inputStates[3] = true
	wio.outputStates[1] = true
	wio.outputStates[5] = true

	snapshot := wio.GetIoDebugSnapshot()

	// Total points: 8 DI + 2 DO + 2 DI + 8 DO = 20
	if len(snapshot.Points) != 20 {
		t.Fatalf("expected 20 points, got %d", len(snapshot.Points))
	}

	// Verify Module 1 (8 DI)
	for i := 0; i < 8; i++ {
		pt := snapshot.Points[i]
		expectedName := fmt.Sprintf("M1:DI%d[%d]", i+1, i)
		if pt.Name != expectedName {
			t.Errorf("point %d: expected name %q, got %q", i, expectedName, pt.Name)
		}
		if pt.Type != IoTypeDigitalInput {
			t.Errorf("point %d: expected type DigitalInput, got %s", i, pt.Type)
		}
		if pt.Index != i {
			t.Errorf("point %d: expected index %d, got %d", i, i, pt.Index)
		}
	}

	// Check active input states
	if !snapshot.Points[0].State {
		t.Error("point 0 (M1:DI1) should be ON")
	}
	if snapshot.Points[1].State {
		t.Error("point 1 (M1:DI2) should be OFF")
	}
	if !snapshot.Points[3].State {
		t.Error("point 3 (M1:DI4) should be ON")
	}

	// Module 2 (2 DO) - points at index 8, 9
	pt8 := snapshot.Points[8]
	if pt8.Name != "M2:DO1[0]" {
		t.Errorf("point 8: expected name M2:DO1[0], got %q", pt8.Name)
	}
	if pt8.Type != IoTypeDigitalOutput {
		t.Errorf("point 8: expected type DigitalOutput, got %s", pt8.Type)
	}
	if pt8.Index != 0 {
		t.Errorf("point 8: expected output index 0, got %d", pt8.Index)
	}

	pt9 := snapshot.Points[9]
	if pt9.Name != "M2:DO2[1]" {
		t.Errorf("point 9: expected name M2:DO2[1], got %q", pt9.Name)
	}
	if !pt9.State {
		t.Error("point 9 (M2:DO2[1]) should be ON (outputStates[1])")
	}

	// Module 3 (2 DI) - points at index 10, 11
	pt10 := snapshot.Points[10]
	if pt10.Name != "M3:DI1[8]" {
		t.Errorf("point 10: expected name M3:DI1[8], got %q", pt10.Name)
	}
	if pt10.Type != IoTypeDigitalInput {
		t.Errorf("point 10: expected type DigitalInput, got %s", pt10.Type)
	}
	if pt10.Index != 8 {
		t.Errorf("point 10: expected input index 8, got %d", pt10.Index)
	}

	// Module 4 (8 DO) - points at index 12..19
	pt12 := snapshot.Points[12]
	if pt12.Name != "M4:DO1[2]" {
		t.Errorf("point 12: expected name M4:DO1[2], got %q", pt12.Name)
	}
	if pt12.Type != IoTypeDigitalOutput {
		t.Errorf("point 12: expected type DigitalOutput, got %s", pt12.Type)
	}
	if pt12.Index != 2 {
		t.Errorf("point 12: expected output index 2, got %d", pt12.Index)
	}

	// Check output state at index 5 (M4:DO4, globalDoIndex=5)
	pt15 := snapshot.Points[15]
	if pt15.Name != "M4:DO4[5]" {
		t.Errorf("point 15: expected name M4:DO4[5], got %q", pt15.Name)
	}
	if !pt15.State {
		t.Error("point 15 (M4:DO4) should be ON (outputStates[5])")
	}

	// All points should be healthy
	for i, pt := range snapshot.Points {
		if !pt.Healthy {
			t.Errorf("point %d (%s): expected healthy", i, pt.Name)
		}
	}
}

func TestWagoGetIoDebugSnapshotNotReady(t *testing.T) {
	wio := &WagoIO{
		modules: []wagoModuleSpec{
			{di: 2, do: 0, description: "2DI"},
		},
		totalDI:          2,
		totalDO:          0,
		inputStates:      make([]bool, 2),
		inputLastChanged: make([]time.Time, 2),
		isReady:          false,
	}

	snapshot := wio.GetIoDebugSnapshot()

	if len(snapshot.Points) != 2 {
		t.Fatalf("expected 2 points, got %d", len(snapshot.Points))
	}

	for i, pt := range snapshot.Points {
		if pt.Healthy {
			t.Errorf("point %d: should not be healthy when driver is not ready", i)
		}
	}
}

func TestWagoGetIoDebugSnapshotEmpty(t *testing.T) {
	wio := &WagoIO{
		isReady: true,
	}

	snapshot := wio.GetIoDebugSnapshot()

	if len(snapshot.Points) != 0 {
		t.Errorf("expected 0 points, got %d", len(snapshot.Points))
	}
}

func TestWagoGetIoDebugSnapshotLastChanged(t *testing.T) {
	wio := &WagoIO{
		modules: []wagoModuleSpec{
			{di: 4, do: 2, description: "4DI 2DO"},
		},
		totalDI:           4,
		totalDO:           2,
		inputStates:       make([]bool, 4),
		outputStates:      make([]bool, 2),
		inputLastChanged:  make([]time.Time, 4),
		outputLastChanged: make([]time.Time, 2),
		isReady:           true,
		lastPollOk:        time.Now(),
	}

	// Mark some as recently changed
	recentChange := time.Now().Add(-30 * time.Second)
	oldChange := time.Now().Add(-5 * time.Minute)
	wio.inputLastChanged[1] = recentChange
	wio.outputLastChanged[0] = oldChange

	snapshot := wio.GetIoDebugSnapshot()

	if len(snapshot.Points) != 6 {
		t.Fatalf("expected 6 points, got %d", len(snapshot.Points))
	}

	// Input 0 (M1:DI1) - never changed
	if !snapshot.Points[0].LastChanged.IsZero() {
		t.Error("point 0: expected zero LastChanged (never changed)")
	}

	// Input 1 (M1:DI2) - recently changed
	if snapshot.Points[1].LastChanged != recentChange {
		t.Errorf("point 1: expected LastChanged %v, got %v", recentChange, snapshot.Points[1].LastChanged)
	}

	// Output 0 (M1:DO1) - changed long ago
	if snapshot.Points[4].LastChanged != oldChange {
		t.Errorf("point 4: expected LastChanged %v, got %v", oldChange, snapshot.Points[4].LastChanged)
	}

	// Output 1 (M1:DO2) - never changed
	if !snapshot.Points[5].LastChanged.IsZero() {
		t.Error("point 5: expected zero LastChanged (never changed)")
	}
}

func TestWagoToggleOutputNotReady(t *testing.T) {
	wio := &WagoIO{
		totalDO:      2,
		outputStates: make([]bool, 2),
		isReady:      false,
	}

	err := wio.ToggleOutput(0)
	if err == nil {
		t.Error("expected error when driver is not ready")
	}
}

func TestWagoToggleOutputOutOfRange(t *testing.T) {
	wio := &WagoIO{
		totalDO:      2,
		outputStates: make([]bool, 2),
		isReady:      true,
		lastPollOk:   time.Now(),
	}

	err := wio.ToggleOutput(-1)
	if err == nil {
		t.Error("expected error for negative index")
	}

	err = wio.ToggleOutput(2)
	if err == nil {
		t.Error("expected error for index out of range")
	}
}

func TestWagoGetDigitalInputModuleLookupUsesGlobalIndex(t *testing.T) {
	wio := &WagoIO{
		modules: []wagoModuleSpec{
			{di: 4, do: 0, description: "M1"},
			{di: 4, do: 0, description: "M2"},
		},
		totalDI: 8,
		inputs: []WagoDI{
			{driver: nil, index: 0}, // M1:DI1
			{driver: nil, index: 4}, // M2:DI1
			{driver: nil, index: 1}, // M1:DI2
			{driver: nil, index: 5}, // M2:DI2
		},
	}

	in, err := wio.GetDigitalInput("1:2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wdi, ok := in.(*WagoDI)
	if !ok {
		t.Fatalf("expected *WagoDI, got %T", in)
	}

	if wdi.index != 1 {
		t.Fatalf("expected global index 1 for 1:2, got %d", wdi.index)
	}
}

func TestWagoGetInGlobalIndexUsesPhysicalOrder(t *testing.T) {
	wio := &WagoIO{
		modules: []wagoModuleSpec{
			{di: 8, do: 0, diOrder: []int{1, 3, 5, 7, 2, 4, 6, 8}, description: "750-436"},
		},
	}

	tests := []struct {
		moduleRel int
		global    int
	}{
		{moduleRel: 1, global: 0},
		{moduleRel: 2, global: 1},
		{moduleRel: 3, global: 2},
		{moduleRel: 4, global: 3},
		{moduleRel: 5, global: 4},
		{moduleRel: 6, global: 5},
		{moduleRel: 7, global: 6},
		{moduleRel: 8, global: 7},
	}

	for _, tc := range tests {
		got := wio.getInGlobalIndex(1, tc.moduleRel)
		if got != tc.global {
			t.Fatalf("module rel %d: expected global %d, got %d", tc.moduleRel, tc.global, got)
		}
	}
}

func TestWagoGetOutGlobalIndexUsesPhysicalOrder(t *testing.T) {
	wio := &WagoIO{
		modules: []wagoModuleSpec{
			{di: 0, do: 8, doOrder: []int{1, 3, 5, 7, 2, 4, 6, 8}, description: "750-530"},
		},
	}

	tests := []struct {
		moduleRel int
		global    int
	}{
		{moduleRel: 1, global: 0},
		{moduleRel: 2, global: 1},
		{moduleRel: 3, global: 2},
		{moduleRel: 4, global: 3},
		{moduleRel: 5, global: 4},
		{moduleRel: 6, global: 5},
		{moduleRel: 7, global: 6},
		{moduleRel: 8, global: 7},
	}

	for _, tc := range tests {
		got := wio.getOutGlobalIndex(1, tc.moduleRel)
		if got != tc.global {
			t.Fatalf("module rel %d: expected global %d, got %d", tc.moduleRel, tc.global, got)
		}
	}
}

func TestWagoBuildPhysicalToProcessMapWithInterleavedModuleOrder(t *testing.T) {
	wio := &WagoIO{
		modules: []wagoModuleSpec{
			{di: 8, do: 0, diOrder: []int{1, 3, 5, 7, 2, 4, 6, 8}, description: "750-436"},
			{di: 0, do: 8, doOrder: []int{1, 3, 5, 7, 2, 4, 6, 8}, description: "750-530"},
		},
		totalDI: 8,
		totalDO: 8,
	}

	inMap := wio.buildPhysicalToProcessMap(true)
	outMap := wio.buildPhysicalToProcessMap(false)

	expected := []int{0, 2, 4, 6, 1, 3, 5, 7}
	for i := range expected {
		if inMap[i] != expected[i] {
			t.Fatalf("input map[%d]: expected %d, got %d", i, expected[i], inMap[i])
		}
		if outMap[i] != expected[i] {
			t.Fatalf("output map[%d]: expected %d, got %d", i, expected[i], outMap[i])
		}
	}
}
