package swkit

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/hubertat/swkit/app"
	"github.com/hubertat/swkit/drivers"
)

// SwKitConfigProvider implements app.ConfigProvider for SwKit
type SwKitConfigProvider struct {
	mu         sync.RWMutex
	sw         *SwKit
	configPath string
	reloadCh   chan struct{}
}

// NewConfigProvider creates a new config provider for the given SwKit instance
func NewConfigProvider(sw *SwKit, configPath string) *SwKitConfigProvider {
	return &SwKitConfigProvider{
		sw:         sw,
		configPath: configPath,
		reloadCh:   make(chan struct{}, 1),
	}
}

// ReloadCh returns a channel that receives a signal when a config reload is requested.
func (p *SwKitConfigProvider) ReloadCh() <-chan struct{} { return p.reloadCh }

// TriggerReload sends a non-blocking reload signal. Safe to call from any goroutine.
func (p *SwKitConfigProvider) TriggerReload() {
	select {
	case p.reloadCh <- struct{}{}:
	default:
	}
}

// Swap points the provider at a freshly-reloaded SwKit instance. Callers
// (main.go's performReload) must call this right after the state provider is
// reloaded, otherwise GetEditableConfig/SaveConfig keep reading/writing
// through the old (torn-down) SwKit, and a web-triggered save after a reload
// would silently revert any out-of-band config changes.
func (p *SwKitConfigProvider) Swap(sk *SwKit) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sw = sk
}

// GetEditableConfig returns the current editable config snapshot
func (p *SwKitConfigProvider) GetEditableConfig() app.EditableConfig {
	p.mu.RLock()
	defer p.mu.RUnlock()
	config := app.EditableConfig{}

	for _, l := range p.sw.Lights {
		config.Lights = append(config.Lights, app.LightEditConfig{
			Name:           l.Name,
			DigitalOutName: l.DigitalOutName,
			DisableHomekit: l.DisableHomekit,
		})
	}

	for _, dl := range p.sw.DimmableLights {
		config.DimmableLights = append(config.DimmableLights, app.DimmableLightEditConfig{
			Name:            dl.Name,
			DigitalOutName:  dl.DigitalOutName,
			AnalogOutName:   dl.AnalogOutName,
			DefaultSetpoint: dl.DefaultSetpoint,
			DisableHomekit:  dl.DisableHomekit,
		})
	}

	for _, o := range p.sw.Outlets {
		config.Outlets = append(config.Outlets, app.OutletEditConfig{
			Name:           o.Name,
			DigitalOutName: o.DigitalOutName,
			DisableHomekit: o.DisableHomekit,
		})
	}

	for _, b := range p.sw.Buttons {
		bc := app.ButtonEditConfig{
			Name:           b.Name,
			EventInputName: b.EventInputName,
			DisableHomekit: b.DisableHomekit,
		}
		for _, cd := range b.ControlDevices {
			bc.ControlDevices = append(bc.ControlDevices, parseControlDeviceToEdit(cd))
		}
		config.Buttons = append(config.Buttons, bc)
	}

	for _, s := range p.sw.Scenes {
		sc := app.SceneEditConfig{Name: s.Name}
		for _, st := range s.States {
			sc.States = append(sc.States, app.SceneStateEditConfig{
				Name:    st.Name,
				Actions: append([]string(nil), st.Actions...),
			})
		}
		config.Scenes = append(config.Scenes, sc)
	}

	// Collect output device names from all controllable device configs
	for _, l := range p.sw.Lights {
		config.OutputDeviceNames = append(config.OutputDeviceNames, l.Name)
	}
	for _, cl := range p.sw.ColorLights {
		config.OutputDeviceNames = append(config.OutputDeviceNames, cl.Name)
	}
	for _, dl := range p.sw.DimmableLights {
		config.OutputDeviceNames = append(config.OutputDeviceNames, dl.Name)
	}
	for _, o := range p.sw.Outlets {
		config.OutputDeviceNames = append(config.OutputDeviceNames, o.Name)
	}
	// Scenes are Controllable too, so they are valid control targets.
	for _, s := range p.sw.Scenes {
		config.OutputDeviceNames = append(config.OutputDeviceNames, s.Name)
	}

	return config
}

// SaveConfig persists the edited config, backing up the old file
func (p *SwKitConfigProvider) SaveConfig(config app.EditableConfig) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Backup existing config file
	if err := p.backupConfig(); err != nil {
		return fmt.Errorf("backup failed: %w", err)
	}

	// Update SwKit config arrays from edited config
	p.sw.Lights = make([]LightConfig, len(config.Lights))
	for i, l := range config.Lights {
		p.sw.Lights[i] = LightConfig{
			Name:           l.Name,
			DigitalOutName: l.DigitalOutName,
			DisableHomekit: l.DisableHomekit,
		}
	}

	p.sw.DimmableLights = make([]DimmableLightConfig, len(config.DimmableLights))
	for i, dl := range config.DimmableLights {
		p.sw.DimmableLights[i] = DimmableLightConfig{
			Name:            dl.Name,
			DigitalOutName:  dl.DigitalOutName,
			AnalogOutName:   dl.AnalogOutName,
			DefaultSetpoint: dl.DefaultSetpoint,
			DisableHomekit:  dl.DisableHomekit,
		}
	}

	p.sw.Outlets = make([]OutletConfig, len(config.Outlets))
	for i, o := range config.Outlets {
		p.sw.Outlets[i] = OutletConfig{
			Name:           o.Name,
			DigitalOutName: o.DigitalOutName,
			DisableHomekit: o.DisableHomekit,
		}
	}

	p.sw.Buttons = make([]ButtonConfig, len(config.Buttons))
	for i, b := range config.Buttons {
		bc := ButtonConfig{
			Name:           b.Name,
			EventInputName: normalizeButtonEventInputIoType(b.EventInputName),
			DisableHomekit: b.DisableHomekit,
		}
		for _, cd := range b.ControlDevices {
			bc.ControlDevices = append(bc.ControlDevices, cd.FormatControlDeviceString())
		}
		p.sw.Buttons[i] = bc
	}

	p.sw.Scenes = make([]SceneConfig, len(config.Scenes))
	for i, s := range config.Scenes {
		sc := SceneConfig{Name: s.Name}
		for _, st := range s.States {
			sc.States = append(sc.States, SceneStateConfig{
				Name:    st.Name,
				Actions: append([]string(nil), st.Actions...),
			})
		}
		p.sw.Scenes[i] = sc
	}

	// Marshal and write (drop nil values so they are not persisted as explicit null)
	data, err := marshalWithoutNilIndent(p.sw)
	if err != nil {
		return fmt.Errorf("marshal failed: %w", err)
	}

	if err := os.WriteFile(p.configPath, data, 0644); err != nil {
		return fmt.Errorf("write failed: %w", err)
	}

	p.TriggerReload()
	return nil
}

// normalizeButtonEventInputIoType migrates button input type d_in -> push_event.
// Invalid or already-correct values are returned unchanged.
func normalizeButtonEventInputIoType(ioId string) string {
	driverName, ioType, ioName, err := drivers.ResolveIoIdString(ioId)
	if err != nil {
		return ioId
	}
	if ioType != drivers.IoTypeDigitalInput {
		return ioId
	}
	return drivers.GetIoIdString(driverName, drivers.IoTypePushEventEmitter, ioName)
}

func (p *SwKitConfigProvider) backupConfig() error {
	src, err := os.Open(p.configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // no file to backup
		}
		return err
	}
	defer src.Close()

	backupPath := p.configPath + "." + time.Now().Format("2006-01-02_15-04-05")
	dst, err := os.Create(backupPath)
	if err != nil {
		return err
	}
	defer dst.Close()

	_, err = io.Copy(dst, src)
	return err
}

// parseControlDeviceToEdit parses a control device config string into
// ControlDeviceEdit. The grammar is "<event>:" followed by the Action grammar
// (see app.ParseAction).
func parseControlDeviceToEdit(s string) app.ControlDeviceEdit {
	parts := strings.Split(s, ":")
	cd := app.ControlDeviceEdit{}
	if len(parts) < 2 {
		return cd
	}
	cd.EventType = strings.ToLower(parts[0])

	remainder := parts[1:]
	if len(remainder) == 1 {
		// <event>:<device> -> default to toggle.
		cd.Action = "toggle"
		cd.DeviceName = remainder[0]
		return cd
	}

	act, err := app.ParseAction(strings.Join(remainder, ":"))
	if err != nil {
		// Best-effort fallback for a malformed string.
		cd.Action = remainder[0]
		cd.DeviceName = remainder[len(remainder)-1]
		return cd
	}
	cd.Action = act.Verb
	cd.Level = act.Level
	cd.DeviceName = act.Device
	return cd
}

// marshalWithoutNilIndent marshals v to pretty JSON after removing nil values.
func marshalWithoutNilIndent(v interface{}) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}

	var decoded interface{}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, err
	}

	cleaned, _ := pruneNilJSONValue(decoded)
	return json.MarshalIndent(cleaned, "", "  ")
}

// pruneNilJSONValue removes nil values from maps/slices recursively.
// It returns the cleaned value and a keep flag (false only when value is nil).
func pruneNilJSONValue(v interface{}) (interface{}, bool) {
	switch t := v.(type) {
	case nil:
		return nil, false
	case map[string]interface{}:
		out := make(map[string]interface{}, len(t))
		for k, child := range t {
			cleanedChild, keep := pruneNilJSONValue(child)
			if keep {
				out[k] = cleanedChild
			}
		}
		return out, true
	case []interface{}:
		out := make([]interface{}, 0, len(t))
		for _, child := range t {
			cleanedChild, keep := pruneNilJSONValue(child)
			if keep {
				out = append(out, cleanedChild)
			}
		}
		return out, true
	default:
		return v, true
	}
}
