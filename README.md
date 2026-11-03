# swkit
swkit - HomeKit enabled switch/input/roller shutter switch, monitor

## features
* HomeKit enabled (github.com)
* switch/button input
* light output
* input - light output relation
* outlet output
* window shutter (roller shutter) with two maintained outputs
* thermostat output
* influx sensor (temperature for thermostat)

## Presto wall panel

`presto/` holds a touch-screen panel app for the Pimoroni Presto that controls
swkit over its control API (lights, outlets, window shutters, scenes, button presses). It's a
separate MicroPython program, see [presto/README.md](presto/README.md).

## window shutters

A window shutter is a roller shutter driven by two digital outputs: one runs
the motor up (open), the other down (close). The selected output stays on for
the whole run; stopping switches both off. Both outputs are never on at once,
and reversing direction waits for the reversal delay first.

```json
"WindowShutters": [
  {
    "Name": "Bedroom shutter",
    "OpenDigitalOutName": "wago|d_out|4",
    "CloseDigitalOutName": "wago|d_out|5",
    "OpenDurationSeconds": 20,
    "CloseDurationSeconds": 18,
    "ReverseDelayMilliseconds": 0,
    "DisableHomekit": false
  }
]
```

* `OpenDurationSeconds` (1-600) is the full opening time. `CloseDurationSeconds`
  is the full closing time; 0 uses the opening time, otherwise 1-600.
* `ReverseDelayMilliseconds`: 0 uses 500 ms; otherwise 1-5000 ms. A true
  zero-delay reversal is intentionally not configurable. Check the value
  against your motor or controller.
* Neither output may be used by any other device.

Position is percent open (0 closed, 100 open) and is **estimated from travel
time, never measured**:

* After a start, restart or any config save (which reloads the config), both
  outputs are off and the position is **unknown**. Nothing moves by itself.
  Saving a config while a shutter moves stops it.
* A full Open or Close sets the position. Runs to 0 or 100 keep the motor on
  for an extra 10% of the travel time so it reaches its end limit, so **the
  motor needs working end limits**. Open at an already open shutter runs just
  that extra time again to recalibrate.
* Positions between 0 and 100 need a known position; until then they are
  rejected (endpoints always work).
* Toggle cycles start, stop, reverse, stop. The first toggle with no known
  direction opens.
* An unknown position is not a fault; HomeKit `StatusFault` and the `is_faulty`
  flag only report output (driver) errors.
* Changes made at the relays by something else are noticed (through polling,
  and output callbacks where the driver supports them) and followed, but the
  position becomes unknown when the moment of the change cannot be known.

Control it from HomeKit (window covering), buttons and scenes (`open`,
`close`, `stop`, `toggle`, `position:<pct>` actions, see below), the control
web page, the TUI, the AI agent and the Presto panel.

Button and scene actions for shutters:

```
single_press:open:Bedroom shutter
double_press:close:Bedroom shutter
long_press:toggle:Bedroom shutter
position:40:Bedroom shutter
after:10s:stop:Bedroom shutter
```

`on`/`off` are not accepted for shutters, and `open`/`close`/`stop`/`position`
only for shutters. Scene steps only start the motor and never wait for the
run to finish; activating another scene state does not stop a moving shutter,
so give the "Off" state an explicit `stop` (the editors add it for you).

Older control clients (the web page or Presto app from before shutter support)
show shutters as a plain on/off tile: their taps send open/close or toggle,
but they have no position or Stop controls and may misreport motion. Update
the clients for full shutter control.

## usage

### mcp23017

#### config

Mcp23017 driver struct have four exposed fields and it looks in `config.json` as follows:
```
Mcp23017 struct {
	BusNo         uint8
	DevNo         uint8
	InvertInputs  bool
	InvertOutputs bool
}
```

It is *important* to note, that `DevNo` max value is 8, and it is not actual device address.
*github.com/racerxdl/go-mcp23017* library is calculating device address like this:
```
_Address  = 0x20
(...)
dev, err := i2c.NewI2C(_Address+devNum, int(bus))
```
no idea why, (mentioned line here)[https://github.com/racerxdl/go-mcp23017/blob/c8f9b9777b0e917fd5ad51254da02d083311e452/mcp23017.go#L140]

So basically, if your i2c detect `sudo i2cdetect -y 1` lists something like this:
```
     0  1  2  3  4  5  6  7  8  9  a  b  c  d  e  f
00:          -- -- -- -- -- -- -- -- -- -- -- -- -- 
10: -- -- -- -- -- -- -- -- -- -- -- -- -- -- -- -- 
20: 20 21 -- -- -- -- -- -- -- -- -- -- -- -- -- -- 
30: -- -- -- -- -- -- -- -- -- -- -- -- -- -- -- -- 
40: -- -- -- -- -- -- -- -- -- -- -- -- -- -- -- -- 
50: -- -- -- -- -- -- -- -- -- -- -- -- -- -- -- -- 
60: -- -- -- -- -- -- -- -- -- -- -- -- -- -- -- -- 
70: -- -- -- -- -- -- -- --   
```
Your first device (0x20) will be DevNo = 0 and second device (0x21) is DevNo = 1.

## todo

* mcp23017 support (input/output)
* Influx logging - outputs state/state change
* 1wire sensor support (thermostat)
* remote iodriver - outputs to other swkit instance

## readonly raspberry OS

https://github.com/hubertat/readonlyrpi

