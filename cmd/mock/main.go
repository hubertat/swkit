package main

import (
	"context"
	"os"
	"time"

	"github.com/charmbracelet/log"

	"github.com/hubertat/swkit"
	"github.com/hubertat/swkit/drivers"
)

var (
	Version string
	Build   string
)

func main() {
	var err error

	log.SetLevel(log.DebugLevel)

	log.Info("swkit started")
	log.Info("mock instance for testing purposes, should work on MacOS")

	ctx := context.Background()

	sk := &swkit.SwKit{
		HkPin: "88008800",
		Lights: []swkit.LightConfig{
			{Name: "mock light", DigitalOutName: "mock_driver|d_out|1"},
		},
		Outlets: []swkit.OutletConfig{
			{Name: "fake outlet", DigitalOutName: "mock_driver|d_out|2"},
		},
		FakeDriver: &drivers.MockIoDriver{},
	}

	logger := log.NewWithOptions(os.Stderr, log.Options{Prefix: "mock"})

	log.Info("will setup swkit...")
	err = sk.Setup(ctx, logger)
	if err != nil {
		log.Fatal("failed to setup swkit", "err", err)
	}
	defer sk.Close()

	sk.FakeDriver.MonitorStateChanges(os.Stdout)

	sk.HkDirectory = "./mock_homekit"

	log.Info("starting mock with HomeKit service")
	go sk.StartTicker(ctx, 250*time.Millisecond, 10)

	log.Fatal(sk.StartHomeKit(ctx, "mock: "+Version))
}
