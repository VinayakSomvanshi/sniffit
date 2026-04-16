package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/vinayak/sniffit/internal/store"
)

// SkipAuthPaths is a list of paths that never require authentication.
var SkipAuthPaths = []string{"/mutate", "/health", "/"}

// AuthMiddleware returns an HTTP handler that enforces authentication.
// When AUTH_ENABLED != "true", it runs in permissive dev mode.
func AuthMiddleware(s *store.Store, limiter *SlidingWindowLimiter) func(http.Handler) http.Handler {
	authEnabled := os.Getenv("AUTH_ENABLED") == "true"
	rateLimitDefault, _ := strconv.Atoi(os.Getenv("RATE_LIMIT_DEFAULT"))
	if rateLimitDefault == 0 {
		rateLimitDefault = 100
	}

	validator := NewAPIKeyValidator(s)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			path := r.URL.Path

			// Always skip /mutate (K8s handles that), /health, and static UI
			for _, skip := range SkipAuthPaths {
				if path == skip || strings.HasPrefix(path, "/ui/") {
					next.ServeHTTP(w, r)
					return
				}
			}

			// OPTIONS preflight — allow without auth
			if r.Method == http.MethodOptions {
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
				w.WriteHeader(http.StatusNoContent)
				return
			}

			// Dev mode: no auth enforcement
			if !authEnabled {
				ctx := WithTenant(r.Context(), &TenantContext{
					TenantID:  "default",
					IsAdmin:   true,
					RateLimit: 10000,
				})
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}

			// Production mode: validate Bearer token
			authHeader := r.Header.Get("Authorization")
			if authHeader == "" && r.URL.Query().Get("token") != "" {
				authHeader = "Bearer " + r.URL.Query().Get("token")
			}
			tc, err := validator.ValidateBearer(r.Context(), authHeader)
			if err != nil {
				if isBrowser(r) {
					// Browser gets HTML login page
					ServeLoginPage(w, r)
					return
				}
				// API client gets JSON 401
				msg := "unauthorized"
				if err == ErrTokenExpired {
					msg = "token_expired"
				}
				APIUnauthorized(w, msg)
				return
			}

			// Rate limit check
			limit := tc.RateLimit
			if limit == 0 {
				limit = rateLimitDefault
			}
			if !limiter.Allow(tc.APIKeyID, limit) {
				http.Error(w, `{"error":"rate_limit_exceeded"}`, http.StatusTooManyRequests)
				return
			}

			// Attach tenant context and proceed
			ctx := WithTenant(r.Context(), tc)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// isBrowser returns true if the User-Agent suggests a browser (not a script).
func isBrowser(r *http.Request) bool {
	ua := r.Header.Get("User-Agent")
	return strings.Contains(ua, "Mozilla") || strings.Contains(ua, "Safari") ||
		strings.Contains(ua, "Chrome") || strings.Contains(ua, "Firefox")
}

// AdminOnly middleware guard — use after AuthMiddleware in handler registration.
func AdminOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tc, ok := FromTenant(r.Context())
		if !ok || !tc.IsAdmin {
			http.Error(w, `{"error":"forbidden","message":"admin required"}`, http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// AuditLogger wraps a handler and logs the request after it completes.
func AuditLogger(s *store.Store, limiter *SlidingWindowLimiter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Skip audit for SSE keepalive pings
			if r.URL.Path == "/api/events" && r.Method == http.MethodGet {
				next.ServeHTTP(w, r)
				return
			}

			tc, _ := FromTenant(r.Context())
			rec := &statusRecorder{ResponseWriter: w, statusCode: http.StatusOK}
			start := r.Context()

			next.ServeHTTP(w, r.WithContext(start))

			// Log asynchronously
			go func() {
				entry := &store.AuditEntry{
					Method:     r.Method,
					Path:       r.URL.Path,
					StatusCode: rec.statusCode,
					IPAddress:  clientIP(r),
					UserAgent:  r.Header.Get("User-Agent"),
				}
				if tc != nil {
					entry.APIKeyID = tc.APIKeyID
					entry.TenantID = tc.TenantID
				}
				_ = s.InsertAudit(context.Background(), entry)
			}()
		})
	}
}

// statusRecorder wraps http.ResponseWriter to capture the status code.
type statusRecorder struct {
	http.ResponseWriter
	statusCode int
}

func (sr *statusRecorder) WriteHeader(code int) {
	sr.statusCode = code
	sr.ResponseWriter.WriteHeader(code)
}

func (sr *statusRecorder) Write(b []byte) (int, error) {
	if sr.statusCode == 0 {
		sr.statusCode = http.StatusOK
	}
	return sr.ResponseWriter.Write(b)
}

// clientIP extracts the real client IP, checking X-Forwarded-For first.
func clientIP(r *http.Request) string {
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		return strings.Split(fwd, ",")[0]
	}
	return r.RemoteAddr
}

// WriteJSON is a helper to write a JSON response.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
