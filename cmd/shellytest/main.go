package main

import (
	"context"
	"time"

	"github.com/charmbracelet/log"
	"github.com/eclipse/paho.golang/paho"
	"github.com/hubertat/swkit/drivers"
)

const clientID = "mq-swk-client3" // Change this to something random if using a public test server

type Handler struct {
	topic string
}

func (h *Handler) MqttSubscribeTopics() []string {
	return []string{h.topic}
}

func (h *Handler) MqttHandle(pub paho.PublishReceived) (bool, error) {
	log.Info("handling mqtt message", "topic", pub.Packet.Topic, "msg", string(pub.Packet.Payload))
	return true, nil
}

func main() {
	// broker := "mqtt://10.100.10.55:1883"
	broker := "mqtt://10.100.80.44:1883"

	ctx := context.Background()

	log.SetLevel(log.DebugLevel)

	sheDriver := &drivers.ShellyIO{
		MqttBroker:   broker,
		MqttClientId: clientID,
	}

	err := sheDriver.Setup(ctx, []string{
		"shelly|d_out|shellypro4pm-083af2be0f68:0",
		"shelly|d_out|shellypro4pm-083af2be0f68:1",
	})

	if err != nil {
		log.Error("failed to setup shelly driver", "error", err)
		return
	}
	log.Info("shelly setup OK")

	go func(d *drivers.ShellyIO) {
		for {
			time.Sleep(5 * time.Second)

			time.Sleep(10 * time.Second)
			log.Info(d.PrintStatus())
		}
	}(sheDriver)

	go func() {
		i0, e := sheDriver.GetDigitalOutput("shellypro4pm-083af2be0f68:0")
		i1, e2 := sheDriver.GetDigitalOutput("shellypro4pm-083af2be0f68:1")
		if e == nil && e2 == nil {
			time.Sleep(15 * time.Second)
			i1.Set(true)
			time.Sleep(15 * time.Second)
			i0.Set(true)
			time.Sleep(30 * time.Second)
			i0.Set(false)
			time.Sleep(10 * time.Second)
			i1.Set(false)
		} else {
			log.Warn("failed getting out", "err", e)
		}
	}()

	log.Info("sleeping for 3 minutes")
	time.Sleep(3 * time.Minute)

}
