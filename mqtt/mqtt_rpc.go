package mqtt

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/eclipse/paho.golang/paho"
)

const publishRequestTopicSuffix = "/rpc"
const responseTopicSuffix = "/rpc"
const notificationTopicSuffix = "/events/rpc"
const onlineStatusTopicSuffix = "/online"

type JsonRpcMessenger struct {
	queue *requestQueue

	handler MqttRpcHandler

	topicRoots []string
	mqttClient *MqttClient
}

func NewJsonRpcMessenger(ctx context.Context, mqttClient *MqttClient, handler MqttRpcHandler) (*JsonRpcMessenger, error) {
	if mqttClient == nil {
		return nil, errors.New("mqtt client is nil")
	}
	if handler == nil {
		return nil, errors.New("handler is nil")
	}

	jrm := &JsonRpcMessenger{
		handler:    handler,
		mqttClient: mqttClient,
		queue:      newRequestQueue(),
		topicRoots: handler.MqttTopicRoots(),
	}

	err := mqttClient.Connect(ctx, []MqttHandler{jrm})
	if err != nil {
		return nil, errors.Join(err, errors.New("failed to connect to mqtt broker"))
	}

	return jrm, nil
}

// MqttSubscribeTopics() []string returns the topics to subscribe to
func (jrm *JsonRpcMessenger) MqttSubscribeTopics() []string {
	topics := []string{
		jrm.mqttClient.ClientId() + responseTopicSuffix,
	}
	for _, topic := range jrm.topicRoots {
		topics = append(topics, topic+publishRequestTopicSuffix)
		topics = append(topics, topic+notificationTopicSuffix)
		topics = append(topics, topic+onlineStatusTopicSuffix)
	}
	return topics
}

// MqttHandle(pub paho.PublishReceived) (bool, error) handles the incoming mqtt message
func (jrm *JsonRpcMessenger) MqttHandle(pub paho.PublishReceived) (bool, error) {
	if !jrm.checkForTopic(pub.Packet.Topic) {
		return false, nil
	}

	switch {
	case strings.EqualFold(pub.Packet.Topic, jrm.mqttClient.ClientId()+responseTopicSuffix):
		response, err := unmarshalRpcResponse(pub.Packet.Payload)
		if err != nil {
			return false, errors.Join(err, errors.New("failed to unmarshal rpc response"))
		}
		req := jrm.queue.getRequest(response.Id)
		if req == nil {
			return false, fmt.Errorf("received response for unknown request id %d", response.Id)
		}
		if !strings.EqualFold(response.Src, req.Dst) {
			return false, fmt.Errorf("response src %s does not match request dst %s", response.Src, req.Dst)
		}
		msg := &RpcMessage{
			MsgType: RpcResponseType,
			Src:     response.Src,
			Dst:     response.Dst,
			Method:  req.Method,
			result:  response.Result,
			err:     response.ResError,
		}
		return jrm.handler.HandleRpcMessage(msg, pub.Packet.Topic), nil

	case strings.HasSuffix(pub.Packet.Topic, onlineStatusTopicSuffix):
		onlineStatus, err := unmarshalOnlineStatus(pub.Packet.Payload)
		if err != nil {
			return false, errors.Join(err, errors.New("failed to unmarshal online status"))
		}

		return jrm.handler.HandleRpcStatus(onlineStatus, pub.Packet.Topic), nil

	case strings.HasSuffix(pub.Packet.Topic, notificationTopicSuffix):
		notification, err := unmarshalRpcNotification(pub.Packet.Payload)
		if err != nil {
			return false, errors.Join(err, errors.New("failed to unmarshal rpc notification"))
		}
		msg := &RpcMessage{
			MsgType: RpcNotificationType,
			Src:     notification.Src,
			Method:  notification.Method,
			params:  notification.Params,
		}
		return jrm.handler.HandleRpcMessage(msg, pub.Packet.Topic), nil

	case strings.HasSuffix(pub.Packet.Topic, publishRequestTopicSuffix):
		// probably no need to handle this, this is for pbulishing requests, so we can ignore it
		// maybe we can use it to debug in the future
		//
		return false, nil
	default:

		return false, nil
	}

}

// SendRequest(dst string, method string, params map[string]interface{}) error sends new rpc json request over mqtt
func (jrm *JsonRpcMessenger) SendRequest(topicRoot string, req RpcRequest) error {
	if len(req.Dst) == 0 || len(req.Method) == 0 {
		return errors.New("invalid request, empty dst and/or method")
	}

	topic := topicRoot + publishRequestTopicSuffix
	if !jrm.checkForTopic(topic) {
		return errors.New("request topic not found in the list of topics")
	}

	nextId := jrm.queue.getNextId()
	frame := requestFrame{
		Jsonrpc: "2.0",
		Src:     jrm.mqttClient.ClientId(),
		Id:      nextId,
		Method:  req.Method,
		Params:  req.Params,
	}

	payload, err := frame.bytes()
	if err != nil {
		return errors.Join(err, errors.New("failed to marshal request frame"))
	}
	err = jrm.mqttClient.Publish(topic, payload)
	if err != nil {
		return errors.Join(err, errors.New("failed to publish request frame"))
	}

	jrm.queue.add(frame, req.Dst)

	return nil
}

// checkForTopic(topic string) bool checks if the topic is in the list of topics
func (jrm *JsonRpcMessenger) checkForTopic(topic string) bool {
	for _, t := range jrm.topicRoots {
		if topic == t+publishRequestTopicSuffix {
			return true
		}
		if topic == t+notificationTopicSuffix {
			return true
		}
		if topic == t+onlineStatusTopicSuffix {
			return true
		}
		if topic == jrm.mqttClient.ClientId()+responseTopicSuffix {
			return true
		}
	}
	return false
}
