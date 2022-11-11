package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/brutella/hap"
	"github.com/brutella/hap/accessory"

	"github.com/hubertat/swkit"
	"github.com/hubertat/swkit/drivers"
)

func main() {
	var err error

	log.Println("swkit started")
	log.Println("mock instance for testing puproses, should work on MacOs")

	syncDuration := 250 * time.Millisecond
	log.Println("syncDuration is ", syncDuration)
	sensorsSyncDuration := 2 * time.Minute
	log.Println("sensorSyncDuration is ", sensorsSyncDuration)

	sk := &swkit.SwKit{}

	sk.HkPin = "88008800"

	sk.Lights = append(sk.Lights, &swkit.Light{Name: "fake light", DriverName: "mock_driver", OutPin: 1})
	sk.Outlets = append(sk.Outlets, &swkit.Outlet{Name: "fake outlet", DriverName: "mock_driver", OutPin: 2})
	sk.FakeDriver = &drivers.MockIoDriver{}

	log.Println("will init swkit drivers...")
	err = sk.InitDrivers()
	defer sk.Close()
	if err != nil {
		panic(err)
	}
	log.Printf("drivers OK!\nwill try to MatchControllers:\n")
	err = sk.MatchControllers()
	if err != nil {
		log.Printf("Matching Controllers returned error: %v\n we will proceed...", err)
	} else {
		log.Println("MatchControllers OK!")
	}

	log.Println("initialize sensor drivers:")
	for _, sDriver := range sk.GetSensorDrivers() {
		log.Printf("\t%s", sDriver.Name())
		err = sDriver.Init()
		if err != nil {
			log.Println(err)
		}
	}

	log.Println("trying to match thermostats:")
	err = sk.MatchSensors()
	if err != nil {
		log.Println(err)
	} else {
		log.Printf("\tOK\n")
	}

	sk.FakeDriver.MonitorStateChanges(os.Stdout)

	sk.PrintIoStatus(os.Stdout)

	if len(sk.HkPin) == 8 {
		log.Println("starting HomeKit service")
		info := accessory.Info{
			Name:         "swkit (fake)",
			Manufacturer: "github.com/hubertat",
		}
		bridge := accessory.NewBridge(info)

		store := hap.NewFsStore("./mock_homekit")
		hkServer, err := hap.NewServer(store, bridge.A, sk.GetHkAccessories()...)
		if err != nil {
			log.Print(err)
			return
		}

		log.Println("setting pin for HomeKit: ", sk.HkPin)
		hkServer.Pin = sk.HkPin

		go sk.StartTicker(syncDuration, sensorsSyncDuration)

		c := make(chan os.Signal)
		signal.Notify(c, os.Interrupt)
		signal.Notify(c, syscall.SIGTERM)

		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			<-c
			// Stop delivering signals.
			signal.Stop(c)
			// Cancel the context to stop the server.
			cancel()
		}()

		log.Println("HomeKit server starting")
		log.Fatalln(hkServer.ListenAndServe(ctx))
	} else {
		log.Println("HomeKit not configured, wont start")
		sk.StartTicker(syncDuration, sensorsSyncDuration)
	}

}
