package repository

import (
	"context"
	"encoding/json"

	"github.com/redis/go-redis/v9"
	"github.com/yourusername/kart-challenge/backend-challenge/internal/models"
)

const (
	productKeyPrefix = "product:"
	productIDsKey   = "product:ids"
)

// ProductCache provides Redis-backed product cache with delta updates
type ProductCache struct {
	rdb *redis.Client
	db  *ProductDBRepo
}

// NewProductCache creates a new product cache
func NewProductCache(rdb *redis.Client, db *ProductDBRepo) *ProductCache {
	return &ProductCache{rdb: rdb, db: db}
}

// GetByID returns a product by ID. Redis first, then DB on miss (and populates cache).
// On Redis failure (connection error, etc.), falls back to DB so service stays available.
func (c *ProductCache) GetByID(ctx context.Context, id string) (*models.Product, error) {
	key := productKeyPrefix + id
	val, err := c.rdb.Get(ctx, key).Result()
	if err == nil {
		var p models.Product
		if err := json.Unmarshal([]byte(val), &p); err != nil {
			return nil, err
		}
		return &p, nil
	}
	if err != redis.Nil {
		// Redis failure: fall back to DB so service stays available
		p, dbErr := c.db.GetByID(ctx, id)
		if dbErr != nil || p == nil {
			return p, dbErr
		}
		_ = c.setProduct(ctx, p) // Best-effort cache repopulation
		return p, nil
	}

	// Cache miss - load from DB and populate Redis
	p, err := c.db.GetByID(ctx, id)
	if err != nil || p == nil {
		return p, err
	}
	if err := c.setProduct(ctx, p); err != nil {
		return p, nil // Return even if cache write fails
	}
	return p, nil
}

// GetAll returns all products. Uses product:ids set + MGET for delta-friendly listing.
// On Redis failure, falls back to DB to ensure availability.
func (c *ProductCache) GetAll(ctx context.Context) ([]models.Product, error) {
	ids, err := c.rdb.SMembers(ctx, productIDsKey).Result()
	if err != nil {
		// Redis failure: fall back to DB so service stays available
		return c.refreshFromDB(ctx)
	}
	if len(ids) == 0 {
		// Cold start - load from DB and populate Redis
		return c.refreshFromDB(ctx)
	}

	keys := make([]string, len(ids))
	for i, id := range ids {
		keys[i] = productKeyPrefix + id
	}
	vals, err := c.rdb.MGet(ctx, keys...).Result()
	if err != nil {
		// Redis failure: fall back to DB
		return c.refreshFromDB(ctx)
	}

	var products []models.Product
	var missing []string
	for i, v := range vals {
		if v == nil {
			missing = append(missing, ids[i])
			continue
		}
		s, ok := v.(string)
		if !ok {
			missing = append(missing, ids[i])
			continue
		}
		var p models.Product
		if err := json.Unmarshal([]byte(s), &p); err != nil {
			missing = append(missing, ids[i])
			continue
		}
		products = append(products, p)
	}

	// Load missing from DB and populate
	for _, id := range missing {
		p, err := c.db.GetByID(ctx, id)
		if err != nil || p == nil {
			continue
		}
		_ = c.setProduct(ctx, p)
		products = append(products, *p)
	}
	return products, nil
}

// Set writes product to DB then Redis (write-through, delta update)
func (c *ProductCache) Set(ctx context.Context, p *models.Product) error {
	if err := c.db.Upsert(ctx, p); err != nil {
		return err
	}
	return c.setProduct(ctx, p)
}

// Delete removes product from DB and Redis (delta update)
func (c *ProductCache) Delete(ctx context.Context, id string) error {
	if err := c.db.Delete(ctx, id); err != nil {
		return err
	}
	key := productKeyPrefix + id
	pipe := c.rdb.Pipeline()
	pipe.Del(ctx, key)
	pipe.SRem(ctx, productIDsKey, id)
	_, err := pipe.Exec(ctx)
	return err
}

func (c *ProductCache) setProduct(ctx context.Context, p *models.Product) error {
	data, err := json.Marshal(p)
	if err != nil {
		return err
	}
	key := productKeyPrefix + p.ID
	pipe := c.rdb.Pipeline()
	pipe.Set(ctx, key, data, 0)
	pipe.SAdd(ctx, productIDsKey, p.ID)
	_, err = pipe.Exec(ctx)
	return err
}

// SetRedisOnly updates only Redis (no DB write). Use after transactional DB+outbox write.
func (c *ProductCache) SetRedisOnly(ctx context.Context, p *models.Product) error {
	return c.setProduct(ctx, p)
}

// DeleteRedisOnly removes product from Redis only. Use after transactional DB+outbox delete.
func (c *ProductCache) DeleteRedisOnly(ctx context.Context, id string) error {
	key := productKeyPrefix + id
	pipe := c.rdb.Pipeline()
	pipe.Del(ctx, key)
	pipe.SRem(ctx, productIDsKey, id)
	_, err := pipe.Exec(ctx)
	return err
}

// refreshFromDB loads all products from DB and populates Redis (cold start)
func (c *ProductCache) refreshFromDB(ctx context.Context) ([]models.Product, error) {
	products, err := c.db.GetAll(ctx)
	if err != nil {
		return nil, err
	}
	if len(products) == 0 {
		return products, nil
	}
	pipe := c.rdb.Pipeline()
	for _, p := range products {
		data, _ := json.Marshal(p)
		pipe.Set(ctx, productKeyPrefix+p.ID, data, 0)
		pipe.SAdd(ctx, productIDsKey, p.ID)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return products, err
	}
	return products, nil
}

// ProductStore interface for handlers and services that need GetByID/GetAll
type ProductStore interface {
	GetByID(ctx context.Context, id string) (*models.Product, error)
	GetAll(ctx context.Context) ([]models.Product, error)
}

// Ensure ProductCache implements ProductStore
var _ ProductStore = (*ProductCache)(nil)
