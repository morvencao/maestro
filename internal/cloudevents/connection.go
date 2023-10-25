package cloudevents

import (
	"context"
	"fmt"
	"os"

	"github.com/kube-orchestra/maestro/internal/db"
	"k8s.io/klog/v2"
	cegeneric "open-cluster-management.io/api/cloudevents/generic"
	"open-cluster-management.io/api/cloudevents/generic/options/mqtt"
	cetypes "open-cluster-management.io/api/cloudevents/generic/types"
)

const (
	mqttClientID       = "MQTT_CLIENT_ID"
	mqttBrokerURL      = "MQTT_BROKER_URL"
	mqttBrokerUsername = "MQTT_BROKER_USERNAME"
	mqttBrokerPassword = "MQTT_BROKER_PASSWORD"
)

type Connection struct {
	cloudEventsSourceClient *cegeneric.CloudEventSourceClient[*db.Resource]
	ResourceChannel         chan db.Resource
	ResourceStatusChannel   chan db.Resource
}

func NewConnection(ctx context.Context) *Connection {
	mqOpts, err := NewMQTTOpts()
	if err != nil {
		panic(err)
	}

	mqClientID := os.Getenv(mqttClientID)
	if len(mqClientID) == 0 {
		panic(fmt.Errorf("%s must be set", mqttClientID))
	}

	sourceOpts := mqtt.NewSourceOptions(mqOpts, mqClientID)

	ceSourceClient, err := cegeneric.NewCloudEventSourceClient[*db.Resource](ctx, sourceOpts,
		&ResourceLister{}, ResourceStatusHashGetter, &Codec{})
	if err != nil {
		panic(err)
	}

	return &Connection{
		cloudEventsSourceClient: ceSourceClient,
		ResourceChannel:         make(chan db.Resource),
		ResourceStatusChannel:   make(chan db.Resource),
	}
}

func (c *Connection) StartSender(ctx context.Context) {
	go func() {
		codec := &Codec{} // TODO use the codec from cloudevents source client
		for msg := range c.ResourceChannel {
			eventType := cetypes.CloudEventsType{
				CloudEventsDataType: codec.EventDataType(),
				SubResource:         cetypes.SubResourceSpec,
				Action:              cetypes.EventAction("create_request"),
			}
			// assume consumer ID here is the cluster ID
			err := c.cloudEventsSourceClient.Publish(ctx, eventType, &msg)
			if err != nil {
				klog.Errorf("failed to publish message with err %v", err)
			}
		}
	}()
}

func (c *Connection) StartStatusReceiver(ctx context.Context) {
	go func() {
		if err := c.cloudEventsSourceClient.Subscribe(ctx, func(action cetypes.ResourceAction, resource *db.Resource) error {
			klog.Infof("setting status %s to db %v", resource.Id, resource.Status.ContentStatus)
			c.ResourceStatusChannel <- *resource
			return db.SetStatusResource(resource.Id, &resource.Status)
		}); err != nil {
			//TODO retry to connect the broker and send resync request
			klog.Errorf("failed to subscribe to host, %v", err)
		}
	}()
}

func NewMQTTOpts() (*mqtt.MQTTOptions, error) {
	brokerURL := os.Getenv(mqttBrokerURL)
	if len(brokerURL) == 0 {
		return nil, fmt.Errorf("%s must be set", mqttBrokerURL)
	}

	brokerUsername := os.Getenv(mqttBrokerUsername)
	if len(brokerUsername) == 0 {
		return nil, fmt.Errorf("%s must be set", mqttBrokerUsername)
	}

	brokerPassword := os.Getenv(mqttBrokerPassword)
	if len(brokerPassword) == 0 {
		return nil, fmt.Errorf("%s must be set", mqttBrokerPassword)
	}

	opts := mqtt.NewMQTTOptions()
	opts.BrokerHost = brokerURL
	opts.Username = brokerUsername
	opts.Password = brokerPassword

	return opts, nil
}