# Implementation Plan: Capability Interfaces + Scenes

## Context & Decisions

Two related changes to the top-level device model in `swkit.go`:

1. **Expand actuation via capability sub-interfaces (Option A).** Keep
   `Controllable` lean (on/off + toggle + name). Add narrow optional interfaces
   (`Dimmable`, `Timed`) and discover them at call sites with type assertions.
   No fat interface, no per-device no-op methods.

2. **Add a multi-state `Scene` object.** A scene has N states (index 0 = "off"
   by convention); `Toggle()` advances to the next state and wraps around.
   Scenes drive devices directly through `Controllable`, reusing the existing
   button control-string grammar and `ControlDevice` resolution.

Locked decisions for this iteration:
- **Option A**, not the `Do(Action)` data approach.
- Scene behavior is **fire-and-forget**: a state is applied once on activation.
  Scenes do NOT re-assert state on `Sync` (no drift enforcement yet).
- **No HomeKit representation for Scene yet.** Scenes are reachable from
  buttons / TUI / control server only. HK mapping is deferred.
- Scene **is** `Controllable` so buttons can trigger scenes through the existing
  `ControlDevice` machinery with no new wiring, and scenes can nest later.
- Partial failures aggregate via `errors.Join` (consistent with the codebase).

Follow the existing per-device pattern everywhere: `XxxConfig` (JSON) + `Xxx`
(runtime) + `NewXxx(...)` constructor + a `getXxx()` collector in `swkit.go`,
plus config-provider and state-provider integration.

---

## Stage 1: `Dimmable` capability interface
**Goal**: Brightness becomes an interface-discoverable capability; remove the
concrete `*DimmableLight` dependency in the state provider.

**Changes**:
- In `swkit.go`, add:
  ```go
  type Dimmable interface {
      SetBrightness(pct int) // 0..100, HomeKit %
  }
  ```
- In `dimmable_light.go`, add an exported `SetBrightness(pct int)` method
  wrapping the existing `updateBrightness` logic (clamp 0..100, scale via
  `GetMinMax`, `Set`). Keep `updateBrightness` as the HK callback or have it
  delegate to `SetBrightness`.
- In `state_provider.go` `SetDeviceBrightness`, replace
  `device.(*DimmableLight)` with `device.(Dimmable)`.

**Success Criteria**:
- `go build ./...` clean; `go test ./...` green.
- No remaining `*DimmableLight` type assertion outside `dimmable_light.go`.
- `ColorLight` can later satisfy `Dimmable` without touching call sites.

**Tests**:
- Unit: `*DimmableLight` satisfies `Dimmable`; `*Light` and `*Outlet` do not
  (compile-time assertion `var _ Dimmable = (*DimmableLight)(nil)`).
- `SetDeviceBrightness` on a non-dimmable device returns the existing
  "does not support brightness" error.

**Status**: Complete

---

## Stage 2: `Timed` control via a SwKit-level orchestrator
**Goal**: Support "set on/off for a specific time, then revert" without
per-device timers.

**Design**: A small scheduler owned by `SwKit` (not per-device) so it composes
with Scenes and avoids duplicating timer/`Close` logic across four device types.

**Changes**:
- Add `Timed` capability interface in `swkit.go`:
  ```go
  type Timed interface {
      SetValueFor(state bool, d time.Duration)
  }
  ```
  (Optional — only if a call site needs device-driven timing. Default path is
  the orchestrator below.)
- Add a `timedControl` helper on `SwKit` that, given a `Controllable`, a target
  state, and a duration, sets the state now and schedules a revert to the prior
  state. Track pending reverts in a map keyed by device name; cancel/replace on
  re-trigger. Stop all pending timers in `SwKit.Close()`.
- Capture "prior state" by reading the device before applying (orchestrator
  tracks it, so the device stays dumb).

**Success Criteria**:
- A timed on/off reverts after the duration; re-triggering before expiry resets
  the timer rather than stacking.
- `Close()` cancels pending reverts (no goroutine/timer leak).

**Tests**:
- Unit with `MockIoDriver`: set-for-duration using a short duration / injected
  clock; assert state after expiry equals prior state.
- Re-trigger before expiry extends correctly.

**Status**: Complete

Notes:
- Added a `Stateful` capability interface (`GetState() (bool, error)`) and
  implemented it on Light, Outlet, ColorLight, DimmableLight. The orchestrator
  reads prior state via this; non-Stateful devices revert to the opposite.
- Did not add the per-device `Timed` interface — the SwKit-level orchestrator
  (`timedController`, exposed via `SwKit.SetDeviceValueFor`) is the only path.
- `timedController.afterFunc` is injectable for deterministic tests.

---

## Stage 3: Scene config + runtime core (fire-and-forget)
**Goal**: A standalone, testable `Scene` type with multi-state cycling. No
SwKit wiring yet.

**Changes** (new file `scene.go`):
- Config structs (reuse button control-string grammar for actions):
  ```go
  type SceneConfig struct {
      Name           string
      States         []SceneStateConfig // index 0 = "off" by convention
      DisableHomekit bool               // reserved; no HK yet
  }
  type SceneStateConfig struct {
      Name    string
      Actions []string // "<action>:<device_name>" e.g. "on:Kitchen"
  }
  ```
- Runtime:
  ```go
  type Scene struct {
      name         string
      states       []sceneState
      currentState atomic.Int32 // 0 = off
      logger       *log.Logger
  }
  type sceneState struct {
      name    string
      actions []ControlDevice // resolved, reusing button's type
  }
  ```
- Methods:
  - `Name() string`
  - `Activate(stateIndex int) error` — apply every action in that state
    (`on`/`off`/`toggle`, plus `brightness` via `Dimmable` assertion if present);
    aggregate failures with `errors.Join`; set `currentState`.
  - `Toggle()` — advance `currentState` modulo `len(states)` (0→1→…→0) and
    `Activate` the new index.
  - `SetValue(bool)` — `true` → `Activate(1)` (or first non-off state);
    `false` → `Activate(0)`. This makes `Scene` satisfy `Controllable`.
- Reuse `ParseControlDeviceString` for parsing `Actions`. The `ControlDevice`
  in `sceneState` ignores the event field (`e`); only `action` + `dev` are used.

**Success Criteria**:
- `var _ Controllable = (*Scene)(nil)` compiles.
- Cycling wraps correctly; state 0 turns member devices off.
- Fire-and-forget: no `Sync`-time re-assertion.

**Tests** (`scene_test.go`, `MockIoDriver`):
- 3-state scene: `Toggle` cycles 0→1→2→0; member outputs match each state.
- `SetValue(false)` returns to state 0.
- Partial failure: one bad action still applies the rest and returns joined err.

**Status**: Complete

Notes:
- The button grammar is event-prefixed and `ControlDevice` has no brightness
  level, so scenes use a dedicated `parseSceneAction` + `sceneAction` type
  rather than reusing `ParseControlDeviceString`/`ControlDevice`. Scene action
  grammar: `on|off|toggle:<device>` and `brightness:<pct>:<device>`.
- `Scene` satisfies `Controllable` (verified by `TestSceneControlsScene`,
  scene-drives-scene). `GetUniqueId` added for future state/HK use.
- Brightness actions apply via the `Dimmable` capability from Stage 1.

---

## Stage 4: Wire Scene into SwKit
**Goal**: Scenes are constructed during setup and controllable from buttons.

**Changes** (`swkit.go`):
- Add `Scenes []SceneConfig` (config) and `scenes []*Scene` (runtime) fields.
- Add a `NewScene(config, resolve func(name string) (Controllable, bool), logger)`
  constructor, or resolve actions inside `Setup`.
- In `Setup()`, build scenes **after** all controllable devices exist (scenes
  reference devices by name, like buttons). Resolve each action's device via
  `getControllableDevices()`; error on unknown device names.
- Include scenes in `getControllableDevices()` so buttons (and other scenes)
  can target them. Add a `getScenes()` collector if a `Device`/sync view is
  wanted later (scenes have no `Sync` work for fire-and-forget — skip or no-op).
- Resolution ordering: devices first, then scenes, then buttons (buttons may
  reference scenes). Verify scene-references-scene resolves regardless of slice
  order, or document that scenes cannot reference later-defined scenes.

**Success Criteria**:
- A button configured with `single_press:toggle:<SceneName>` cycles the scene.
- Unknown device/scene name in a scene action fails `Setup` with a clear error.
- `go build ./...` / `go test ./...` green.

**Tests**:
- Integration via `cmd/mock` style setup: button toggles a scene, member device
  states reflect the active scene state.

**Status**: Complete

Notes:
- Added `Scenes []SceneConfig` / `scenes []*Scene` fields, a
  `resolveControllable(name)` helper, and a scene-build loop in `Setup` placed
  after physical devices and before buttons. Scenes are appended to
  `getControllableDevices()` (last), so buttons can target them.
- Resolution ordering: a scene sees devices + earlier-built scenes only, so it
  can reference earlier scenes but not later ones — this also prevents cycles by
  construction. Covered by `TestSetupSceneReferences{Earlier,Later}Scene`.
- Drive-by bugfix: `MockIoDriver.Setup` stored IOs by full id but lookups use
  the resolved name part, so `cmd/mock` and any Setup-based test errored with
  "mock output N not found". Now stores by name. (Committed separately.)

---

## Stage 5: Config + state provider integration
**Goal**: Scenes are visible/editable in the TUI config editor and status views.

**Changes**:
- `app`: add `SceneEditConfig` / `SceneStateEditConfig` mirroring the device
  edit-config pattern; add scenes to `EditableConfig`.
- `config_provider.go`: map `SceneConfig` ↔ `SceneEditConfig` in
  `GetEditableConfig` and `SaveConfig`; reuse `FormatControlDeviceString` /
  `parseControlDeviceToEdit` for action strings.
- `state_provider.go`: expose scene state (name, current state index/name,
  member relations) — add a `DeviceTypeScene` and a state-builder paralleling
  the button one.

**Success Criteria**:
- Scenes round-trip through save/reload without loss.
- TUI lists scenes and their current state.

**Tests**:
- Config round-trip test (parallel to `config_provider_test.go`):
  load → edit → save → reload preserves scenes and actions.

**Status**: Complete

Notes:
- `app`: added `SceneEditConfig`/`SceneStateEditConfig` + `EditableConfig.Scenes`;
  `DeviceTypeScene`, `DeviceState.SceneStateIndex`/`SceneStateNames`, and
  `StateSummary.ScenesCount`.
- `config_provider.go`: scenes round-trip in `GetEditableConfig`/`SaveConfig`;
  scene names added to `OutputDeviceNames` (valid control targets).
- `state_provider.go`: `buildSceneState`, scenes appended to `GetState` device
  list, `getControllableByIndex` extended (scenes after buttons; scenes return
  a non-nil Controllable), `getDeviceState` handles `*Scene` (on = state != 0).
- TUI/control server: scene icon (`🎬`), scenes are controllable in the control
  server. The device list and toggle/set paths are generic, so scenes show and
  toggle without further per-type UI code. Scene-action editing in the config
  editor form is not added (raw action strings round-trip); deferred to UI work.
- Scene action edit configs keep raw strings (the scene grammar differs from the
  button control grammar, so `ControlDeviceEdit` is not reused).

---

## Stage 6: Scene config editor + timed-control exposure
**Goal**: Make scenes fully editable in the TUI config editor, and make the
Stage 2 timed controller (`SwKit.SetDeviceValueFor`) reachable by users. Two
independent sub-stages; 6A and 6B can land in either order.

Context: today scenes round-trip through the config provider and render in the
status list, but there is no add/edit/delete *form* (unlike Lights/Outlets/
Buttons), and `SetDeviceValueFor` has no caller. The config editor
(`ui/tui/config_editor.go`) is a mode-per-screen state machine: `ConfigMode`
enum, `configListItemType` list items, `addTypeChoices`, per-type
`updateEditXxx` handlers + `rebuildItems()`, and a single-level ControlDevices
sub-editor for buttons (`ctrlCursor`/`ctrlEditing`/`ctrlFieldCursor`) plus a
control-by-relation wizard (`wizardOutputDevices`, `ConfigModeCtrlWizard*`).

### Stage 6A: Scene config editor (TUI form)

Scenes are **doubly nested** (scene → states → actions), deeper than any
existing form. Model it as three screens reusing existing idioms:

- New `ConfigMode`s: `ConfigModeEditScene` (scene name + state list),
  `ConfigModeEditSceneState` (state name + action list), and an action editor
  reusing the control-by-relation wizard pattern (device picker → action →
  optional brightness/duration), producing a scene action string.
- New `configItemScene` in `configListItemType`; add to `addTypeChoices`
  (`IconScene`) and to `rebuildItems()`.
- Editor state additions: scene index, state cursor/index, action cursor, and
  action-builder fields (device name, action, brightness level).
- Handlers: `updateEditScene`, `updateEditSceneState`, action add/edit/delete;
  view/render funcs for the three lists.

**Action string parse/format** — the TUI works with `app.SceneEditConfig` (raw
action strings) and must parse them to populate the editor and format edits
back. Introduce `app.ParseSceneAction(s) (action string, level int, dev string,
err error)` and `app.FormatSceneAction(action string, level int, dev string)`
in the `app` package, and have swkit's `parseSceneAction` (scene.go) delegate to
the `app` parser to keep one grammar definition. (This is the small refactor the
Stage 3 note anticipated.)

- Action targets: reuse `wizardOutputDevices()` but also include
  `DeviceTypeScene` (scenes may target scenes). Offer `brightness` only when the
  selected target is `DeviceTypeDimmableLight`.

**Success Criteria**:
- Add a scene with ≥2 states and per-state actions via the TUI, save, reload —
  round-trips with no loss; edit and delete work; back-navigation is consistent
  with other forms.
- `go build ./...` / `go test ./...` green.

**Tests**:
- `app.ParseSceneAction`/`FormatSceneAction` round-trip table test, and a test
  that swkit's `parseSceneAction` still rejects the same invalid inputs.
- Config-editor model test (driving `Update` with key msgs, as existing config
  editor tests do) for add-scene → add-state → add-action → save, asserting the
  resulting `EditableConfig.Scenes`.

**Status**: Complete

Notes:
- `app.ParseSceneAction`/`FormatSceneAction` added; swkit's `parseSceneAction`
  delegates to the app parser (single grammar definition).
- New editor modes `ConfigModeEditScene` and `ConfigModeEditSceneState` (with an
  inline action editor cycling action/device/level). Scene editor code in
  `ui/tui/config_editor_scene.go`; `config_editor.go` got the consts, struct
  fields, and list/add/delete/route/help hooks. Scenes show in the list and in
  `OutputDeviceNames`.
- Brightness level cycles by 5, only for the `brightness` verb; the editor does
  not yet restrict brightness to dimmable targets (runtime validates).

### Stage 6B: Timed-control exposure

Make `SetDeviceValueFor` usable. Primary path is provider + UI; scene timed
actions are an optional follow-on.

**Path 1 (recommended): provider + control server + TUI**
- Add to `app.StateProvider` (or the control interface) a method
  `SetDeviceValueFor(index int, state bool, seconds int) app.ControlResult`;
  implement on `SwKitProvider` by resolving the controllable via
  `getControllableByIndex` and calling `sw.SetDeviceValueFor`.
- Control server (`server/control_server.go`): accept an optional duration
  (e.g. `seconds` param) on the set action; when present, route to the timed
  method. Surface `Controllable` devices only (scenes included).
- TUI: a keybinding on a controllable device to "set on for N seconds" (prompt
  for duration via the existing text input), calling the provider method.

**Path 2 (optional): timed scene actions**
- Extend the scene grammar with `on-for:<seconds>:<device>` /
  `off-for:<seconds>:<device>` (and matching `app.ParseSceneAction`/format).
- Requires threading the orchestrator into `Scene`: inject a
  `timedSetter func(Controllable, bool, time.Duration)` into `NewScene` (from
  `SwKit.Setup`, bound to `sw.SetDeviceValueFor`), and have `sceneAction.apply`
  use it for timed actions. Keep non-timed scenes working with a nil setter.

**Test seam**: `SwKit.timed` uses real `time.AfterFunc`; add a way to inject
`afterFunc` (constructor option or setter) so provider/scene timed-action tests
fire deterministically, as `timedController`'s own tests already do.

**Success Criteria**:
- A user can set a device on for N seconds via the control server and/or TUI and
  observe it revert to its prior state.
- (If Path 2) a scene `on-for:...` action sets and auto-reverts.

**Tests**:
- Provider `SetDeviceValueFor` delegates and reverts (injected `afterFunc`).
- (If Path 2) scene timed action applies then reverts.

**Status**: Path 1 Complete; Path 2 not done (deferred, optional)

Notes:
- `app.DeviceController` gained `SetDeviceValueFor(index, state, seconds)`;
  implemented on `SwKitProvider` (resolves via `getControllableByIndex`,
  rejects buttons and non-positive durations, delegates to
  `SwKit.SetDeviceValueFor`).
- Control server: new `set_for` action (`{value, seconds}`) on
  `/api/devices/{index}/set_for`. Web UI (HTML/JS) button not added — API only.
- TUI: `f` on the Devices tab opens an "on for N seconds" prompt (default 60),
  mirroring the existing IO-naming input pattern; help footer updated.
- Test seam: `timedController.afterFunc` is already injectable; provider tests
  set it directly (same package) to fire reverts deterministically.
- Mock controllers (`agent` test, new `server` test fake) gained the method.

---

## Out of scope (future)
- HomeKit representation for scenes (stateless switch vs. one Switch per state).
- Scene drift enforcement on `Sync` (turning a scene into a persistent "mode").
- `Action`-as-data refactor (Option B) — only revisit if action serialization
  across the agent/API becomes a primary need.
- Stage 6B Path 2 (timed scene actions like `on-for:<seconds>:<device>`) —
  needs threading the orchestrator into `Scene`.
- Control web UI button for timed control (the `set_for` API exists).
