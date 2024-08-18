package shelly

import (
	"strings"
	"sync"
	"time"

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

	queue       requestQueue
	sendingLock sync.Mutex
}

func NewShellyMqtt(devices []*ShellyDevice, publisher mqtt.Publisher) *ShellyMqtt {
	smh := ShellyMqtt{
		devices:   devices,
		publisher: publisher,
		queue:     requestQueue{},
	}

	for _, dev := range devices {
		dev.mqttHandler = &smh
	}

	return &smh
}

func (smh *ShellyMqtt) MqttHandle(pub paho.PublishReceived) (bool, error) {
	log.Debug("received mqtt message", "topic", pub.Packet.Topic)

	if !smh.topicMatched(pub.Packet.Topic) {
		return false, nil
	}

	switch {
	case strings.HasSuffix(pub.Packet.Topic, onlineStatusTopicSuffix):
		log.Debug("handling shelly mqtt", "type", "online status")
		return smh.handleOnlineStatus(pub)

	case strings.EqualFold(pub.Packet.Topic, smh.responseTopic()):
		log.Debug("handling shelly mqtt", "type", "response")
		return smh.handleResponse(pub)

	case strings.HasSuffix(pub.Packet.Topic, notificationTopicSuffix):
		log.Debug("handling shelly mqtt", "type", "notification")
		return smh.handleNotification(pub)

	case strings.HasSuffix(pub.Packet.Topic, publishRequestTopicSuffix):
		log.Debug("handling (ignoring) shelly mqtt", "type", "publish request")
		// device := smh.findDeviceByTopic(pub.Packet.Topic)
		// if device == nil {
		// 	log.Error("failed to find device by topic", "topic", pub.Packet.Topic)
		// 	return false, nil
		// }

		// probably no need to handle this, this is for pbulishing requests, so we can ignore it
		// maybe we can use it to debug in the future
		//
		return false, nil
	default:
		log.Info("unexpected topic", "topic", pub.Packet.Topic)
		return false, nil
	}
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

func (smh *ShellyMqtt) handleNotification(pub paho.PublishReceived) (bool, error) {
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

	if !strings.EqualFold(rpc.Src, device.Id) {
		log.Error("rpc src mismatch", "src", rpc.Src, "expected src", device.Id)
		return false, nil
	}

	log.Debug("handling rpc notification", "method", rpc.Method, "device", device.Id)

	switch rpc.Method {
	case "NotifyStatus":
		status := GetStatus{}
		err = rpc.UnmarshalParams(&status)
		if err != nil {
			log.Error("failed to unmarshal NotifyStatus", "error", err, "device", device.Id)
			return false, err
		}

		err = device.UpdateFromStatus(status)
		if err != nil {
			log.Error("failed to update from status", "error", err, "method", rpc.Method, "device", device.Id)
			return false, err
		}

	case "NotifyFullStatus":
		status := GetStatus{}
		err = rpc.UnmarshalParams(&status)
		if err != nil {
			log.Error("failed to unmarshal NotifyStatus", "error", err, "device", device.Id)
			return false, err
		}

		err = device.FillStatus(status)
		if err != nil {
			log.Error("failed to fill status", "error", err, "method", rpc.Method, "device", device.Id)
			return false, err
		}

	case "NotifyEvent":
		log.Warn("not implemented notification", "method", rpc.Method)
		return false, nil

	default:
		log.Warn("unknown notification", "method", rpc.Method)
		return false, nil
	}

	return true, nil
}

func (smh *ShellyMqtt) handleResponse(pub paho.PublishReceived) (bool, error) {
	rpc, err := unmarshalRpcResponse(pub.Packet.Payload)
	if err != nil {
		log.Error("failed to unmarshal rpc message", "error", err, "message", string(pub.Packet.Payload))
		return false, err
	}

	device := smh.deviceById(rpc.Src)
	if device == nil {
		log.Error("failed to find device by id", "id", rpc.Src)
		return false, nil
	}

	request := smh.queue.getRequest(rpc.Id)
	if request == nil {
		log.Error("failed to find request by id", "id", rpc.Id)
		return false, nil
	}

	switch request.Method {
	case "GetStatus":
		status := GetStatus{}
		err = rpc.UnmarshalResult(&status)
		if err != nil {
			log.Error("failed to unmarshal rpc result", "error", err)
			return false, err
		}

		err = device.FillStatus(status)
		if err != nil {
			log.Error("failed to fill status", "error", err)
			return false, err
		}

		return true, nil
	default:
		log.Error("received unsupported response", "method", request.Method, "device", device.Id)
		return false, nil
	}
}

func (smh *ShellyMqtt) handleOnlineStatus(pub paho.PublishReceived) (bool, error) {
	device := smh.findDeviceByTopic(pub.Packet.Topic)
	if device == nil {
		log.Error("failed to find device by topic", "topic", pub.Packet.Topic)
		return false, nil
	}

	isOnline, err := unmarshalOnlineStatus(pub.Packet.Payload)
	if err != nil {
		log.Error("failed to unmarshal online status", "error", err)
		return false, err
	}

	log.Debug("got online status notification", "device", device, "is online", isOnline)

	if isOnline {
		log.Debug("requesting full status update", "publisher", smh.publisher)
		req := smh.newRpcRequest("Shelly.GetStatus", nil)
		err := smh.publishDeviceMessage(device, req)
		if err != nil {
			retryAfter := time.Second * 25
			log.Error("failed to publish GetStatus request", "error", err, "retry after", retryAfter)
			log.Debug("got error while publishing GetStatus request", "connReady", smh.publisher.ConnectionReady())
			time.Sleep(retryAfter)
			err = smh.publishDeviceMessage(device, req)
			if err != nil {
				log.Error("failed to publish GetStatus request", "error", err)
				return false, err
			}
		}
	}

	return true, nil
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

func (smh *ShellyMqtt) newRpcRequest(method string, params map[string]interface{}) requestFrame {
	smh.sendingLock.Lock()
	defer smh.sendingLock.Unlock()

	req := requestFrame{
		Jsonrpc: "2.0",
		Src:     smh.publisher.ClientId(),
		Id:      smh.queue.getNextId(),
		Method:  method,
		Params:  params,
	}

	return req
}

func (smh *ShellyMqtt) publishDeviceMessage(dev *ShellyDevice, msg requestFrame) error {
	smh.sendingLock.Lock()
	defer smh.sendingLock.Unlock()

	bytes, err := msg.bytes()
	if err != nil {
		return err
	}

	topic := dev.Id + publishRequestTopicSuffix
	err = smh.publisher.Publish(topic, bytes)
	if err == nil {
		smh.queue = smh.queue.add(msg, dev.Id)
	}
	return err
}
