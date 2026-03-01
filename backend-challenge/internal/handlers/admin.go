package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/gorilla/mux"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/yourusername/kart-challenge/backend-challenge/internal/models"
	"github.com/yourusername/kart-challenge/backend-challenge/internal/outbox"
	"github.com/yourusername/kart-challenge/backend-challenge/internal/repository"
)

// AdminProductHandler handles admin product endpoints
type AdminProductHandler struct {
	cache         *repository.ProductCache
	productDB     *repository.ProductDBRepo
	inventoryRepo *repository.InventoryRepo
	outboxRepo    *outbox.Repository
	pool          *pgxpool.Pool
}

// NewAdminProductHandler creates a new admin product handler
func NewAdminProductHandler(cache *repository.ProductCache, productDB *repository.ProductDBRepo, inventoryRepo *repository.InventoryRepo, outboxRepo *outbox.Repository, pool *pgxpool.Pool) *AdminProductHandler {
	return &AdminProductHandler{
		cache:         cache,
		productDB:     productDB,
		inventoryRepo: inventoryRepo,
		outboxRepo:    outboxRepo,
		pool:          pool,
	}
}

// setProductAtomic writes product + outbox in one transaction, then syncs Redis
func (h *AdminProductHandler) setProductAtomic(ctx context.Context, p *models.Product, eventType string) error {
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := h.productDB.UpsertExec(ctx, tx, p); err != nil {
		return err
	}
	if h.outboxRepo != nil {
		ev, err := outbox.NewEvent(outbox.AggregateProduct, p.ID, eventType, p)
		if err != nil {
			return err
		}
		if err := h.outboxRepo.Insert(ctx, tx, ev); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	return h.cache.SetRedisOnly(ctx, p)
}

// deleteProductAtomic deletes product + outbox in one transaction, then syncs Redis
func (h *AdminProductHandler) deleteProductAtomic(ctx context.Context, productID string) error {
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := h.productDB.DeleteExec(ctx, tx, productID); err != nil {
		return err
	}
	if h.outboxRepo != nil {
		ev, err := outbox.NewEvent(outbox.AggregateProduct, productID, outbox.EventProductDeleted, map[string]string{"id": productID})
		if err != nil {
			return err
		}
		if err := h.outboxRepo.Insert(ctx, tx, ev); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	return h.cache.DeleteRedisOnly(ctx, productID)
}

// CreateProduct handles POST /admin/product
func (h *AdminProductHandler) CreateProduct(w http.ResponseWriter, r *http.Request) {
	var p models.Product
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}
	if p.ID == "" || p.Name == "" {
		writeError(w, http.StatusUnprocessableEntity, "id and name are required")
		return
	}
	if p.Price < 0 {
		writeError(w, http.StatusUnprocessableEntity, "price must be non-negative")
		return
	}

	if err := h.setProductAtomic(r.Context(), &p, outbox.EventProductCreated); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to create product")
		return
	}
	// Ensure inventory row exists for new product (default 0, admin can update later)
	_ = h.inventoryRepo.EnsureExists(r.Context(), p.ID, 0)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(p)
}

// UpdateProduct handles PUT /admin/product/{productId}
func (h *AdminProductHandler) UpdateProduct(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	productID := strings.TrimSpace(vars["productId"])
	if productID == "" {
		writeError(w, http.StatusBadRequest, "productId is required")
		return
	}

	var p models.Product
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}
	p.ID = productID
	if p.Name == "" {
		writeError(w, http.StatusUnprocessableEntity, "name is required")
		return
	}
	if p.Price < 0 {
		writeError(w, http.StatusUnprocessableEntity, "price must be non-negative")
		return
	}

	if err := h.setProductAtomic(r.Context(), &p, outbox.EventProductUpdated); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to update product")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(p)
}

// PatchProduct handles PATCH /admin/product/{productId} - partial update
func (h *AdminProductHandler) PatchProduct(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	productID := strings.TrimSpace(vars["productId"])
	if productID == "" {
		writeError(w, http.StatusBadRequest, "productId is required")
		return
	}

	existing, err := h.cache.GetByID(r.Context(), productID)
	if err != nil || existing == nil {
		writeError(w, http.StatusNotFound, "product not found")
		return
	}

	var patch struct {
		Name     *string               `json:"name"`
		Price    *float64              `json:"price"`
		Category *string               `json:"category"`
		Image    *models.ProductImage `json:"image"`
	}
	if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}

	if patch.Name != nil {
		existing.Name = *patch.Name
	}
	if patch.Price != nil {
		if *patch.Price < 0 {
			writeError(w, http.StatusUnprocessableEntity, "price must be non-negative")
			return
		}
		existing.Price = *patch.Price
	}
	if patch.Category != nil {
		existing.Category = *patch.Category
	}
	if patch.Image != nil {
		existing.Image = *patch.Image
	}

	if err := h.setProductAtomic(r.Context(), existing, outbox.EventProductUpdated); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to update product")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(existing)
}

// DeleteProduct handles DELETE /admin/product/{productId}
func (h *AdminProductHandler) DeleteProduct(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	productID := strings.TrimSpace(vars["productId"])
	if productID == "" {
		writeError(w, http.StatusBadRequest, "productId is required")
		return
	}

	if err := h.deleteProductAtomic(r.Context(), productID); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to delete product")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
