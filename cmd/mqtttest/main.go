/*
 * Copyright (c) 2024 Contributors to the Eclipse Foundation
 *
 *  All rights reserved. This program and the accompanying materials
 *  are made available under the terms of the Eclipse Public License v2.0
 *  and Eclipse Distribution License v1.0 which accompany this distribution.
 *
 * The Eclipse Public License is available at
 *    https://www.eclipse.org/legal/epl-2.0/
 *  and the Eclipse Distribution License is available at
 *    http://www.eclipse.org/org/documents/edl-v10.php.
 *
 *  SPDX-License-Identifier: EPL-2.0 OR BSD-3-Clause
 */

package main

import (
	"context"
	"time"

	"github.com/charmbracelet/log"
	"github.com/eclipse/paho.golang/paho"
	"github.com/hubertat/swkit/mqtt"
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

	log.SetLevel(log.DebugLevel)

	mc, err := mqtt.NewMqttClient(broker, clientID)
	if err != nil {
		log.Error("failed to create mqtt client", "error", err)
		return
	}

	tHan := &Handler{topic: "testTopic"}

	mqttHandlers := []mqtt.MqttHandler{
		tHan,
	}

	err = mc.Connect(context.Background(), mqttHandlers)
	if err != nil {
		log.Error("failed to connect to mqtt broker", "error", err)
		return
	}

	log.Info("mqtt client connected")

	p := []byte("hello from swk!")
	mc.Publish(tHan.topic, p)

	go func(mc *mqtt.MqttClient) {
		time.Sleep(12 * time.Second)

		pl := []byte("2nd msg")
		mc.Publish(tHan.topic, pl)
	}(mc)

	log.Info("sleeping for 2 minutes")
	time.Sleep(2 * time.Minute)

	<-mc.Done() // Wait for clean shutdown (cancelling the context triggered the shutdown)
}
