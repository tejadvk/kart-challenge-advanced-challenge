package outbox

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Execer is the minimal interface for executing SQL (tx or pool)
type Execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// Repository handles outbox persistence
type Repository struct {
	pool *pgxpool.Pool
}

// NewRepository creates a new outbox repository
func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

// Insert adds an event to the outbox within the given transaction
func (r *Repository) Insert(ctx context.Context, exec Execer, event *Event) error {
	_, err := exec.Exec(ctx, `
		INSERT INTO outbox (id, aggregate_type, aggregate_id, event_type, payload, status)
		VALUES (gen_random_uuid(), $1, $2, $3, $4, $5)
	`, event.AggregateType, event.AggregateID, event.EventType, event.Payload, event.Status)
	return err
}

// PendingEvent is an event retrieved for processing
type PendingEvent struct {
	ID            string
	AggregateType string
	AggregateID   string
	EventType     string
	Payload       []byte
	CreatedAt     time.Time
}

// ClaimPending atomically claims up to limit pending events for processing
func (r *Repository) ClaimPending(ctx context.Context, limit int) ([]PendingEvent, error) {
	rows, err := r.pool.Query(ctx, `
		UPDATE outbox
		SET status = 'processing'
		WHERE id IN (
			SELECT id FROM outbox
			WHERE status = 'pending'
			ORDER BY created_at
			LIMIT $1
			FOR UPDATE SKIP LOCKED
		)
		RETURNING id, aggregate_type, aggregate_id, event_type, payload, created_at
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []PendingEvent
	for rows.Next() {
		var e PendingEvent
		err := rows.Scan(&e.ID, &e.AggregateType, &e.AggregateID, &e.EventType, &e.Payload, &e.CreatedAt)
		if err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

// MarkProcessed marks an event as successfully processed
func (r *Repository) MarkProcessed(ctx context.Context, id string) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE outbox SET status = 'processed', processed_at = NOW() WHERE id = $1
	`, id)
	return err
}

// MarkFailed marks an event as failed with error message
func (r *Repository) MarkFailed(ctx context.Context, id string, errMsg string) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE outbox SET status = 'failed', error_message = $2, processed_at = NOW() WHERE id = $1
	`, id, errMsg)
	return err
}

// ResetStaleProcessing resets events stuck in 'processing' (e.g. worker crashed)
// olderThanMinutes: reset events in processing state longer than this many minutes
func (r *Repository) ResetStaleProcessing(ctx context.Context, olderThanMinutes int) (int64, error) {
	tag, err := r.pool.Exec(ctx, `
		UPDATE outbox SET status = 'pending'
		WHERE status = 'processing' AND created_at < NOW() - ($1 || ' minutes')::interval
	`, olderThanMinutes)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// ResetFailedForRetry resets failed events to pending for retry after retryAfterMinutes.
// Use for transient broker failures. Returns count reset.
func (r *Repository) ResetFailedForRetry(ctx context.Context, retryAfterMinutes int, limit int) (int64, error) {
	tag, err := r.pool.Exec(ctx, `
		UPDATE outbox SET status = 'pending', error_message = NULL
		WHERE id IN (
			SELECT id FROM outbox
			WHERE status = 'failed' AND processed_at < NOW() - ($1 || ' minutes')::interval
			ORDER BY processed_at
			LIMIT $2
		)
	`, retryAfterMinutes, limit)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
