package repository

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/yourusername/kart-challenge/backend-challenge/internal/models"
)

// ProductExecer is the minimal interface for product writes (tx or pool)
type ProductExecer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// ProductQueryExecer is the minimal interface for product reads within a transaction
type ProductQueryExecer interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// ExistAllInTx checks that all product IDs exist in the products table within the transaction.
// Returns (false, firstMissingID, nil) if any product was deleted between resolution and reserve.
// Uses a single query (EXCEPT) to find missing IDs — O(1) DB round-trips instead of O(n).
func (r *ProductDBRepo) ExistAllInTx(ctx context.Context, q ProductQueryExecer, productIDs []string) (allExist bool, firstMissingID string, err error) {
	if len(productIDs) == 0 {
		return true, "", nil
	}
	var missing string
	err = q.QueryRow(ctx, `
		SELECT id FROM unnest($1::text[]) AS t(id)
		EXCEPT SELECT id FROM products WHERE id = ANY($1)
		LIMIT 1
	`, productIDs).Scan(&missing)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return true, "", nil
		}
		return false, "", err
	}
	return false, missing, nil
}

// ProductDBRepo handles product persistence in PostgreSQL.
// When readPool is set (read replica), GetByID and GetAll use it; writes use pool.
type ProductDBRepo struct {
	pool     *pgxpool.Pool
	readPool *pgxpool.Pool
}

// NewProductDBRepo creates a new product DB repository (reads and writes use same pool)
func NewProductDBRepo(pool *pgxpool.Pool) *ProductDBRepo {
	return &ProductDBRepo{pool: pool}
}

// NewProductDBRepoWithReplica creates a repo that routes reads to replica, writes to primary
func NewProductDBRepoWithReplica(writePool, readPool *pgxpool.Pool) *ProductDBRepo {
	return &ProductDBRepo{pool: writePool, readPool: readPool}
}

func (r *ProductDBRepo) poolForRead() *pgxpool.Pool {
	if r.readPool != nil {
		return r.readPool
	}
	return r.pool
}

// GetByID returns a product by ID from DB, or nil if not found (uses read replica if configured)
func (r *ProductDBRepo) GetByID(ctx context.Context, id string) (*models.Product, error) {
	var pid, name, category string
	var price float64
	var imageJSON []byte
	err := r.poolForRead().QueryRow(ctx, `
		SELECT id, name, price, category, image FROM products WHERE id = $1
	`, id).Scan(&pid, &name, &price, &category, &imageJSON)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	var img models.ProductImage
	_ = json.Unmarshal(imageJSON, &img)
	return &models.Product{
		ID:       pid,
		Name:     name,
		Price:    price,
		Category: category,
		Image:    img,
	}, nil
}

// GetAll returns all products from DB (uses read replica if configured)
func (r *ProductDBRepo) GetAll(ctx context.Context) ([]models.Product, error) {
	rows, err := r.poolForRead().Query(ctx, `
		SELECT id, name, price, category, image FROM products ORDER BY id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var products []models.Product
	for rows.Next() {
		var id, name, category string
		var price float64
		var imageJSON []byte
		if err := rows.Scan(&id, &name, &price, &category, &imageJSON); err != nil {
			return nil, err
		}
		var img models.ProductImage
		_ = json.Unmarshal(imageJSON, &img)
		products = append(products, models.Product{
			ID:       id,
			Name:     name,
			Price:    price,
			Category: category,
			Image:    img,
		})
	}
	return products, rows.Err()
}

// Create inserts a product into DB
func (r *ProductDBRepo) Create(ctx context.Context, p *models.Product) error {
	imgJSON, _ := json.Marshal(p.Image)
	_, err := r.pool.Exec(ctx, `
		INSERT INTO products (id, name, price, category, image)
		VALUES ($1, $2, $3, $4, $5)
	`, p.ID, p.Name, p.Price, p.Category, imgJSON)
	return err
}

// Update updates a product in DB
func (r *ProductDBRepo) Update(ctx context.Context, p *models.Product) error {
	imgJSON, _ := json.Marshal(p.Image)
	_, err := r.pool.Exec(ctx, `
		UPDATE products SET name = $2, price = $3, category = $4, image = $5, updated_at = CURRENT_TIMESTAMP
		WHERE id = $1
	`, p.ID, p.Name, p.Price, p.Category, imgJSON)
	return err
}

// Upsert inserts or updates a product
func (r *ProductDBRepo) Upsert(ctx context.Context, p *models.Product) error {
	imgJSON, _ := json.Marshal(p.Image)
	_, err := r.pool.Exec(ctx, `
		INSERT INTO products (id, name, price, category, image)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (id) DO UPDATE SET
			name = EXCLUDED.name,
			price = EXCLUDED.price,
			category = EXCLUDED.category,
			image = EXCLUDED.image,
			updated_at = CURRENT_TIMESTAMP
	`, p.ID, p.Name, p.Price, p.Category, imgJSON)
	return err
}

// Delete removes a product from DB
func (r *ProductDBRepo) Delete(ctx context.Context, id string) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM products WHERE id = $1`, id)
	return err
}

// UpsertExec inserts or updates a product using the given execer (for use in transaction)
func (r *ProductDBRepo) UpsertExec(ctx context.Context, exec ProductExecer, p *models.Product) error {
	imgJSON, _ := json.Marshal(p.Image)
	_, err := exec.Exec(ctx, `
		INSERT INTO products (id, name, price, category, image)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (id) DO UPDATE SET
			name = EXCLUDED.name,
			price = EXCLUDED.price,
			category = EXCLUDED.category,
			image = EXCLUDED.image,
			updated_at = CURRENT_TIMESTAMP
	`, p.ID, p.Name, p.Price, p.Category, imgJSON)
	return err
}

// DeleteExec removes a product using the given execer (for use in transaction)
func (r *ProductDBRepo) DeleteExec(ctx context.Context, exec ProductExecer, id string) error {
	_, err := exec.Exec(ctx, `DELETE FROM products WHERE id = $1`, id)
	return err
}
