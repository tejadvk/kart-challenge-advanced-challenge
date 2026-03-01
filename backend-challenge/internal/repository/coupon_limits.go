package repository

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// CouponLimitsRepo handles admin-configurable coupon usage limits (persisted in DB)
type CouponLimitsRepo struct {
	pool *pgxpool.Pool
}

// NewCouponLimitsRepo creates a new coupon limits repository
func NewCouponLimitsRepo(pool *pgxpool.Pool) *CouponLimitsRepo {
	return &CouponLimitsRepo{pool: pool}
}

// GetMaxUses returns the max_uses for a coupon from DB. (limit, exists).
// Returns (0, false) if not in DB (caller should fall back to config).
func (r *CouponLimitsRepo) GetMaxUses(ctx context.Context, code string) (int, bool, error) {
	code = strings.TrimSpace(strings.ToUpper(code))
	var maxUses int
	err := r.pool.QueryRow(ctx, `SELECT max_uses FROM coupon_limits WHERE coupon_code = $1`, code).Scan(&maxUses)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, false, nil
		}
		return 0, false, err
	}
	return maxUses, true, nil
}

// SetLimit upserts the max_uses for a coupon. 0 = unlimited.
func (r *CouponLimitsRepo) SetLimit(ctx context.Context, code string, maxUses int) error {
	code = strings.TrimSpace(strings.ToUpper(code))
	if maxUses < 0 {
		maxUses = 0
	}
	_, err := r.pool.Exec(ctx, `
		INSERT INTO coupon_limits (coupon_code, max_uses)
		VALUES ($1, $2)
		ON CONFLICT (coupon_code) DO UPDATE SET
			max_uses = EXCLUDED.max_uses,
			updated_at = CURRENT_TIMESTAMP
	`, code, maxUses)
	return err
}

// GetAll returns all coupon limits (code -> max_uses)
func (r *CouponLimitsRepo) GetAll(ctx context.Context) (map[string]int, error) {
	rows, err := r.pool.Query(ctx, `SELECT coupon_code, max_uses FROM coupon_limits`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := make(map[string]int)
	for rows.Next() {
		var code string
		var maxUses int
		if err := rows.Scan(&code, &maxUses); err != nil {
			return nil, err
		}
		m[code] = maxUses
	}
	return m, rows.Err()
}
