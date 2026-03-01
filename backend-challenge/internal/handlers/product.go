package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/yourusername/kart-challenge/backend-challenge/internal/repository"
)

// ProductHandler handles product endpoints
type ProductHandler struct {
	store repository.ProductStore
}

// NewProductHandler creates a new product handler
func NewProductHandler(store repository.ProductStore) *ProductHandler {
	return &ProductHandler{store: store}
}

// ListProducts handles GET /product
func (h *ProductHandler) ListProducts(w http.ResponseWriter, r *http.Request) {
	products, err := h.store.GetAll(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to fetch products")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(products)
}

// GetProduct handles GET /product/{productId}
func (h *ProductHandler) GetProduct(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	productID := vars["productId"]

	if productID == "" {
		writeError(w, http.StatusBadRequest, "productId is required")
		return
	}

	product, err := h.store.GetByID(r.Context(), productID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to fetch product")
		return
	}
	if product == nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(product)
}
