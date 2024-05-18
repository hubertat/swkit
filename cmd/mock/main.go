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
	log.Info("mock instance for testing puproses, should work on MacOs")

	syncDuration := 250 * time.Millisecond
	log.Info("syncDuration is ", "syncDuration", syncDuration)

	sk := &swkit.SwKit{}

	sk.HkPin = "88008800"

	sk.MqttBroker = "mqtt://10.100.10.55:1883"

	sk.Lights = append(sk.Lights, &swkit.Light{Name: "shelly light", DriverName: "shelly", OutPin: 1})
	sk.Outlets = append(sk.Outlets, &swkit.Outlet{Name: "fake outlet", DriverName: "mock_driver", OutPin: 2})

	sk.FakeDriver = &drivers.MockIoDriver{}

	sk.Shelly = &drivers.ShellyIO{
		Outputs: []drivers.ShellyOutput{
			{Pin: 1, Id: "shellypro4.0", SwitchNo: 1},
		},
	}
	ctx := context.Background()

	log.Info("will init swkit drivers...")
	err = sk.InitDrivers(ctx)
	defer sk.Close()
	if err != nil {
		log.Fatal("failed to init drivers", "err", err)
	}
	log.Info("will init swkit IOs...")
	err = sk.InitIos()
	if err != nil {
		log.Fatal("failed to init IOs", "err", err)
	}

	log.Info("drivers OK! Will try to MatchControllers:")
	err = sk.MatchControllers()
	if err != nil {
		log.Error("Matching Controllers returned error: %v\n we will proceed...", "err", err)
	} else {
		log.Info("MatchControllers OK!")
	}

	log.Info("match controllers done, will init mqtt")
	err = sk.InitMqtt()
	if err != nil {
		log.Error("InitMqtt returned error: %v\n we will proceed...", "err", err)
	}

	sk.FakeDriver.MonitorStateChanges(os.Stdout)

	sk.PrintIoStatus(os.Stdout)

	log.Info("starting mock with HomeKit service")

	go sk.StartTicker(syncDuration)

	sk.HkDirectory = "./mock_homekit"
	log.Fatal(sk.StartHomeKit(context.Background(), "mock: "+Version))

}
