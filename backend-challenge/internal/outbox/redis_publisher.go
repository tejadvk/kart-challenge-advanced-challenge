package outbox

import (
	"context"
	"log"

	"github.com/redis/go-redis/v9"
)

// RedisPublisher publishes events to Redis Streams
type RedisPublisher struct {
	client *redis.Client
	stream string
}

// RedisPublisherConfig configures the Redis publisher
type RedisPublisherConfig struct {
	Client *redis.Client
	Stream string // Redis stream key, default "outbox"
}

// NewRedisPublisher creates a Redis Streams publisher
func NewRedisPublisher(cfg RedisPublisherConfig) *RedisPublisher {
	stream := cfg.Stream
	if stream == "" {
		stream = "outbox"
	}
	return &RedisPublisher{
		client: cfg.Client,
		stream: stream,
	}
}

// Publish adds the event to the Redis stream
func (p *RedisPublisher) Publish(ctx context.Context, event *EventToPublish) error {
	args := &redis.XAddArgs{
		Stream: p.stream,
		Values: map[string]interface{}{
			"id":             event.ID,
			"aggregate_type": event.AggregateType,
			"aggregate_id":   event.AggregateID,
			"event_type":     event.EventType,
			"payload":        event.Payload,
		},
	}
	_, err := p.client.XAdd(ctx, args).Result()
	if err != nil {
		log.Printf("[outbox] redis publish failed: %v", err)
		return err
	}
	return nil
}

// Close is a no-op for Redis (connection pool managed elsewhere)
func (p *RedisPublisher) Close() error {
	return nil
}
