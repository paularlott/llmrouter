package middleware

import (
	"net/http"
	"strings"
)

// Auth creates a middleware that validates bearer token if configured
func Auth(token string) func(http.HandlerFunc) http.HandlerFunc {
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			// If no token is configured, skip authentication
			if token == "" {
				next(w, r)
				return
			}

			// Get Authorization header
			authHeader := r.Header.Get("Authorization")
			if authHeader == "" {
				http.Error(w, "Authorization header required", http.StatusUnauthorized)
				return
			}

			// Check for Bearer token format
			if !strings.HasPrefix(authHeader, "Bearer ") {
				http.Error(w, "Invalid authorization format", http.StatusUnauthorized)
				return
			}

			// Extract and validate token
			providedToken := strings.TrimPrefix(authHeader, "Bearer ")
			if providedToken != token {
				http.Error(w, "Invalid token", http.StatusUnauthorized)
				return
			}

			// Token is valid, proceed to next handler
			next(w, r)
		}
	}
}