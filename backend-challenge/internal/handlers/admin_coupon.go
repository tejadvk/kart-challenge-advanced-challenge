package handlers

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/gorilla/mux"
	"github.com/yourusername/kart-challenge/backend-challenge/internal/repository"
)

// AdminCouponHandler handles admin coupon endpoints
type AdminCouponHandler struct {
	couponUsageRepo *repository.CouponUsageRepo
	couponLimitsRepo *repository.CouponLimitsRepo
}

// NewAdminCouponHandler creates a new admin coupon handler
func NewAdminCouponHandler(couponUsageRepo *repository.CouponUsageRepo, couponLimitsRepo *repository.CouponLimitsRepo) *AdminCouponHandler {
	return &AdminCouponHandler{
		couponUsageRepo:  couponUsageRepo,
		couponLimitsRepo: couponLimitsRepo,
	}
}

// CouponItem is the response shape for GET /admin/coupons
type CouponItem struct {
	CouponCode string `json:"couponCode"`
	UsedCount  int    `json:"usedCount"`
	MaxUses    *int   `json:"maxUses,omitempty"` // nil = use default from config
}

// ListCoupons handles GET /admin/coupons
func (h *AdminCouponHandler) ListCoupons(w http.ResponseWriter, r *http.Request) {
	usage, err := h.couponUsageRepo.GetAll(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to fetch coupon usage")
		return
	}
	limits, err := h.couponLimitsRepo.GetAll(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to fetch coupon limits")
		return
	}
	// Build map of usage by code
	usageByCode := make(map[string]int)
	for _, u := range usage {
		usageByCode[u.CouponCode] = u.UsedCount
	}
	// Merge: all from usage + any limits-only codes
	seen := make(map[string]bool)
	items := make([]CouponItem, 0) // empty slice, not nil, so JSON encodes as [] not null
	for code, used := range usageByCode {
		seen[code] = true
		item := CouponItem{CouponCode: code, UsedCount: used}
		if max, ok := limits[code]; ok {
			item.MaxUses = &max
		}
		items = append(items, item)
	}
	for code, max := range limits {
		if !seen[code] {
			items = append(items, CouponItem{CouponCode: code, UsedCount: 0, MaxUses: &max})
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(items)
}

// UpdateCouponLimit handles PUT /admin/coupons/{code}/limit
func (h *AdminCouponHandler) UpdateCouponLimit(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	code := strings.TrimSpace(strings.ToUpper(vars["code"]))
	if code == "" {
		writeError(w, http.StatusBadRequest, "coupon code is required")
		return
	}
	var body struct {
		MaxUses int `json:"maxUses"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}
	if body.MaxUses < 0 {
		writeError(w, http.StatusUnprocessableEntity, "maxUses must be non-negative (0 = unlimited)")
		return
	}
	if err := h.couponLimitsRepo.SetLimit(r.Context(), code, body.MaxUses); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to update coupon limit")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"couponCode": code,
		"maxUses":    body.MaxUses,
	})
}

// ResetCouponUsage handles PUT /admin/coupons/{code}/reset
func (h *AdminCouponHandler) ResetCouponUsage(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	code := strings.TrimSpace(strings.ToUpper(vars["code"]))
	if code == "" {
		writeError(w, http.StatusBadRequest, "coupon code is required")
		return
	}
	if err := h.couponUsageRepo.ResetUsage(r.Context(), code); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to reset coupon usage")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"couponCode": code,
		"usedCount":  0,
	})
}
