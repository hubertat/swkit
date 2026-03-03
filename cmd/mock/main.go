package main

import (
	"context"
	"os"
	"time"

	"github.com/charmbracelet/log"

	"github.com/hubertat/swkit"
	"github.com/hubertat/swkit/drivers"
	"github.com/hubertat/swkit/logging"
)

var (
	Version string
	Build   string
)

func main() {
	var err error

	logging.Init(logging.Config{
		Writer: os.Stderr,
		Level:  log.DebugLevel,
	})
	loggerFactory := logging.NewFactory(nil)
	logger := loggerFactory.Logger(logging.PrefixMock)

	logger.Info("swkit started")
	logger.Info("mock instance for testing purposes, should work on MacOS")

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

	logger.Info("will setup swkit...")
	err = sk.Setup(ctx, logger)
	if err != nil {
		logger.Fatal("failed to setup swkit", "err", err)
	}
	defer sk.Close()

	sk.FakeDriver.MonitorStateChanges(os.Stdout)

	sk.HkDirectory = "./mock_homekit"

	logger.Info("starting mock with HomeKit service")
	go sk.StartTicker(ctx, 250*time.Millisecond, 10)

	logger.Fatal(sk.StartHomeKit(ctx, "mock: "+Version))
}
