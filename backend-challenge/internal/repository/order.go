package repository

import (
	"context"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/yourusername/kart-challenge/backend-challenge/internal/models"
)

// OrderRepo handles order persistence
type OrderRepo struct {
	pool *pgxpool.Pool
}

// NewOrderRepo creates a new order repository
func NewOrderRepo(pool *pgxpool.Pool) *OrderRepo {
	return &OrderRepo{pool: pool}
}

// OrderTx is the minimal interface for running queries in a transaction
type OrderTx interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// Create persists an order and its items within a transaction
func (r *OrderRepo) Create(ctx context.Context, tx OrderTx, order *models.Order) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO orders (id, total, discounts)
		VALUES ($1, $2, $3)
	`, order.ID, order.Total, order.Discounts)
	if err != nil {
		return err
	}

	for _, item := range order.Items {
		_, err = tx.Exec(ctx, `
			INSERT INTO order_items (order_id, product_id, quantity)
			VALUES ($1, $2, $3)
		`, order.ID, item.ProductID, item.Quantity)
		if err != nil {
			return err
		}
	}

	return nil
}
