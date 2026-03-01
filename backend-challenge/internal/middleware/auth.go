package middleware

import (
	"net/http"
	"os"
)

// APIKey returns the API key from env, or default (OpenAPI spec: apitest)
func APIKey() string {
	if k := os.Getenv("API_KEY"); k != "" {
		return k
	}
	return "apitest"
}

// AdminAPIKey returns the admin API key from env, or default
func AdminAPIKey() string {
	if k := os.Getenv("ADMIN_API_KEY"); k != "" {
		return k
	}
	return "admin"
}

// RequireAdminAPIKey returns a middleware that checks for the admin_api_key header
func RequireAdminAPIKey(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("admin_api_key")
		if key == "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"message":"Missing admin_api_key header"}`))
			return
		}
		if key != AdminAPIKey() {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			w.Write([]byte(`{"message":"Invalid admin_api_key"}`))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RequireAPIKey returns a middleware that checks for the api_key header
func RequireAPIKey(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("api_key")
		if key == "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"message":"Missing api_key header"}`))
			return
		}
		if key != APIKey() {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			w.Write([]byte(`{"message":"Invalid api_key"}`))
			return
		}
		next.ServeHTTP(w, r)
	})
}
