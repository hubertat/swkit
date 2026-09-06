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
- `p` on an IO field (field 2 of light/button edit) opens an IO point picker showing live detected points filtered by type (outputs for lights, inputs for buttons).
- If an IO point has a custom in-session name from IO Debug, the picker shows `custom_name [hardware_name]`.
- Selecting an IO point from the picker writes the raw IO ID string into config.
- Button control mappings editable inline (event/action/device cycling).
- `ctrl+r` saves via `ConfigProvider` (works over SSH; `ctrl+s` is also supported locally but may be intercepted by SSH terminals as XON/XOFF). When clean, the same key refreshes the editor snapshot and requests an application config reload.
- Saves are revision-checked; a stale save is refused and prompts before discarding local edits (see Data Flow below).

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
- TUI can also force a sync with `StateProvider.GetState()`, run as a `tea.Cmd` rather than inline in `Update` (it calls into every driver and would otherwise freeze the session's event loop).

2) User Actions
- Device toggles use `DeviceController.ToggleDevice(index)`.
- IO output toggles use `IoOutputController.ToggleIoOutput(driver, index)`.
- Config edits are persisted via `ConfigProvider.SaveConfigIfRevision(config, revision)`. The editor records the `Revision()` paired with the config it loaded, so a concurrent or out-of-band edit is reported as `app.ErrConfigRevisionMismatch` instead of being silently overwritten. On conflict the edits are kept and a `y/N` prompt asks before discarding them; only `y` discards.
- The editor's snapshot self-heals: `ConfigEditor.RefreshIfStale()` reloads when the provider's revision has moved on, while the editor is clean. This matters because `TriggerReload` is asynchronous — it signals the application's reload loop and returns — so the config the app loads moments later is picked up on a following state tick rather than needing a second keypress.

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
- Ensure the config provider persists new fields, and that they are covered by `Revision()` — a field excluded from the revision hash will not trigger a stale-save conflict when it changes underneath an editor.
- To add a new addable device type: add an entry to `addableDeviceTypes` (package-level var in `config_editor.go`) and handle the new `configListItemType` in `addDeviceOfType()`.

### IO Picker
- IO picker shows live points from `AppState.IoDebug` fed to `ConfigEditor.SetIoPoints()` on each state update.
- IO picker also receives in-session custom IO names from IO Debug (`Model.ioNames`) via `ConfigEditor.SetIoDisplayNames()` and uses them for labels only.
- Only drivers that implement `IoDebugProvider` surface points here (e.g., Shelly, Wago); others show nothing.
- The picker is filtered by type: "output" for lights, "input" for buttons.

## Known Limitations / Design Notes
- IO naming is session-only unless exported. Both export (`w`) and import exist; import accepts only regular files up to 1 MB, so a FIFO or character device cannot hang the session, and both run as commands off the `Update` path.
- IO picker custom labels are also session-only; persisted config still stores raw IO IDs.
- Chat streaming UI exists, but current integration is not streaming.
- List alignment can drift with emoji/wide glyphs due to byte-based padding.
- IO picker only shows points from drivers implementing `IoDebugProvider` (e.g., Shelly, Wago). GPIO and MCP23017 do not currently surface debug points.
- Sessions are bounded by a TUI-level idle watchdog (see `SshServerConfig.IdleTimeoutSeconds`), which measures real key input. A transport-level deadline cannot: the TUI repaints about once a second on its own, which refreshes it.
- Log lines are only consumed while the Logs tab is open. The broadcaster replays its ring buffer on subscribe, so recent history still appears on re-entry.

## Known Open Issues (Accepted, Not Fixed)

These are known and deliberately outstanding. They are recorded here so they
are not rediscovered as new findings.

- **SSH can run unauthenticated (accepted risk).** If no `authorized_keys` file
  resolves, the SSH server starts with no authentication, by default on all
  interfaces, and everything the TUI can reach — hardware controls, config
  save/reload, the AI agent — is reachable by any client that can connect. This
  is a deliberate configuration choice for backward compatibility, not an
  oversight: auth turns on with no code change as soon as a key is present.
  The fail-open path is loud (a startup warning plus one per connection,
  naming the remote address and user). Session caps and idle expiry limit
  resource exposure only; they do not limit access. To close it, drop a public
  key into the path named by `SshServerConfig.AuthorizedKeysPath` (default
  `.ssh/authorized_keys`), and/or set `BindAddress` to `127.0.0.1`.
- **Orphaned goroutines are bounded per call, not process-wide.** A driver call
  that blocks forever cannot be interrupted — Go offers no way to kill a
  running goroutine — so the abandoned caller lingers until the driver returns.
  Every such site is made safe rather than eliminated: the goroutine can always
  deliver into a private buffered channel and exit, so it can never panic on a
  closed channel or corrupt live state, and nothing downstream waits on it.
  What is not bounded is the total: each new subscription (so each reconnect)
  can strand one poll, manual refresh and export have no in-flight guard, and
  each tool timeout strands one handler. With a permanently wedged driver these
  accumulate over time. Properly fixing it means pushing deadlines down into
  the driver and controller calls themselves, which is a larger change than
  adding more guards at the call sites.
