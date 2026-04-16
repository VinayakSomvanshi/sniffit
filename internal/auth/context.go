package auth

import (
	"context"
)

// contextKey is the type for context values used by this package.
type contextKey string

const tenantCtxKey contextKey = "tenant_id"

// TenantContext holds the authenticated identity for a request.
type TenantContext struct {
	TenantID  string // the tenant this request belongs to
	APIKeyID  string // the API key ID used to authenticate
	IsAdmin   bool   // true if this key has admin privileges
	RateLimit int    // requests per minute allowed for this key
}

// WithTenant returns a new context that carries tc.
func WithTenant(ctx context.Context, tc *TenantContext) context.Context {
	return context.WithValue(ctx, tenantCtxKey, tc)
}

// FromTenant returns the TenantContext stored in ctx, or false if none.
func FromTenant(ctx context.Context) (*TenantContext, bool) {
	tc, ok := ctx.Value(tenantCtxKey).(*TenantContext)
	return tc, ok
}
