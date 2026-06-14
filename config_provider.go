package swkit

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/hubertat/swkit/app"
	"github.com/hubertat/swkit/drivers"
)

// SwKitConfigProvider implements app.ConfigProvider for SwKit
type SwKitConfigProvider struct {
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

// GetEditableConfig returns the current editable config snapshot
func (p *SwKitConfigProvider) GetEditableConfig() app.EditableConfig {
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
			Name:           dl.Name,
			DigitalOutName: dl.DigitalOutName,
			AnalogOutName:  dl.AnalogOutName,
			DisableHomekit: dl.DisableHomekit,
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

	return config
}

// SaveConfig persists the edited config, backing up the old file
func (p *SwKitConfigProvider) SaveConfig(config app.EditableConfig) error {
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
			Name:           dl.Name,
			DigitalOutName: dl.DigitalOutName,
			AnalogOutName:  dl.AnalogOutName,
			DisableHomekit: dl.DisableHomekit,
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

// parseControlDeviceToEdit parses a control device config string into ControlDeviceEdit
func parseControlDeviceToEdit(s string) app.ControlDeviceEdit {
	parts := strings.Split(s, ":")
	cd := app.ControlDeviceEdit{}

	if len(parts) >= 2 {
		cd.EventType = strings.ToLower(parts[0])
	}

	if len(parts) == 3 {
		cd.Action = parts[1]
		cd.DeviceName = parts[2]
	} else if len(parts) == 2 {
		cd.Action = "toggle"
		cd.DeviceName = parts[1]
	}

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
