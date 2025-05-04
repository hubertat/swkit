package arduino

import (
	"errors"
	"fmt"
	"hash/crc32"
)

// Packet overhead with CRC32 checksum, packet length and tag
const PacketOverhead uint = 4 + 2 + 1

type PacketType byte

const (
	PACKET_TYPE_NONE                     PacketType = 0x00
	PACKET_TYPE_ARDUINOPRO_CONFIG        PacketType = 'C'
	PACKET_TYPE_ARDUINOPRO_CONFIG_NOTICE PacketType = 'c'
	PACKET_TYPE_ARDUINOPRO_STATUS        PacketType = 'S'
	PACKET_TYPE_ARDUINOPRO_COMMAND       PacketType = 'D'
	PACKET_TYPE_ARDUINOPRO_RESPONSE      PacketType = 'R'
	PACKET_TYPE_ARDUINOPRO_NOTREADY      PacketType = 'N'
	PACKET_TYPE_RPIXEL_SET               PacketType = 'P'
)

func (pt PacketType) String() string {
	switch pt {
	case PACKET_TYPE_NONE:
		return "PACKET_TYPE_NONE"
	case PACKET_TYPE_ARDUINOPRO_CONFIG:
		return "PACKET_TYPE_ARDUINOPRO_CONFIG"
	case PACKET_TYPE_ARDUINOPRO_STATUS:
		return "PACKET_TYPE_ARDUINOPRO_STATUS"
	case PACKET_TYPE_ARDUINOPRO_COMMAND:
		return "PACKET_TYPE_ARDUINOPRO_COMMAND"
	case PACKET_TYPE_ARDUINOPRO_RESPONSE:
		return "PACKET_TYPE_ARDUINOPRO_RESPONSE"
	case PACKET_TYPE_ARDUINOPRO_NOTREADY:
		return "PACKET_TYPE_ARDUINOPRO_NOTREADY"
	case PACKET_TYPE_RPIXEL_SET:
		return "PACKET_TYPE_RPIXEL_SET"
	default:
		return "PACKET_TYPE_UNKNOWN(" + string(pt) + ")"
	}
}

// Packet represents a data packet (received or to be send over UDP)
// data packet construction:
// * packet size (2 bytes)
// * packet tag (1 byte)
// * actual data/command (variable size)
// * crc32 checksum (4 bytes)
type Packet struct {
	tag  PacketType
	data []byte
}

func (pck Packet) PacketType() PacketType {
	return pck.tag
}

func (pck Packet) Data() []byte {
	return pck.data
}

func (pck Packet) Len() int {
	return len(pck.data) + int(PacketOverhead)
}

func (pck Packet) GetRawData() []byte {
	rawBytes := []byte{}
	packetLen := uint16(pck.Len())
	// Convert packet length to byte
	rawBytes = append(rawBytes, byte(packetLen>>8)) // Most significant byte
	rawBytes = append(rawBytes, byte(packetLen))    // Least significant byte
	rawBytes = append(rawBytes, byte(pck.tag))
	rawBytes = append(rawBytes, pck.data...)

	crcChecksum := crc32.ChecksumIEEE(rawBytes)

	rawBytes = append(rawBytes, byte(crcChecksum>>24)) // Most significant byte
	rawBytes = append(rawBytes, byte(crcChecksum>>16))
	rawBytes = append(rawBytes, byte(crcChecksum>>8))
	rawBytes = append(rawBytes, byte(crcChecksum)) // Least significant byte

	return rawBytes
}

func ParsePacket(rawData []byte) (Packet, error) {
	if len(rawData) < 4 {
		return Packet{}, errors.New("packet size too small")
	}

	var packetLength uint16
	packetLength = uint16(rawData[0])<<8 | uint16(rawData[1])

	if len(rawData) != int(packetLength) {
		return Packet{}, fmt.Errorf("packet length mismatch, received packet length: %d, actual packet length: %d", packetLength, len(rawData))
	}

	var packetTag byte
	packetTag = rawData[2]

	crcBytes := rawData[len(rawData)-4:]
	crcChecksum := uint32(crcBytes[0])<<24 | uint32(crcBytes[1])<<16 | uint32(crcBytes[2])<<8 | uint32(crcBytes[3])

	if crcChecksum != crc32.ChecksumIEEE(rawData[:len(rawData)-4]) {
		return Packet{}, errors.New("crc checksum not matched")
	}

	return Packet{
		tag:  PacketType(packetTag),
		data: rawData[3 : len(rawData)-4],
	}, nil
}

func NewPacket(packetType PacketType, data []byte) Packet {
	return Packet{tag: packetType, data: data}
}
