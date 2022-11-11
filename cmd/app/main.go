package main

import (
	"context"
	"encoding/json"
	"flag"
	"io"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/brutella/hap"
	"github.com/brutella/hap/accessory"
	"github.com/hubertat/servicemaker"

	"github.com/hubertat/swkit"
)

const defaultSyncInterval = "250ms"
const defaultSensorsSyncInterval = "2m"
const defaultHomeKitDirectory = "./homekit"

var (
	config              = flag.String("config", "config.json", "path of the configuration file")
	flagInstall         = flag.Bool("install", false, "Install service in os")
	syncInterval        = flag.String("sync", defaultSyncInterval, "sync interval (time.Duration)")
	sensorsSyncInterval = flag.String("sensors-sync", defaultSensorsSyncInterval, "sensors sync interval (time.Duration)")

	swkService = servicemaker.ServiceMaker{
		User:               "swkit",
		UserGroups:         []string{"gpio"},
		ServicePath:        "/etc/systemd/system/swkit.service",
		ServiceDescription: "SwKit service: HomeKit enabled switch/input/roller shutter controller. github.com/hubertat/swkit",
		ExecDir:            "/srv/swkit",
		ExecName:           "swkit",
	}
)

func main() {
	log.Println("swkit started")
	flag.Parse()

	if *flagInstall {
		err := swkService.InstallService()
		if err != nil {
			panic(err)
		} else {
			log.Println("service installed!")
			return
		}
	}

	syncDuration, err := time.ParseDuration(*syncInterval)
	if err != nil {
		panic(err)
	}
	sensorsSyncDuration, err := time.ParseDuration(*sensorsSyncInterval)
	if err != nil {
		panic(err)
	}

	sk := &swkit.SwKit{}
	configFile, err := os.Open(*config)
	if err == nil {
		cBuff, err := io.ReadAll(configFile)
		if err != nil {
			log.Fatalf("failed reading config file: %v\n", err)
		}

		err = json.Unmarshal(cBuff, sk)
		if err != nil {
			log.Fatalf("failed unmarshalling json config: %v", err)
		}
	} else {
		log.Fatalf("can't find/open config file (%s), will terminate. Reason: \n%v\n", *config, err)
	}
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

	sk.PrintIoStatus(os.Stdout)

	if len(sk.HkPin) == 8 {
		log.Println("starting HomeKit service")
		info := accessory.Info{
			Name:         "swkit",
			Manufacturer: "github.com/hubertat",
		}
		bridge := accessory.NewBridge(info)

		var store hap.Store
		if len(sk.HkDirectory) > 1 {
			store = hap.NewFsStore(sk.HkDirectory)
		} else {
			store = hap.NewFsStore(defaultHomeKitDirectory)
		}

		hkServer, err := hap.NewServer(store, bridge.A, sk.GetHkAccessories()...)
		if err != nil {
			log.Print(err)
			return
		}
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
