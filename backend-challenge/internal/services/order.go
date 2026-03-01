package services

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/yourusername/kart-challenge/backend-challenge/internal/config"
	"github.com/yourusername/kart-challenge/backend-challenge/internal/models"
	"github.com/yourusername/kart-challenge/backend-challenge/internal/outbox"
	"github.com/yourusername/kart-challenge/backend-challenge/internal/repository"
	"github.com/yourusername/kart-challenge/backend-challenge/pkg/coupon"
)

const (
	maxPlaceOrderRetries = 4
	retryBaseDelay       = 5 * time.Millisecond
)

// Sentinel errors for order validation
var (
	ErrEmptyOrder = errors.New("empty order: no valid products")
)

// ProductNotFoundError is returned when a product ID in the order does not exist
type ProductNotFoundError struct {
	ProductID string
}

func (e *ProductNotFoundError) Error() string {
	return fmt.Sprintf("product not found: %s", e.ProductID)
}

// InsufficientStockError is returned when inventory is not enough for the order
type InsufficientStockError struct {
	ProductID string
	Requested int
	Available int
}

func (e *InsufficientStockError) Error() string {
	return fmt.Sprintf("insufficient stock for product %s: requested %d, available %d", e.ProductID, e.Requested, e.Available)
}

// ErrCouponLimitExceeded is returned when coupon usage limit is reached
var ErrCouponLimitExceeded = errors.New("coupon usage limit exceeded")

// ErrIdempotencyConflict is returned when a duplicate request is in progress
var ErrIdempotencyConflict = errors.New("duplicate request in progress")

const (
	CodeHAPPYHOURS = "HAPPYHOURS"
	CodeHAPPYHRS   = "HAPPYHRS"
	CodeBUYGETONE  = "BUYGETONE"
)

// OrderService handles order processing and discount logic
type OrderService struct {
	pool            *pgxpool.Pool
	productStore    repository.ProductStore
	productDB       *repository.ProductDBRepo
	inventoryRepo   *repository.InventoryRepo
	orderRepo       *repository.OrderRepo
	couponUsageRepo   *repository.CouponUsageRepo
	couponLimitsRepo  *repository.CouponLimitsRepo
	idempotencyRepo   *repository.IdempotencyRepo
	outboxRepo        *outbox.Repository
	couponVal         *coupon.Validator
	couponConfig      *config.CouponLimitsConfig
}

// NewOrderService creates a new order service
func NewOrderService(
	pool *pgxpool.Pool,
	productStore repository.ProductStore,
	productDB *repository.ProductDBRepo,
	inventoryRepo *repository.InventoryRepo,
	orderRepo *repository.OrderRepo,
	couponUsageRepo *repository.CouponUsageRepo,
	couponLimitsRepo *repository.CouponLimitsRepo,
	idempotencyRepo *repository.IdempotencyRepo,
	outboxRepo *outbox.Repository,
	couponVal *coupon.Validator,
	couponConfig *config.CouponLimitsConfig,
) *OrderService {
	return &OrderService{
		pool:             pool,
		productStore:     productStore,
		productDB:        productDB,
		inventoryRepo:    inventoryRepo,
		orderRepo:        orderRepo,
		couponUsageRepo:  couponUsageRepo,
		couponLimitsRepo: couponLimitsRepo,
		idempotencyRepo:  idempotencyRepo,
		outboxRepo:       outboxRepo,
		couponVal:        couponVal,
		couponConfig:     couponConfig,
	}
}

// getMaxUses returns the usage limit for a coupon. DB limits override config.
func (s *OrderService) getMaxUses(ctx context.Context, code string) int {
	if s.couponLimitsRepo != nil {
		if limit, ok, err := s.couponLimitsRepo.GetMaxUses(ctx, code); err == nil && ok {
			return limit
		}
	}
	return s.couponConfig.GetMaxUses(code)
}

// PlaceOrder processes an order request and returns the order with totals and discounts.
// Uses optimistic locking (no SELECT FOR UPDATE) to minimize contention; retries on
// InsufficientStock when a concurrent order may have rolled back.
// If idempotencyKey is non-empty, duplicate requests return the cached response.
func (s *OrderService) PlaceOrder(ctx context.Context, req models.OrderReq, idempotencyKey string) (*models.Order, error) {
	idempotencyKey = strings.TrimSpace(idempotencyKey)

	var lastErr error
	for attempt := 0; attempt < maxPlaceOrderRetries; attempt++ {
		order, err := s.placeOrderOnce(ctx, req, idempotencyKey)
		if err == nil {
			return order, nil
		}
		lastErr = err

		// Retry only on InsufficientStock (concurrent order may have rolled back)
		var insuf *InsufficientStockError
		if !errors.As(err, &insuf) {
			return nil, err
		}

		if attempt < maxPlaceOrderRetries-1 {
			delay := retryBaseDelay * time.Duration(1<<attempt)
			jitter := time.Duration(rand.Int63n(int64(delay)))
			select {
			case <-time.After(delay + jitter):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
	}
	return nil, lastErr
}

// placeOrderOnce runs a single attempt to place the order.
func (s *OrderService) placeOrderOnce(ctx context.Context, req models.OrderReq, idempotencyKey string) (*models.Order, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	// Idempotency: check for duplicate request
	if idempotencyKey != "" {
		if cached, ok := s.idempotencyRepo.Get(ctx, tx, idempotencyKey); ok {
			return cached, nil
		}
		processing, err := s.idempotencyRepo.IsProcessing(ctx, tx, idempotencyKey)
		if err != nil {
			return nil, err
		}
		if processing {
			return nil, ErrIdempotencyConflict
		}
		inserted, err := s.idempotencyRepo.Reserve(ctx, tx, idempotencyKey)
		if err != nil {
			return nil, err
		}
		if !inserted {
			// Another request reserved it - check if they completed
			if cached, ok := s.idempotencyRepo.Get(ctx, tx, idempotencyKey); ok {
				return cached, nil
			}
			return nil, ErrIdempotencyConflict
		}
	}

	// Aggregate quantities by product
	productQty := make(map[string]int)
	for _, item := range req.Items {
		if item.Quantity <= 0 {
			continue
		}
		product, err := s.productStore.GetByID(ctx, item.ProductID)
		if err != nil {
			return nil, err
		}
		if product == nil {
			return nil, &ProductNotFoundError{ProductID: item.ProductID}
		}
		productQty[item.ProductID] += item.Quantity
	}

	if len(productQty) == 0 {
		return nil, ErrEmptyOrder
	}

	// Sort product IDs for deterministic order
	productIDs := make([]string, 0, len(productQty))
	for id := range productQty {
		productIDs = append(productIDs, id)
	}
	sort.Strings(productIDs)

	// Validate products still exist inside TX (guards against admin delete between resolution and reserve)
	if s.productDB != nil {
		exist, missingID, err := s.productDB.ExistAllInTx(ctx, tx, productIDs)
		if err != nil {
			return nil, err
		}
		if !exist {
			return nil, &ProductNotFoundError{ProductID: missingID}
		}
	}

	order := &models.Order{
		ID:        uuid.New().String(),
		Items:     make([]models.OrderItem, 0, len(productQty)),
		Products:  make([]models.Product, 0, len(productQty)),
		Total:     0,
		Discounts: 0,
	}

	var subtotal float64
	var lowestPrice *float64

	// Optimistic reserve: atomic UPDATE, no blocking (avoids SELECT FOR UPDATE contention)
	for _, productID := range productIDs {
		qty := productQty[productID]
		product, err := s.productStore.GetByID(ctx, productID)
		if err != nil {
			return nil, err
		}
		if product == nil {
			return nil, &ProductNotFoundError{ProductID: productID}
		}

		reserved, available, err := s.inventoryRepo.ReserveOptimistic(ctx, tx, productID, qty)
		if err != nil {
			if errors.Is(err, repository.ErrProductNotInInventory) {
				return nil, &InsufficientStockError{ProductID: productID, Requested: qty, Available: 0}
			}
			return nil, err
		}
		if !reserved {
			return nil, &InsufficientStockError{ProductID: productID, Requested: qty, Available: available}
		}

		order.Items = append(order.Items, models.OrderItem{ProductID: productID, Quantity: qty})
		order.Products = append(order.Products, *product)
		subtotal += product.Price * float64(qty)

		if lowestPrice == nil || product.Price < *lowestPrice {
			lowestPrice = &product.Price
		}
	}

	order.Total = subtotal

	// Apply coupon discount if valid (with usage limit check)
	if req.CouponCode != "" {
		code := strings.TrimSpace(strings.ToUpper(req.CouponCode))
		if s.couponVal.IsValid(req.CouponCode) {
			maxUses := s.getMaxUses(ctx, code)
			if err := s.couponUsageRepo.CheckAndIncrement(ctx, tx, code, maxUses); err != nil {
				if errors.Is(err, repository.ErrCouponLimitExceeded) {
					return nil, ErrCouponLimitExceeded
				}
				return nil, err
			}
			switch code {
			case CodeHAPPYHOURS, CodeHAPPYHRS:
				order.Discounts = subtotal * 0.18
			case CodeBUYGETONE:
				if lowestPrice != nil {
					order.Discounts = *lowestPrice
				}
			}
		}
	}

	order.Total = subtotal - order.Discounts
	if order.Total < 0 {
		order.Total = 0
	}

	// Persist order
	if err := s.orderRepo.Create(ctx, tx, order); err != nil {
		return nil, err
	}

	// Complete idempotency record
	if idempotencyKey != "" {
		if err := s.idempotencyRepo.Complete(ctx, tx, idempotencyKey, order); err != nil {
			return nil, err
		}
	}

	// Outbox: emit OrderPlaced event in same transaction
	if s.outboxRepo != nil {
		ev, err := outbox.NewEvent(outbox.AggregateOrder, order.ID, outbox.EventOrderPlaced, order)
		if err != nil {
			return nil, err
		}
		if err := s.outboxRepo.Insert(ctx, tx, ev); err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	return order, nil
}
