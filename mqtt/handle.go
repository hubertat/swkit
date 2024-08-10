package mqtt

import "github.com/eclipse/paho.golang/paho"

type MqttHandler interface {
	MqttHandle(paho.PublishReceived) (bool, error)
	MqttSubscribeTopics() []string
}

type Publisher interface {
	Publish(topic string, payload []byte) error
	ClientId() string
}
