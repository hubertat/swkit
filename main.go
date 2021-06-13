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
)

var (
	config = flag.String("config", "config.json", "path of the configuration file")
)

func main() {
	log.Println("swkit started")
	flag.Parse()
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
	log.Println("starting HomeKit service")
	info := accessory.Info{
		Name:         "swkit",
		Manufacturer: "github.com/hubertat",
		ID:           1,
	}
	bridge := accessory.NewBridge(info)
	config := hc.Config{
		Pin:         "12002100",
		SetupId:     "SWKT",
		StoragePath: "hk",
	}
	t, err := hc.NewIPTransport(config, bridge.Accessory, swkit.GetHkAccessories()...)
	if err != nil {
		log.Print(err)
		return
	}

	go swkit.StartTicker(75 * time.Millisecond)

	hc.OnTermination(func() {
		<-t.Stop()
	})

	t.Start()

}
