package main

import "github.com/racerxdl/go-mcp23017"

type McpDevice struct {
	device *mcp23017.Device

	Inputs  []McpIn
	Outputs []McpOut
}

func NewMcpDevice(Bus, DevNum uint8) (mcp *McpDevice, err error) {
	d, err := mcp23017.Open(Bus, DevNum)
	if err != nil {
		return
	}

	mcp = &McpDevice{}
	mcp.device = d
	return
}

func (mcp *McpDevice) Setup(inputs []uint8, outputs []uint8) (err error) {
	for _, inputPin := range inputs {
		err = mcp.device.PinMode(inputPin, mcp23017.INPUT)
		if err != nil {
			return
		}
		err = mcp.device.SetPullUp(inputPin, true)
		if err != nil {
			return
		}
		mcp.Inputs = append(mcp.Inputs, McpIn{Pin: inputPin, Mcp: mcp})
	}

	for _, outputPin := range outputs {
		err = mcp.device.PinMode(outputPin, mcp23017.OUTPUT)
		mcp.Outputs = append(mcp.Outputs, McpOut{Pin: outputPin, Mcp: mcp})
	}

	return
}

type McpIn struct {
	Pin uint8
	Mcp *McpDevice
}

func (mi *McpIn) Read() (state bool, err error) {
	rawState, err := mi.Mcp.device.DigitalRead(mi.Pin)
	if err != nil {
		return
	}

	state = bool(rawState)
	return
}

type McpOut struct {
	Pin uint8
	Mcp *McpDevice
}
