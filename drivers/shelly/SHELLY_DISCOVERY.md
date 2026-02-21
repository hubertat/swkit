# Shelly Discovery Plan Notes

This document captures essential context for implementing dynamic Shelly device discovery over MQTT in `swkit`.

## Current Driver Behavior (as-is)

- `ShellyIO.Setup` only creates devices from configured IO IDs (`shelly|d_in|<device>:<n>`, `shelly|d_out|<device>:<n>`).
- `ShellyDevice` instances are created empty first, then populated by `Shelly.GetStatus`.
- `JsonRpcMessenger` only accepts topics derived from `MqttTopicRoots()` (configured devices), so unknown devices are ignored.
- Result: no runtime onboarding of Shelly devices that are not already present in config.

## Is MQTT Discovery Feasible?

Yes.

Shelly Gen2 docs provide two complementary mechanisms:

- RPC channels per device:
  - `<shelly-id>/rpc`
  - `<src>/rpc`
  - `<shelly-id>/events/rpc`
  - `<shelly-id>/online`
- MQTT control announce flow:
  - publish `announce` to `shellies/command` (broadcast)
  - devices publish info on `shellies/announce` and `<topic_prefix>/announce`
  - `<topic_prefix>` defaults to `<device_id>` unless overridden

Together this is enough to discover device IDs/topic roots, then start RPC status polling and notification handling.

## Discovery/Data Flow Target

1. Bootstrap subscriptions (not tied to known devices):
   - `shellies/announce`
   - `+/announce` (or equivalent wildcard for topic prefixes)
   - optional `+/online`
   - `<clientId>/rpc` for responses
2. Trigger announce:
   - publish `announce` on `shellies/command`
3. On announce:
   - parse device identity (id/model/etc)
   - register device topic root (`topic_prefix` when available, else device id)
4. Per discovered root:
   - subscribe to `<root>/rpc`, `<root>/events/rpc`, `<root>/online`
   - create/lookup `ShellyDevice`
   - send `Shelly.GetStatus`
5. On status/notifications:
   - `Shelly.GetStatus` response -> `FillStatus` (full init)
   - `NotifyStatus` -> `UpdateFromStatus` (incremental update)
   - `NotifyEvent` -> map input events to `PushEventEmitter` handlers
6. Matching to configured swkit IO:
   - existing `matchDevices()` logic can still bind configured inputs/outputs to discovered+ready devices

## Required Refactor Areas

- `MqttTopicRoots()` must support dynamic growth after startup.
- `JsonRpcMessenger.checkForTopic` currently hard-rejects unknown topics; needs discovery-aware path.
- Need a runtime registry for:
  - `deviceId -> ShellyDevice`
  - `topicRoot -> deviceId` (important when `topic_prefix != deviceId`)
- Ensure thread-safe updates (announce/status/event handlers vs read paths).

## Integration Model with Configured swkit Devices

- Configured swkit devices remain authoritative for what HomeKit/app exposes.
- Discovery should populate runtime Shelly inventory and states.
- Configured IOs use discovered devices when IDs match; unconfigured Shelly units stay unbound but observable.

## Risks / Edge Cases

- Device MQTT config may disable control/announce behavior.
- Custom `topic_prefix` breaks assumptions if code keys only by device ID prefix.
- Partial status or profile changes can alter component counts; `FillStatus` currently rejects fewer switches than previously known.
- Reconnect/resubscribe behavior must preserve discovery state.

## Suggested Implementation Phases

1. Add passive discovery topic subscriptions + registry (no behavior changes for control).
2. Ingest announce and create discovered device entries.
3. Enable dynamic RPC topic subscriptions and status initialization for discovered devices.
4. Bind configured IOs using existing match logic; keep strict validation/errors for missing channels.
5. Add observability/debug status for discovered vs configured vs matched devices.

## References

- Shelly Gen2 RPC channels:
  - https://shelly-api-docs.shelly.cloud/gen2/General/RPCChannels/
- Shelly Gen2 notifications:
  - https://shelly-api-docs.shelly.cloud/gen2/General/Notifications
- Shelly Gen2 MQTT control and announce topics:
  - https://shelly-api-docs.shelly.cloud/gen2/0.14/ComponentsAndServices/Mqtt/
- Shelly RPC protocol:
  - https://shelly-api-docs.shelly.cloud/gen2/General/RPCProtocol/
