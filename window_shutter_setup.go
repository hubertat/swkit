package swkit

import (
	"errors"
	"fmt"
	"strings"

	"github.com/hubertat/swkit/app"
	"github.com/hubertat/swkit/drivers"
)

// resolveDigitalOutput resolves a d_out io id to the driver's output.
func (sw *SwKit) resolveDigitalOutput(ioId string) (drivers.DigitalOutput, error) {
	ioName, driver, err := sw.getDriverAndNameForIo(ioId, drivers.IoTypeDigitalOutput)
	if err != nil {
		return nil, errors.Join(err, fmt.Errorf("failed to get driver and name for io %s", ioId))
	}
	return driver.GetDigitalOutput(ioName)
}

// outputOwners maps every output io id configured on a non-shutter device to
// the device that owns it (lights, color lights incl. their RGBW output,
// dimmable lights, outlets). It reads the original config ids, so it works
// for every driver, unlike runtime String() values.
func (sw *SwKit) outputOwners() map[string]string {
	owners := make(map[string]string)
	add := func(ioId, owner string) {
		if strings.TrimSpace(ioId) != "" {
			owners[strings.TrimSpace(ioId)] = owner
		}
	}
	for _, l := range sw.Lights {
		add(l.DigitalOutName, "light "+l.Name)
	}
	for _, cl := range sw.ColorLights {
		add(cl.DigitalOutName, "color light "+cl.Name)
		add(cl.RgbwOutName, "color light "+cl.Name)
	}
	for _, dl := range sw.DimmableLights {
		add(dl.DigitalOutName, "dimmable light "+dl.Name)
		add(dl.AnalogOutName, "dimmable light "+dl.Name)
	}
	for _, o := range sw.Outlets {
		add(o.DigitalOutName, "outlet "+o.Name)
	}
	return owners
}

// ColorLightOutputOwners returns read-only output ownership metadata for the
// color lights (which are not part of the editable config): config io id ->
// "color light <name>". Editors use it so a shutter cannot claim a color
// light's output.
func (sw *SwKit) ColorLightOutputOwners() map[string]string {
	owners := make(map[string]string)
	for _, cl := range sw.ColorLights {
		if id := strings.TrimSpace(cl.DigitalOutName); id != "" {
			owners[id] = cl.Name
		}
		if id := strings.TrimSpace(cl.RgbwOutName); id != "" {
			owners[id] = cl.Name
		}
	}
	return owners
}

// validateWindowShutterConfigs applies the shared shutter rules at setup:
// field ranges and output types (app.ValidateWindowShutterFields), globally
// unique names, and outputs not shared with any other configured device.
func (sw *SwKit) validateWindowShutterConfigs() error {
	if len(sw.WindowShutters) == 0 {
		return nil
	}
	names := make(map[string]int)
	countName := func(n string) { names[strings.TrimSpace(n)]++ }
	for _, l := range sw.Lights {
		countName(l.Name)
	}
	for _, cl := range sw.ColorLights {
		countName(cl.Name)
	}
	for _, dl := range sw.DimmableLights {
		countName(dl.Name)
	}
	for _, o := range sw.Outlets {
		countName(o.Name)
	}
	for _, b := range sw.Buttons {
		countName(b.Name)
	}
	for _, s := range sw.Scenes {
		countName(s.Name)
	}
	for _, ws := range sw.WindowShutters {
		countName(ws.Name)
	}

	owners := sw.outputOwners()
	shutterOutputs := make(map[string]string)
	var errs error
	for _, ws := range sw.WindowShutters {
		label := fmt.Sprintf("window shutter %q", ws.Name)
		if strings.TrimSpace(ws.Name) == "" {
			errs = errors.Join(errs, errors.New("window shutter: name must not be empty"))
		} else if names[strings.TrimSpace(ws.Name)] > 1 {
			errs = errors.Join(errs, fmt.Errorf("%s: device name is used more than once", label))
		}
		for _, msg := range app.ValidateWindowShutterFields(windowShutterToEdit(ws), nil) {
			errs = errors.Join(errs, fmt.Errorf("%s: %s", label, msg))
		}
		for _, ioId := range []string{ws.OpenDigitalOutName, ws.CloseDigitalOutName} {
			id := strings.TrimSpace(ioId)
			if id == "" {
				continue
			}
			if owner, ok := owners[id]; ok {
				errs = errors.Join(errs, fmt.Errorf("%s: output %q is already used by %s", label, id, owner))
			}
			if other, ok := shutterOutputs[id]; ok && other != ws.Name {
				errs = errors.Join(errs, fmt.Errorf("%s: output %q is already used by window shutter %q", label, id, other))
			}
			shutterOutputs[id] = ws.Name
		}
	}
	return errs
}

func windowShutterToEdit(ws WindowShutterConfig) app.WindowShutterEditConfig {
	return app.WindowShutterEditConfig{
		Name:                     ws.Name,
		OpenDigitalOutName:       ws.OpenDigitalOutName,
		CloseDigitalOutName:      ws.CloseDigitalOutName,
		OpenDurationSeconds:      ws.OpenDurationSeconds,
		CloseDurationSeconds:     ws.CloseDurationSeconds,
		ReverseDelayMilliseconds: ws.ReverseDelayMilliseconds,
		DisableHomekit:           ws.DisableHomekit,
	}
}

func windowShutterFromEdit(ws app.WindowShutterEditConfig) WindowShutterConfig {
	return WindowShutterConfig{
		Name:                     ws.Name,
		OpenDigitalOutName:       ws.OpenDigitalOutName,
		CloseDigitalOutName:      ws.CloseDigitalOutName,
		OpenDurationSeconds:      ws.OpenDurationSeconds,
		CloseDurationSeconds:     ws.CloseDurationSeconds,
		ReverseDelayMilliseconds: ws.ReverseDelayMilliseconds,
		DisableHomekit:           ws.DisableHomekit,
	}
}

// shutdownWindowShutters stops every initialized shutter (nil-safe,
// idempotent) and joins the errors.
func (sw *SwKit) shutdownWindowShutters() error {
	var errs error
	for _, ws := range sw.windowShutters {
		if ws == nil {
			continue
		}
		if err := ws.Shutdown(); err != nil {
			errs = errors.Join(errs, err)
		}
	}
	return errs
}

// rollbackSetup undoes a failed Setup: pending scene/timed work is cancelled,
// initialized shutters are stopped, then the drivers that completed Setup are
// closed (in that order, so outputs are deenergized while the drivers still
// work).
func (sw *SwKit) rollbackSetup(setupDrivers []drivers.IoDriver) error {
	if sw.timed != nil {
		sw.timed.Close()
	}
	for _, scene := range sw.scenes {
		scene.Close()
	}
	errs := sw.shutdownWindowShutters()
	for _, d := range setupDrivers {
		if d == nil {
			continue
		}
		if err := d.Close(); err != nil {
			errs = errors.Join(errs, err)
		}
	}
	return errs
}
