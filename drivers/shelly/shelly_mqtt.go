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

type ShellyMqttHandler struct {
	devices   []*ShellyDevice
	publisher mqtt.Publisher
}

func NewShellyMqttHandler(devices []*ShellyDevice, publisher mqtt.Publisher) *ShellyMqttHandler {
	return &ShellyMqttHandler{
		devices:   devices,
		publisher: publisher,
	}
}

func (smh *ShellyMqttHandler) MqttHandle(pub paho.PublishReceived) (bool, error) {
	log.Debug("received mqtt message", "topic", pub.Packet.Topic)

	if !smh.topicMatched(pub.Packet.Topic) {
		return false, nil
	}

	rpc, err := UnmarshalRpcMessage(pub.Packet.Payload)
	if err != nil {
		log.Error("failed to unmarshal rpc message", "error", err)
		return false, err
	}

	dev := smh.deviceById(rpc.Src)
	if dev == nil {
		log.Debug("received message from unknown device", "device", rpc.Src)
		return false, nil
	}

	switch pub.Packet.Topic {
	case dev.Id + notificationTopicSuffix:
		if !strings.EqualFold(rpc.Method, "NotifyStatus") {
			log.Error("received unexpected rpc method", "method", rpc.Method, "expected", "NotifyStatus")
			return false, nil
		}

		notify := &NotifyStatus{}
		err = rpc.UnmarshalParams(notify)
		if err != nil {
			log.Error("failed to unmarshal NotifyStatus", "error", err)
			return false, err
		}

		err = notify.FillSwitches(dev.Switches)
		if err != nil {
			log.Error("failed to fill switches", "error", err)
			return false, err
		}

	case dev.Id + onlineStatusTopicSuffix:

	case dev.Id + publishRequestTopicSuffix:

	case smh.responseTopic():

	default:
		log.Info("unexpected topic", "topic", pub.Packet.Topic)
		return false, nil
	}

	return true, nil
}

func (smh *ShellyMqttHandler) deviceById(id string) *ShellyDevice {
	for _, dev := range smh.devices {
		if strings.EqualFold(dev.Id, id) {
			return dev
		}
	}
	return nil
}

func (smh *ShellyMqttHandler) topicMatched(topic string) bool {
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

func (smh *ShellyMqttHandler) responseTopic() string {
	return smh.publisher.ClientId() + responseTopicSuffix
}

func (smh *ShellyMqttHandler) deviceTopics(suffix string) (topics []string) {
	for _, dev := range smh.devices {
		topics = append(topics, dev.Id+suffix)
	}
	return
}
