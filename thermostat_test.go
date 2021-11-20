package main

import "testing"

func assertFloats(t testing.TB, got, want float64) {
	t.Helper()

	if got != want {
		t.Errorf("got: %f, want: %f", got, want)
	}
}

func assertInts(t testing.TB, got, want int) {
	t.Helper()

	if got != want {
		t.Errorf("got: %d, want: %d", got, want)
	}
}

func TestThremostatInit(t *testing.T) {

	thermo := Thermostat{}
	thermo.DriverName = "mock_driver"
	thermo.HeatPin = uint8(5)

	md := MockIoDriver{}

	err := thermo.Init(&md)
	if err == nil {
		t.Error("got nil error when Init with not ready driver")
	}

	md.Setup([]uint8{}, []uint8{5})

	err = thermo.Init(&md)
	if err != nil {
		t.Errorf("got error from Thermostat Init: %v", err)
	}

	got := thermo.MinimumTemperature
	want := thermostatMinimumTemperature
	assertFloats(t, got, want)

	got = thermo.MaximumTemperature
	want = thermostatMaximumTemperature
	assertFloats(t, got, want)

	got = thermo.StepTemperature
	want = thermostatTemperatureStep
	assertFloats(t, got, want)

	got = thermo.CoolingThreshold
	want = defaultThermostatThreshold
	assertFloats(t, got, want)

	got = thermo.HeatingThreshold
	assertFloats(t, got, want)
}

func TestCheckHeatingCondition(t *testing.T) {
	thermo := Thermostat{}
	thermo.DriverName = "mock_driver"
	thermo.HeatPin = uint8(3)

	md := MockIoDriver{}
	md.Setup([]uint8{}, []uint8{3})
	thermo.Init(&md)

	heatOut, _ := md.GetOutput(3)

	thermo.CurrentTemperature = 20.0
	thermo.TargetTemperature = 15.0
	if thermo.checkHeatingCondition() != false {
		t.Error("heatingCondition mismatch")
	}

	thermo.CurrentTemperature = 20.3
	thermo.TargetTemperature = 20.5
	if thermo.checkHeatingCondition() != false {
		t.Error("heatingCondition mismatch")
	}

	thermo.CurrentTemperature = 18
	thermo.TargetTemperature = 20.5
	if thermo.checkHeatingCondition() != true {
		t.Error("heatingCondition mismatch")
	}
	heatOut.Set(true)

	thermo.CurrentTemperature = 20.7
	thermo.TargetTemperature = 20.5
	if thermo.checkHeatingCondition() != true {
		t.Error("heatingCondition mismatch")
	}

	thermo.CurrentTemperature = 21.8
	thermo.TargetTemperature = 20.5
	if thermo.checkHeatingCondition() != false {
		t.Error("heatingCondition mismatch")
	}
}

func TestGetCurrentHeatingCoolingState(t *testing.T) {
	thermo := Thermostat{}
	thermo.DriverName = "mock_driver"
	thermo.HeatPin = uint8(3)
	thermo.CoolPin = uint8(5)
	thermo.CoolingEnabled = true

	md := MockIoDriver{}
	md.Setup([]uint8{}, []uint8{3, 5})
	thermo.Init(&md)

	heatOut, _ := md.GetOutput(3)
	coolOut, _ := md.GetOutput(5)

	heatOut.Set(false)
	state, _ := heatOut.GetState()
	assertBools(t, state, false)

	heatOut.Set(true)
	state, _ = heatOut.GetState()
	assertBools(t, state, true)

	want := 1
	got := thermo.getCurrentHeatingCoolingState()
	assertInts(t, got, want)

	heatOut.Set(false)
	coolOut.Set(false)
	want = 0
	got = thermo.getCurrentHeatingCoolingState()
	assertInts(t, got, want)

	heatOut.Set(false)
	coolOut.Set(true)
	want = 2
	got = thermo.getCurrentHeatingCoolingState()
	assertInts(t, got, want)
}

func TestCalculateOutputs(t *testing.T) {
	thermo := Thermostat{}
	thermo.DriverName = "mock_driver"
	thermo.HeatPin = uint8(3)
	thermo.CoolPin = uint8(5)
	thermo.CoolingEnabled = true

	md := MockIoDriver{}
	md.Setup([]uint8{}, []uint8{3, 5})
	thermo.Init(&md)

	heatOut, _ := md.GetOutput(3)
	coolOut, _ := md.GetOutput(5)

	thermo.TargetState = 0
	heatState, _ := heatOut.GetState()
	coolState, _ := coolOut.GetState()
	thermo.calculateOutputs()
	assertBools(t, heatState, false)
	assertBools(t, coolState, false)

	thermo.TargetState = 1

	thermo.CurrentTemperature = 19.0
	thermo.TargetTemperature = 21.5
	thermo.calculateOutputs()

	heatState, _ = heatOut.GetState()
	coolState, _ = coolOut.GetState()
	assertBools(t, heatState, true)
	assertBools(t, coolState, false)

	thermo.CurrentTemperature = 23.0
	thermo.TargetTemperature = 21.5
	thermo.calculateOutputs()

	heatState, _ = heatOut.GetState()
	coolState, _ = coolOut.GetState()
	assertBools(t, heatState, false)
	assertBools(t, coolState, false)

}
