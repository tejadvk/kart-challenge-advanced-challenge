package observability

import (
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	httpRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "Total number of HTTP requests",
		},
		[]string{"method", "path", "status"},
	)
	httpRequestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "http_request_duration_seconds",
			Help:    "HTTP request duration in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"method", "path"},
	)
)

func init() {
	prometheus.MustRegister(httpRequestsTotal, httpRequestDuration)
}

// MetricsAuthToken returns the token required for /metrics when METRICS_AUTH_TOKEN is set
func MetricsAuthToken() string {
	return os.Getenv("METRICS_AUTH_TOKEN")
}

// Handler returns the Prometheus metrics handler for /metrics.
// When METRICS_AUTH_TOKEN is set, requests must include Authorization: Bearer <token> or ?token=<token>
func Handler() http.Handler {
	h := promhttp.Handler()
	token := MetricsAuthToken()
	if token == "" {
		return h
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		qToken := r.URL.Query().Get("token")
		auth := r.Header.Get("Authorization")
		var got string
		if strings.HasPrefix(auth, "Bearer ") {
			got = strings.TrimPrefix(auth, "Bearer ")
		} else if qToken != "" {
			got = qToken
		}
		if got != token {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"message":"Unauthorized"}`))
			return
		}
		h.ServeHTTP(w, r)
	})
}

// Middleware returns an HTTP middleware that records request count and duration.
// Paths are normalized to reduce cardinality: /product/1 -> /product/{id}
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		path := normalizedPath(r.URL.Path)
		wrapped := &responseWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(wrapped, r)
		duration := time.Since(start).Seconds()
		status := strconv.Itoa(wrapped.status)
		httpRequestDuration.WithLabelValues(r.Method, path).Observe(duration)
		httpRequestsTotal.WithLabelValues(r.Method, path, status).Inc()
	})
}

// normalizedPath reduces path cardinality for metrics: /product/123, /product/, /admin/product/1 -> /product/{id}, /admin/product/{id}
func normalizedPath(path string) string {
	if path == "" {
		return "/"
	}
	path = strings.TrimSuffix(path, "/")
	if path == "" {
		return "/"
	}
	if strings.HasPrefix(path, "/product/") || path == "/product" {
		return "/product/{id}"
	}
	if strings.HasPrefix(path, "/admin/product/") || path == "/admin/product" {
		return "/admin/product/{id}"
	}
	return path
}

type responseWriter struct {
	http.ResponseWriter
	status int
}

func (w *responseWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}
