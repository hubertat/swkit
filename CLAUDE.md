# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

**swkit** is a HomeKit-enabled switch/input/roller shutter controller for Raspberry Pi and similar devices. It provides a bridge between physical IO hardware (GPIO, MCP23017, Grenton, Shelly devices) and Apple HomeKit, allowing home automation control through the Home app.

Key technologies:
- Go 1.22+
- HomeKit integration via `github.com/brutella/hap`
- MQTT support via `github.com/eclipse/paho.golang`
- Hardware IO drivers for various platforms
- JSON configuration-based device setup

## Build & Test Commands

### Building

```bash
# Build for local development (uses platform defaults)
make build

# Build for Raspberry Pi (ARM7, Linux)
make build-raspberry

# Build for macOS (ARM64)
make build-darwin

# Build both targets
make all
```

Built binaries are output to the `./rel/` directory with version tags from git.

### Running

```bash
# Run the main application (requires config.json)
go run cmd/app/main.go -config config.json

# Run with debug logging
go run cmd/app/main.go -config config.json -debug

# Install as systemd service (Linux only)
./swkit -install
```

### Testing

```bash
# Run all tests
go test ./...

# Run tests with verbose output
go test -v ./...

# Run tests in a specific package
go test -v ./drivers

# Run a specific test
go test -v ./drivers -run TestPushEventDetector
```

### Other Commands

```bash
# Format code
go fmt ./...

# Tidy dependencies
go mod tidy

# Check for issues (if using golangci-lint)
golangci-lint run
```

## Architecture

### Core Components

**SwKit (swkit.go)** - Central orchestrator that:
- Manages IO drivers (GPIO, MCP23017, Grenton, Shelly, Mock)
- Coordinates devices (Lights, ColorLights, Outlets, Buttons)
- Handles HomeKit bridge setup and device registration
- Runs periodic sync loop to maintain state consistency between hardware and HomeKit

**IO Driver System (drivers/)** - Abstraction layer for hardware:
- `IoDriver` interface: Common contract for all hardware drivers
- IO ID format: `<driver_name>|<io_type>|<io_name>` (e.g., `gpio|d_out|5` or `shelly|d_in|shellyplus1pm-ABCD1234:0`)
- IO Types: DigitalOutput, DigitalInput, PushEventEmitter, AnalogOutput, RgbwOutput
- Each driver implements `Setup()`, `Close()`, and getter methods for specific IO types

**Device Types**:
- **Light** (`light.go`): Simple on/off light with digital output
- **ColorLight** (`color_light.go`): RGB/RGBW light with digital output + color control
- **Outlet** (`outlet.go`): Simple on/off outlet with digital output
- **Button** (`button.go`): Stateless switch with push event detection, can control other devices

**MQTT Integration (mqtt/)** - For Shelly device communication:
- `MqttClient`: Connection manager with auto-reconnect
- `JsonRpcMessenger`: Implements Shelly Gen 2+ RPC protocol
- `MqttHandler` interface: Devices implement this to handle MQTT messages

### Key Patterns

1. **IO Resolution Flow**:
   - Config specifies IO ID strings (e.g., `"DigitalOutName": "gpio|d_out|5"`)
   - `SwKit.Setup()` parses all IO IDs and groups by driver
   - Each driver's `Setup()` method is called with its relevant IO IDs
   - Devices retrieve specific IO interfaces (DigitalOutput, etc.) from drivers

2. **State Synchronization**:
   - `SwKit.StartTicker()` runs periodic sync loop
   - Each device implements `Sync(force bool)` to reconcile hardware ↔ HomeKit state
   - Force sync periodically ensures consistency even without changes

3. **HomeKit Integration**:
   - Devices implement `HkThing` interface
   - `InitHk()` creates HAP accessories
   - HomeKit callbacks trigger hardware changes via IO drivers
   - Hardware changes update HomeKit characteristics

4. **Event-Driven Buttons**:
   - `PushEventDetector` (`drivers/push_event_detector.go`): Detects single/double/triple/long press patterns
   - Buttons subscribe to specific event types
   - Events can trigger actions on controllable devices (lights, outlets)

### Driver-Specific Details

**GPIO Driver** (`drivers/gpio_driver.go`):
- Uses `go-rpio` for Raspberry Pi GPIO
- Supports input filtering/debouncing via `FilterInputsMs`
- Pins identified by GPIO number (e.g., `5`, `17`, `27`)

**MCP23017 Driver** (`drivers/mcpio_driver.go`):
- I2C expander for additional IO
- `DevNo` is relative (0-7), actual address is `0x20 + DevNo`
- Pins identified by port letter + number (e.g., `a3`, `b7`)

**Shelly Driver** (`drivers/shelly_driver.go`):
- Communicates via MQTT using Gen 2+ RPC protocol
- Device discovery and matching via status messages
- Supports inputs (switches) and outputs (relays)
- IO ID format: `<device_id>:<component_index>` (e.g., `shellyplus1pm-ABCD1234:0`)
- Health monitoring with periodic status checks

**Grenton Driver** (`drivers/grentonio_driver.go`):
- Custom protocol for Grenton smart home devices
- Device ID + feature ID addressing

**Mock Driver** (`drivers/mock_io_driver.go`):
- In-memory simulation for testing
- No hardware required

### Configuration

The application uses JSON configuration (`config.json` by default) with structure:

```json
{
  "Name": "My Home",
  "HkPin": "12345678",
  "Lights": [
    {
      "Name": "Living Room",
      "DigitalOutName": "gpio|d_out|5"
    }
  ],
  "Buttons": [
    {
      "Name": "Wall Switch",
      "EventInputName": "shelly|d_in|device-id:0",
      "ControlDevices": ["single_press:toggle:Living Room"]
    }
  ],
  "Gpio": {
    "InvertOutputs": false,
    "FilterInputsMs": 50
  },
  "Shelly": {
    "MqttBroker": "mqtt://192.168.1.100:1883",
    "MqttClientId": "swkit-main"
  },
  "SshServer": {
    "Enabled": true,
    "Port": 2222,
    "BindAddress": "",
    "AuthorizedKeysPath": "",
    "MaxSessions": 8,
    "IdleTimeoutSeconds": 900,
    "MaxTimeoutSeconds": 0
  }
}
```

Control device format: `<event>:<action>:<device_name>` where action is `on`, `off`, or `toggle`.

### SSH server configuration (security-relevant)

Every `SshServer` field is backward compatible at its zero value, which means
the defaults are permissive. Read this before exposing the port:

- `BindAddress`: empty binds **all interfaces** (`:port`). Set `"127.0.0.1"` to
  restrict to loopback.
- `AuthorizedKeysPath`: empty means `.ssh/authorized_keys`. If that file
  exists, public-key auth is enforced. **If it does not exist, the server
  starts with no authentication at all** and logs a warning at startup and per
  connection. An explicitly configured path that does not exist is a hard
  error, so asking for auth never silently degrades to open access.
- `MaxSessions` (default 8) caps concurrent sessions; over-cap connections are
  refused before a TUI program is allocated.
- `IdleTimeoutSeconds` (default 900) is enforced by the TUI as real key-input
  inactivity. A transport-level deadline cannot do this, because the TUI
  repaints roughly once a second on its own and that refreshes it.
- `MaxTimeoutSeconds` (default 0, disabled) is the only unconditional cap on
  total session lifetime.

## Development Notes

### Adding a New Device Type

1. Create struct with config and runtime state
2. Implement `Device` interface: `Sync(bool) error`, `Name() string`
3. Implement `HkThing` interface: `InitHk() *accessory.A`, `GetUniqueId() uint64`
4. Add constructor that retrieves IO from driver
5. Add config struct to `SwKit` and setup logic in `SwKit.Setup()`

### Adding a New IO Driver

1. Create driver struct implementing `IoDriver` interface
2. Implement `Setup(ctx, ios)` to parse IO IDs and initialize hardware
3. Implement getter methods for relevant IO types (return error for unsupported types)
4. Add to `MapAllIoDrivers()` in `io_driver.go`
5. Add driver field to `SwKit` struct

### Working with Shelly Devices

- Gen 2+ devices use JSON-RPC over MQTT
- Status messages arrive on `<device_id>/status/<component>`
- RPC requests sent to `<device_id>/rpc`
- See `drivers/shelly/` for component abstractions (Switch, Input, etc.)
- Device discovery happens via MQTT announcements and status matching
- **Important**: For button inputs (PushEventEmitter), use `d_in` type in IO ID, not `push_event` (Shelly driver limitation)

### Testing Considerations

- Use `MockIoDriver` for unit tests that need IO
- Driver tests typically require specific hardware or mocks
- HomeKit testing requires actual iOS device or simulator
- MQTT can be tested with local broker (mosquitto)

## Common Pitfalls

1. **MCP23017 addressing**: `DevNo` in config is NOT the I2C address. Address is `0x20 + DevNo`.

2. **IO ID format**: Must be exact `driver|type|name` with correct type string (use `IoType.IdString()`).

3. **Shelly device IDs**: Format is `device-id:component-index`, where device ID comes from Shelly's MQTT topic.

4. **State sync timing**: Devices must handle both hardware→HomeKit and HomeKit→hardware updates. Race conditions possible without proper synchronization.

5. **Driver Setup order**: All drivers must complete `Setup()` before device initialization, as devices retrieve IO interfaces during setup.

6. **Context cancellation**: Drivers with goroutines must respect context cancellation and clean up in `Close()`.

7. **Shelly button inputs**: Despite buttons using `GetPushEventEmitter()`, you must configure them with `d_in` IO type (`shelly|d_in|...`), not `push_event`. The Setup method doesn't handle `IoTypePushEventEmitter`.

## Accepted Open Issues (Known — Do Not Re-report as New)

Both of these have been reviewed and deliberately left open. They are recorded
here so they are not repeatedly rediscovered.

1. **SSH may run unauthenticated (accepted configuration risk).** With no
   `authorized_keys` file present, the SSH server serves the full TUI — hardware
   control, config save/reload, the AI agent — to any client that can reach the
   port, by default on all interfaces. This is a deliberate backward-compatibility
   choice, not an oversight; auth engages with no code change as soon as a key
   file exists, and the fail-open path warns loudly at startup and per connection.
   Session caps and idle expiry bound *resource* use only, never access. See the
   SSH server configuration section above for how to close it.

2. **Orphaned goroutines are bounded per call, not process-wide.** A driver or
   tool call that blocks forever cannot be interrupted — Go cannot kill a running
   goroutine — so an abandoned caller lingers until the callee returns. Every such
   site is made *safe* rather than eliminated: the stranded goroutine always has a
   private buffered channel to deliver into and exit, so it can never panic on a
   closed channel or corrupt live state, and nothing downstream waits on it. What
   is unbounded is the cumulative total: each new `Subscribe` (so each SSH
   reconnect) can strand one poll, manual refresh and IO-name export have no
   in-flight guard, and each tool timeout strands one handler. A permanently
   wedged driver therefore grows goroutines slowly over time.

   **When adding code that calls into a driver**, follow the established pattern:
   resolve what you need under `SwKitProvider.mu`, release the lock, and only then
   make the blocking call (see `GetState` and `ToggleDevice` for the canonical
   comments). Never hold `p.mu` across a driver call — `Reload` needs the write
   lock, and a wedged call would block config hot reload process-wide. The real
   fix for the residual leak is deadlines pushed down into the driver and
   controller interfaces themselves, which do not currently take a context.
