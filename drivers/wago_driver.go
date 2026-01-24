package drivers

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/simonvetter/modbus"
)

const wagoDriverName = "wago"
const wagoOutputReadOffset = 512
const wagoDefaultModbusPort = 502
const wagoDefaultModbusTimeoutMs = 1000
const wagoDefaultPollIntervalMs = 10
const wagoStaleThreshold = 1 * time.Second

// Verification registers (holding registers, FC3)
const wagoRegisterDOCount = 0x1024 // Number of digital output bits
const wagoRegisterDICount = 0x1025 // Number of digital input bits

// wagoModuleSpec defines the IO counts for a Wago module
type wagoModuleSpec struct {
	di int // digital input count
	do int // digital output count
}

// wagoModuleSpecs maps module part numbers to their IO specifications
var wagoModuleSpecs = map[string]wagoModuleSpec{
	"750-436": {di: 8, do: 0}, // 8DI 24V
	"750-512": {di: 0, do: 2}, // 2DO relay
	"750-611": {di: 2, do: 0}, // Power supply with 2DI
}

// WagoIO implements IoDriver for Wago 750-3xx modbus controllers
type WagoIO struct {
	Address          string
	Port             uint
	ModulesInstalled []string
	PollIntervalMs   uint

	client  *modbus.ModbusClient
	mu      sync.RWMutex
	isReady bool

	inputs  []WagoDI
	outputs []WagoDO
	pushers []*PushEventDetector

	totalDI int
	totalDO int

	// Local state cache
	inputStates  []bool
	outputStates []bool
	lastPollOk   time.Time
	pollTicker   *time.Ticker
	stopPoll     chan struct{}
}

// WagoDI implements DigitalInput for Wago digital inputs
type WagoDI struct {
	driver *WagoIO
	index  uint16
}

// WagoDO implements DigitalOutput for Wago digital outputs
type WagoDO struct {
	driver        *WagoIO
	index         uint16
	onStateUpdate func(bool)
}

func (wio *WagoIO) String() string {
	return wagoDriverName
}

func (wio *WagoIO) IsReady() bool {
	wio.mu.RLock()
	defer wio.mu.RUnlock()
	return wio.isReady
}

func (wio *WagoIO) Setup(ctx context.Context, ios []string) error {
	if wio.Address == "" {
		return errors.New("wago driver: address is required")
	}

	if wio.Port == 0 {
		wio.Port = wagoDefaultModbusPort
	}

	if wio.PollIntervalMs == 0 {
		wio.PollIntervalMs = wagoDefaultPollIntervalMs
	}

	// Calculate total DI and DO counts from installed modules
	for _, modulePartNo := range wio.ModulesInstalled {
		spec, ok := wagoModuleSpecs[modulePartNo]
		if !ok {
			return fmt.Errorf("wago driver: unknown module %s", modulePartNo)
		}
		wio.totalDI += spec.di
		wio.totalDO += spec.do
	}

	// Initialize state caches
	wio.inputStates = make([]bool, wio.totalDI)
	wio.outputStates = make([]bool, wio.totalDO)

	// Create modbus client
	connString := fmt.Sprintf("tcp://%s:%d", wio.Address, wio.Port)
	client, err := modbus.NewClient(&modbus.ClientConfiguration{
		URL:     connString,
		Timeout: wagoDefaultModbusTimeoutMs * time.Millisecond,
	})
	if err != nil {
		return errors.Join(err, fmt.Errorf("wago driver: failed to create modbus client for %s", connString))
	}

	err = client.Open()
	if err != nil {
		return errors.Join(err, fmt.Errorf("wago driver: failed to open modbus connection to %s", connString))
	}

	wio.client = client

	// Verify IO counts match hardware
	if err := wio.verifyIOCounts(); err != nil {
		wio.client.Close()
		return err
	}

	// Parse IO IDs and create input/output instances
	for _, io := range ios {
		driver, ioType, ioId, err := ResolveIoIdString(io)
		if err != nil {
			return errors.Join(err, fmt.Errorf("wago driver: invalid io id format: %s", io))
		}

		if !strings.EqualFold(driver, wio.String()) {
			return fmt.Errorf("wago driver: driver name mismatch, expected %s, got %s", wio.String(), driver)
		}

		index, err := strconv.Atoi(ioId)
		if err != nil {
			return errors.Join(err, fmt.Errorf("wago driver: failed to parse io index: %s", ioId))
		}

		switch ioType {
		case IoTypeDigitalInput:
			if index < 0 || index >= wio.totalDI {
				return fmt.Errorf("wago driver: digital input index %d out of range (0-%d)", index, wio.totalDI-1)
			}
			wio.inputs = append(wio.inputs, WagoDI{
				driver: wio,
				index:  uint16(index),
			})

		case IoTypeDigitalOutput:
			if index < 0 || index >= wio.totalDO {
				return fmt.Errorf("wago driver: digital output index %d out of range (0-%d)", index, wio.totalDO-1)
			}
			wio.outputs = append(wio.outputs, WagoDO{
				driver: wio,
				index:  uint16(index),
			})

		case IoTypePushEventEmitter:
			if index < 0 || index >= wio.totalDI {
				return fmt.Errorf("wago driver: push event emitter (di) index %d out of range (0-%d)", index, wio.totalDI-1)
			}
			dIn := WagoDI{
				driver: wio,
				index:  uint16(index),
			}

			wio.inputs = append(wio.inputs, dIn)
			wio.pushers = append(wio.pushers, NewPushEventDetector(&dIn, fmt.Sprintf("%d", index), nil))

		default:
			return fmt.Errorf("wago driver: unsupported io type: %s", ioType.String())
		}
	}

	// Initial state read before starting poll loop
	if err := wio.refreshStates(); err != nil {
		wio.client.Close()
		return errors.Join(err, errors.New("wago driver: failed initial state read"))
	}

	// Start polling loop
	wio.stopPoll = make(chan struct{})
	wio.pollTicker = time.NewTicker(time.Duration(wio.PollIntervalMs) * time.Millisecond)
	go wio.pollLoop()

	// Start push detectors
	for _, push := range wio.pushers {
		push.Start()
	}

	wio.isReady = true
	return nil
}

func (wio *WagoIO) verifyIOCounts() error {
	// Read DO count register (0x1024)
	doCount, err := wio.client.ReadRegister(wagoRegisterDOCount, modbus.HOLDING_REGISTER)
	if err != nil {
		return errors.Join(err, errors.New("wago driver: failed to read DO count register"))
	}

	// Read DI count register (0x1025)
	diCount, err := wio.client.ReadRegister(wagoRegisterDICount, modbus.HOLDING_REGISTER)
	if err != nil {
		return errors.Join(err, errors.New("wago driver: failed to read DI count register"))
	}

	if int(doCount) != wio.totalDO {
		return fmt.Errorf("wago driver: DO count mismatch - config expects %d, hardware reports %d", wio.totalDO, doCount)
	}

	if int(diCount) != wio.totalDI {
		return fmt.Errorf("wago driver: DI count mismatch - config expects %d, hardware reports %d", wio.totalDI, diCount)
	}

	return nil
}

func (wio *WagoIO) pollLoop() {
	for {
		select {
		case <-wio.stopPoll:
			return
		case <-wio.pollTicker.C:
			_ = wio.refreshStates()
		}
	}
}

func (wio *WagoIO) refreshStates() error {
	var errs error
	var inputs, outputs []bool

	// FC2 - batch read all discrete inputs (outside lock)
	if wio.totalDI > 0 {
		var err error
		inputs, err = wio.client.ReadDiscreteInputs(0, uint16(wio.totalDI))
		if err != nil {
			errs = errors.Join(errs, fmt.Errorf("failed to read discrete inputs: %w", err))
		}
	}

	// FC1 - batch read all coils at offset 512 (outside lock)
	if wio.totalDO > 0 {
		var err error
		outputs, err = wio.client.ReadCoils(wagoOutputReadOffset, uint16(wio.totalDO))
		if err != nil {
			errs = errors.Join(errs, fmt.Errorf("failed to read coils: %w", err))
		}
	}

	// DEBUG
	// for ix, in := range inputs {
	// 	if in {
	// 		fmt.Println("wago input is ON", ix)
	// 	}
	// }

	// Lock only for copying to state slices
	wio.mu.Lock()
	if inputs != nil {
		copy(wio.inputStates, inputs)
	}
	if outputs != nil {
		copy(wio.outputStates, outputs)
	}
	if errs == nil {
		wio.lastPollOk = time.Now()
	}
	wio.mu.Unlock()

	return errs
}

func (wio *WagoIO) isStateStale() bool {
	return time.Since(wio.lastPollOk) > wagoStaleThreshold
}

func (wio *WagoIO) Close() error {
	wio.mu.Lock()
	defer wio.mu.Unlock()

	wio.isReady = false

	// Stop pushers
	for _, push := range wio.pushers {
		push.Stop()
	}

	// Stop polling
	if wio.pollTicker != nil {
		wio.pollTicker.Stop()
		close(wio.stopPoll)
	}

	if wio.client == nil {
		return nil
	}

	// Turn off all outputs before closing
	for i := range wio.outputs {
		_ = wio.client.WriteCoil(wio.outputs[i].index, false)
	}

	return wio.client.Close()
}

func (wio *WagoIO) GetDigitalInput(id string) (DigitalInput, error) {
	index, err := strconv.Atoi(id)
	if err != nil {
		return nil, errors.Join(err, fmt.Errorf("wago driver: failed to parse input id: %s", id))
	}

	for ix := range wio.inputs {
		if wio.inputs[ix].index == uint16(index) {
			return &wio.inputs[ix], nil
		}
	}

	return nil, fmt.Errorf("wago driver: digital input %d not found", index)
}

func (wio *WagoIO) GetDigitalOutput(id string) (DigitalOutput, error) {
	index, err := strconv.Atoi(id)
	if err != nil {
		return nil, errors.Join(err, fmt.Errorf("wago driver: failed to parse output id: %s", id))
	}

	for ix := range wio.outputs {
		if wio.outputs[ix].index == uint16(index) {
			return &wio.outputs[ix], nil
		}
	}

	return nil, fmt.Errorf("wago driver: digital output %d not found", index)
}

func (wio *WagoIO) GetAnalogOutput(id string) (AnalogOutput, error) {
	return nil, errors.New("wago driver: analog output not implemented")
}

func (wio *WagoIO) GetRgbwOutput(id string) (RgbwOutput, error) {
	return nil, errors.New("wago driver: rgbw output not implemented")
}

func (wio *WagoIO) GetPushEventEmitter(id string) (PushEventEmitter, error) {
	index, err := strconv.Atoi(id)
	if err != nil {
		return nil, errors.Join(err, fmt.Errorf("wago driver: failed to parse push event emitter id: %s", id))
	}

	for ix := range wio.pushers {
		if strings.EqualFold(wio.pushers[ix].name, id) {
			return wio.pushers[ix], nil
		}
	}

	return nil, fmt.Errorf("wago driver: push event emitter %d not found", index)
}

// WagoDI methods

func (wdi *WagoDI) GetState() (bool, error) {
	wdi.driver.mu.RLock()
	defer wdi.driver.mu.RUnlock()

	if !wdi.driver.isReady {
		return false, errors.New("wago driver: not ready")
	}

	if wdi.driver.isStateStale() {
		return false, errors.New("wago driver: state is stale, last successful poll >1s ago")
	}

	return wdi.driver.inputStates[wdi.index], nil
}

func (wdi *WagoDI) String() string {
	return GetIoIdString(wagoDriverName, IoTypeDigitalInput, strconv.Itoa(int(wdi.index)))
}

func (wdi *WagoDI) IsHealthy() bool {
	wdi.driver.mu.RLock()
	defer wdi.driver.mu.RUnlock()
	return wdi.driver.isReady && !wdi.driver.isStateStale()
}

// WagoDO methods

func (wdo *WagoDO) GetState() (bool, error) {
	wdo.driver.mu.RLock()
	defer wdo.driver.mu.RUnlock()

	if !wdo.driver.isReady {
		return false, errors.New("wago driver: not ready")
	}

	if wdo.driver.isStateStale() {
		return false, errors.New("wago driver: state is stale, last successful poll >1s ago")
	}

	return wdo.driver.outputStates[wdo.index], nil
}

func (wdo *WagoDO) Set(state bool) error {
	wdo.driver.mu.Lock()
	defer wdo.driver.mu.Unlock()

	if !wdo.driver.isReady {
		return errors.New("wago driver: not ready")
	}

	// FC5 - Write Single Coil
	err := wdo.driver.client.WriteCoil(wdo.index, state)
	if err != nil {
		return errors.Join(err, fmt.Errorf("wago driver: failed to write digital output %d", wdo.index))
	}

	if wdo.onStateUpdate != nil {
		wdo.onStateUpdate(state)
	}

	return nil
}

func (wdo *WagoDO) String() string {
	return GetIoIdString(wagoDriverName, IoTypeDigitalOutput, strconv.Itoa(int(wdo.index)))
}

func (wdo *WagoDO) SetOnStateUpdate(onStateUpdate func(bool)) error {
	wdo.onStateUpdate = onStateUpdate
	return nil
}

func (wdo *WagoDO) IsHealthy() bool {
	wdo.driver.mu.RLock()
	defer wdo.driver.mu.RUnlock()
	return wdo.driver.isReady && !wdo.driver.isStateStale()
}
