package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"time"
)

var ErrNotFound = errors.New("not found")

// APIKey represents a stored API key (plaintext never stored).
type APIKey struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	KeyHash    string     `json:"-"` // never serialized
	TenantID   string     `json:"tenant_id"`
	RateLimit  int        `json:"rate_limit"`
	IsAdmin    bool       `json:"is_admin"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
}

// GenerateAPIKey creates a new API key, returning the plaintext token (only time it's readable).
// The plaintext token should be given to the user and never stored.
func (s *Store) GenerateAPIKey(ctx context.Context, name, tenantID string, rateLimit int, isAdmin bool) (*APIKey, string, error) {
	plaintext := generateToken(32)
	hash := sha256Hash(plaintext)
	id := generateID()

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO api_keys (id, key_hash, name, tenant_id, rate_limit, is_admin)
		VALUES (?, ?, ?, ?, ?, ?)
	`, id, hash, name, tenantID, rateLimit, isAdmin)
	if err != nil {
		return nil, "", err
	}

	return &APIKey{
		ID:        id,
		Name:      name,
		KeyHash:   hash,
		TenantID:  tenantID,
		RateLimit: rateLimit,
		IsAdmin:   isAdmin,
		CreatedAt: time.Now(),
	}, plaintext, nil
}

// GetAPIKeyByHash looks up an API key by its SHA-256 hash.
func (s *Store) GetAPIKeyByHash(ctx context.Context, keyHash string) (*APIKey, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, name, key_hash, tenant_id, rate_limit, is_admin, created_at, last_used_at, expires_at
		FROM api_keys WHERE key_hash = ?
	`, keyHash)
	return scanAPIKey(row)
}

// ListAPIKeys returns all API keys for a tenant (hash never exposed).
func (s *Store) ListAPIKeys(ctx context.Context, tenantID string) ([]*APIKey, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, key_hash, tenant_id, rate_limit, is_admin, created_at, last_used_at, expires_at
		FROM api_keys WHERE tenant_id = ?
		ORDER BY created_at DESC
	`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var keys []*APIKey
	for rows.Next() {
		k, err := scanAPIKey(rows)
		if err != nil {
			return nil, err
		}
		keys = append(keys, k)
	}
	return keys, rows.Err()
}

// DeleteAPIKey deletes an API key by ID.
func (s *Store) DeleteAPIKey(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM api_keys WHERE id = ?`, id)
	return err
}

// TouchAPIKey updates last_used_at (called async after each authenticated request).
func (s *Store) TouchAPIKey(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE api_keys SET last_used_at = CURRENT_TIMESTAMP WHERE id = ?`, id)
	return err
}

// sha256Hash returns the hex-encoded SHA-256 of a plaintext token.
func sha256Hash(plaintext string) string {
	h := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(h[:])
}

// generateToken returns a cryptographically random hex string.
func generateToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	return hex.EncodeToString(b)
}

// generateID returns a random ID string.
func generateID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	return hex.EncodeToString(b)
}

// scanAPIKey scans an API key row.
func scanAPIKey(scanner interface{ Scan(...any) error }) (*APIKey, error) {
	var k APIKey
	var lastUsed, expires sql.NullString
	var created string
	err := scanner.Scan(&k.ID, &k.Name, &k.KeyHash, &k.TenantID, &k.RateLimit, &k.IsAdmin,
		&created, &lastUsed, &expires)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	k.CreatedAt = parseTimestamp(created)
	if lastUsed.Valid {
		t := parseTimestamp(lastUsed.String)
		k.LastUsedAt = &t
	}
	if expires.Valid {
		t := parseTimestamp(expires.String)
		k.ExpiresAt = &t
	}
	return &k, nil
}
