package redis

import (
	"context"
	"os"

	"github.com/redis/go-redis/v9"
)

// Config holds Redis connection settings
type Config struct {
	URL string
}

// New creates a new Redis client
func New(ctx context.Context, cfg Config) (*redis.Client, error) {
	url := cfg.URL
	if url == "" {
		url = os.Getenv("REDIS_URL")
	}
	if url == "" {
		url = "redis://localhost:6379/0"
	}

	opts, err := redis.ParseURL(url)
	if err != nil {
		return nil, err
	}

	client := redis.NewClient(opts)
	if err := client.Ping(ctx).Err(); err != nil {
		client.Close()
		return nil, err
	}

	return client, nil
}
