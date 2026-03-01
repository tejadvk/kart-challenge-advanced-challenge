package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/yourusername/kart-challenge/backend-challenge/internal/models"
	"github.com/yourusername/kart-challenge/backend-challenge/internal/services"
)

// IdempotencyKeyHeader is the HTTP header for idempotent requests
const IdempotencyKeyHeader = "Idempotency-Key"

// OrderHandler handles order endpoints
type OrderHandler struct {
	service *services.OrderService
}

// NewOrderHandler creates a new order handler
func NewOrderHandler(service *services.OrderService) *OrderHandler {
	return &OrderHandler{service: service}
}

// PlaceOrder handles POST /order
func (h *OrderHandler) PlaceOrder(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	var req models.OrderReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}

	// Validate required fields
	if len(req.Items) == 0 {
		writeError(w, http.StatusUnprocessableEntity, "items is required and must not be empty")
		return
	}

	for _, item := range req.Items {
		if item.ProductID == "" {
			writeError(w, http.StatusUnprocessableEntity, "items[].productId is required")
			return
		}
		if item.Quantity <= 0 {
			writeError(w, http.StatusUnprocessableEntity, "items[].quantity must be positive")
			return
		}
	}

	idempotencyKey := strings.TrimSpace(r.Header.Get(IdempotencyKeyHeader))
	if idempotencyKey == "" {
		idempotencyKey = strings.TrimSpace(r.Header.Get("X-Idempotency-Key"))
	}
	if len(idempotencyKey) > 255 {
		writeError(w, http.StatusBadRequest, "Idempotency-Key must be at most 255 characters")
		return
	}

	order, err := h.service.PlaceOrder(r.Context(), req, idempotencyKey)
	if err != nil {
		var pnf *services.ProductNotFoundError
		if errors.As(err, &pnf) {
			writeError(w, http.StatusUnprocessableEntity, pnf.Error())
			return
		}
		var insf *services.InsufficientStockError
		if errors.As(err, &insf) {
			writeError(w, http.StatusConflict, insf.Error())
			return
		}
		if errors.Is(err, services.ErrEmptyOrder) {
			writeError(w, http.StatusUnprocessableEntity, "no valid products in order")
			return
		}
		if errors.Is(err, services.ErrCouponLimitExceeded) {
			writeError(w, http.StatusUnprocessableEntity, "coupon usage limit exceeded")
			return
		}
		if errors.Is(err, services.ErrIdempotencyConflict) {
			writeError(w, http.StatusConflict, "duplicate request in progress")
			return
		}
		writeError(w, http.StatusInternalServerError, "Failed to place order")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(order)
}

func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"message": message})
}
