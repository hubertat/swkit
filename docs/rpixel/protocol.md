# rpixels UDP protocol

## Scope and goals

`rpixels` is an RGBW NeoPixel-ring controller connected over Wi-Fi. UDP is its
device transport. It is deliberately independent from the transport used by
other `swkit` drivers: GPIO is local, and Shelly uses JSON-RPC over MQTT.

The shared integration point is `swkit`'s IO capability model, especially
`RgbwOutput`. The Arduino driver adapts that model to this binary UDP protocol.

UDP is appropriate for current display state and animations because updates are
small and a newer desired state can replace an older one. Commands that require
a result use a request ID and a response, with client retries when no response
arrives.

This document describes protocol version `1`. Devices report their version in
`S`. A change that existing clients cannot parse increments the version;
appending fields to a payload does not.

## Common frame

Each UDP datagram contains exactly one complete frame. No stream framing is
needed beyond this validation field.

```text
+------------------+---------+------------------+------------------+
| length (u16, BE) | type    | payload (0..N B) | CRC-32 (u32, BE) |
+------------------+---------+------------------+------------------+
```

- `length` is the total number of bytes in the frame, including the length
  field and checksum. Frames are 7 to 512 bytes long.
- `type` is an ASCII byte identifying the packet.
- CRC-32 is the standard IEEE CRC-32 (as in zlib, Ethernet, and Go's
  `crc32.ChecksumIEEE`): polynomial `0x04C11DB7`, reflected input and output,
  initial value and final XOR `0xFFFFFFFF`. It is calculated over all
  preceding bytes: `length`, `type`, and payload.
- A frame is dropped without any reply when it is shorter than 7 or longer than
  512 bytes, when its declared and actual lengths differ, or when its CRC does
  not match. A client only learns of this by timing out.
- A well-framed packet whose payload does not match its type is rejected as
  described for each type below.

This framing is already represented by `swkit/drivers/arduino/packet.go`.

Test vectors, byte for byte:

```text
G, request_id 1:
  00 09 47 00 01 b9 f8 13 44
P, white 255, mode I, brightness 128:
  00 0d 50 00 00 00 ff 49 80 2c 42 ab 49
A, request_id 2, fade, full ring, 0 ms, one stage white 255 at brightness 128:
  00 17 41 00 02 02 01 00 00 00 00 00 00 01 00 00 00 ff 80 ee db 30 ea
```

## Colour and brightness

Every colour is four channels, red, green, blue, and white, each `0..255`.
Brightness is a separate `0..255` value that scales all channels of all pixels
together (the LED driver outputs roughly `channel × brightness / 255`).
Brightness is strip-global on this hardware: it always applies to the whole
strip, even when a command targets a single ring.

The ring has 40 pixels: an inner ring of 16 (pixels `0..15`) and an outer ring
of 24 (pixels `16..39`).

## Immediate-set packet

`P` sets the display without a request ID and remains the baseline command used
by a generic `RgbwOutput.Set(r, g, b, w)` operation.

```text
type: 'P'
payload: red, green, blue, white, mode, brightness
         u8   u8     u8    u8     ASCII  u8
```

```text
mode 'L'  2400 ms circular wipe of the full ring to the colour, forward/left
mode 'R'  2400 ms circular wipe of the full ring to the colour, reverse/right
mode 'I'  immediate: the colour is applied at once
other     200 ms circular wipe of the full ring to off, reverse/right; the
          colour bytes are ignored
```

A non-zero `brightness` sets the brightness. `brightness = 0` keeps the current
brightness: the brightness the display settles to once any running animation
finishes. This differs from `A`, where a stage brightness of `0` means dark.

`P` never receives a reply. A `P` whose payload is not exactly six bytes is
dropped. Use `A` when the sender needs a result; see Client guidance.

## Animation packet

Do not add a new packet type for every effect. Use one extensible animation
command whose kind identifies the effect:

```text
type: 'A'
payload:
  request_id      u16 BE
  animation_kind  u8
  target          u8
  direction_mode  u8
  initial_direction u8
  stage_duration_ms u32 BE
  stage_count     u8
  stages          stage_count × (red, green, blue, white, brightness)
```

Initial enums:

```text
animation_kind: 1 circular_wipe, 2 fade
target:         1 full_ring, 2 inner_ring, 3 outer_ring
direction_mode:  0 none, 1 fixed, 2 alternating
direction:       0 none, 1 forward/left, 2 reverse/right
```

An `A` is rejected with `R rejected` (detail `12`) when any of these holds:

- the payload is shorter than 11 bytes, or not exactly
  `11 + 5 × stage_count` bytes;
- `stage_count` is `0` or greater than `16`;
- `animation_kind`, `target`, `direction_mode`, or `initial_direction` is
  outside the values above.

A rejected `A` changes nothing on the display. As for every rejection, the
reply is only sent when a `request_id` can be read: an `A` with a payload
shorter than two bytes gets no reply.

An animation is an ordered sequence of stages. Each stage gives its target
RGBW color and brightness. The first stage begins from the state currently
displayed by the device; there is no starting state in the command. The next
stage begins automatically after the previous stage completes.

`stage_duration_ms` is the one timing value for every stage in the animation.
For a circular wipe, firmware derives its per-pixel timing from the duration
and the number of pixels in the selected target:

```text
step_delay_ms = stage_duration_ms / pixel_count
```

This keeps the requested visual duration consistent for rings with differing
pixel counts. A 40-pixel wipe previously performed with a 60 ms per-pixel
delay is represented by `stage_duration_ms = 2400`.

`circular_wipe` changes pixels one at a time in the selected circular order.
With `fixed` direction mode, every stage uses `initial_direction`. With
`alternating` mode, it flips direction after each stage: left, right, left,
right, and so on. Direction mode `none` behaves as `fixed`, and direction
`none` walks forward/left. A zero RGBW state is an off-wipe. A wipe sets the
stage's brightness for the whole strip at the start of the stage, then changes
the pixel colours one at a time.

Forward/left walks the full ring as pixels `0` to `15`, then `39` down to `16`;
the inner ring as `0` to `15`; and the outer ring as `39` down to `16`.
Reverse/right is the opposite order.

`fade` changes all selected pixels together from their current displayed state
to each stage's target state over `stage_duration_ms`, interpolating the
colour and the brightness linearly. Direction fields are ignored for a fade. A
zero RGBW/brightness state is fade-out.

`stage_duration_ms = 0` applies each stage immediately, when the `A` is
received: the sender gets `R accepted` followed at once by `R completed`.

All durations are unsigned integer milliseconds; no floating-point values are
used on the wire. Stage durations above `2^31 - 1` ms (about 24.8 days) are not
supported.

## Stop-animation packet

```text
type: 'X'
payload:
  request_id u16 BE
  set_off    u8
```

Stops the active animation. `set_off = 0` freezes the display at the pixels
currently shown; any non-zero `set_off` additionally sets an immediate
RGBW-zero state. Neither changes the brightness. `X` always replies with `R`:
`accepted` for a well-formed payload, or `rejected` (detail `12`) when the
payload is not exactly three bytes but a `request_id` can be read. Stopping an
idle device is accepted.

## Acknowledgement packet

```text
type: 'R'
payload:
  request_id u16 BE
  status     u8
  detail     u8
```

```text
status: 0 accepted, 1 rejected, 2 unsupported, 3 completed, 4 cancelled
```

`detail` carries a `SwkitError` code:

```text
 0  none: always used with accepted, completed, and cancelled
 4  INVALID_ID: the packet type has no handler (with unsupported)
12  INVALID_PACKET: the payload does not match the packet type (with rejected)
```

Other `SwkitError` values are reserved for future use. `R` is sent to the
request's source address and port.

`unsupported` is the reply to a well-framed packet whose type has no handler.
The device takes the `request_id` from the first two payload bytes, and sends
nothing when the payload is shorter than two bytes. So that clients can match
this reply, every request type, including future ones, starts its payload with
`request_id u16 BE`.

An accepted `A` later receives exactly one terminal `R`, sent to the address
and port that started it:

- `completed` when its last stage finishes;
- `cancelled` when anything replaces it first: `P`, another `A`, `X`, or a
  local action on the device (see Command lifecycle).

A terminal `R` is sent once and never retried; see Client guidance.

The device never replies to `R` or `S` frames, not even with `unsupported`.
Otherwise two devices could bounce responses back and forth indefinitely.

## Status request and response packets

```text
type: 'G'
payload:
  request_id u16 BE
```

`G` asks a device for its identity, capabilities, and current state. It may be
sent unicast, or broadcast for discovery (see below). The device replies with
`S` to the request's source address and port. `G` changes nothing, so it is not
subject to request-ID replay: every `G` gets an `S` built from the state at the
time it is received. A `G` whose payload is not exactly two bytes gets
`R rejected` (detail `12`) when a `request_id` can be read.

```text
type: 'S'
payload (23 bytes):
  request_id        u16 BE   copied from the G
  protocol_version  u8       1
  device_id         8 bytes  unique device identifier, see Discovery
  pixel_count       u16 BE   40
  inner_ring_count  u8       16
  outer_ring_count  u8       24
  max_stages        u8       largest stage_count accepted in A (16)
  animation_kinds   u8       bit (kind - 1) set for each supported
                             animation_kind: 0x03 = circular_wipe | fade
  flags             u8       bit 0: an animation is running
                             bit 1: pixels are on once it settles
                             bit 2: mixed: pixels settle to different colours
  brightness        u8       brightness once the running animation settles
  red               u8       colour once the running animation settles
  green             u8
  blue              u8
  white             u8
```

`S` describes the settled state: where the display is heading once the running
animation finishes, or the current state when no animation runs. A client
reading `S` mid-fade sees the end of the fade, not an intermediate frame.

- "Pixels are on" means at least one pixel has a non-zero colour and the
  brightness is non-zero.
- The colour is the single colour every pixel settles to. When pixels settle to
  different colours, for example after an animation that targets one ring or
  an `X` in the middle of a wipe, the mixed flag is set and the colour fields
  are zero.

Clients must ignore bytes after the fields they know: later protocol revisions
may append fields to `S` without changing `protocol_version`.

## Command lifecycle

- Starting a `P` or `A` command cancels any active animation and replaces the
  desired display state. `X` cancels it too.
- Local actions on the device also replace the display state and cancel a
  running `A`:
  - HTTP `GET /set/on`: a 2400 ms forward/left wipe to white 255 at the current
    brightness, or at brightness 60 when that is 0;
  - HTTP `GET /set/off`: a 2400 ms reverse/right wipe to off;
  - HTTP `GET /set/toggle`: `/set/off` when pixels are on, otherwise `/set/on`;
  - an over-the-air update sets every pixel off when it starts, and white 255
    when it ends, just before the device reboots. A running `A` may not
    receive its `cancelled` before the reboot.
- After boot, the display settles at white 255, brightness 60, following a
  short start-up animation.
- A replacement command starts from the pixels currently displayed at the time
  it is received, even when it interrupts an unfinished stage.
- Each frame sees the display as of its receipt, even when the device was busy
  with other work when the running animation's end time passed.
- Effects must be non-blocking: firmware stores active animation state and
  advances it from `loop()` based on `millis()`. It must continue serving UDP,
  HTTP, and OTA while an effect runs.
- `A` and `X` receive an immediate `R`: accepted, rejected, or unsupported. An
  accepted `A` later receives its terminal `R`, completed or cancelled.
- The device keeps the results of recently handled `A` and `X` requests. A
  retry with the same ID replays its latest result (including a later
  `completed` or `cancelled`) rather than handling the command again.
  - The device remembers the 8 most recent requests, shared by all senders;
    the oldest is forgotten first.
  - A remembered result expires 5 seconds after it was last updated.
  - After it expires or is forgotten, a repeated ID is handled as a new
    command: a repeated `A` would start its animation again.
- Request IDs are scoped to the sender's IP address and UDP port. Retries must
  come from the same socket; two clients may use the same IDs independently.

## Packet types

```text
P  immediate RGBW display state
A  start an animation
X  stop the active animation; optionally set an immediate off state
R  command acknowledgement, error, or completion result
G  request device identity, capabilities, and current state
S  device identity, capabilities, and state response
```

## Client guidance

This section is advice for controller implementations, not device behaviour.

- **Socket.** Send from one UDP socket bound to any local port, and read
  replies on it. The device answers from port `8888` to the request's source
  address and port.
- **Request IDs.** Keep a 16-bit counter per socket, increment it for every new
  request, and let it wrap. Never reuse an ID for a different command within 5
  seconds.
- **Retries.** When no `R` or `S` arrives, resend the identical frame with the
  same request ID, for example after 250 ms, then 500 ms, then 1 s. Finish
  retrying well within the 5-second replay window, so that a retried `A` replays
  its result instead of starting again.
- **Offline devices.** While rejoining Wi-Fi, a device can stop answering for
  up to about 15 seconds. When retries fail, treat the device as offline and
  rediscover it.
- **Setting a colour with confirmation.** Prefer `A` with `animation_kind = 2`
  (fade), `target = 1`, `stage_duration_ms = 0`, and one stage. It applies
  immediately like `P` mode `I`, but gets `R accepted` and `R completed`.
  Reserve `P` for fire-and-forget updates.
- **Completion.** A terminal `R` can be lost and is not retried. If none arrives
  shortly after the animation's expected end (`stage_count ×
  stage_duration_ms`), send `G`: flag bit 0 is clear once the animation has
  finished.
- **Out-of-band changes.** HTTP endpoints, OTA updates, and other clients
  change the display without telling the controller, and the device sends no
  notifications. A controller that mirrors device state should poll `G`, for
  example every few seconds, and read the settled colour, brightness, and
  flags from `S`.
- **Mixed state.** When flag bit 2 is set, the device shows more than one
  colour. A single `RgbwOutput` should report its own last requested colour or
  treat the state as unknown, not the zero colour fields.

## Security

The protocol has no authentication and no encryption. Anyone who can send UDP
packets to the device can change what it displays. Keep devices on a trusted
network segment.

## Discovery

### Device identifier and hostname

Each device is identified by the 8-byte unique ID of its RP2040 board (the
flash chip's unique ID). It is stable across reboots, firmware updates, and IP
address changes, so clients should key devices by it rather than by address or
name. `S` carries the raw 8 bytes; text forms use 16 lowercase hex digits.

The device's mDNS hostname is `rpxl-<id>`, for example
`rpxl-e6614103e7452d2f.local`. It is derived only from the ID, so two devices
never clash on the network.

### mDNS / DNS-SD

Devices advertise one DNS-SD service, whose instance name is the hostname:

```text
rpxl-<id>._rpixels._udp.local   SRV -> rpxl-<id>.local, port 8888
```

The service carries no TXT keys. Its TXT record is empty, because the
arduino-pico mDNS responder (SimpleMDNS) cannot attach TXT items to custom
services. `S` is the authoritative source of protocol version, device ID, and
capabilities. If TXT keys are added later, they will only duplicate `S`, and
clients must not require them.

A client discovers devices by browsing `_rpixels._udp.local` and resolving
each instance to an address and port. It then sends `G`, and keys the device by
the `device_id` in `S`, skipping devices whose `protocol_version` it does not
support. A device that loses power sends no goodbye, so its record lingers
until its TTL expires; clients should also treat an unanswered request as a
sign that the device is offline.

The same hostname carries the `_arduino._tcp` service used for over-the-air
firmware updates.

### Broadcast fallback

Where mDNS is unavailable, a client can broadcast `G` to UDP port `8888` on the
local subnet (for example `192.168.1.255`, or `255.255.255.255`). Every device
answers with `S`, unicast to the client's source address and port. The client
collects replies for a short window, around one second, and removes duplicates
by `device_id`. As with any UDP request, the client should repeat the broadcast
a few times, because single datagrams can be lost.

Both mechanisms are limited to one network segment. Reaching devices on another
subnet or VLAN needs an mDNS reflector or a broadcast relay.
