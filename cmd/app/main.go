package main

import (
	"context"
	"encoding/json"
	"flag"
	"io"
	"os"
	"time"

	"github.com/charmbracelet/log"
	"github.com/hubertat/servicemaker"
	"github.com/hubertat/swkit"
)

const defaultSyncInterval = "330ms"
const defaultSensorsSyncInterval = "10s"

var (
	Version string
	Build   string

	config              = flag.String("config", "config.json", "path of the configuration file")
	flagInstall         = flag.Bool("install", false, "Install service in os")
	syncInterval        = flag.String("sync", defaultSyncInterval, "sync interval (time.Duration)")
	sensorsSyncInterval = flag.String("sensors-sync", defaultSensorsSyncInterval, "sensors sync interval (time.Duration)")
	debug               = flag.Bool("debug", false, "debug mode")

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
	log.Info("swkit started", "version", Version)
	flag.Parse()

	if *debug {
		log.SetLevel(log.DebugLevel)
		log.Info("debug mode enabled")
	}

	if *flagInstall {
		err := swkService.InstallService()
		if err != nil {
			panic(err)
		} else {
			log.Info("service installed, will exit")
			return
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	syncDuration, err := time.ParseDuration(*syncInterval)
	if err != nil {
		panic(err)
	}

	sk := &swkit.SwKit{}
	configFile, err := os.Open(*config)
	if err == nil {
		cBuff, err := io.ReadAll(configFile)
		if err != nil {
			log.Fatal("failed reading config file", "error", err)
		}

		err = json.Unmarshal(cBuff, sk)
		if err != nil {
			log.Fatal("failed unmarshalling json config", "error", err)
		}
	} else {
		log.Fatal("can't find/open config file, will terminate.", "file", *config, "error", err)
	}
	log.Info("will init swkit drivers...")
	err = sk.InitDrivers(ctx)
	defer sk.Close()
	if err != nil {
		panic(err)
	}
	log.Info("will init swkit IOs...")
	err = sk.InitIos()
	if err != nil {
		panic(err)
	}

	log.Info("drivers OK!")
	log.Info("will try to MatchControllers...")
	err = sk.MatchControllers()
	if err != nil {
		log.Error("Matching Controllers returned error", "error", err)
	} else {
		log.Info("controllers matched OK")
	}

	sk.PrintIoStatus(os.Stdout)

	if len(sk.HkPin) == 8 {
		log.Info("HomeKit configured, starting", "pin", sk.HkPin)

		log.Info("starting sync ticker", "interval", syncDuration)
		go sk.StartTicker(syncDuration)

		if sk.StartHomeKit(context.Background(), Version) == nil {
			log.Info("homekit terminated ok")
		} else {
			log.Error("homekit terminated with error")
		}

	} else {
		log.Info("starting sync ticker", "interval", syncDuration)
		sk.StartTicker(syncDuration)
	}

}
