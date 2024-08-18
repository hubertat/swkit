package mqtt

import "github.com/eclipse/paho.golang/paho"

type MqttHandler interface {
	MqttHandle(paho.PublishReceived) (bool, error)
	MqttSubscribeTopics() []string
}

type Publisher interface {
	Publish(topic string, payload []byte) error
	ClientId() string
	ConnectionReady() bool
}

// RpcMessageType represents the type of the rpc message
// it is int, 0 - request, 1 - response, 2 - notification
type RpcMessageType int

const (
	RpcRequestType RpcMessageType = iota
	RpcResponseType
	RpcNotificationType
)

type MqttRpcHandler interface {
	HandleRpcStatus(online bool, topic string) bool
	HandleRpcMessage(msg *RpcMessage, topic string) bool
	MqttTopicRoots() []string
}

type Messenger interface {
	SendRequest(topicRoot string, req RpcRequest) error
}
