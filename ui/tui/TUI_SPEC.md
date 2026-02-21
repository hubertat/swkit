# TUI Specification (swkit)

This document describes the current TUI implementation, its scope, abstraction layers, and how it connects to the rest of the codebase. It is intended to help you understand what exists today and how to extend it safely.

## Scope and Responsibilities
- The TUI is a BubbleTea app that renders state, provides limited control actions, and optionally a chat interface.
- It does not own business logic or authoritative state. That lives in `app/` (and `agent/` for chat).
- The TUI consumes state via interfaces and sends control/config actions through those same interfaces.

## Implemented Functionality (Feature Inventory)

### Navigation / Global
- Tabbed navigation across: Dashboard, Drivers, Devices, Config, HomeKit, IO Debug, Chat.
- Global key bindings for navigation, help, refresh, quit.
- Contextual help bar shows key hints per mode.

### Dashboard
- Summary boxes: Drivers (ready/issue status), Devices (counts), HomeKit (enabled + count).
- Uses `AppState.Summary()` for aggregated counts.

### Drivers
- List view of drivers with Ready / Not Ready and optional status info.

### Devices
- Split view: left list of devices, right detail panel for selected device.
- Shows type, HomeKit status, health, and IO config bindings.
- Control: toggle selected device with `enter`.

### Config Editor
- Edit lights and buttons (name, IO binding, disable HomeKit).
- Add/remove lights and buttons.
- `a` in list mode opens a device type selector (Light, Button) instead of adding a fixed type.
- `p` on an IO field (field 2 of light/button edit) opens an IO point picker showing live detected points filtered by type (outputs for lights, inputs for buttons). Select to auto-fill the IO ID.
- Button control mappings editable inline (event/action/device cycling).
- `ctrl+s` saves via `ConfigProvider`.

### HomeKit
- Read-only view: enabled status, PIN, address, accessory count.

### IO Debug
- Filtered view: All / Inputs / Outputs.
- All mode shows inputs and outputs side-by-side.
- Per-point indicators: state, health, activity timer.
- Name IO points in-session and export names to `io_names.json`.

### Chat
- Optional chat panel if agent is configured.
- Message history with markdown rendering via Glamour.
- If no agent, TUI displays a disabled message.
- UI supports streaming messages, but current integration returns only a final response.

## Abstraction Layers and Boundaries

### Core / Server Layer (outside TUI)
- Owns canonical state, logic, and persistence.
- Key definitions live in `app/`:
  - `AppState`, `DeviceState`, `DriverState`, `IoPointDebugState`, `HomeKitState`.
  - `EditableConfig` and related edit types.
- Interfaces used by the TUI:
  - `app.StateProvider` (state subscription and `GetState()`).
  - `app.DeviceController` (device toggles).
  - `app.IoOutputController` (IO output toggles).
  - `app.ConfigProvider` (config load/save).

### TUI Layer (`ui/tui`)
- `tui.go`: BubbleTea model, state subscription, routing, rendering, and input handling.
- `config_editor.go`: Config editor component.
- `chat.go`: Chat component.
- `keymap.go`: Key bindings.
- `theme.go`: Styles and glyphs.

### What Lives Where
- Server/Core owns:
  - Device/driver state, IO debug data, HomeKit state.
  - Config read/write rules and validation.
  - Control execution (toggle logic, IO writes).
- TUI owns:
  - Rendering, input handling, local UI state (cursor, filters, selection).
  - Transient state only (in-session IO names, status messages).

## Relations with the Rest of the Code (Data Flow)

1) State Subscription
- TUI calls `StateProvider.Subscribe(ctx, interval)` and updates its model on each `StateUpdateMsg`.
- TUI can also force a sync with `StateProvider.GetState()`.

2) User Actions
- Device toggles use `DeviceController.ToggleDevice(index)`.
- IO output toggles use `IoOutputController.ToggleIoOutput(driver, index)`.
- Config edits are persisted via `ConfigProvider.SaveConfig(config)`.

3) Chat
- Uses `agent.Agent.Chat(ctx, text)` to retrieve assistant responses.

## Key Data Structures Used by the TUI
- `app.AppState`: master snapshot.
- `app.DeviceState`: device list for Devices tab.
- `app.IoPointDebugState`: IO Debug rendering + activity timer.
- `app.EditableConfig`: config editing model (lights, buttons).
- `app.ControlDeviceEdit`: button control mappings.

## Extending the TUI (Implementation Guidance)

### New Tab
- Add a new `Tab` enum in `ui/tui/tui.go`.
- Update `AllTabs()` and `Tab.String()`.
- Render its content in `Model.View()` and `renderTabBar()`.

### New Global Key or Action
- Add binding in `ui/tui/keymap.go`.
- Handle it in `Model.Update()` before tab-specific logic.

### New Data Fields
- Extend `app.AppState` (server side).
- Ensure `StateProvider` populates it.
- Render in TUI as needed.

### New Control Actions
- Add a new interface in `app/` (similar to `DeviceController`).
- Implement it on the provider.
- Use interface assertion in the TUI to call it.

### Extend Config Editor
- Update `EditableConfig` in `app/` for new fields.
- Add fields/modes in `config_editor.go`.
- Ensure `SaveConfig` persists new fields.
- To add a new addable device type: add an entry to `addableDeviceTypes` (package-level var in `config_editor.go`) and handle the new `configListItemType` in `addDeviceOfType()`.

### IO Picker
- IO picker shows live points from `AppState.IoDebug` fed to `ConfigEditor.SetIoPoints()` on each state update.
- Only drivers that implement `IoDebugProvider` surface points here (e.g., Shelly, Wago); others show nothing.
- The picker is filtered by type: "output" for lights, "input" for buttons.

## Known Limitations / Design Notes
- IO naming is session-only unless exported; no import path exists.
- Chat streaming UI exists, but current integration is not streaming.
- List alignment can drift with emoji/wide glyphs due to byte-based padding.
- IO picker only shows points from drivers implementing `IoDebugProvider` (e.g., Shelly, Wago). GPIO and MCP23017 do not currently surface debug points.
