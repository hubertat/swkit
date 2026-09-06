# Plan: Config Editor in TUI

## Overview

Add a Config tab to the existing Bubbletea TUI that allows editing Light and Button configuration objects, with save/backup functionality via Ctrl+S.

## Architecture

### Data Flow

```
SwKit struct (exported config fields)
       ↓ read
ConfigProvider (app/config.go interface)
       ↓
ConfigEditor (ui/tui/) ← user edits
       ↓ save
ConfigProvider.SaveConfig()
       ↓
Backup old config.json → config.json.2026-02-21_15-04-05
Write new config.json (json.MarshalIndent of full SwKit)
```

### Key Design Decisions

1. **Config provider interface** in `app/` package - keeps TUI decoupled from SwKit internals
2. **Edit a copy** of config arrays in memory; only persist on explicit Ctrl+S
3. **Full re-marshal** of SwKit struct on save (all exported fields preserved, driver configs included)
4. **Structured ControlDevices editor** - select event/action/device from known values rather than raw string editing

---

## New Files

### 1. `app/config.go` — Config state types and provider interface

```go
type ConfigProvider interface {
    GetEditableConfig() EditableConfig
    SaveConfig(config EditableConfig) error
}

type EditableConfig struct {
    Lights  []LightEditConfig
    Buttons []ButtonEditConfig
    // Read-only context: names of output devices for button target selection
    OutputDeviceNames []string
}

type LightEditConfig struct {
    Name           string
    DigitalOutName string  // raw IO ID string, edited manually
    DisableHomekit bool
}

type ButtonEditConfig struct {
    Name           string
    EventInputName string  // raw IO ID string, edited manually
    ControlDevices []ControlDeviceEdit
    DisableHomekit bool
}

type ControlDeviceEdit struct {
    EventType  string  // "single_press", "double_press", "triple_press", "long_press"
    Action     string  // "toggle", "on", "off"
    DeviceName string  // name of target output device
}
```

### 2. `config_provider.go` (root swkit package) — ConfigProvider implementation

```go
type SwKitConfigProvider struct {
    sw         *SwKit
    configPath string
}

func NewConfigProvider(sw *SwKit, configPath string) *SwKitConfigProvider

func (p *SwKitConfigProvider) GetEditableConfig() app.EditableConfig
// Reads from sw.Lights, sw.Buttons
// Parses ControlDevices strings into ControlDeviceEdit structs
// Collects output device names from sw.Lights, sw.ColorLights, sw.Outlets

func (p *SwKitConfigProvider) SaveConfig(config app.EditableConfig) error
// 1. Backup: copy configPath → configPath.YYYY-MM-DD_HH-MM-SS
// 2. Update sw.Lights and sw.Buttons from EditableConfig
//    (convert ControlDeviceEdit back to "event:action:device" strings)
// 3. json.MarshalIndent(sw, "", "  ") → write to configPath
```

### 3. `ui/tui/config_editor.go` — Config editor TUI component

Main component managing the config tab. State machine with these modes:

**Modes:**
- `ConfigModeList` — browsable list of Lights and Buttons
- `ConfigModeEditLight` — form for editing a single Light
- `ConfigModeEditButton` — form for editing a single Button
- `ConfigModeEditControlDevice` — sub-form for editing a single ControlDevice entry

**ConfigEditor struct:**
```go
type ConfigEditor struct {
    provider    app.ConfigProvider
    config      app.EditableConfig   // working copy
    dirty       bool                 // unsaved changes exist
    mode        ConfigMode

    // List mode state
    cursor      int
    items       []configListItem     // flattened: lights then buttons

    // Edit mode state
    editIndex   int                  // index in lights/buttons array
    editType    string               // "light" or "button"
    fieldCursor int                  // which form field is selected
    editing     bool                 // text input active
    textInput   textinput.Model

    // Control device edit state
    ctrlCursor  int                  // cursor within control devices list
    ctrlEditing bool                 // editing a control device
    ctrlFieldCursor int             // which field in control device form

    // Message state
    statusMsg   string
    theme       Theme
}
```

**List View Layout:**
```
┌─ Config ──────────────────────────────────┐
│  Lights                                    │
│  > 💡 Living Room     gpio|d_out|5         │
│    💡 Bedroom          gpio|d_out|6         │
│                                            │
│  Buttons                                   │
│    👆 Wall Switch      shelly|d_in|dev:0    │
│    👆 Hallway Switch   shelly|d_in|dev:1    │
│                                            │
│  [unsaved changes]                         │
└────────────────────────────────────────────┘
  a: add  enter: edit  d: delete  ctrl+s: save
```

**Light Edit Form Layout:**
```
┌─ Edit Light ──────────────────────────────┐
│                                            │
│  Name:            [Living Room        ]    │
│  IO Output:       [gpio|d_out|5       ]    │
│  Disable HomeKit: [ No ]                   │
│                                            │
│                                            │
└────────────────────────────────────────────┘
  ↑↓: navigate  enter: edit field  space: toggle bool  esc: back
```

**Button Edit Form Layout:**
```
┌─ Edit Button ─────────────────────────────┐
│                                            │
│  Name:            [Wall Switch        ]    │
│  Event Input:     [shelly|d_in|dev:0  ]    │
│  Disable HomeKit: [ No ]                   │
│                                            │
│  Control Devices:                          │
│  > single_press → toggle → Living Room     │
│    double_press → on     → Bedroom         │
│                                            │
│  a: add control  d: delete  enter: edit    │
└────────────────────────────────────────────┘
```

**Control Device Edit (inline within Button form):**
```
│  > Event:  [◀ single_press ▶]              │
│    Action: [◀ toggle        ▶]              │
│    Device: [◀ Living Room   ▶]              │
```
Uses Left/Right arrows to cycle through available options.

**Key Bindings (Config tab specific):**
- `a` — Add new item (light in lights section, button in buttons section)
- `d` — Delete selected item (with cursor on it)
- `Enter` — Edit selected item / confirm field edit
- `Esc` — Back from edit form to list / cancel current edit
- `Space` — Toggle boolean field
- `←/→` — Cycle options in ControlDevice editor
- `Ctrl+S` — Save config (global, works from any mode)

### 4. Modifications to existing files

**`ui/tui/tui.go`:**
- Add `TabConfig` to Tab enum (between TabDevices and TabHomeKit)
- Add `configEditor ConfigEditor` field to Model
- Wire config editor in Update() — forward keys when on Config tab
- Wire Ctrl+S as global save shortcut when config tab has changes
- Render config editor in View()

**`ui/tui/keymap.go`:**
- Add `Save` key binding (`ctrl+s`)
- Add `Add` key binding (`a`)
- Add `Delete` key binding (`d`, `delete`)
- Update ShortHelp() to show context-appropriate bindings

**`ui/tui/theme.go`:**
- Add `FormField` style — for form labels
- Add `FormFieldActive` style — for currently editing field
- Add `FormInput` style — for text input areas
- Add `DirtyIndicator` style — for "[unsaved changes]" indicator

**`cmd/tui/main.go`:**
- Create `SwKitConfigProvider` with config path
- Pass to TUI model constructor

**`cmd/app/main.go`:**
- Create `SwKitConfigProvider` with config path
- Pass to TUI model constructor (when `--tui` flag is used)

---

## Implementation Steps

### Step 1: Config types and provider interface
- Create `app/config.go` with types and interface
- Create `config_provider.go` with SwKitConfigProvider implementation
- Implement GetEditableConfig (read from SwKit)
- Implement SaveConfig (backup + write)

### Step 2: Config editor component — list mode
- Create `ui/tui/config_editor.go`
- Implement ConfigEditor struct with list mode
- Render light/button list with cursor navigation
- Add/delete items in list

### Step 3: Config editor component — edit forms
- Light edit form with Name, DigitalOutName, DisableHomekit fields
- Button edit form with Name, EventInputName, DisableHomekit fields
- Text input for string fields, toggle for booleans
- Track dirty state

### Step 4: Button ControlDevices editor
- Sub-list within button edit form
- Add/edit/delete control device entries
- Cycle-select for EventType (single_press, double_press, triple_press, long_press)
- Cycle-select for Action (toggle, on, off)
- Cycle-select for DeviceName (from OutputDeviceNames list)
- Convert to/from config string format on save/load

### Step 5: Wire into TUI
- Add TabConfig to tab enum and navigation
- Add ConfigEditor to Model struct
- Update constructors to accept ConfigProvider
- Handle Ctrl+S globally for save
- Add theme styles for form elements
- Update keymap with new bindings and context-aware help

### Step 6: Update entry points
- Modify `cmd/tui/main.go` to create and pass ConfigProvider
- Modify `cmd/app/main.go` to create and pass ConfigProvider
- Ensure config path is available to provider

### Step 7: Testing and polish
- Test add/edit/delete lights and buttons
- Test ControlDevices editor
- Test save with backup
- Test that saved config is valid and loadable
- Handle edge cases: empty config, long names, special characters
