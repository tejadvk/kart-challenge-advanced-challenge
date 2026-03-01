package config

import (
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"sync"
)

// CouponLimitsConfig holds configurable coupon usage limits
type CouponLimitsConfig struct {
	mu sync.RWMutex

	// DefaultMaxUses is the global limit when no per-code limit is set (0 = unlimited)
	DefaultMaxUses int

	// PerCode limits override DefaultMaxUses for specific codes (e.g. {"HAPPYHOURS": 50})
	PerCode map[string]int
}

// NewCouponLimitsConfig loads config from environment and optional file.
// Environment:
//   - COUPON_MAX_USES: default limit (e.g. 100), 0 = unlimited
//   - COUPON_LIMITS_FILE: path to JSON file with per-code limits
//   - COUPON_LIMITS_JSON: inline JSON e.g. {"HAPPYHOURS":50,"BUYGETONE":100}
func NewCouponLimitsConfig() *CouponLimitsConfig {
	cfg := &CouponLimitsConfig{
		DefaultMaxUses: 0, // unlimited by default
		PerCode:       make(map[string]int),
	}

	if v := os.Getenv("COUPON_MAX_USES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			cfg.DefaultMaxUses = n
		}
	}

	// Inline JSON takes precedence
	if v := os.Getenv("COUPON_LIMITS_JSON"); v != "" {
		var m map[string]int
		if err := json.Unmarshal([]byte(v), &m); err == nil {
			cfg.PerCode = m
		}
	}

	// File can add/override
	if path := os.Getenv("COUPON_LIMITS_FILE"); path != "" {
		if data, err := os.ReadFile(path); err == nil {
			var m map[string]int
			if err := json.Unmarshal(data, &m); err == nil {
				for k, v := range m {
					cfg.PerCode[k] = v
				}
			}
		}
	}

	return cfg
}

// GetMaxUses returns the usage limit for a coupon code.
// Per-code limit overrides default. Returns 0 for unlimited.
func (c *CouponLimitsConfig) GetMaxUses(code string) int {
	c.mu.RLock()
	defer c.mu.RUnlock()

	code = strings.TrimSpace(strings.ToUpper(code))
	if limit, ok := c.PerCode[code]; ok {
		return limit
	}
	return c.DefaultMaxUses
}

// SetDefaultMaxUses updates the default limit (for tests or reload)
func (c *CouponLimitsConfig) SetDefaultMaxUses(n int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.DefaultMaxUses = n
}

// SetPerCodeLimit sets a per-code limit
func (c *CouponLimitsConfig) SetPerCodeLimit(code string, limit int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.PerCode == nil {
		c.PerCode = make(map[string]int)
	}
	c.PerCode[strings.ToUpper(code)] = limit
}
