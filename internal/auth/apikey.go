package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/vinayak/sniffit/internal/store"
)

var (
	ErrNoBearerToken = errors.New("missing Bearer token")
	ErrEmptyToken   = errors.New("empty bearer token")
	ErrInvalidToken = errors.New("invalid bearer token")
	ErrTokenExpired = errors.New("token expired")
)

// APIKeyValidator validates Bearer tokens against the store.
type APIKeyValidator struct {
	store *store.Store
}

// NewAPIKeyValidator creates a new validator backed by the given store.
func NewAPIKeyValidator(s *store.Store) *APIKeyValidator {
	return &APIKeyValidator{store: s}
}

// ValidateBearer extracts and validates a Bearer token from the Authorization header.
func (v *APIKeyValidator) ValidateBearer(ctx context.Context, authHeader string) (*TenantContext, error) {
	if !strings.HasPrefix(authHeader, "Bearer ") {
		return nil, ErrNoBearerToken
	}
	token := strings.TrimPrefix(authHeader, "Bearer ")
	if token == "" {
		return nil, ErrEmptyToken
	}

	hash := sha256Hash(token)
	apiKey, err := v.store.GetAPIKeyByHash(ctx, hash)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, ErrInvalidToken
		}
		return nil, err
	}

	if apiKey.ExpiresAt != nil && apiKey.ExpiresAt.Before(time.Now()) {
		return nil, ErrTokenExpired
	}

	// Update last_used_at asynchronously (fire-and-forget)
	go func() {
		_ = v.store.TouchAPIKey(context.Background(), apiKey.ID)
	}()

	return &TenantContext{
		TenantID:  apiKey.TenantID,
		APIKeyID:  apiKey.ID,
		IsAdmin:   apiKey.IsAdmin,
		RateLimit: apiKey.RateLimit,
	}, nil
}

func sha256Hash(plaintext string) string {
	h := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(h[:])
}
