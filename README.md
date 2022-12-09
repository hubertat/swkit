# swkit
swkit - HomeKit enabled switch/input/roller shutter switch, monitor

## features
* HomeKit enabled (github.com)
* switch/button input
* light output
* input - light output relation
* outlet output
* thermostat output
* influx sensor (temperature for thermostat)

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

