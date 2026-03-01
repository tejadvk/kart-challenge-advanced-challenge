package repository

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrCouponLimitExceeded is returned when coupon usage limit is reached
var ErrCouponLimitExceeded = errors.New("coupon usage limit exceeded")

// CouponUsageRepo handles coupon usage tracking
type CouponUsageRepo struct {
	pool *pgxpool.Pool
}

// NewCouponUsageRepo creates a new coupon usage repository
func NewCouponUsageRepo(pool *pgxpool.Pool) *CouponUsageRepo {
	return &CouponUsageRepo{pool: pool}
}

// CouponTx is the minimal interface for running queries in a transaction
type CouponTx interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// CheckAndIncrement verifies usage is under limit, then increments.
// Uses INSERT to ensure row exists, SELECT FOR UPDATE to lock, then UPDATE.
// Returns ErrCouponLimitExceeded if used_count >= maxUses (before increment).
// If maxUses is 0, no limit is enforced.
func (r *CouponUsageRepo) CheckAndIncrement(ctx context.Context, tx CouponTx, code string, maxUses int) error {
	// Ensure row exists for consistent locking
	_, _ = tx.Exec(ctx, `
		INSERT INTO coupon_usage (coupon_code, used_count)
		VALUES ($1, 0)
		ON CONFLICT (coupon_code) DO NOTHING
	`, code)

	if maxUses == 0 {
		_, err := tx.Exec(ctx, `
			UPDATE coupon_usage SET used_count = used_count + 1, updated_at = CURRENT_TIMESTAMP
			WHERE coupon_code = $1
		`, code)
		return err
	}

	var usedCount int
	err := tx.QueryRow(ctx, `
		SELECT used_count FROM coupon_usage WHERE coupon_code = $1 FOR UPDATE
	`, code).Scan(&usedCount)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			usedCount = 0
		} else {
			return err
		}
	}

	if usedCount >= maxUses {
		return ErrCouponLimitExceeded
	}

	_, err = tx.Exec(ctx, `
		UPDATE coupon_usage SET used_count = used_count + 1, updated_at = CURRENT_TIMESTAMP
		WHERE coupon_code = $1
	`, code)
	return err
}

// CouponUsageItem represents a coupon's usage for API responses
type CouponUsageItem struct {
	CouponCode string `json:"couponCode"`
	UsedCount  int    `json:"usedCount"`
}

// GetAll returns all coupon usage rows
func (r *CouponUsageRepo) GetAll(ctx context.Context) ([]CouponUsageItem, error) {
	rows, err := r.pool.Query(ctx, `SELECT coupon_code, used_count FROM coupon_usage ORDER BY coupon_code`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []CouponUsageItem
	for rows.Next() {
		var i CouponUsageItem
		if err := rows.Scan(&i.CouponCode, &i.UsedCount); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	return items, rows.Err()
}

// ResetUsage sets used_count to 0 for a coupon
func (r *CouponUsageRepo) ResetUsage(ctx context.Context, code string) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE coupon_usage SET used_count = 0, updated_at = CURRENT_TIMESTAMP
		WHERE coupon_code = $1
	`, code)
	return err
}
