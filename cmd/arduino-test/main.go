package main

import (
	"flag"
	"log"
	"net/netip"

	"github.com/hubertat/swkit/drivers/arduino"
)

func main() {
	rpixel := flag.Bool("rpixel", false, "test arduino remote pixel")
	apro := flag.Bool("apro", false, "test arduino pro")
	addr := flag.String("address", "10.100.10.150", "enter remote device address")

	if *rpixel {

		// Rpixel test
		remoteAddr, err := netip.ParseAddr(*addr)
		if err != nil {
			log.Fatal(err)
		}

		log.Println("parsed remote address: ", remoteAddr.String())

		pixl := arduino.NewRPixel(remoteAddr)
		pixl.SetColorRGB(0xFF, 0xFF, 0xFF)

		uc := arduino.NewUdpComm()
		err = uc.AddPixel(pixl)
		if err != nil {
			log.Fatal(err)
		}

		err = uc.SetPixel(0)
		if err != nil {
			log.Fatal(err)
		}

		log.Println("test complete!")
	}

	if *apro {

		// ArduinoPro test:
		remoteAddr, err := netip.ParseAddr(*addr)
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

}
