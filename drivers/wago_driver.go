package drivers

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/log"
	"github.com/hubertat/swkit/logging"
	"github.com/simonvetter/modbus"
)

const wagoDriverName = "wago"
const wagoOutputReadOffset = 512
const wagoDefaultModbusPort = 502
const wagoDefaultModbusTimeoutMs = 1000
const wagoDefaultPollIntervalMs = 10
const wagoStaleThreshold = 1 * time.Second

// Analog output process image. Output words live in the holding-register space
// (separate from the digital coil space): write via FC6/FC16 and read back via
// FC3 at the SAME address (0..N-1). Unlike digital coils there is no +512
// readback mirror for words. The AO word index counts only analog/word output
// channels.
// NOTE: verify the register base and the 0..0x7FFF=0..10V word scaling against
// the coupler's WBM "Modbus Mapping" page before trusting hardware values.
const wagoAnalogOutputReadOffset = 0
const wagoAnalogOutputMax = 0x7FFF // 32767, full-scale (10V) for a 0-10V module

// The analog count register (0x1022) reports the process image size in bits;
// WAGO analog channels are 16-bit words, so bits = channels * 16. (The digital
// count registers report bits too, but there it equals the channel count.)
const wagoAnalogBitsPerChannel = 16

// Verification registers (holding registers, FC3)
const wagoRegisterAOCount = 0x1022 // Number of analog output words
const wagoRegisterDOCount = 0x1024 // Number of digital output bits
const wagoRegisterDICount = 0x1025 // Number of digital input bits

// wagoModuleSpec defines the IO counts for a Wago module
type wagoModuleSpec struct {
	di          int // digital input count
	do          int // digital output count
	ao          int // analog output channel count
	diOrder     []int
	doOrder     []int
	aoOrder     []int
	description string
}

// wagoModuleSpecs maps module part numbers to their IO specifications
var wagoModuleSpecs = map[string]wagoModuleSpec{
	// 750-436 front terminal/channel order is interleaved (1,5,2,6,3,7,4,8).
	// The explicit order map makes module-relative IDs follow physical channel order.
	"750-436": {di: 8, do: 0, diOrder: []int{1, 3, 5, 7, 2, 4, 6, 8}, description: "8DI 24V (-)"},
	"750-512": {di: 0, do: 2, description: "2DO relay"},                                        // 2DO relay
	"750-611": {di: 2, do: 0, description: "Fused power (230VAC) supply with diagnostics 2DI"}, // Power supply with 2DI
	"750-610": {di: 2, do: 0, description: "Fused power (24VDC) supply with diagnostics 2DI"},  // Power supply with 2DI
	// Keep output channel semantics consistent with 8-channel physical terminal order.
	"750-530": {di: 0, do: 8, doOrder: []int{1, 3, 5, 7, 2, 4, 6, 8}, description: "8DO output 24VDC (+)"},
	"750-550": {ao: 2, description: "2AO 0-10V DC"},
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
	logger  *log.Logger

	modules       []wagoModuleSpec
	inputs        []WagoDI
	outputs       []WagoDO
	analogOutputs []WagoAO
	pushers       []*PushEventDetector

	totalDI int
	totalDO int
	totalAO int

	// Local state cache
	inputStates             []bool
	outputStates            []bool
	analogOutputStates      []uint16
	inputLastChanged        []time.Time
	outputLastChanged       []time.Time
	analogOutputLastChanged []time.Time
	lastPollOk              time.Time
	pollTicker              *time.Ticker
	stopPoll                chan struct{}

	// Mapping between physical channel order (driver-facing) and coupler process-image order.
	diPhysicalToProcess []int
	doPhysicalToProcess []int
	aoPhysicalToProcess []int
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

// WagoAO implements AnalogOutput for Wago analog output channels (e.g. 750-550).
// The value is the raw 16-bit process word; for a 0-10V module 0 = 0V and
// 0x7FFF (32767) = 10V.
type WagoAO struct {
	driver *WagoIO
	index  uint16
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
	wio.logger = logging.NewLogger(logging.PrefixWago)

	if wio.Address == "" {
		return errors.New("wago driver: address is required")
	}

	if wio.Port == 0 {
		wio.Port = wagoDefaultModbusPort
	}

	if wio.PollIntervalMs == 0 {
		wio.PollIntervalMs = wagoDefaultPollIntervalMs
	}

	wio.logger.Debug("setup starting", "address", wio.Address, "port", wio.Port)

	// Calculate total DI and DO counts from installed modules
	for _, modulePartNo := range wio.ModulesInstalled {
		spec, ok := wagoModuleSpecs[modulePartNo]
		if !ok {
			return fmt.Errorf("wago driver: unknown module %s", modulePartNo)
		}
		wio.modules = append(wio.modules, spec)
		wio.totalDI += spec.di
		wio.totalDO += spec.do
		wio.totalAO += spec.ao
	}

	// Initialize state caches
	wio.inputStates = make([]bool, wio.totalDI)
	wio.outputStates = make([]bool, wio.totalDO)
	wio.analogOutputStates = make([]uint16, wio.totalAO)
	wio.inputLastChanged = make([]time.Time, wio.totalDI)
	wio.outputLastChanged = make([]time.Time, wio.totalDO)
	wio.analogOutputLastChanged = make([]time.Time, wio.totalAO)
	wio.diPhysicalToProcess = wio.buildPhysicalToProcessMap(wagoChannelDI)
	wio.doPhysicalToProcess = wio.buildPhysicalToProcessMap(wagoChannelDO)
	wio.aoPhysicalToProcess = wio.buildPhysicalToProcessMap(wagoChannelAO)

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

		moduleNo, relIndex, err := wio.parseIoId(ioId)
		if err != nil {
			return errors.Join(err, fmt.Errorf("wago driver: failed to parse io index: %s", ioId))
		}

		switch ioType {
		case IoTypeDigitalInput:
			index := relIndex
			if moduleNo != 0 {
				index = wio.getInGlobalIndex(moduleNo, relIndex)
				if index < 0 {
					return fmt.Errorf("wago driver: failed to get global input index (module: %d, rel index: %d)", moduleNo, relIndex)
				}
			}
			if index < 0 || index >= wio.totalDI {
				return fmt.Errorf("wago driver: digital input index %d out of range (0-%d)", index, wio.totalDI-1)
			}
			wio.inputs = append(wio.inputs, WagoDI{
				driver: wio,
				index:  uint16(index),
			})

		case IoTypePushEventEmitter:
			index := relIndex
			if moduleNo != 0 {
				index = wio.getInGlobalIndex(moduleNo, relIndex)
				if index < 0 {
					return fmt.Errorf("wago driver: failed to get global input index (module: %d, rel index: %d)", moduleNo, relIndex)
				}
			}
			if index < 0 || index >= wio.totalDI {
				return fmt.Errorf("wago driver: push event emitter (di) index %d out of range (0-%d)", index, wio.totalDI-1)
			}
			dIn := WagoDI{
				driver: wio,
				index:  uint16(index),
			}
			wio.inputs = append(wio.inputs, dIn)
			wio.pushers = append(wio.pushers, NewPushEventDetector(&wio.inputs[len(wio.inputs)-1], fmt.Sprintf("%d", index), nil, wio.logger))

		case IoTypeDigitalOutput:
			index := relIndex
			if moduleNo != 0 {
				index = wio.getOutGlobalIndex(moduleNo, relIndex)
				if index < 0 {
					return fmt.Errorf("wago driver: failed to get global output index (module: %d, rel index: %d)", moduleNo, relIndex)
				}
			}
			if index < 0 || index >= wio.totalDO {
				return fmt.Errorf("wago driver: digital output index %d out of range (0-%d)", index, wio.totalDO-1)
			}
			wio.outputs = append(wio.outputs, WagoDO{
				driver: wio,
				index:  uint16(index),
			})

		case IoTypeAnalogOutput:
			index := relIndex
			if moduleNo != 0 {
				index = wio.getAoGlobalIndex(moduleNo, relIndex)
				if index < 0 {
					return fmt.Errorf("wago driver: failed to get global analog output index (module: %d, rel index: %d)", moduleNo, relIndex)
				}
			}
			if index < 0 || index >= wio.totalAO {
				return fmt.Errorf("wago driver: analog output index %d out of range (0-%d)", index, wio.totalAO-1)
			}
			wio.analogOutputs = append(wio.analogOutputs, WagoAO{
				driver: wio,
				index:  uint16(index),
			})

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
	wio.logger.Info("setup complete", "totalDI", wio.totalDI, "totalDO", wio.totalDO)
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

	// Read AO count register (0x1022) and verify when analog outputs are configured.
	// The register reports the analog output process image size in bits, so the
	// expected value is the configured channel count times 16 bits per channel.
	if wio.totalAO > 0 {
		aoBits, err := wio.client.ReadRegister(wagoRegisterAOCount, modbus.HOLDING_REGISTER)
		if err != nil {
			return errors.Join(err, errors.New("wago driver: failed to read AO count register"))
		}
		expectedBits := wio.totalAO * wagoAnalogBitsPerChannel
		if int(aoBits) != expectedBits {
			return fmt.Errorf("wago driver: AO count mismatch - config expects %d channels (%d bits), hardware reports %d bits (%d channels)",
				wio.totalAO, expectedBits, aoBits, int(aoBits)/wagoAnalogBitsPerChannel)
		}
	}

	return nil
}

func (wio *WagoIO) pollLoop() {
	consecutiveErrors := 0
	const maxConsecutiveErrors = 5

	for {
		select {
		case <-wio.stopPoll:
			return
		case <-wio.pollTicker.C:
			err := wio.refreshStates()
			if err != nil {
				consecutiveErrors++
				if consecutiveErrors == 1 {
					wio.logger.Warn("wago driver: poll error started", "error", err)
				}
				if consecutiveErrors >= maxConsecutiveErrors {
					wio.logger.Error("wago driver: max consecutive errors reached, reconnecting", "errors", consecutiveErrors)
					wio.reconnect()
					consecutiveErrors = 0
				}
			} else {
				if consecutiveErrors > 0 {
					wio.logger.Info("wago driver: poll recovered after errors", "errorCount", consecutiveErrors)
				}
				consecutiveErrors = 0
			}
		}
	}
}

func (wio *WagoIO) reconnect() {
	wio.mu.Lock()
	defer wio.mu.Unlock()

	connString := fmt.Sprintf("tcp://%s:%d", wio.Address, wio.Port)
	wio.logger.Warn("wago driver: attempting reconnection", "address", connString)

	if wio.client != nil {
		_ = wio.client.Close()
	}

	client, err := modbus.NewClient(&modbus.ClientConfiguration{
		URL:     connString,
		Timeout: wagoDefaultModbusTimeoutMs * time.Millisecond,
	})
	if err != nil {
		wio.logger.Error("wago driver: failed to create client during reconnect", "error", err)
		return
	}

	if err = client.Open(); err != nil {
		wio.logger.Error("wago driver: failed to open connection during reconnect", "error", err)
		return
	}

	wio.client = client
	wio.logger.Info("wago driver: reconnection successful", "address", connString)
}

func (wio *WagoIO) refreshStates() error {
	wio.mu.Lock()
	defer wio.mu.Unlock()

	if wio.client == nil {
		return errors.New("wago driver: client not initialized")
	}

	var errs error
	var inputs []bool
	var outputs []bool
	var analogOutputs []uint16

	// FC2 - batch read all discrete inputs
	if wio.totalDI > 0 {
		var err error
		inputs, err = wio.client.ReadDiscreteInputs(0, uint16(wio.totalDI))
		if err != nil {
			errs = errors.Join(errs, fmt.Errorf("failed to read discrete inputs: %w", err))
		}
	}

	// FC1 - batch read all coils at offset 512
	if wio.totalDO > 0 {
		var err error
		outputs, err = wio.client.ReadCoils(wagoOutputReadOffset, uint16(wio.totalDO))
		if err != nil {
			errs = errors.Join(errs, fmt.Errorf("failed to read coils: %w", err))
		}
	}

	// FC3 - batch read analog output readback words at offset 512
	if wio.totalAO > 0 {
		var err error
		analogOutputs, err = wio.client.ReadRegisters(wagoAnalogOutputReadOffset, uint16(wio.totalAO), modbus.HOLDING_REGISTER)
		if err != nil {
			errs = errors.Join(errs, fmt.Errorf("failed to read analog output registers: %w", err))
		}
	}

	// Debug log active inputs (driver-facing physical index)
	for ix, in := range wio.remapInputsToPhysical(inputs) {
		if in {
			wio.logger.Debug("input is ON", "index", ix)
		}
	}

	now := time.Now()
	if inputs != nil {
		physicalInputs := wio.remapInputsToPhysical(inputs)
		for i, v := range physicalInputs {
			if i < len(wio.inputStates) && wio.inputStates[i] != v {
				wio.inputLastChanged[i] = now
			}
		}
		copy(wio.inputStates, physicalInputs)
	}
	if outputs != nil {
		physicalOutputs := wio.remapOutputsToPhysical(outputs)
		for i, v := range physicalOutputs {
			if i < len(wio.outputStates) && wio.outputStates[i] != v {
				wio.outputLastChanged[i] = now
			}
		}
		copy(wio.outputStates, physicalOutputs)
	}
	if analogOutputs != nil {
		physicalAnalogOutputs := wio.remapAnalogOutputsToPhysical(analogOutputs)
		for i, v := range physicalAnalogOutputs {
			if i < len(wio.analogOutputStates) && wio.analogOutputStates[i] != v {
				wio.analogOutputLastChanged[i] = now
			}
		}
		copy(wio.analogOutputStates, physicalAnalogOutputs)
	}
	if errs == nil {
		wio.lastPollOk = now
	}

	return errs
}

func (wio *WagoIO) isStateStale() bool {
	return time.Since(wio.lastPollOk) > wagoStaleThreshold
}

// parseIoId parses an IO ID string and returns an error if it's invalid.
// it can parse different formats and response accordingly
// - integer index of io (int)
// - module number and relative io number for specific module (int, int): 2:1
// - module number and relative io number for specific module (int, string): 2:a, 2:A
func (wio *WagoIO) parseIoId(id string) (module int, index int, err error) {
	intIndex, err := strconv.Atoi(id)
	if err == nil {
		// It is integer index, no module no provided
		return 0, intIndex, nil
	}

	idParts := strings.Split(id, ":")
	if len(idParts) == 2 {
		modulePart := strings.ToLower(idParts[0])
		indexPart := strings.ToLower(idParts[1])
		moduleIndex, err := strconv.Atoi(modulePart)
		if err == nil && moduleIndex > 0 {
			// moduleIndex is integer - we got it
		}
		if moduleIndex == 0 {
			// moduleIndex is not integer, try to parse letter (a to z)
			moduleIndex = int(modulePart[0] - 'a' + 1)
			if moduleIndex < 1 || moduleIndex > 26 {
				return 0, 0, fmt.Errorf("invalid module number: %s", modulePart)
			}
		}
		// same for indexPart
		indexIndex, err := strconv.Atoi(indexPart)
		if err == nil && indexIndex > 0 {
			// indexIndex is integer - we got it
		}
		if indexIndex == 0 {
			// indexIndex is not integer, try to parse letter (a to z)
			indexIndex = int(indexPart[0] - 'a' + 1)
			if indexIndex < 1 || indexIndex > 26 {
				return 0, 0, fmt.Errorf("invalid io number: %s", indexPart)
			}
		}
		return moduleIndex, indexIndex, nil
	}

	// invalid io id format
	return 0, 0, fmt.Errorf("invalid io id format: %s", id)
}

func (wio *WagoIO) getInGlobalIndex(moduleNo int, ioIndex int) int {
	if moduleNo > len(wio.modules) {
		return -1
	}
	module := wio.modules[moduleNo-1]
	if ioIndex > module.di {
		return -1
	}

	// Count all inputs until we get to our moduleNo and ioIndex (our values are 1 based!)
	inCount := 0
	for ix, m := range wio.modules {
		if ix+1 == moduleNo {
			return inCount + ioIndex - 1
		}
		inCount += m.di
	}

	return -1
}

func (wio *WagoIO) getOutGlobalIndex(moduleNo int, ioIndex int) int {
	if moduleNo > len(wio.modules) {
		return -1
	}
	module := wio.modules[moduleNo-1]
	if ioIndex > module.do {
		return -1
	}

	// Count all outputs until we get to our moduleNo and ioIndex (our values are 1 based!)
	outCount := 0
	for ix, m := range wio.modules {
		if ix+1 == moduleNo {
			return outCount + ioIndex - 1
		}
		outCount += m.do
	}

	return -1
}

func (wio *WagoIO) getAoGlobalIndex(moduleNo int, ioIndex int) int {
	if moduleNo > len(wio.modules) {
		return -1
	}
	module := wio.modules[moduleNo-1]
	if ioIndex > module.ao {
		return -1
	}

	// Count all analog outputs until we get to our moduleNo and ioIndex (1 based!)
	aoCount := 0
	for ix, m := range wio.modules {
		if ix+1 == moduleNo {
			return aoCount + ioIndex - 1
		}
		aoCount += m.ao
	}

	return -1
}

func (wio *WagoIO) mapAoPhysicalToProcess(physicalIndex int) int {
	if physicalIndex < 0 || physicalIndex >= len(wio.aoPhysicalToProcess) {
		return physicalIndex
	}
	return wio.aoPhysicalToProcess[physicalIndex]
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
		processIndex := wio.mapOutputPhysicalToProcess(int(wio.outputs[i].index))
		_ = wio.client.WriteCoil(uint16(processIndex), false)
	}

	// Drive all analog outputs to 0 before closing
	for i := range wio.analogOutputs {
		processIndex := wio.mapAoPhysicalToProcess(int(wio.analogOutputs[i].index))
		_ = wio.client.WriteRegister(uint16(processIndex), 0)
	}

	return wio.client.Close()
}

// GetDigitalInput retrieves a digital input by its ID.
// use naming patter
func (wio *WagoIO) GetDigitalInput(id string) (DigitalInput, error) {
	moduleNo, index, err := wio.parseIoId(id)
	if err != nil {
		return nil, errors.Join(err, fmt.Errorf("wago driver: failed to parse input id: %s", id))
	}

	if moduleNo == 0 {
		for ix := range wio.inputs {
			if wio.inputs[ix].index == uint16(index) {
				return &wio.inputs[ix], nil
			}
		}
	} else {
		if moduleNo > len(wio.modules) {
			return nil, fmt.Errorf("wago driver: digital input not found, module %d not found", moduleNo)
		}
		module := wio.modules[moduleNo-1]
		if index > module.di {
			return nil, fmt.Errorf("wago driver: digital input %d not found, selected module (%d) has %d inputs", index, moduleNo, module.di)
		}
		globIndex := wio.getInGlobalIndex(moduleNo, index)
		if globIndex < 0 {
			return nil, fmt.Errorf("wago driver: digital input %d not found, failed to get global index", index)
		}
		for ix := range wio.inputs {
			if wio.inputs[ix].index == uint16(globIndex) {
				return &wio.inputs[ix], nil
			}
		}
	}

	return nil, fmt.Errorf("wago driver: digital input %d not found", index)
}

func (wio *WagoIO) GetDigitalOutput(id string) (DigitalOutput, error) {
	moduleNo, index, err := wio.parseIoId(id)
	if err != nil {
		return nil, errors.Join(err, fmt.Errorf("wago driver: failed to parse output id: %s", id))
	}

	if moduleNo == 0 {
		for ix := range wio.outputs {
			if wio.outputs[ix].index == uint16(index) {
				return &wio.outputs[ix], nil
			}
		}
	} else {
		if moduleNo > len(wio.modules) {
			return nil, fmt.Errorf("wago driver: digital output not found, module %d not found", moduleNo)
		}
		module := wio.modules[moduleNo-1]
		if index > module.do {
			return nil, fmt.Errorf("wago driver: digital output %d not found, selected module (%d) has %d outputs", index, moduleNo, module.do)
		}
		globIndex := wio.getOutGlobalIndex(moduleNo, index)
		if globIndex < 0 {
			return nil, fmt.Errorf("wago driver: digital output %d not found, failed to get global index", index)
		}
		for ix := range wio.outputs {
			if wio.outputs[ix].index == uint16(globIndex) {
				return &wio.outputs[ix], nil
			}
		}
	}

	return nil, fmt.Errorf("wago driver: digital output %d not found", index)
}

func (wio *WagoIO) GetAnalogOutput(id string) (AnalogOutput, error) {
	moduleNo, index, err := wio.parseIoId(id)
	if err != nil {
		return nil, errors.Join(err, fmt.Errorf("wago driver: failed to parse analog output id: %s", id))
	}

	if moduleNo == 0 {
		for ix := range wio.analogOutputs {
			if wio.analogOutputs[ix].index == uint16(index) {
				return &wio.analogOutputs[ix], nil
			}
		}
	} else {
		if moduleNo > len(wio.modules) {
			return nil, fmt.Errorf("wago driver: analog output not found, module %d not found", moduleNo)
		}
		module := wio.modules[moduleNo-1]
		if index > module.ao {
			return nil, fmt.Errorf("wago driver: analog output %d not found, selected module (%d) has %d analog outputs", index, moduleNo, module.ao)
		}
		globIndex := wio.getAoGlobalIndex(moduleNo, index)
		if globIndex < 0 {
			return nil, fmt.Errorf("wago driver: analog output %d not found, failed to get global index", index)
		}
		for ix := range wio.analogOutputs {
			if wio.analogOutputs[ix].index == uint16(globIndex) {
				return &wio.analogOutputs[ix], nil
			}
		}
	}

	return nil, fmt.Errorf("wago driver: analog output %d not found", index)
}

func (wio *WagoIO) GetRgbwOutput(id string) (RgbwOutput, error) {
	return nil, errors.New("wago driver: rgbw output not implemented")
}

func (wio *WagoIO) GetPushEventEmitter(id string) (PushEventEmitter, error) {
	moduleNo, index, err := wio.parseIoId(id)
	if err != nil {
		return nil, errors.Join(err, fmt.Errorf("wago driver: failed to parse push event emitter id: %s", id))
	}

	// Convert to global index if module specified
	globIndex := index
	if moduleNo != 0 {
		if moduleNo > len(wio.modules) {
			return nil, fmt.Errorf("wago driver: push event emitter not found, module %d not found", moduleNo)
		}
		module := wio.modules[moduleNo-1]
		if index > module.di {
			return nil, fmt.Errorf("wago driver: push event emitter %d not found, selected module (%d) has %d inputs", index, moduleNo, module.di)
		}
		globIndex = wio.getInGlobalIndex(moduleNo, index)
		if globIndex < 0 {
			return nil, fmt.Errorf("wago driver: push event emitter %d not found, failed to get global index", index)
		}
	}

	// Pushers are named by their global index
	globIndexStr := fmt.Sprintf("%d", globIndex)
	for ix := range wio.pushers {
		if strings.EqualFold(wio.pushers[ix].name, globIndexStr) {
			return wio.pushers[ix], nil
		}
	}

	return nil, fmt.Errorf("wago driver: push event emitter %d (global index %d) not found", index, globIndex)
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
	processIndex := wdo.driver.mapOutputPhysicalToProcess(int(wdo.index))
	err := wdo.driver.client.WriteCoil(uint16(processIndex), state)
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

// WagoAO methods

func (wao *WagoAO) GetMinMax() (int, int) {
	return 0, wagoAnalogOutputMax
}

func (wao *WagoAO) GetState() (int, error) {
	wao.driver.mu.RLock()
	defer wao.driver.mu.RUnlock()

	if !wao.driver.isReady {
		return 0, errors.New("wago driver: not ready")
	}

	if wao.driver.isStateStale() {
		return 0, errors.New("wago driver: state is stale, last successful poll >1s ago")
	}

	return int(wao.driver.analogOutputStates[wao.index]), nil
}

func (wao *WagoAO) Set(value int) error {
	if value < 0 {
		value = 0
	}
	if value > wagoAnalogOutputMax {
		value = wagoAnalogOutputMax
	}

	wao.driver.mu.Lock()
	defer wao.driver.mu.Unlock()

	if !wao.driver.isReady {
		return errors.New("wago driver: not ready")
	}

	// FC6 - Write Single (holding) Register
	processIndex := wao.driver.mapAoPhysicalToProcess(int(wao.index))
	err := wao.driver.client.WriteRegister(uint16(processIndex), uint16(value))
	if err != nil {
		return errors.Join(err, fmt.Errorf("wago driver: failed to write analog output %d", wao.index))
	}

	return nil
}

func (wao *WagoAO) String() string {
	return GetIoIdString(wagoDriverName, IoTypeAnalogOutput, strconv.Itoa(int(wao.index)))
}

func (wao *WagoAO) IsHealthy() bool {
	wao.driver.mu.RLock()
	defer wao.driver.mu.RUnlock()
	return wao.driver.isReady && !wao.driver.isStateStale()
}

// GetIoDebugSnapshot returns a snapshot of all IO points for debug display
func (wio *WagoIO) GetIoDebugSnapshot() IoDebugSnapshot {
	wio.mu.RLock()
	defer wio.mu.RUnlock()

	healthy := wio.isReady && !wio.isStateStale()
	var points []IoPointState

	diIndex := 0
	doIndex := 0
	aoIndex := 0
	for mIdx, mod := range wio.modules {
		moduleNum := mIdx + 1

		for i := 0; i < mod.di; i++ {
			state := false
			var lastChanged time.Time
			if diIndex < len(wio.inputStates) {
				state = wio.inputStates[diIndex]
				lastChanged = wio.inputLastChanged[diIndex]
			}
			points = append(points, IoPointState{
				Index:       diIndex,
				Name:        fmt.Sprintf("M%d:DI%d[%d]", moduleNum, i+1, diIndex),
				Type:        IoTypeDigitalInput,
				State:       state,
				Healthy:     healthy,
				LastChanged: lastChanged,
			})
			diIndex++
		}

		for i := 0; i < mod.do; i++ {
			state := false
			var lastChanged time.Time
			if doIndex < len(wio.outputStates) {
				state = wio.outputStates[doIndex]
				lastChanged = wio.outputLastChanged[doIndex]
			}
			points = append(points, IoPointState{
				Index:       doIndex,
				Name:        fmt.Sprintf("M%d:DO%d[%d]", moduleNum, i+1, doIndex),
				Type:        IoTypeDigitalOutput,
				State:       state,
				Healthy:     healthy,
				LastChanged: lastChanged,
			})
			doIndex++
		}

		for i := 0; i < mod.ao; i++ {
			value := 0
			var lastChanged time.Time
			if aoIndex < len(wio.analogOutputStates) {
				value = int(wio.analogOutputStates[aoIndex])
				lastChanged = wio.analogOutputLastChanged[aoIndex]
			}
			points = append(points, IoPointState{
				Index:       aoIndex,
				Name:        fmt.Sprintf("M%d:AO%d[%d]", moduleNum, i+1, aoIndex),
				Type:        IoTypeAnalogOutput,
				Healthy:     healthy,
				LastChanged: lastChanged,
				Value:       value,
				Min:         0,
				Max:         wagoAnalogOutputMax,
			})
			aoIndex++
		}
	}

	return IoDebugSnapshot{Points: points}
}

// ToggleOutput toggles a digital output by its global index
func (wio *WagoIO) ToggleOutput(index int) error {
	wio.mu.Lock()
	defer wio.mu.Unlock()

	if !wio.isReady {
		return errors.New("wago driver: not ready")
	}

	if index < 0 || index >= wio.totalDO {
		return fmt.Errorf("wago driver: output index %d out of range (0-%d)", index, wio.totalDO-1)
	}

	newState := !wio.outputStates[index]
	processIndex := wio.mapOutputPhysicalToProcess(index)
	err := wio.client.WriteCoil(uint16(processIndex), newState)
	if err != nil {
		return fmt.Errorf("wago driver: failed to toggle output %d: %w", index, err)
	}

	return nil
}

// SetAnalogOutput sets an analog output to a raw value by its global index.
// Satisfies drivers.IoAnalogOutputSetter.
func (wio *WagoIO) SetAnalogOutput(index int, value int) error {
	wio.mu.Lock()
	defer wio.mu.Unlock()

	if !wio.isReady {
		return errors.New("wago driver: not ready")
	}

	if index < 0 || index >= wio.totalAO {
		return fmt.Errorf("wago driver: analog output index %d out of range (0-%d)", index, wio.totalAO-1)
	}

	if value < 0 {
		value = 0
	}
	if value > wagoAnalogOutputMax {
		value = wagoAnalogOutputMax
	}

	processIndex := wio.mapAoPhysicalToProcess(index)
	if err := wio.client.WriteRegister(uint16(processIndex), uint16(value)); err != nil {
		return fmt.Errorf("wago driver: failed to set analog output %d: %w", index, err)
	}

	return nil
}

// WagoDriverDetails holds structured detail info for the WAGO driver.
type WagoDriverDetails struct {
	Address string           `json:"address"`
	Port    uint             `json:"port"`
	Modules []WagoModuleInfo `json:"modules"`
}

// WagoModuleInfo holds summary info for a single installed WAGO module.
type WagoModuleInfo struct {
	Index       int    `json:"index"`
	PartNumber  string `json:"part_number"`
	Description string `json:"description"`
	DI          int    `json:"di"`
	DO          int    `json:"do"`
}

// DriverDetails returns structured details about installed WAGO modules.
func (wio *WagoIO) DriverDetails() interface{} {
	wio.mu.RLock()
	defer wio.mu.RUnlock()
	details := WagoDriverDetails{Address: wio.Address, Port: wio.Port}
	for i, spec := range wio.modules {
		partNumber := ""
		if i < len(wio.ModulesInstalled) {
			partNumber = wio.ModulesInstalled[i]
		}
		details.Modules = append(details.Modules, WagoModuleInfo{
			Index:       i + 1,
			PartNumber:  partNumber,
			Description: spec.description,
			DI:          spec.di,
			DO:          spec.do,
		})
	}
	return details
}

// Status returns a summary of the driver's current state
func (wio *WagoIO) Status() string {
	modules := strings.Join(wio.ModulesInstalled, ",")
	if len(modules) > 30 {
		modules = modules[:27] + "..."
	}
	return fmt.Sprintf("%s:%d DI:%d DO:%d [%s]", wio.Address, wio.Port, wio.totalDI, wio.totalDO, modules)
}

type wagoChannelKind int

const (
	wagoChannelDI wagoChannelKind = iota
	wagoChannelDO
	wagoChannelAO
)

// moduleChannels returns the channel count and process-image order for a module
// for the given channel kind.
func (m wagoModuleSpec) channels(kind wagoChannelKind) (count int, order []int) {
	switch kind {
	case wagoChannelDI:
		return m.di, m.diOrder
	case wagoChannelDO:
		return m.do, m.doOrder
	case wagoChannelAO:
		return m.ao, m.aoOrder
	}
	return 0, nil
}

func (wio *WagoIO) buildPhysicalToProcessMap(kind wagoChannelKind) []int {
	var total int
	switch kind {
	case wagoChannelDI:
		total = wio.totalDI
	case wagoChannelDO:
		total = wio.totalDO
	case wagoChannelAO:
		total = wio.totalAO
	}
	if total == 0 {
		return nil
	}

	mapping := make([]int, total)
	physBase := 0
	procBase := 0
	for _, module := range wio.modules {
		channelCount, order := module.channels(kind)
		if channelCount == 0 {
			continue
		}

		for i := 0; i < channelCount; i++ {
			processLocal := i + 1
			if len(order) == channelCount {
				processLocal = order[i]
			}
			mapping[physBase+i] = procBase + processLocal - 1
		}

		physBase += channelCount
		procBase += channelCount
	}
	return mapping
}

func (wio *WagoIO) remapInputsToPhysical(processInputs []bool) []bool {
	if processInputs == nil {
		return nil
	}
	if len(wio.diPhysicalToProcess) != len(processInputs) {
		cp := make([]bool, len(processInputs))
		copy(cp, processInputs)
		return cp
	}
	physical := make([]bool, len(processInputs))
	for physIx, processIx := range wio.diPhysicalToProcess {
		if processIx >= 0 && processIx < len(processInputs) {
			physical[physIx] = processInputs[processIx]
		}
	}
	return physical
}

func (wio *WagoIO) remapOutputsToPhysical(processOutputs []bool) []bool {
	if processOutputs == nil {
		return nil
	}
	if len(wio.doPhysicalToProcess) != len(processOutputs) {
		cp := make([]bool, len(processOutputs))
		copy(cp, processOutputs)
		return cp
	}
	physical := make([]bool, len(processOutputs))
	for physIx, processIx := range wio.doPhysicalToProcess {
		if processIx >= 0 && processIx < len(processOutputs) {
			physical[physIx] = processOutputs[processIx]
		}
	}
	return physical
}

func (wio *WagoIO) remapAnalogOutputsToPhysical(processOutputs []uint16) []uint16 {
	if processOutputs == nil {
		return nil
	}
	if len(wio.aoPhysicalToProcess) != len(processOutputs) {
		cp := make([]uint16, len(processOutputs))
		copy(cp, processOutputs)
		return cp
	}
	physical := make([]uint16, len(processOutputs))
	for physIx, processIx := range wio.aoPhysicalToProcess {
		if processIx >= 0 && processIx < len(processOutputs) {
			physical[physIx] = processOutputs[processIx]
		}
	}
	return physical
}

func (wio *WagoIO) mapOutputPhysicalToProcess(physicalIndex int) int {
	if physicalIndex < 0 || physicalIndex >= len(wio.doPhysicalToProcess) {
		return physicalIndex
	}
	return wio.doPhysicalToProcess[physicalIndex]
}
