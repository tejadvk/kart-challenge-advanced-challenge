package outbox

import "context"

// Publisher publishes outbox events to a message broker (Redis, Kafka, etc.)
type Publisher interface {
	// Publish sends an event to the broker. Implementations may map event types to
	// streams/topics or use a single destination.
	Publish(ctx context.Context, event *EventToPublish) error

	// Close releases any broker resources. Safe to call multiple times.
	Close() error
}

// EventToPublish is the minimal event representation for publishing
type EventToPublish struct {
	ID            string
	AggregateType string
	AggregateID   string
	EventType     string
	Payload       []byte
}
