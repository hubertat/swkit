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

type rpixelDevice struct {
	RPixel *RPixel
	Conn   *net.UDPConn
}

type UdpComm struct {
	devices []remoteDevice
	rpixels []rpixelDevice
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

func (uc *UdpComm) AddPixel(rpi *RPixel) error {
	if rpi == nil {
		return errors.New("got nil arduinoPro device")
	}

	if !rpi.address.IsValid() {
		return errors.New("arduinoPro device address is invalid")
	}

	udpAddr, err := net.ResolveUDPAddr("udp", rpi.address.String()+fmt.Sprintf(":%d", udpRemotePort))
	if err != nil {
		return errors.Join(errors.New("failed to resolve UDP address"), err)
	}

	conn, err := net.DialUDP("udp", nil, udpAddr)
	if err != nil {
		return errors.Join(errors.New("failed to dial UDP connection"), err)
	}

	log.Println("[D] connection OK, adding to slice and will listen on: ", conn.LocalAddr().String())

	uc.rpixels = append(uc.rpixels, rpixelDevice{
		rpi,
		conn,
	})

	return nil
}

func (uc *UdpComm) GetPixel(id int) *RPixel {
	if id < 0 || id >= len(uc.rpixels) {
		return nil
	}

	return uc.rpixels[id].RPixel
}

func (uc *UdpComm) SetPixel(id int) error {
	rpi := uc.GetPixel(id)
	if rpi == nil {
		return errors.New("failed to get pixel")
	}

	packet := rpi.GetSettingPacket('0')
	bytesWritten, err := uc.rpixels[id].Conn.Write(packet.GetRawData())
	if err != nil {
		return errors.Join(errors.New("failed to send packet"), err)
	}
	if bytesWritten != packet.Len() {
		return errors.New("failed to send full packet")
	}

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
		configPacket := dev.ArduinoPro.ConfigPacket()
		bytesWritten, err := dev.Conn.Write(configPacket.GetRawData())
		var writeErr error
		if err != nil {
			writeErr = errors.Join(errs, errors.Join(fmt.Errorf("failed to send config to %s", dev.ArduinoPro.address.String()), err))
		}
		if bytesWritten != configPacket.Len() {
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
				packet, err := ParsePacket(buf[:n])
				if err != nil {
					log.Println("failed to parse packet: ", err)
				} else {
					err = dev.ArduinoPro.ReadStatusPacket(packet)
					if err != nil {
						log.Println("[E] failed to read status packet for "+dev.ArduinoPro.address.String(), err)
					} else {
						log.Println("[D] status packet read OK for " + dev.ArduinoPro.address.String())
					}
				}
			}
		}
	}
}
