package mock

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

 _ "embed"

	"github.com/charmbracelet/log"
	"github.com/hubertat/swkit/mqtt"
)

//go:embed shelly_rpc/getconfig_resp.json
var shellyGetConfigResp []byte

//go:embed shelly_rpc/getstatus_resp.json
var shellyGetStatusResp []byte


const publishRequestTopicSuffix = "/rpc"
const responseTopicSuffix = "/rpc"

type FakeShellyMqttRpc struct {
	clientId string
	handlers []mqtt.MqttHandler

	l *log.Logger
}

type rpcRequest struct {
	Id     int
	Src    string
	Method string
}

func (fs *FakeShellyMqttRpc) Connect(ctx context.Context, handlers []mqtt.MqttHandler) (err error) {
	if len(handlers) == 0 {
		fs.l.Error("failed on new FakeShellyMqttRpc: no handlers provided")
		return errors.New("no handlers")
	}

	for _, handler := range handlers {
		fs.l.Info("setting up (fake) handler", "topics", handler.MqttSubscribeTopics())
		fs.handlers = append(fs.handlers, handler)
	}

	return nil
}

func (fs *FakeShellyMqttRpc) Publish(topic string, payload []byte) (err error) {
	if len(topic) == 0 {
		fs.l.Error("failed on new FakeShellyMqttRpc: no topic provided")
		return errors.New("no topic")
	}

	log.Info("publishing to topic", "topic", topic, "payload", string(payload))

	if strings.HasSuffix(topic, publishRequestTopicSuffix) {
		shellyId := strings.Split(topic, "/")[0]
		fs.l.Info("recognized request topic", "shelly id", shellyId)
		go fs.processRequest(topic, payload)
	} else {
		fs.l.Info("unrecognized topic, ignoring")
	}

	return nil
}

func (fs *FakeShellyMqttRpc) processRequest(topic string, payload []byte) {
	fs.l.Debug("processing request", "topic", topic, "payload", string(payload))

	var req rpcRequest
	err = json.Unmarshal(payload, &req)
	if err != nil {
		fs.l.Error("failed to unmarshal payload", "payload", string(payload))
		return
	}


	fs.l.Info("unmarshaled payload", "request", req)
	response := []byte{}
	switch req.Method {
		case "Shelly.GetConfig":
			fs.l.Debug("responding to GetConfig request")
			response = shellyGetConfigResp
		case "Shelly.GetStatus":
			fs.l.Debug("responding to GetStatus request")
			response = shellyGetStatusResp

		default:
		fs.l.Info("unrecognized rpc method", "method", req.Method)
		return
	}

	fs.l.Debug("publishing response", "response", string(response))
	for _, h := range fs.handlers {
		h.Publish(topic + responseTopicSuffix, response)
	}
}

func NewFakeShellyMqttRpc(clientId string) *FakeShellyMqttRpc {
	return &FakeShellyMqttRpc{
		clientId: clientId,
		l: log.NewWithOptions(log.Options{
			log.WithPrefix("fake mqtt rpc 🥸")
		}),
	}
}
