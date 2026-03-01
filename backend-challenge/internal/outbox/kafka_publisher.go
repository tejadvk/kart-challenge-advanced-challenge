package outbox

import (
	"context"
	"fmt"
	"log"

	"github.com/segmentio/kafka-go"
)

// KafkaPublisher publishes events to Kafka topics
type KafkaPublisher struct {
	writer *kafka.Writer
	// eventTypeToTopic maps event type to Kafka topic
	eventTypeToTopic map[string]string
	defaultTopic     string
}

// KafkaPublisherConfig configures the Kafka publisher
type KafkaPublisherConfig struct {
	Brokers          []string
	DefaultTopic     string   // used when event type has no mapping
	EventTypeToTopic map[string]string
}

// NewKafkaPublisher creates a Kafka publisher
func NewKafkaPublisher(cfg KafkaPublisherConfig) *KafkaPublisher {
	writer := &kafka.Writer{
		Addr:         kafka.TCP(cfg.Brokers...),
		Balancer:     &kafka.LeastBytes{},
		RequiredAcks: kafka.RequireOne,
	}
	topicMap := cfg.EventTypeToTopic
	if topicMap == nil {
		topicMap = defaultEventTypeToTopic()
	}
	defTopic := cfg.DefaultTopic
	if defTopic == "" {
		defTopic = "outbox"
	}
	return &KafkaPublisher{
		writer:           writer,
		eventTypeToTopic: topicMap,
		defaultTopic:     defTopic,
	}
}

func defaultEventTypeToTopic() map[string]string {
	return map[string]string{
		EventOrderPlaced:    "orders.placed",
		EventProductCreated: "products.created",
		EventProductUpdated: "products.updated",
		EventProductDeleted: "products.deleted",
	}
}

// Publish writes the event to the appropriate Kafka topic
func (p *KafkaPublisher) Publish(ctx context.Context, event *EventToPublish) error {
	topic := p.eventTypeToTopic[event.EventType]
	if topic == "" {
		topic = p.defaultTopic
	}
	msg := kafka.Message{
		Key:   []byte(event.AggregateID),
		Value: event.Payload,
		Headers: []kafka.Header{
			{Key: "event_type", Value: []byte(event.EventType)},
			{Key: "aggregate_type", Value: []byte(event.AggregateType)},
			{Key: "event_id", Value: []byte(event.ID)},
		},
	}
	err := p.writer.WriteMessages(ctx, kafka.Message{
		Topic:   topic,
		Key:     msg.Key,
		Value:   msg.Value,
		Headers: msg.Headers,
	})
	if err != nil {
		log.Printf("[outbox] kafka publish failed: %v", err)
		return fmt.Errorf("kafka publish: %w", err)
	}
	return nil
}

// Close closes the Kafka writer
func (p *KafkaPublisher) Close() error {
	return p.writer.Close()
}
