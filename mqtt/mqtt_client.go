package mqtt

import (
	"context"
	"net/url"
	"time"

	"github.com/charmbracelet/log"
	"github.com/eclipse/paho.golang/autopaho"
	"github.com/eclipse/paho.golang/paho"
	"github.com/hubertat/swkit/logging"
)

const subscribeTimeout = 10 * time.Second
const connectTimeout = 30 * time.Second
const publishTimeoutSeconds = 4
const mqqtKeepAlive = 20
const mqqtSessionExpiry = 60

type MqttClient struct {
	clientId  string
	brokerUrl *url.URL

	conn      *autopaho.ConnectionManager
	connReady bool
	logger    *log.Logger

	handlers []MqttHandler
}

func NewMqttClient(broker string, clientId string) (mc *MqttClient, err error) {
	mc = &MqttClient{
		clientId: clientId,
		logger:   logging.NewLogger(logging.PrefixMqtt),
	}

	mc.brokerUrl, err = url.Parse(broker)

	return
}

func (mc *MqttClient) Connect(ctx context.Context, handlers []MqttHandler) (err error) {
	var cm *autopaho.ConnectionManager

	mc.handlers = []MqttHandler{}
	for _, handlerExt := range handlers {
		h := handlerExt
		mc.handlers = append(mc.handlers, h)

		mc.logger.Debug("setting up mqtt topics config", "topics", h.MqttSubscribeTopics())
	}

	mc.logger.Debug("mqtt autopaho NewConnection", "timeout", connectTimeout)
	cm, err = autopaho.NewConnection(ctx, mc.clientConfig())
	if err != nil {
		return
	}
	mc.conn = cm
	mc.logger.Debug("NewConnection done", "err", err)

	mc.logger.Debug("AwaitConnection")
	err = cm.AwaitConnection(ctx)
	mc.logger.Debug("AwaitConnection done", "err", err)

	return
}

func (mc *MqttClient) Disconnect(ctx context.Context) error {
	mc.handlers = []MqttHandler{}

	return mc.conn.Disconnect(ctx)
}

func (mc *MqttClient) Done() <-chan struct{} {
	return mc.conn.Done()
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

func (mc *MqttClient) ClientId() string {
	return mc.clientId
}

func (mc *MqttClient) ConnectionReady() bool {
	return mc.connReady && mc.conn != nil
}

func (mc *MqttClient) clientConfig() autopaho.ClientConfig {
	return autopaho.ClientConfig{
		BrokerUrls:                    []*url.URL{mc.brokerUrl},
		KeepAlive:                     mqqtKeepAlive,
		SessionExpiryInterval:         mqqtSessionExpiry,
		CleanStartOnInitialConnection: true,
		OnConnectionUp:                mc.onConnUp,
		OnConnectError:                mc.onConnError,
		ClientConfig: paho.ClientConfig{
			ClientID:           mc.clientId,
			OnClientError:      mc.onConnError,
			OnServerDisconnect: mc.onSrvDisconnect,
			OnPublishReceived:  mc.onPublishRecv(),
		},
	}
}

func (mc *MqttClient) topics() (topics []string) {
	for _, h := range mc.handlers {
		topics = append(topics, h.MqttSubscribeTopics()...)
	}

	return
}

func (mc *MqttClient) onConnUp(cm *autopaho.ConnectionManager, connAck *paho.Connack) {
	mc.logger.Info("Connected to MQTT broker")

	subs := []paho.SubscribeOptions{}
	for _, topic := range mc.topics() {
		subs = append(subs, paho.SubscribeOptions{
			QoS:   1,
			Topic: topic,
		})
	}

	mc.logger.Debug("subscribing to mqtt", "subs", subs, "timeout", subscribeTimeout)

	ctx, cancel := context.WithTimeout(context.Background(), subscribeTimeout)
	defer cancel()

	_, err := cm.Subscribe(ctx, &paho.Subscribe{
		Subscriptions: subs,
	})

	mc.logger.Debug("subscribed!")

	if err != nil {
		mc.logger.Error("Failed to subscribe to topics", "err", err)
	}

	mc.connReady = true
}

func (mc *MqttClient) onConnError(err error) {
	mc.logger.Error("Received Mqtt connection error", "err", err)
	mc.connReady = false
}

func (mc *MqttClient) onSrvDisconnect(d *paho.Disconnect) {
	mc.logger.Info("Disconnected from MQTT broker")
	mc.connReady = false
}

func (mc *MqttClient) onPublishRecv() []func(paho.PublishReceived) (bool, error) {
	pubs := []func(paho.PublishReceived) (bool, error){}

	for _, h := range mc.handlers {
		pubs = append(pubs, h.MqttHandle)
	}

	return pubs
}
