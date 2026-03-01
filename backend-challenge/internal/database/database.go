package database

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"strconv"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/yourusername/kart-challenge/backend-challenge/internal/models"
)

// Config holds database connection settings
type Config struct {
	URL      string
	MinConns int32
	MaxConns int32
}

// PoolDefaults returns sensible defaults for connection pool
func PoolDefaults() (min, max int32) {
	return 2, 20
}

// DB wraps the connection pool
type DB struct {
	Pool *pgxpool.Pool
}

// New creates a new database connection pool with configurable MinConns/MaxConns
func New(ctx context.Context, cfg Config) (*DB, error) {
	poolConfig, err := pgxpool.ParseConfig(cfg.URL)
	if err != nil {
		return nil, err
	}
	if cfg.MinConns > 0 {
		poolConfig.MinConns = cfg.MinConns
	}
	if cfg.MaxConns > 0 {
		poolConfig.MaxConns = cfg.MaxConns
	}
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return &DB{Pool: pool}, nil
}

// ConfigFromEnv builds Config from environment (DB_POOL_MIN_CONNS, DB_POOL_MAX_CONNS)
func ConfigFromEnv(url string) Config {
	cfg := Config{URL: url}
	if v := os.Getenv("DB_POOL_MIN_CONNS"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 32); err == nil && n >= 0 {
			cfg.MinConns = int32(n)
		}
	}
	if v := os.Getenv("DB_POOL_MAX_CONNS"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 32); err == nil && n > 0 {
			cfg.MaxConns = int32(n)
		}
	}
	return cfg
}

// NewReadReplica creates a separate connection pool for read replica (DATABASE_READ_REPLICA_URL)
func NewReadReplica(ctx context.Context, url string, cfg Config) (*pgxpool.Pool, error) {
	if url == "" {
		return nil, nil
	}
	poolConfig, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, err
	}
	if cfg.MinConns > 0 {
		poolConfig.MinConns = cfg.MinConns
	}
	if cfg.MaxConns > 0 {
		poolConfig.MaxConns = cfg.MaxConns
	}
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
}

// Close closes the connection pool
func (db *DB) Close() {
	if db != nil && db.Pool != nil {
		db.Pool.Close()
	}
}

// Migrate runs schema migrations
func (db *DB) Migrate(ctx context.Context) error {
	_, err := db.Pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS inventory (
			product_id VARCHAR(50) PRIMARY KEY,
			quantity INT NOT NULL DEFAULT 0,
			updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		);

		CREATE TABLE IF NOT EXISTS orders (
			id UUID PRIMARY KEY,
			total DECIMAL(12, 2) NOT NULL,
			discounts DECIMAL(12, 2) NOT NULL DEFAULT 0,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		);

		CREATE TABLE IF NOT EXISTS order_items (
			order_id UUID NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
			product_id VARCHAR(50) NOT NULL,
			quantity INT NOT NULL,
			PRIMARY KEY (order_id, product_id)
		);

		CREATE INDEX IF NOT EXISTS idx_order_items_order_id ON order_items(order_id);

		CREATE TABLE IF NOT EXISTS coupon_usage (
			coupon_code VARCHAR(50) PRIMARY KEY,
			used_count INT NOT NULL DEFAULT 0,
			updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		);

		CREATE TABLE IF NOT EXISTS coupon_limits (
			coupon_code VARCHAR(50) PRIMARY KEY,
			max_uses INT NOT NULL DEFAULT 0,
			updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		);

		CREATE TABLE IF NOT EXISTS idempotency_keys (
			idempotency_key VARCHAR(255) PRIMARY KEY,
			status VARCHAR(20) NOT NULL DEFAULT 'processing',
			order_id UUID REFERENCES orders(id),
			response_json JSONB,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		);

		CREATE TABLE IF NOT EXISTS products (
			id VARCHAR(50) PRIMARY KEY,
			name VARCHAR(255) NOT NULL,
			price DECIMAL(12, 2) NOT NULL,
			category VARCHAR(100) NOT NULL,
			image JSONB NOT NULL DEFAULT '{}',
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		);

		CREATE TABLE IF NOT EXISTS outbox (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			aggregate_type VARCHAR(50) NOT NULL,
			aggregate_id VARCHAR(100) NOT NULL,
			event_type VARCHAR(100) NOT NULL,
			payload JSONB NOT NULL,
			status VARCHAR(20) NOT NULL DEFAULT 'pending',
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			processed_at TIMESTAMP,
			error_message TEXT
		);

		CREATE INDEX IF NOT EXISTS idx_outbox_status ON outbox(status);
		CREATE INDEX IF NOT EXISTS idx_outbox_created_at ON outbox(created_at);
	`)
	return err
}

// SeedInventory inserts initial inventory for known products if not exists
func (db *DB) SeedInventory(ctx context.Context, productIDs []string, initialQty int) error {
	for _, id := range productIDs {
		_, err := db.Pool.Exec(ctx, `
			INSERT INTO inventory (product_id, quantity)
			VALUES ($1, $2)
			ON CONFLICT (product_id) DO NOTHING
		`, id, initialQty)
		if err != nil {
			return err
		}
	}
	log.Printf("Seeded inventory: %d products with quantity %d", len(productIDs), initialQty)
	return nil
}

// SeedProductsFromJSON loads products from JSON file and inserts into DB if table is empty
func (db *DB) SeedProductsFromJSON(ctx context.Context, jsonPath string) error {
	data, err := os.ReadFile(jsonPath)
	if err != nil {
		return err
	}
	var products []models.Product
	if err := json.Unmarshal(data, &products); err != nil {
		return err
	}
	for _, p := range products {
		imgJSON, _ := json.Marshal(p.Image)
		_, err := db.Pool.Exec(ctx, `
			INSERT INTO products (id, name, price, category, image)
			VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (id) DO UPDATE SET
				name = EXCLUDED.name,
				price = EXCLUDED.price,
				category = EXCLUDED.category,
				image = EXCLUDED.image,
				updated_at = CURRENT_TIMESTAMP
		`, p.ID, p.Name, p.Price, p.Category, imgJSON)
		if err != nil {
			return err
		}
	}
	log.Printf("Seeded products: %d from %s", len(products), jsonPath)
	return nil
}
