package arduino

import (
	"errors"
	"fmt"
	"log"
	"net"
)

const packetSizeLimit uint = 512
const udpRemotePort uint = 8888

type remoteDevice struct {
	ArduinoPro *ArduinoPro
	Conn       *net.UDPConn
	ready      bool
}

type UdpComm struct {
	devices []remoteDevice
}

func NewUdpComm() *UdpComm {
	return &UdpComm{}
}

func (uc *UdpComm) AddDevice(arduinoPro *ArduinoPro) error {
	if arduinoPro == nil {
		return errors.New("got nil arduinoPro device")
	}

	if !arduinoPro.address.IsValid() {
		return errors.New("arduinoPro device address is invalid")
	}

	udpAddr, err := net.ResolveUDPAddr("udp", arduinoPro.address.String()+fmt.Sprintf(":%d", udpRemotePort))
	if err != nil {
		return errors.Join(errors.New("failed to resolve UDP address"), err)
	}

	conn, err := net.DialUDP("udp", nil, udpAddr)
	if err != nil {
		return errors.Join(errors.New("failed to dial UDP connection"), err)
	}

	log.Println("[D] connection OK, adding to slice and will listen on: ", conn.LocalAddr().String())

	uc.devices = append(uc.devices, remoteDevice{
		arduinoPro,
		conn,
		false,
	})

	return nil
}

func (uc *UdpComm) Close() error {
	var errs error
	for ix, dev := range uc.devices {
		err := dev.Conn.Close()
		if err != nil {
			errs = errors.Join(errs, errors.Join(fmt.Errorf("failed to close UDP connection for %s", dev.ArduinoPro.address.String()), err))
		} else {
			// TODO what to do here, keep device? keep connection?
			uc.devices[ix].Conn = nil
		}
	}

	uc.devices = nil

	return errs
}

func (uc *UdpComm) SendConfigs() error {
	var errs error
	for ix, dev := range uc.devices {
		configPacket := dev.ArduinoPro.ConfigBytes()
		bytesWritten, err := dev.Conn.Write(configPacket)
		var writeErr error
		if err != nil {
			writeErr = errors.Join(errs, errors.Join(fmt.Errorf("failed to send config to %s", dev.ArduinoPro.address.String()), err))
		}
		if bytesWritten != len(configPacket) {
			writeErr = errors.Join(errs, fmt.Errorf("failed to send full config to %s", dev.ArduinoPro.address.String()))
		}
		if writeErr != nil {
			errs = errors.Join(errs, writeErr)
		} else {
			log.Println("[D] device " + dev.ArduinoPro.address.String() + " config sent OK, waiting for response")
			dev.ready = true
			uc.devices[ix] = dev
		}
	}

	return errs
}

func (uc *UdpComm) ListenLoop() {
	for {
		for _, dev := range uc.devices {
			if dev.ready {
				buf := make([]byte, packetSizeLimit)
				n, addr, err := dev.Conn.ReadFromUDP(buf)
				if err != nil {
					log.Println("[E] failed to read from UDP connection for " + dev.ArduinoPro.address.String())
					continue
				}

				log.Println("[D] received", n, "bytes from", addr, ":", buf[:n])
				err = dev.ArduinoPro.ReadStatusPacket(buf)
				if err != nil {
					log.Println("[E] failed to read status packet for " + dev.ArduinoPro.address.String())
				} else {
					log.Println("[D] status packet read OK for " + dev.ArduinoPro.address.String())
				}
			}
		}
	}
}
