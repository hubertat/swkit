package swkit

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/hubertat/swkit/app"
	"github.com/hubertat/swkit/drivers"
)

// maxIoNamesFileSize caps the size of a file LoadIoNames will read. Import is
// reachable over SSH via the TUI's IO-name import prompt, so the read must
// reject huge or endlessly-growing input rather than exhaust memory; 1 MB is
// generously large for a names file (JSON records of driver/type/index/name).
const maxIoNamesFileSize = 1 << 20 // 1 MB

// SwKitProvider implements app.StateProvider for SwKit
type SwKitProvider struct {
	mu      sync.RWMutex
	sw      *SwKit
	namesMu sync.RWMutex
	names   map[string]string
}

// NewStateProvider creates a new state provider for the given SwKit instance
func NewStateProvider(sw *SwKit) *SwKitProvider {
	return &SwKitProvider{sw: sw, names: make(map[string]string)}
}

// Reload atomically swaps the underlying SwKit instance.
// Called during config hot-reload after the new SwKit is set up.
func (p *SwKitProvider) Reload(newSk *SwKit) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sw = newSk
}

// SetIoName sets or deletes (when name is empty) a custom name for the given key.
func (p *SwKitProvider) SetIoName(key, name string) {
	p.namesMu.Lock()
	defer p.namesMu.Unlock()
	if name == "" {
		delete(p.names, key)
	} else {
		p.names[key] = name
	}
}

// GetIoName returns the custom name for a single IO key.
func (p *SwKitProvider) GetIoName(key string) string {
	p.namesMu.RLock()
	defer p.namesMu.RUnlock()
	return p.names[key]
}

// GetIoNames returns a copy of all custom names.
func (p *SwKitProvider) GetIoNames() map[string]string {
	p.namesMu.RLock()
	defer p.namesMu.RUnlock()
	cp := make(map[string]string, len(p.names))
	for k, v := range p.names {
		cp[k] = v
	}
	return cp
}

// LoadIoNames reads a JSON file of named IO points and populates the names map.
// Only regular files up to maxIoNamesFileSize are accepted: this rejects
// FIFOs and other special files that could block the read forever, and bounds
// memory use even if the file grows between the stat and the read.
func (p *SwKitProvider) LoadIoNames(path string) error {
	// Stat the path (not an opened handle) before opening it: opening a FIFO
	// for read blocks until a writer connects, so the regular-file check must
	// happen first or this call inherits exactly the hang it's meant to
	// reject. There is a narrow TOCTOU window if path is replaced with a
	// special file between this stat and the Open below; that is accepted
	// (no sandboxing/allowlist is in scope here), same as any other stat-then-open.
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat io names file %q: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("io names file %q is not a regular file (mode %s)", path, info.Mode())
	}
	if info.Size() > maxIoNamesFileSize {
		return fmt.Errorf("io names file %q is %d bytes, exceeds %d byte limit", path, info.Size(), maxIoNamesFileSize)
	}

	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open io names file %q: %w", path, err)
	}
	defer f.Close()

	data, err := io.ReadAll(io.LimitReader(f, maxIoNamesFileSize+1))
	if err != nil {
		return fmt.Errorf("read io names file %q: %w", path, err)
	}
	if len(data) > maxIoNamesFileSize {
		return fmt.Errorf("io names file %q exceeds %d byte limit", path, maxIoNamesFileSize)
	}

	type entry struct {
		Driver string `json:"driver"`
		Type   string `json:"type"`
		Index  int    `json:"index"`
		Name   string `json:"name"`
	}
	var entries []entry
	if err := json.Unmarshal(data, &entries); err != nil {
		return fmt.Errorf("parse io names file %q: %w", path, err)
	}
	p.namesMu.Lock()
	defer p.namesMu.Unlock()
	for _, e := range entries {
		if e.Name != "" {
			key := app.IoDebugKey(e.Driver, e.Type, e.Index)
			p.names[key] = e.Name
		}
	}
	return nil
}

// SaveIoNames writes all named IO points to a JSON file.
// It reads current state to populate the hw_name field.
func (p *SwKitProvider) SaveIoNames(path string) error {
	state := p.GetState()

	// Build a map from key → hw_name
	hwNames := make(map[string]string, len(state.IoDebug))
	for _, pt := range state.IoDebug {
		key := app.IoDebugKey(pt.DriverName, pt.Type, pt.Index)
		hwNames[key] = pt.Name
	}

	type entry struct {
		Driver string `json:"driver"`
		Type   string `json:"type"`
		Index  int    `json:"index"`
		HwName string `json:"hw_name"`
		Name   string `json:"name"`
	}

	names := p.GetIoNames()
	entries := make([]entry, 0, len(names))
	for key, name := range names {
		// Parse key back: "driver|type|index"
		// Find points from state that match this key
		for _, pt := range state.IoDebug {
			if app.IoDebugKey(pt.DriverName, pt.Type, pt.Index) == key {
				entries = append(entries, entry{
					Driver: pt.DriverName,
					Type:   pt.Type,
					Index:  pt.Index,
					HwName: hwNames[key],
					Name:   name,
				})
				break
			}
		}
	}

	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// GetState returns the current application state snapshot.
//
// It only holds p.mu for long enough to read the current *SwKit pointer; the
// rest of the snapshot is built against that local copy without the lock
// held. Reload swaps the pointer under p.mu.Lock(), so a GetState already in
// flight simply finishes building its snapshot against the SwKit instance it
// started with. That is correct snapshot semantics (the caller gets a
// consistent view of "some" recent state) and, critically, it means a driver
// call blocking inside GetState no longer holds p.mu and cannot stall Reload
// or a concurrent GetState.
func (p *SwKitProvider) GetState() app.AppState {
	p.mu.RLock()
	sw := p.sw
	p.mu.RUnlock()

	state := app.AppState{
		Name:      sw.Name,
		Timestamp: time.Now(),
	}

	// Collect driver states in stable alphabetical order
	driverNames := make([]string, 0, len(sw.ioDrivers))
	for name := range sw.ioDrivers {
		driverNames = append(driverNames, name)
	}
	sort.Strings(driverNames)
	for _, name := range driverNames {
		driver := sw.ioDrivers[name]
		ds := app.DriverState{
			Name:  name,
			Ready: driver.IsReady(),
		}

		// Get status info if available
		if statusProvider, ok := driver.(DriverStatusProvider); ok {
			ds.StatusInfo = statusProvider.Status()
		}

		// Get structured details if available
		if detailProvider, ok := driver.(DriverDetailProvider); ok {
			if d := detailProvider.DriverDetails(); d != nil {
				if b, err := json.Marshal(d); err == nil {
					ds.Details = json.RawMessage(b)
				}
			}
		}

		state.Drivers = append(state.Drivers, ds)
	}

	// Collect device states
	for _, light := range sw.lights {
		ds := p.buildLightState(light)
		state.Devices = append(state.Devices, ds)
	}

	for _, colorLight := range sw.colorLights {
		ds := p.buildColorLightState(colorLight)
		state.Devices = append(state.Devices, ds)
	}

	for _, dimmableLight := range sw.dimmableLights {
		ds := p.buildDimmableLightState(dimmableLight)
		state.Devices = append(state.Devices, ds)
	}

	for _, outlet := range sw.outlets {
		ds := p.buildOutletState(outlet)
		state.Devices = append(state.Devices, ds)
	}

	for _, button := range sw.buttons {
		ds := p.buildButtonState(button)
		state.Devices = append(state.Devices, ds)
	}

	for _, scene := range sw.scenes {
		ds := p.buildSceneState(scene)
		state.Devices = append(state.Devices, ds)
	}

	// Collect IO debug data from drivers that support it (typed fields for stable order)
	collectIoDebug := func(driverName string, provider drivers.IoDebugProvider) {
		snapshot := provider.GetIoDebugSnapshot()
		for _, pt := range snapshot.Points {
			ioType := "input"
			switch pt.Type {
			case drivers.IoTypeDigitalOutput:
				ioType = "output"
			case drivers.IoTypeAnalogOutput:
				ioType = "analog_output"
			}
			state.IoDebug = append(state.IoDebug, app.IoPointDebugState{
				DriverName:  driverName,
				Index:       pt.Index,
				Name:        pt.Name,
				Type:        ioType,
				State:       pt.State,
				Healthy:     pt.Healthy,
				LastChanged: pt.LastChanged,
				LastEvent:   pt.LastEvent,
				Value:       pt.Value,
				Min:         pt.Min,
				Max:         pt.Max,
			})
		}
	}
	if sw.Wago != nil {
		collectIoDebug(sw.Wago.String(), sw.Wago)
	}
	if sw.Shelly != nil {
		collectIoDebug(sw.Shelly.String(), sw.Shelly)
	}

	// Build IO id → device name map for annotating IO debug points
	ioDeviceMap := make(map[string]string)
	for _, ds := range state.Devices {
		if ds.OutputIoId != "" {
			ioDeviceMap[ds.OutputIoId] = ds.Name
		}
		if ds.RgbwIoId != "" {
			ioDeviceMap[ds.RgbwIoId] = ds.Name
		}
		if ds.AnalogIoId != "" {
			ioDeviceMap[ds.AnalogIoId] = ds.Name
		}
		if ds.EventInputId != "" {
			ioDeviceMap[ds.EventInputId] = ds.Name
		}
	}
	// Annotate each IO point with the device that uses it (if any)
	for i, pt := range state.IoDebug {
		ioTypeStr := "d_in"
		switch pt.Type {
		case "output":
			ioTypeStr = "d_out"
		case "analog_output":
			ioTypeStr = "a_out"
		}
		ioId := fmt.Sprintf("%s|%s|%d", pt.DriverName, ioTypeStr, pt.Index)
		if deviceName, ok := ioDeviceMap[ioId]; ok {
			state.IoDebug[i].ConfiguredAs = deviceName
		}
	}

	// Apply custom names
	ioNames := p.GetIoNames()
	for i, pt := range state.IoDebug {
		key := app.IoDebugKey(pt.DriverName, pt.Type, pt.Index)
		state.IoDebug[i].CustomName = ioNames[key]
	}

	// Collect HomeKit state
	state.HomeKit = app.HomeKitState{
		Enabled:     len(sw.HkPin) == 8,
		Pin:         sw.HkPin,
		Address:     sw.HkAddress,
		DeviceCount: len(sw.getHkThings()),
	}

	return state
}

// Subscribe returns a channel that receives state updates at the specified interval.
// The channel is buffered (size 1): periodic sends are non-blocking so a slow consumer
// never stalls the ticker goroutine.
// runGetState runs GetState on its own goroutine and reports the result on a
// dedicated, single-use buffered channel (capacity 1). A dedicated channel per
// call is required, not just convenient: if GetState is blocked inside a
// driver call when the caller gives up on it (context cancelled, or a fresh
// call started because the previous one is still stuck), the abandoned
// goroutine must still be able to send its eventual result without anyone
// listening or panicking. Sharing the subscriber's own channel would risk a
// send on it after Subscribe's goroutine has returned and closed it.
func (p *SwKitProvider) runGetState() <-chan app.AppState {
	result := make(chan app.AppState, 1)
	go func() {
		result <- p.GetState()
	}()
	return result
}

// Subscribe returns a channel that receives state updates at the specified interval.
// The channel is buffered (size 1): periodic sends are non-blocking so a slow consumer
// never stalls the ticker goroutine.
//
// GetState reads driver state and can block indefinitely if a driver's
// call hangs (e.g. a wedged Shelly/MQTT round trip). Every GetState call is
// therefore run on its own goroutine, and the subscriber selects on its
// result versus ctx.Done() so that cancellation always wins immediately,
// even mid-poll. Only one GetState call is allowed in flight at a time: while
// one is still running, ticks are skipped rather than spawning another
// goroutine, so a stuck driver cannot accumulate goroutines at the ticker's
// rate. A goroutine stuck inside a blocked driver call is leaked until that
// call returns (Go cannot interrupt it), but it can only ever write to its
// own dedicated result channel, never to ch, so it cannot panic on a closed
// channel or corrupt a later subscription.
func (p *SwKitProvider) Subscribe(ctx context.Context, interval time.Duration) <-chan app.AppState {
	ch := make(chan app.AppState, 1)

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		defer close(ch)

		// Initial fetch: run off-goroutine so cancellation can win the race
		// even if this very first GetState blocks in a driver call.
		pending := p.runGetState()

		for {
			select {
			case state := <-pending:
				select {
				case ch <- state:
				default:
				}
				pending = nil
			case <-ticker.C:
				if pending == nil {
					pending = p.runGetState()
				}
				// else: previous GetState still running, skip this tick.
			case <-ctx.Done():
				return
			}
		}
	}()

	return ch
}

func (p *SwKitProvider) buildLightState(light *Light) app.DeviceState {
	isOn := false
	isHealthy := true
	outputIoId := ""

	if light.output != nil {
		if state, err := light.output.GetState(); err == nil {
			isOn = state
		}
		isHealthy = isOutputHealthy(light.output)
		outputIoId = light.output.String()
	}

	return app.DeviceState{
		Name:           light.name,
		Type:           app.DeviceTypeLight,
		IsOn:           isOn,
		IsHealthy:      isHealthy,
		IsFaulty:       light.isFaulty,
		HomeKitEnabled: !light.disableHomekit,
		OutputIoId:     outputIoId,
	}
}

func (p *SwKitProvider) buildColorLightState(cl *ColorLight) app.DeviceState {
	isOn := false
	isHealthy := true
	outputIoId := ""
	rgbwIoId := ""

	if cl.onDigitalOut != nil {
		if state, err := cl.onDigitalOut.GetState(); err == nil {
			isOn = state
		}
		isHealthy = isOutputHealthy(cl.onDigitalOut)
		outputIoId = cl.onDigitalOut.String()
	}

	if cl.rgbwOut != nil {
		rgbwIoId = cl.rgbwOut.String()
	}

	return app.DeviceState{
		Name:           cl.name,
		Type:           app.DeviceTypeColorLight,
		IsOn:           isOn,
		IsHealthy:      isHealthy,
		IsFaulty:       cl.isFaulty,
		HomeKitEnabled: !cl.disableHomekit,
		OutputIoId:     outputIoId,
		RgbwIoId:       rgbwIoId,
	}
}

func (p *SwKitProvider) buildDimmableLightState(dl *DimmableLight) app.DeviceState {
	isOn := false
	isHealthy := true
	outputIoId := ""
	analogIoId := ""
	brightness := 0

	if dl.onOut != nil {
		if state, err := dl.onOut.GetState(); err == nil {
			isOn = state
		}
		isHealthy = isOutputHealthy(dl.onOut)
		outputIoId = dl.onOut.String()
	}

	if dl.briOut != nil {
		analogIoId = dl.briOut.String()
		if raw, err := dl.briOut.GetState(); err == nil {
			min, max := dl.briOut.GetMinMax()
			brightness = convertIntRange(raw, min, max, 0, 100)
		}
	}

	return app.DeviceState{
		Name:           dl.name,
		Type:           app.DeviceTypeDimmableLight,
		IsOn:           isOn,
		IsHealthy:      isHealthy,
		IsFaulty:       dl.isFaulty,
		HomeKitEnabled: !dl.disableHomekit,
		OutputIoId:     outputIoId,
		AnalogIoId:     analogIoId,
		Brightness:     brightness,
	}
}

func (p *SwKitProvider) buildOutletState(outlet *Outlet) app.DeviceState {
	isOn := false
	isHealthy := true
	outputIoId := ""

	if outlet.output != nil {
		if state, err := outlet.output.GetState(); err == nil {
			isOn = state
		}
		isHealthy = isOutputHealthy(outlet.output)
		outputIoId = outlet.output.String()
	}

	return app.DeviceState{
		Name:           outlet.name,
		Type:           app.DeviceTypeOutlet,
		IsOn:           isOn,
		IsHealthy:      isHealthy,
		IsFaulty:       outlet.isFaulty,
		HomeKitEnabled: !outlet.disableHomekit,
		OutputIoId:     outputIoId,
	}
}

func (p *SwKitProvider) buildButtonState(button *Button) app.DeviceState {
	isHealthy := true
	eventInputId := ""
	if button.emitter != nil {
		isHealthy = button.emitter.IsHealthy()
		eventInputId = button.emitter.String()
	}

	var relations []app.ButtonControlRelation
	for _, ctrl := range button.controlThis {
		relations = append(relations, app.ButtonControlRelation{
			EventType:  ctrl.e.String(),
			Action:     ctrl.verb,
			Level:      ctrl.level,
			DeviceName: ctrl.dev.Name(),
		})
	}

	lastEventType, lastEventTime := button.LastEvent()
	lastEventTypeStr := ""
	if !lastEventTime.IsZero() {
		lastEventTypeStr = lastEventType.String()
	}

	return app.DeviceState{
		Name:             button.name,
		Type:             app.DeviceTypeButton,
		IsOn:             false, // Buttons are stateless
		IsHealthy:        isHealthy,
		IsFaulty:         false, // Buttons don't track faulty state
		HomeKitEnabled:   !button.disableHomekit,
		EventInputId:     eventInputId,
		ControlRelations: relations,
		LastEventType:    lastEventTypeStr,
		LastEventTime:    lastEventTime,
	}
}

func (p *SwKitProvider) buildSceneState(scene *Scene) app.DeviceState {
	idx := scene.CurrentState()
	return app.DeviceState{
		Name:            scene.name,
		Type:            app.DeviceTypeScene,
		IsOn:            idx != 0, // state 0 is "off" by convention
		IsHealthy:       true,
		HomeKitEnabled:  false, // scenes have no HomeKit representation yet
		SceneStateIndex: idx,
		SceneStateNames: scene.StateNames(),
	}
}

func isOutputHealthy(output drivers.DigitalOutput) bool {
	if output == nil {
		return false
	}
	return output.IsHealthy()
}

// ToggleDevice toggles the device at the given index
func (p *SwKitProvider) ToggleDevice(index int) app.ControlResult {
	p.mu.RLock()
	defer p.mu.RUnlock()
	device, deviceName, _, err := p.getControllableByIndex(index)
	if err != nil {
		return app.ControlResult{Error: err}
	}
	if device == nil {
		return app.ControlResult{
			DeviceName: deviceName,
			Action:     "toggle",
			Error:      fmt.Errorf("buttons cannot be toggled"),
		}
	}

	device.Toggle()

	// Get new state by reading from the device
	newState := p.getDeviceState(device)

	return app.ControlResult{
		DeviceName: device.Name(),
		Action:     "toggle",
		NewState:   newState,
	}
}

// SetDevice sets the device at the given index to the given state
func (p *SwKitProvider) SetDevice(index int, state bool) app.ControlResult {
	p.mu.RLock()
	defer p.mu.RUnlock()
	device, deviceName, _, err := p.getControllableByIndex(index)
	if err != nil {
		return app.ControlResult{Error: err}
	}
	if device == nil {
		return app.ControlResult{
			DeviceName: deviceName,
			Action:     actionName(state),
			Error:      fmt.Errorf("buttons cannot be controlled"),
		}
	}

	device.SetValue(state)

	return app.ControlResult{
		DeviceName: device.Name(),
		Action:     actionName(state),
		NewState:   state,
	}
}

// SetDeviceBrightness sets the brightness (0-100) of the dimmable device at the
// given index. Only dimmable lights support brightness.
func (p *SwKitProvider) SetDeviceBrightness(index int, pct int) app.ControlResult {
	p.mu.RLock()
	defer p.mu.RUnlock()
	device, deviceName, _, err := p.getControllableByIndex(index)
	if err != nil {
		return app.ControlResult{Error: err}
	}
	dimmable, ok := device.(Dimmable)
	if !ok {
		return app.ControlResult{
			DeviceName: deviceName,
			Action:     "brightness",
			Error:      fmt.Errorf("device does not support brightness"),
		}
	}

	dimmable.SetBrightness(pct)

	newState := p.getDeviceState(device)
	return app.ControlResult{
		DeviceName: device.Name(),
		Action:     "brightness",
		NewState:   newState,
	}
}

// AdjustDeviceBrightness changes the brightness of the dimmable device at the
// given index by delta (relative, may be negative), clamped to 0-100. Mirrors
// the brightness_up/brightness_down logic in applyVerb.
func (p *SwKitProvider) AdjustDeviceBrightness(index int, delta int) app.ControlResult {
	p.mu.RLock()
	defer p.mu.RUnlock()
	device, deviceName, _, err := p.getControllableByIndex(index)
	if err != nil {
		return app.ControlResult{Error: err}
	}
	dimmable, ok := device.(Dimmable)
	if !ok {
		return app.ControlResult{
			DeviceName: deviceName,
			Action:     "brightness",
			Error:      fmt.Errorf("device does not support brightness"),
		}
	}

	cur, err := dimmable.GetBrightness()
	if err != nil {
		return app.ControlResult{
			DeviceName: device.Name(),
			Action:     "brightness",
			Error:      err,
		}
	}
	dimmable.SetBrightness(cur + delta) // SetBrightness clamps to 0-100

	newState := p.getDeviceState(device)
	return app.ControlResult{
		DeviceName: device.Name(),
		Action:     "brightness",
		NewState:   newState,
	}
}

// SetDeviceValueFor sets the controllable device at the given index to state for
// the given duration (seconds), then reverts to its prior state.
func (p *SwKitProvider) SetDeviceValueFor(index int, state bool, seconds int) app.ControlResult {
	p.mu.RLock()
	defer p.mu.RUnlock()
	device, deviceName, _, err := p.getControllableByIndex(index)
	if err != nil {
		return app.ControlResult{Error: err}
	}
	if device == nil {
		return app.ControlResult{
			DeviceName: deviceName,
			Action:     actionName(state),
			Error:      fmt.Errorf("buttons cannot be controlled"),
		}
	}
	if seconds <= 0 {
		return app.ControlResult{
			DeviceName: device.Name(),
			Action:     actionName(state),
			Error:      fmt.Errorf("duration must be positive"),
		}
	}

	p.sw.SetDeviceValueFor(device, state, time.Duration(seconds)*time.Second)

	return app.ControlResult{
		DeviceName: device.Name(),
		Action:     fmt.Sprintf("%s for %ds", actionName(state), seconds),
		NewState:   state,
	}
}

// getControllableByIndex returns the Controllable device at the given index
// Device order matches GetState(): lights -> colorLights -> dimmableLights -> outlets -> buttons -> scenes
// Returns nil Controllable for buttons since they don't implement the interface
func (p *SwKitProvider) getControllableByIndex(index int) (Controllable, string, app.DeviceType, error) {
	lightsCount := len(p.sw.lights)
	colorLightsCount := len(p.sw.colorLights)
	dimmableLightsCount := len(p.sw.dimmableLights)
	outletsCount := len(p.sw.outlets)
	buttonsCount := len(p.sw.buttons)
	scenesCount := len(p.sw.scenes)

	if index < 0 || index >= lightsCount+colorLightsCount+dimmableLightsCount+outletsCount+buttonsCount+scenesCount {
		return nil, "", "", fmt.Errorf("device index %d out of range", index)
	}

	// Lights
	if index < lightsCount {
		return p.sw.lights[index], p.sw.lights[index].name, app.DeviceTypeLight, nil
	}
	index -= lightsCount

	// ColorLights
	if index < colorLightsCount {
		return p.sw.colorLights[index], p.sw.colorLights[index].name, app.DeviceTypeColorLight, nil
	}
	index -= colorLightsCount

	// DimmableLights
	if index < dimmableLightsCount {
		return p.sw.dimmableLights[index], p.sw.dimmableLights[index].name, app.DeviceTypeDimmableLight, nil
	}
	index -= dimmableLightsCount

	// Outlets
	if index < outletsCount {
		return p.sw.outlets[index], p.sw.outlets[index].name, app.DeviceTypeOutlet, nil
	}
	index -= outletsCount

	// Buttons - return nil for Controllable since they don't implement the interface
	if index < buttonsCount {
		return nil, p.sw.buttons[index].name, app.DeviceTypeButton, nil
	}
	index -= buttonsCount

	// Scenes
	if index < scenesCount {
		return p.sw.scenes[index], p.sw.scenes[index].name, app.DeviceTypeScene, nil
	}

	return nil, "", "", fmt.Errorf("device index %d out of range", index)
}

// getDeviceState reads the current on/off state from the device
func (p *SwKitProvider) getDeviceState(device Controllable) bool {
	switch d := device.(type) {
	case *Light:
		if d.output != nil {
			if state, err := d.output.GetState(); err == nil {
				return state
			}
		}
	case *ColorLight:
		if d.onDigitalOut != nil {
			if state, err := d.onDigitalOut.GetState(); err == nil {
				return state
			}
		}
	case *DimmableLight:
		if d.onOut != nil {
			if state, err := d.onOut.GetState(); err == nil {
				return state
			}
		}
	case *Outlet:
		if d.output != nil {
			if state, err := d.output.GetState(); err == nil {
				return state
			}
		}
	case *Scene:
		return d.CurrentState() != 0
	}
	return false
}

func actionName(state bool) string {
	if state {
		return "on"
	}
	return "off"
}

// ToggleIoOutput toggles a raw IO output by driver name and output index
func (p *SwKitProvider) ToggleIoOutput(driverName string, outputIndex int) error {
	p.mu.RLock()
	defer p.mu.RUnlock()
	driver, ok := p.sw.ioDrivers[driverName]
	if !ok {
		return fmt.Errorf("driver %q not found", driverName)
	}
	toggler, ok := driver.(drivers.IoOutputToggler)
	if !ok {
		return fmt.Errorf("driver %q does not support output toggling", driverName)
	}
	return toggler.ToggleOutput(outputIndex)
}

// SetIoAnalogOutput sets a raw analog IO output by driver name and output index.
func (p *SwKitProvider) SetIoAnalogOutput(driverName string, outputIndex int, value int) error {
	p.mu.RLock()
	defer p.mu.RUnlock()
	driver, ok := p.sw.ioDrivers[driverName]
	if !ok {
		return fmt.Errorf("driver %q not found", driverName)
	}
	setter, ok := driver.(drivers.IoAnalogOutputSetter)
	if !ok {
		return fmt.Errorf("driver %q does not support analog output setting", driverName)
	}
	return setter.SetAnalogOutput(outputIndex, value)
}
