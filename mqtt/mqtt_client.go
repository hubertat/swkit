package mqtt

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"time"

	"github.com/charmbracelet/log"
	"github.com/eclipse/paho.golang/autopaho"
	"github.com/eclipse/paho.golang/paho"
)

const subscribeTimeoutSeconds = 15
const connectionTimeoutSeconds = 5
const publishTimeoutSeconds = 4

type MqttHandler interface {
	MqttHandle(pub *paho.Publish)
	MqttSubscribeTopic() string
}

type Publisher interface {
	Publish(topic string, payload []byte) error
}

type MqttClient struct {
	config autopaho.ClientConfig
	conn   *autopaho.ConnectionManager
	logger *log.Logger
	topics []string
}

func (mc *MqttClient) Publish(topic string, payload []byte) (err error) {
	ctx, cancel := context.WithTimeout(context.Background(), publishTimeoutSeconds*time.Second)
	defer cancel()

	_, err = mc.conn.Publish(ctx, &paho.Publish{
		Topic:   topic,
		QoS:     1,
		Payload: payload,
	})
	return
}

func (mc *MqttClient) onConnUp(cm *autopaho.ConnectionManager, connAck *paho.Connack) {
	mc.logger.Info("Connected to MQTT broker")

	subs := []paho.SubscribeOptions{}
	for _, topic := range mc.topics {
		subs = append(subs, paho.SubscribeOptions{
			QoS:   1,
			Topic: topic,
		})
	}

	mc.logger.Debug("subscribing mqtt", "subs", subs)

	ctx, cancel := context.WithTimeout(context.Background(), subscribeTimeoutSeconds*time.Second)
	defer cancel()

	_, err := cm.Subscribe(ctx, &paho.Subscribe{
		Subscriptions: subs,
	})
	mc.logger.Debug("subscribed mqtt", "err", err)

	if err != nil {
		mc.logger.Error("Failed to subscribe to topics", "err", err)
	}
}

func (mc *MqttClient) onConnError(err error) {
	mc.logger.Error("Received Mqtt connection error", "err", err)
}

func (mc *MqttClient) onSrvDisconnect(d *paho.Disconnect) {
	mc.logger.Info("Disconnected from MQTT broker")
}

func (mc *MqttClient) onPublishRecv() []func(paho.PublishReceived) (bool, error) {
	return []func(paho.PublishReceived) (bool, error){
		func(pr paho.PublishReceived) (bool, error) {
			fmt.Printf("received message on topic %s; body: %s (retain: %t)\n", pr.Packet.Topic, pr.Packet.Payload, pr.Packet.Retain)
			return true, nil
		},
	}
}

func (mc *MqttClient) Connect(handlers []MqttHandler) (err error) {
	var cm *autopaho.ConnectionManager

	ctx, cancel := context.WithTimeout(context.Background(), connectionTimeoutSeconds*time.Second)
	defer cancel()

	mc.topics = []string{}
	for _, h := range handlers {
		mc.logger.Debug("setting up mqtt topics config", "topic", h.MqttSubscribeTopic())
		mc.topics = append(mc.topics, h.MqttSubscribeTopic())
		// mc.config.ClientConfig.Router.RegisterHandler(h.MqttSubscribeTopic(), h.MqttHandle)
	}

	mc.logger.Debug("NewConnection")
	cm, err = autopaho.NewConnection(ctx, mc.config)
	if err != nil {
		return
	}
	mc.logger.Debug("NewConnection done", "err", err)

	mc.logger.Debug("AwaitConnection")
	err = cm.AwaitConnection(ctx)
	mc.logger.Debug("AwaitConnection done", "err", err)

	return
}

func (mc *MqttClient) Disconnect(ctx context.Context) error {
	for _, topic := range mc.topics {
		mc.config.ClientConfig.Router.UnregisterHandler(topic)
	}

	mc.topics = []string{}

	return mc.conn.Disconnect(ctx)
}

func NewMqttClient(broker string, clientId string) (mc *MqttClient, err error) {
	addr, err := url.Parse(broker)
	if err != nil {
		return
	}

	mc = &MqttClient{
		logger: log.NewWithOptions(os.Stderr, log.Options{
			Prefix: "MqttClient 🐰: ",
			Level:  log.GetLevel(),
		}),
	}

	mc.config = autopaho.ClientConfig{
		BrokerUrls:            []*url.URL{addr},
		KeepAlive:             20,
		SessionExpiryInterval: 60,
		OnConnectionUp:        mc.onConnUp,
		OnConnectError:        mc.onConnError,
		ClientConfig: paho.ClientConfig{
			ClientID:           clientId,
			OnClientError:      mc.onConnError,
			OnServerDisconnect: mc.onSrvDisconnect,
			OnPublishReceived:  mc.onPublishRecv(),
		},
	}

	return
}
