package main

const grentonioDriverName = "grenton"

//  ** set state payload example **
// server/homebridge
// {
// 	"Kind": "Light",
// 	"Clu":  "CLU_0d1cf087",
// 	"Id":   "DOU0302",
// 	"Cmd": "SET",
// 	"Light": {
// 	  "State": true
// 	}
//   }

// ** get state (multiple) payload example
// /multi/read/

// [{
// 	"Kind": "Light",
// 	"Clu":  "CLU_0d1cf087",
// 	"Id":   "DOU0302"
//   }]

// response example:
// [
//   {
//     "Id": "DOU0302",
//     "Kind": "Light",
//     "Light": {
//       "State": true
//     },
//     "Clu": "CLU_0d1cf087"
//   }
// ]

type GrentonOutput struct {
	Grenton *GrentonIO

	state bool
	clu   string
	id    uint8
}

func (gro *GrentonOutput) GetState() (bool, error) {
	return gro.state, nil
}

func (gro *GrentonOutput) Set(bool) error {
	return nil
}

type GrentonIO struct {
	GateAddress string

	outputs []GrentonOutput
}

func (gio *GrentonIO) Setup(inputs []uint8, outputs []uint8) error {
	return nil
}

func (gio *GrentonIO) Close() error {
	return nil
}

func (gio *GrentonIO) NameId() string {
	return grentonioDriverName
}

func (gio *GrentonIO) GetUniqueId(ioPin uint8) uint64 {
	return 0
}

func (gio *GrentonIO) IsReady() bool {
	return false
}

func (gio *GrentonIO) GetInput(pin uint8) (DigitalInput, error) {
	return nil, nil
}

func (gio *GrentonIO) GetOutput(pin uint8) (DigitalOutput, error) {
	return nil, nil
}

func (gio *GrentonIO) GetAllIo() (inputs []uint8, outputs []uint8) {
	return
}
