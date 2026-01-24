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
	"github.com/hubertat/swkit/logging"
)

const defaultSyncInterval = "330ms"
const defaultSensorsSyncInterval = "10s"
const defaultForceSyncEveryCycle = 1000

var (
	Version string
	Build   string

	config              = flag.String("config", "config.json", "path of the configuration file")
	flagInstall         = flag.Bool("install", false, "Install service in os")
	syncInterval        = flag.String("sync", defaultSyncInterval, "sync interval (time.Duration)")
	forceSyncEveryCycle = flag.Int("force-sync-every", defaultForceSyncEveryCycle, "force sync every n cycles")
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
	flag.Parse()
	if *debug {
		log.SetLevel(log.DebugLevel)
	}
	logger := logging.NewLogger(logging.PrefixMain)
	logger.Info("swkit started", "version", Version)

	if *debug {
		logger.Warn("debug mode enabled")
	}

	if *flagInstall {
		err := swkService.InstallService()
		if err != nil {
			panic(err)
		} else {
			logger.Info("service installed, will exit")
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
			logger.Fatal("failed reading config file", "error", err)
		}

		err = json.Unmarshal(cBuff, sk)
		if err != nil {
			logger.Fatal("failed unmarshalling json config", "error", err)
		}
	} else {
		logger.Fatal("can't find/open config file, will terminate.", "file", *config, "error", err)
	}
	logger.Info("will setup SwKit...")
	err = sk.Setup(ctx, logger)
	defer sk.Close()
	if err != nil {
		logger.Fatal("failed to setup swkit", "err", err)
	}
	logger.Debug("swkit done OK")

	sk.PrintIoStatus(os.Stdout)

	if len(sk.HkPin) == 8 {
		logger.Info("HomeKit configured, starting", "pin", sk.HkPin)

		logger.Info("starting sync ticker", "interval", syncDuration)
		go sk.StartTicker(syncDuration, *forceSyncEveryCycle)

		if sk.StartHomeKit(context.Background(), Version) == nil {
			logger.Info("homekit terminated ok")
		} else {
			logger.Error("homekit terminated with error")
		}

	} else {
		logger.Info("starting sync ticker", "interval", syncDuration)
		sk.StartTicker(syncDuration, *forceSyncEveryCycle)
	}

}
