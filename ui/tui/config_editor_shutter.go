package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/hubertat/swkit/app"
)

// Window shutter editing in the Config tab. Fields, in cursor order:
//
//	0 Name, 1 Open output, 2 Close output, 3 Opening time (s),
//	4 Closing time (s, 0 = same as opening), 5 Reversal delay (ms,
//	0 = 500 ms default), 6 Disable HomeKit.
//
// Ranges mirror app.ValidateWindowShutterFields; out-of-range input is
// rejected in place with a status message rather than clamped silently.
const (
	shutterFieldName = iota
	shutterFieldOpen
	shutterFieldClose
	shutterFieldOpenDuration
	shutterFieldCloseDuration
	shutterFieldReverseDelay
	shutterFieldDisableHomekit
	shutterFieldCount
)

// currentShutter returns the window shutter selected in the list.
func (ce *ConfigEditor) currentShutter() (*app.WindowShutterEditConfig, bool) {
	if ce.cursor < 0 || ce.cursor >= len(ce.items) {
		return nil, false
	}
	item := ce.items[ce.cursor]
	if item.itemType != configItemWindowShutter || item.index >= len(ce.config.WindowShutters) {
		return nil, false
	}
	return &ce.config.WindowShutters[item.index], true
}

// updateEditShutter handles keys in window shutter edit mode.
func (ce *ConfigEditor) updateEditShutter(msg tea.Msg) tea.Cmd {
	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	ws, ok := ce.currentShutter()
	if !ok {
		ce.mode = ConfigModeList
		return nil
	}
	if ce.editing {
		return ce.handleShutterTextEditing(keyMsg, ws)
	}
	switch keyMsg.String() {
	case "up", "k":
		if ce.fieldCursor > 0 {
			ce.fieldCursor--
		}
	case "down", "j":
		if ce.fieldCursor < shutterFieldCount-1 {
			ce.fieldCursor++
		}
	case "enter":
		return ce.startEditingShutterField(ws)
	case "p":
		switch ce.fieldCursor {
		case shutterFieldOpen:
			ce.openIoPicker("output", shutterFieldOpen)
		case shutterFieldClose:
			ce.openIoPicker("output", shutterFieldClose)
		}
	case " ":
		if ce.fieldCursor == shutterFieldDisableHomekit {
			ws.DisableHomekit = !ws.DisableHomekit
			ce.markDirty()
		}
	case "esc":
		ce.mode = ConfigModeList
	}
	return nil
}

// startEditingShutterField begins editing the selected shutter field.
func (ce *ConfigEditor) startEditingShutterField(ws *app.WindowShutterEditConfig) tea.Cmd {
	var value string
	switch ce.fieldCursor {
	case shutterFieldName:
		value = ws.Name
	case shutterFieldOpen:
		value = ws.OpenDigitalOutName
	case shutterFieldClose:
		value = ws.CloseDigitalOutName
	case shutterFieldOpenDuration:
		value = strconv.Itoa(ws.OpenDurationSeconds)
	case shutterFieldCloseDuration:
		value = strconv.Itoa(ws.CloseDurationSeconds)
	case shutterFieldReverseDelay:
		value = strconv.Itoa(ws.ReverseDelayMilliseconds)
	case shutterFieldDisableHomekit:
		ws.DisableHomekit = !ws.DisableHomekit
		ce.markDirty()
		return nil
	default:
		return nil
	}
	ce.textInput.SetValue(value)
	ce.textInput.Focus()
	ce.editing = true
	return textinput.Blink
}

// handleShutterTextEditing commits or cancels a shutter text field edit.
func (ce *ConfigEditor) handleShutterTextEditing(msg tea.KeyMsg, ws *app.WindowShutterEditConfig) tea.Cmd {
	switch msg.Type {
	case tea.KeyEnter:
		value := strings.TrimSpace(ce.textInput.Value())
		parseRange := func(label string, min, max int, allowZero bool) (int, bool) {
			v, err := strconv.Atoi(value)
			if err != nil || (v != 0 || !allowZero) && (v < min || v > max) {
				if allowZero {
					ce.statusMsg = fmt.Sprintf("%s: 0 or %d-%d", label, min, max)
				} else {
					ce.statusMsg = fmt.Sprintf("%s: %d-%d", label, min, max)
				}
				return 0, false
			}
			return v, true
		}
		switch ce.fieldCursor {
		case shutterFieldName:
			if value != "" && value != ws.Name {
				old := ws.Name
				ws.Name = value
				ce.renameTargetReferences(old, value)
				ce.refreshOutputDeviceNames()
				ce.rebuildItems()
				ce.markDirty()
			}
		case shutterFieldOpen:
			ws.OpenDigitalOutName = value
			ce.markDirty()
		case shutterFieldClose:
			ws.CloseDigitalOutName = value
			ce.markDirty()
		case shutterFieldOpenDuration:
			if v, ok := parseRange("Opening time (s)", app.MinShutterDurationSeconds, app.MaxShutterDurationSeconds, false); ok {
				ws.OpenDurationSeconds = v
				ce.markDirty()
			}
		case shutterFieldCloseDuration:
			if v, ok := parseRange("Closing time (s; 0 = same as opening)", app.MinShutterDurationSeconds, app.MaxShutterDurationSeconds, true); ok {
				ws.CloseDurationSeconds = v
				ce.markDirty()
			}
		case shutterFieldReverseDelay:
			v, err := strconv.Atoi(value)
			if err != nil || v < 0 || v > app.MaxShutterReverseDelayMilliseconds {
				ce.statusMsg = "Reversal delay (ms): " + app.ShutterReverseDelayHelp
			} else {
				ws.ReverseDelayMilliseconds = v
				ce.markDirty()
			}
		}
		ce.editing = false
		ce.textInput.Blur()
	case tea.KeyEsc:
		ce.editing = false
		ce.textInput.Blur()
	default:
		var cmd tea.Cmd
		ce.textInput, cmd = ce.textInput.Update(msg)
		return cmd
	}
	return nil
}

// renameTargetReferences rewrites every reference to a renamed control target
// (button relations, scene device lists and scene steps, delayed ones
// included) so a rename never leaves dangling references.
func (ce *ConfigEditor) renameTargetReferences(oldName, newName string) {
	if oldName == "" || oldName == newName {
		return
	}
	for bi := range ce.config.Buttons {
		for ci := range ce.config.Buttons[bi].ControlDevices {
			if ce.config.Buttons[bi].ControlDevices[ci].DeviceName == oldName {
				ce.config.Buttons[bi].ControlDevices[ci].DeviceName = newName
			}
		}
	}
	for si := range ce.config.Scenes {
		sc := &ce.config.Scenes[si]
		for di, d := range sc.Devices {
			if d == oldName {
				sc.Devices[di] = newName
			}
		}
		for sti := range sc.States {
			for ai, a := range sc.States[sti].Actions {
				step, err := app.ParseSceneStep(a)
				if err != nil || step.Device != oldName {
					continue
				}
				step.Device = newName
				sc.States[sti].Actions[ai] = step.String()
			}
		}
	}
}

// removeTargetReferences drops every reference to a deleted control target:
// button relations, scene device-list entries and steps (delayed included).
func (ce *ConfigEditor) removeTargetReferences(name string) {
	for bi := range ce.config.Buttons {
		b := &ce.config.Buttons[bi]
		kept := b.ControlDevices[:0]
		for _, cd := range b.ControlDevices {
			if cd.DeviceName != name {
				kept = append(kept, cd)
			}
		}
		b.ControlDevices = kept
	}
	for si := range ce.config.Scenes {
		sc := &ce.config.Scenes[si]
		devs := sc.Devices[:0]
		for _, d := range sc.Devices {
			if d != name {
				devs = append(devs, d)
			}
		}
		sc.Devices = devs
		for sti := range sc.States {
			acts := sc.States[sti].Actions[:0]
			for _, a := range sc.States[sti].Actions {
				if step, err := app.ParseSceneStep(a); err == nil && step.Device == name {
					continue
				}
				acts = append(acts, a)
			}
			sc.States[sti].Actions = acts
		}
	}
}

// viewEditShutter renders the window shutter edit form.
func (ce *ConfigEditor) viewEditShutter(theme Theme) string {
	ws, ok := ce.currentShutter()
	if !ok {
		return theme.Muted.Render("No item selected")
	}
	title := theme.BoxTitle.Render(IconShutter + " Edit Window Shutter")
	closeDur := strconv.Itoa(ws.CloseDurationSeconds)
	if ws.CloseDurationSeconds == 0 {
		closeDur = "0 (same as opening)"
	}
	delay := strconv.Itoa(ws.ReverseDelayMilliseconds)
	if ws.ReverseDelayMilliseconds == 0 {
		delay = "0 (500 ms default)"
	}
	fields := []string{
		title, "",
		ce.renderTextField(theme, "Name", ws.Name, shutterFieldName),
		ce.renderTextField(theme, "Open output", ws.OpenDigitalOutName, shutterFieldOpen),
		ce.renderTextField(theme, "Close output", ws.CloseDigitalOutName, shutterFieldClose),
		ce.renderTextField(theme, "Opening time s", strconv.Itoa(ws.OpenDurationSeconds), shutterFieldOpenDuration),
		ce.renderTextField(theme, "Closing time s", closeDur, shutterFieldCloseDuration),
		ce.renderTextField(theme, "Reverse delay ms", delay, shutterFieldReverseDelay),
		ce.renderBoolField(theme, "Disable HomeKit", ws.DisableHomekit, shutterFieldDisableHomekit),
		"",
		theme.Muted.Render(fmt.Sprintf("  Travel times: %d-%d s (closing 0 = opening time).", app.MinShutterDurationSeconds, app.MaxShutterDurationSeconds)),
		theme.Muted.Render("  Reverse delay: " + app.ShutterReverseDelayHelp + "."),
		theme.Muted.Render("  Endpoint runs add 10% of the travel time (needs end limits)."),
		theme.Muted.Render("  Saving reloads the config: moving shutters stop and"),
		theme.Muted.Render("  positions are unknown until the next full open/close."),
		theme.Muted.Render("  p on an output field opens the IO picker."),
	}
	return theme.Box.Width(66).Render(strings.Join(fields, "\n")) + ce.viewStatus(theme)
}
