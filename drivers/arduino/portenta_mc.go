package arduino

import (
	"errors"
	"net"
)

type IoMode uint8

const (
	IoModeUnset IoMode = iota
	IoModeInput
	IoModeOutput
)

type PortentaMachineControl struct {
	dInputs   [8]bool
	dOutputs  [8]bool
	dIOstates [12]bool
	dIOConfig [12]IoMode

	remoteAddr *net.UDPAddr

	conn    *net.UDPConn
	isReady bool
}

func NewPortentaMachineControl(remoteAddr string) (*PortentaMachineControl, error) {
	udpAddr, err := net.ResolveUDPAddr("udp", remoteAddr)
	if err != nil {
		return nil, errors.Join(errors.New("failed to create new Portenta Machine Control: "), err)
	}

	return &PortentaMachineControl{
		remoteAddr: udpAddr,
	}, nil
}
