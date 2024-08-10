package shelly

import (
	"strings"

	"github.com/charmbracelet/log"
	"github.com/eclipse/paho.golang/paho"
	"github.com/hubertat/swkit/mqtt"
)

const publishRequestTopicSuffix = "/rpc"
const responseTopicSuffix = "/rpc"
const notificationTopicSuffix = "/events/rpc"
const onlineStatusTopicSuffix = "/online"

type ShellyMqtt struct {
	devices   []*ShellyDevice
	publisher mqtt.Publisher
}

func NewShellyMqtt(devices []*ShellyDevice, publisher mqtt.Publisher) *ShellyMqtt {
	smh := &ShellyMqtt{
		devices:   devices,
		publisher: publisher,
	}

	for _, dev := range devices {
		dev.mqttHandler = smh
	}

	return smh
}

func (smh *ShellyMqtt) MqttHandle(pub paho.PublishReceived) (bool, error) {
	log.Debug("received mqtt message", "topic", pub.Packet.Topic)

	if !smh.topicMatched(pub.Packet.Topic) {
		return false, nil
	}

	switch {
	case strings.HasSuffix(pub.Packet.Topic, onlineStatusTopicSuffix):
		device := smh.findDeviceByTopic(pub.Packet.Topic)
		if device == nil {
			log.Error("failed to find device by topic", "topic", pub.Packet.Topic)
			return false, nil
		}

		isOnline, err := UnmarshalOnlineStatus(pub.Packet.Payload)
		if err != nil {
			log.Error("failed to unmarshal online status", "error", err)
			return false, err
		}

		log.Debug("got online status notification", "device", device, "is online", isOnline)

		if isOnline {
			log.Debug("requesting full status update")
			req := smh.newRpcRequest("Shelly.GetStatus", nil)
			err := smh.publishDeviceMessage(device, req)
			if err != nil {
				log.Error("failed to publish GetStatus request", "error", err)
				return false, err
			}
		}

		return true, nil

	case strings.EqualFold(pub.Packet.Topic, smh.responseTopic()):

		rpc, err := UnmarshalRpcMessage(pub.Packet.Payload)
		if err != nil {
			log.Error("failed to unmarshal rpc message", "error", err, "message", string(pub.Packet.Payload))
			return false, err
		}

		device := smh.deviceById(rpc.Src)
		if device == nil {
			log.Error("failed to find device by id", "id", rpc.Src)
			return false, nil
		}

		status := &GetStatus{}
		err = rpc.UnmarshalResult(status)
		if err != nil {
			log.Error("failed to unmarshal rpc result", "error", err)
			return false, err
		}

		log.Debug("got status response", "device", device, "status", status)

	// process response
	case strings.HasSuffix(pub.Packet.Topic, notificationTopicSuffix):
		device := smh.findDeviceByTopic(pub.Packet.Topic)
		if device == nil {
			log.Error("failed to find device by topic", "topic", pub.Packet.Topic)
			return false, nil
		}

		rpc, err := UnmarshalRpcMessage(pub.Packet.Payload)
		if err != nil {
			log.Error("failed to unmarshal rpc message", "error", err, "message", string(pub.Packet.Payload))
			return false, err
		}

		if !strings.EqualFold(rpc.Method, "NotifyStatus") || !strings.EqualFold(rpc.Src, device.Id) {
			log.Error("rpc method or src mismatch", "method", rpc.Method, "expected method", "NotifyStatus", "src", rpc.Src, "expected src", device.Id)
			return false, nil
		}

		notify := &NotifyStatus{}
		err = rpc.UnmarshalParams(notify)
		if err != nil {
			log.Error("failed to unmarshal NotifyStatus", "error", err)
			return false, err
		}

		switches := device.Switches
		err = notify.FillSwitches(switches)
		if err != nil {
			log.Error("failed to fill switches", "error", err)
			return false, err
		}
		device.Switches = switches

		log.Debug("processed notification", "device", device)

	case strings.HasSuffix(pub.Packet.Topic, publishRequestTopicSuffix):
		device := smh.findDeviceByTopic(pub.Packet.Topic)
		if device == nil {
			log.Error("failed to find device by topic", "topic", pub.Packet.Topic)
			return false, nil
		}

	// handle this
	default:
		log.Info("unexpected topic", "topic", pub.Packet.Topic)
		return false, nil
	}

	return true, nil
}

func (smh *ShellyMqtt) MqttSubscribeTopics() []string {
	topics := []string{smh.responseTopic()}
	for _, dev := range smh.devices {
		topics = append(topics, dev.Id+notificationTopicSuffix)
		topics = append(topics, dev.Id+onlineStatusTopicSuffix)
		topics = append(topics, dev.Id+publishRequestTopicSuffix)
	}
	return topics
}

func (smh *ShellyMqtt) deviceById(id string) *ShellyDevice {
	for _, dev := range smh.devices {
		if strings.EqualFold(dev.Id, id) {
			return dev
		}
	}
	return nil
}

func (smh *ShellyMqtt) topicMatched(topic string) bool {
	if strings.EqualFold(topic, smh.responseTopic()) {
		return true
	}

	for _, dev := range smh.devices {
		if strings.EqualFold(topic, dev.Id+notificationTopicSuffix) {
			return true
		}
		if strings.EqualFold(topic, dev.Id+onlineStatusTopicSuffix) {
			return true
		}
		if strings.EqualFold(topic, dev.Id+publishRequestTopicSuffix) {
			return true
		}
	}

	return false
}

func (smh *ShellyMqtt) responseTopic() string {
	return smh.publisher.ClientId() + responseTopicSuffix
}

func (smh *ShellyMqtt) deviceTopics(suffix string) (topics []string) {
	for _, dev := range smh.devices {
		topics = append(topics, dev.Id+suffix)
	}
	return
}

func (smh *ShellyMqtt) findDeviceByTopic(topic string) *ShellyDevice {
	for _, dev := range smh.devices {
		if strings.HasPrefix(topic, dev.Id) {
			return dev
		}
	}

	return nil
}

func (smh *ShellyMqtt) newRpcRequest(method string, params map[string]interface{}) rpcRequest {
	req := rpcRequest{
		Jsonrpc: "2.0",
		Src:     smh.publisher.ClientId(),
		Method:  method,
		Params:  params,
	}
	if params != nil {
		req.Params = params
	}

	return req
}

func (smh *ShellyMqtt) publishDeviceMessage(dev *ShellyDevice, msg rpcRequest) error {
	bytes, err := msg.bytes()
	if err != nil {
		return err
	}

	topic := dev.Id + publishRequestTopicSuffix
	return smh.publisher.Publish(topic, bytes)
}
