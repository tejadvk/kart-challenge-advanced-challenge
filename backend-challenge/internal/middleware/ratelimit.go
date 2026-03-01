package middleware

import (
	"net/http"
	"os"
	"strconv"
	"sync"

	"golang.org/x/time/rate"
)

// RateLimitConfig holds rate limit configuration from env
func RateLimitConfig() (rps int, enabled bool) {
	v := os.Getenv("RATE_LIMIT_RPS")
	if v == "" {
		return 0, false
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

var (
	globalLimiter     *rate.Limiter
	globalLimiterOnce sync.Once
)

func getGlobalLimiter(rps int) *rate.Limiter {
	globalLimiterOnce.Do(func() {
		burst := rps * 2
		if burst < 2 {
			burst = 2
		}
		globalLimiter = rate.NewLimiter(rate.Limit(rps), burst)
	})
	return globalLimiter
}

// Paths exempt from rate limiting (health probes, metrics for load balancers)
var exemptPaths = []string{"/health/live", "/health/ready", "/metrics"}

func isExemptFromRateLimit(path string) bool {
	for _, p := range exemptPaths {
		if path == p {
			return true
		}
	}
	return false
}

// RateLimit returns a middleware that limits requests per second when RATE_LIMIT_RPS is set.
// /health/live, /health/ready, and /metrics are exempt so load balancers and orchestrators work.
// If RATE_LIMIT_RPS is 0 or unset, requests pass through unchanged.
func RateLimit(next http.Handler) http.Handler {
	rps, enabled := RateLimitConfig()
	if !enabled {
		return next
	}
	limiter := getGlobalLimiter(rps)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isExemptFromRateLimit(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		if !limiter.Allow() {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"message":"Rate limit exceeded"}`))
			return
		}
		next.ServeHTTP(w, r)
	})
}

