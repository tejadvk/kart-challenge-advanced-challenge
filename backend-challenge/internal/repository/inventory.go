package repository

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrProductNotInInventory is returned when a product has no inventory row
var ErrProductNotInInventory = errors.New("product not in inventory")

// InventoryRepo handles inventory operations
type InventoryRepo struct {
	pool *pgxpool.Pool
}

// NewInventoryRepo creates a new inventory repository
func NewInventoryRepo(pool *pgxpool.Pool) *InventoryRepo {
	return &InventoryRepo{pool: pool}
}

// Tx is the minimal interface for running queries in a transaction
type Tx interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// ReserveOptimistic atomically decrements inventory using optimistic locking.
// No SELECT FOR UPDATE: uses a single UPDATE with predicate, avoiding row-level
// lock contention. Multiple concurrent orders can proceed in parallel.
//
// Returns: (reserved=true, available=0, nil) on success.
// Returns: (reserved=false, available, ErrProductNotInInventory) if no inventory row.
// Returns: (reserved=false, available, nil) if insufficient stock (available = current qty).
func (r *InventoryRepo) ReserveOptimistic(ctx context.Context, tx Tx, productID string, quantity int) (reserved bool, available int, err error) {
	tag, err := tx.Exec(ctx, `
		UPDATE inventory 
		SET quantity = quantity - $1, updated_at = CURRENT_TIMESTAMP 
		WHERE product_id = $2 AND quantity >= $1
	`, quantity, productID)
	if err != nil {
		return false, 0, err
	}
	if tag.RowsAffected() > 0 {
		return true, 0, nil
	}

	// 0 rows: either no row exists or insufficient quantity — fetch for error reporting
	var qty int
	rowErr := tx.QueryRow(ctx, `SELECT quantity FROM inventory WHERE product_id = $1`, productID).Scan(&qty)
	if rowErr != nil {
		if errors.Is(rowErr, pgx.ErrNoRows) {
			return false, 0, ErrProductNotInInventory
		}
		return false, 0, rowErr
	}
	return false, qty, nil
}

// Reserve checks and decrements inventory within a transaction (legacy, uses optimistic UPDATE).
// Deprecated: use ReserveOptimistic for better concurrency.
func (r *InventoryRepo) Reserve(ctx context.Context, tx Tx, productID string, quantity int) error {
	reserved, _, err := r.ReserveOptimistic(ctx, tx, productID, quantity)
	if err != nil {
		return err
	}
	if !reserved {
		return ErrProductNotInInventory
	}
	return nil
}

// EnsureExists ensures an inventory row exists for the product (for new products from admin)
func (r *InventoryRepo) EnsureExists(ctx context.Context, productID string, defaultQty int) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO inventory (product_id, quantity)
		VALUES ($1, $2)
		ON CONFLICT (product_id) DO NOTHING
	`, productID, defaultQty)
	return err
}

// InventoryItem represents a product's inventory (JSON uses camelCase for API)
type InventoryItem struct {
	ProductID string `json:"productId"`
	Quantity  int    `json:"quantity"`
}

// GetAll returns all inventory rows
func (r *InventoryRepo) GetAll(ctx context.Context) ([]InventoryItem, error) {
	rows, err := r.pool.Query(ctx, `SELECT product_id, quantity FROM inventory ORDER BY product_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []InventoryItem
	for rows.Next() {
		var i InventoryItem
		if err := rows.Scan(&i.ProductID, &i.Quantity); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	return items, rows.Err()
}

// SetQuantity sets the quantity for a product (creates row if not exists)
func (r *InventoryRepo) SetQuantity(ctx context.Context, productID string, quantity int) error {
	if quantity < 0 {
		quantity = 0
	}
	_, err := r.pool.Exec(ctx, `
		INSERT INTO inventory (product_id, quantity)
		VALUES ($1, $2)
		ON CONFLICT (product_id) DO UPDATE SET
			quantity = EXCLUDED.quantity,
			updated_at = CURRENT_TIMESTAMP
	`, productID, quantity)
	return err
}
