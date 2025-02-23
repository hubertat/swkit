package main

import (
	"log"
	"net/netip"

	"github.com/hubertat/swkit/drivers/arduino"
)

func main() {

	remoteAddr, err := netip.ParseAddr("10.100.10.150")
	if err != nil {
		log.Fatal(err)
	}

	log.Println("parsed remote address: ", remoteAddr.String())

	log.Println("creating new arduino pro device")

	pins := []arduino.Pin{
		arduino.NewInputPin(2, true),
		arduino.NewOutputPin(3),
	}
	arduinoPro := arduino.NewArduinoPro(remoteAddr, pins)

	log.Println(arduinoPro.String())

	log.Println("creating new udp comm")
	uc := arduino.NewUdpComm()

	log.Println("adding device to udp comm")

	err = uc.AddDevice(arduinoPro)
	if err != nil {
		log.Fatal(err)
	}

	log.Println("starting listening loop")
	go uc.ListenLoop()

	log.Println("sending config(s)")
	err = uc.SendConfigs()
	if err != nil {
		log.Panic("sending configs failed: ", err)
	}

	log.Println("configs sent, looping forever")
	for {
	}
}
