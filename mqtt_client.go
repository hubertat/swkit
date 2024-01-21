package swkit

import (
	"context"
	"net/url"
	"os"
	"time"

	"github.com/charmbracelet/log"
	"github.com/eclipse/paho.golang/autopaho"
	"github.com/eclipse/paho.golang/paho"
	"github.com/hubertat/swkit/drivers/shelly"
)

const subscribeTimeoutSeconds = 10

type MqttClient struct {
	config autopaho.ClientConfig
	conn   *autopaho.ConnectionManager
	logger *log.Logger
	topics []string

	publishers []Publisher
}

type Publisher struct {
	topic  string
	client *MqttClient
}

func (pub *Publisher) Publish(payload []byte) error {
	_, err := pub.client.conn.Publish(context.Background(), &paho.Publish{
		Topic:   pub.topic,
		QoS:     1,
		Payload: payload,
	})
	if err != nil {
		return err
	}
	return nil
}

func (pub *Publisher) GetClientId() string {
	return pub.client.config.ClientConfig.ClientID
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

	ctx, cancel := context.WithTimeout(context.Background(), subscribeTimeoutSeconds*time.Second)
	defer cancel()

	_, err := cm.Subscribe(ctx, &paho.Subscribe{
		Subscriptions: subs,
	})

	if err != nil {
		mc.logger.Error("Failed to subscribe to topics", "err", err)
	}
}

func (mc *MqttClient) onConnError(err error) {
	mc.logger.Error("Received Mqtt related error", "err", err)
}

func (mc *MqttClient) onSrvDisconnect(d *paho.Disconnect) {
	mc.logger.Info("Disconnected from MQTT broker")
}

func (mc *MqttClient) Connect(ctx context.Context, shellyDevices []*shelly.ShellyDevice) (err error) {
	var cm *autopaho.ConnectionManager

	mc.topics = []string{}
	for _, shelly := range shellyDevices {
		mc.topics = append(mc.topics, shelly.MqttSubscribeTopic())

		mc.config.ClientConfig.Router.RegisterHandler(shelly.MqttSubscribeTopic(), shelly.MqttHandler)

		mc.publishers = append(mc.publishers, Publisher{
			topic:  shelly.MqttPublishTopic(),
			client: mc,
		})
	}

	cm, err = autopaho.NewConnection(ctx, mc.config)
	if err != nil {
		return
	}

	err = cm.AwaitConnection(ctx)

	return
}

func (mc *MqttClient) Disconnect(ctx context.Context) error {
	for _, topic := range mc.topics {
		mc.config.ClientConfig.Router.UnregisterHandler(topic)
	}

	mc.topics = []string{}

	return mc.conn.Disconnect(ctx)
}

func NewMqttClient(broker string, topics []string, clientId string) (mc *MqttClient, err error) {
	addr, err := url.Parse(broker)
	if err != nil {
		return
	}

	mc = &MqttClient{
		logger: log.NewWithOptions(os.Stderr, log.Options{
			Prefix: "MqttClient 🐰: ",
		}),
	}

	mc.config = autopaho.ClientConfig{
		BrokerUrls:     []*url.URL{addr},
		KeepAlive:      20,
		OnConnectionUp: mc.onConnUp,
		OnConnectError: mc.onConnError,
		ClientConfig: paho.ClientConfig{
			ClientID:           clientId,
			OnClientError:      mc.onConnError,
			OnServerDisconnect: mc.onSrvDisconnect,
		},
	}

	return
}
