package repository

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/yourusername/kart-challenge/backend-challenge/internal/models"
)

// IdempotencyRepo handles idempotency key storage and lookup
type IdempotencyRepo struct {
	pool *pgxpool.Pool
}

// NewIdempotencyRepo creates a new idempotency repository
func NewIdempotencyRepo(pool *pgxpool.Pool) *IdempotencyRepo {
	return &IdempotencyRepo{pool: pool}
}

// IdempotencyTx is the minimal interface for running queries in a transaction
type IdempotencyTx interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// Status constants for idempotency keys
const (
	IdempotencyStatusProcessing = "processing"
	IdempotencyStatusCompleted  = "completed"
)

// Get retrieves a completed response for an idempotency key.
// Returns (order, true) if found and completed, (nil, false) if not found or still processing.
func (r *IdempotencyRepo) Get(ctx context.Context, tx IdempotencyTx, key string) (*models.Order, bool) {
	var status string
	var responseJSON []byte
	err := tx.QueryRow(ctx, `
		SELECT status, response_json FROM idempotency_keys WHERE idempotency_key = $1
	`, key).Scan(&status, &responseJSON)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, false
		}
		return nil, false
	}

	if status != IdempotencyStatusCompleted || len(responseJSON) == 0 {
		return nil, false
	}

	var order models.Order
	if err := json.Unmarshal(responseJSON, &order); err != nil {
		return nil, false
	}
	return &order, true
}

// IsProcessing returns true if the key exists with status 'processing'
func (r *IdempotencyRepo) IsProcessing(ctx context.Context, tx IdempotencyTx, key string) (bool, error) {
	var status string
	err := tx.QueryRow(ctx, `
		SELECT status FROM idempotency_keys WHERE idempotency_key = $1
	`, key).Scan(&status)
	if err != nil {
		if err == pgx.ErrNoRows {
			return false, nil
		}
		return false, err
	}
	return status == IdempotencyStatusProcessing, nil
}

// Reserve inserts an idempotency key with status 'processing'.
// Returns true if we inserted (first request), false if key already existed (duplicate).
func (r *IdempotencyRepo) Reserve(ctx context.Context, tx IdempotencyTx, key string) (inserted bool, err error) {
	tag, err := tx.Exec(ctx, `
		INSERT INTO idempotency_keys (idempotency_key, status)
		VALUES ($1, $2)
		ON CONFLICT (idempotency_key) DO NOTHING
	`, key, IdempotencyStatusProcessing)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// Complete stores the order response for a key
func (r *IdempotencyRepo) Complete(ctx context.Context, tx IdempotencyTx, key string, order *models.Order) error {
	responseJSON, err := json.Marshal(order)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		UPDATE idempotency_keys
		SET status = $1, order_id = $2, response_json = $3
		WHERE idempotency_key = $4
	`, IdempotencyStatusCompleted, order.ID, responseJSON, key)
	return err
}

// DeleteOlderThan removes idempotency keys older than the given duration.
// Used for TTL cleanup. Returns the number of rows deleted.
func (r *IdempotencyRepo) DeleteOlderThan(ctx context.Context, olderThanHours int) (int64, error) {
	tag, err := r.pool.Exec(ctx, `
		DELETE FROM idempotency_keys WHERE created_at < NOW() - ($1 || ' hours')::interval
	`, olderThanHours)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// ResetStaleProcessing deletes keys stuck in 'processing' longer than the given minutes.
// Allows retried requests to proceed when the original request crashed before completing.
func (r *IdempotencyRepo) ResetStaleProcessing(ctx context.Context, staleMinutes int) (int64, error) {
	tag, err := r.pool.Exec(ctx, `
		DELETE FROM idempotency_keys
		WHERE status = $1 AND created_at < NOW() - ($2 || ' minutes')::interval
	`, IdempotencyStatusProcessing, staleMinutes)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
