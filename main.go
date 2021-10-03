package main

import (
	"encoding/json"
	"flag"
	"io"
	"log"
	"os"
	"time"

	"github.com/brutella/hc"
	"github.com/brutella/hc/accessory"
	"github.com/hubertat/servicemaker"
)

var (
	config      = flag.String("config", "config.json", "path of the configuration file")
	flagInstall = flag.Bool("install", false, "Install service in os")
	swkService  = servicemaker.ServiceMaker{
		User:               "swkit",
		ServicePath:        "/etc/systemd/system/swkit.service",
		ServiceDescription: "SwKit service: HomeKit enabled switch/input/roller shutter controller using RPi GPIO.",
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

	swkit := &SwKit{}

	configFile, err := os.Open(*config)
	if err == nil {
		cBuff, err := io.ReadAll(configFile)
		if err != nil {
			log.Fatalf("failed reading config file: %v\n", err)
		}

		err = json.Unmarshal(cBuff, swkit)
		if err != nil {
			log.Fatalf("failed unmarshalling json config: %v", err)
		}
	} else {
		log.Fatalf("can't find/open config file (%s), running on defaults\n%v\n", *config, err)
	}
	if len(swkit.HkPin) == 8 && len(swkit.HkSetupId) == 4 {
		log.Println("starting HomeKit service")
		info := accessory.Info{
			Name:         "swkit",
			Manufacturer: "github.com/hubertat",
			ID:           1,
		}
		bridge := accessory.NewBridge(info)
		config := hc.Config{
			Pin:         swkit.HkPin,
			SetupId:     swkit.HkSetupId,
			StoragePath: "hk",
		}
		t, err := hc.NewIPTransport(config, bridge.Accessory, swkit.GetHkAccessories()...)
		if err != nil {
			log.Print(err)
			return
		}

		go swkit.StartTicker(750 * time.Millisecond)

		hc.OnTermination(func() {
			<-t.Stop()
		})

		t.Start()
	} else {
		log.Println("HomeKit not configured, wont start")
		swkit.StartTicker(750 * time.Millisecond)
	}

}
