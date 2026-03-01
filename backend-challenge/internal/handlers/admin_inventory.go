package handlers

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/gorilla/mux"
	"github.com/yourusername/kart-challenge/backend-challenge/internal/repository"
)

// AdminInventoryHandler handles admin inventory endpoints
type AdminInventoryHandler struct {
	inventoryRepo *repository.InventoryRepo
}

// NewAdminInventoryHandler creates a new admin inventory handler
func NewAdminInventoryHandler(inventoryRepo *repository.InventoryRepo) *AdminInventoryHandler {
	return &AdminInventoryHandler{inventoryRepo: inventoryRepo}
}

// ListInventory handles GET /admin/inventory
func (h *AdminInventoryHandler) ListInventory(w http.ResponseWriter, r *http.Request) {
	items, err := h.inventoryRepo.GetAll(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to fetch inventory")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(items)
}

// UpdateInventory handles PUT /admin/inventory/{productId}
func (h *AdminInventoryHandler) UpdateInventory(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	productID := strings.TrimSpace(vars["productId"])
	if productID == "" {
		writeError(w, http.StatusBadRequest, "productId is required")
		return
	}

	var body struct {
		Quantity int `json:"quantity"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}
	if body.Quantity < 0 {
		writeError(w, http.StatusUnprocessableEntity, "quantity must be non-negative")
		return
	}

	if err := h.inventoryRepo.SetQuantity(r.Context(), productID, body.Quantity); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to update inventory")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"productId": productID,
		"quantity":  body.Quantity,
	})
}
