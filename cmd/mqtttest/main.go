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
	"time"

	"github.com/charmbracelet/log"
	"github.com/eclipse/paho.golang/paho"
	"github.com/hubertat/swkit/mqtt"
)

const clientID = "mq-swk-client" // Change this to something random if using a public test server
const topic = "shellypro4.0/events/rpc"

type Handler struct {
	topic string
}

func (h *Handler) MqttSubscribeTopic() string {
	return h.topic
}

func (h *Handler) MqttHandle(pub *paho.Publish) {
	log.Info("received mqtt message from", "topic", pub.Topic)
}

func main() {
	broker := "mqtt://10.100.10.55:1883"

	log.SetLevel(log.DebugLevel)

	mc, err := mqtt.NewMqttClient(broker, clientID)
	if err != nil {
		log.Error("failed to create mqtt client", "error", err)
		return
	}

	mqttHandlers := []mqtt.MqttHandler{
		&Handler{topic: topic},
		&Handler{topic: "testTopic"},
	}

	err = mc.Connect(mqttHandlers)
	if err != nil {
		log.Error("failed to connect to mqtt broker", "error", err)
		return
	}

	log.Info("mqtt client connected")
	log.Info("sleeping for 10 hours")
	time.Sleep(10 * time.Hour)
}
