package store

import (
	"context"
	"database/sql"
	"time"
)

// AuditEntry represents an audit log entry.
type AuditEntry struct {
	ID         int64     `json:"id"`
	APIKeyID   string    `json:"api_key_id,omitempty"`
	TenantID   string    `json:"tenant_id,omitempty"`
	Method     string    `json:"method"`
	Path       string    `json:"path"`
	StatusCode int       `json:"status_code"`
	IPAddress  string    `json:"ip_address,omitempty"`
	UserAgent  string    `json:"user_agent,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

// InsertAudit inserts an audit entry (called async after each API request).
func (s *Store) InsertAudit(ctx context.Context, entry *AuditEntry) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO audit_log (api_key_id, tenant_id, method, path, status_code, ip_address, user_agent)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, entry.APIKeyID, entry.TenantID, entry.Method, entry.Path,
		entry.StatusCode, entry.IPAddress, entry.UserAgent)
	return err
}

// ListAuditEntries returns audit entries for a tenant (or all if tenantID is empty).
func (s *Store) ListAuditEntries(ctx context.Context, tenantID string, limit, offset int) ([]*AuditEntry, error) {
	var rows *sql.Rows
	var err error
	if tenantID != "" {
		rows, err = s.db.QueryContext(ctx, `
			SELECT id, api_key_id, tenant_id, method, path, status_code, ip_address, user_agent, created_at
			FROM audit_log WHERE tenant_id = ?
			ORDER BY created_at DESC LIMIT ? OFFSET ?
		`, tenantID, limit, offset)
	} else {
		rows, err = s.db.QueryContext(ctx, `
			SELECT id, api_key_id, tenant_id, method, path, status_code, ip_address, user_agent, created_at
			FROM audit_log
			ORDER BY created_at DESC LIMIT ? OFFSET ?
		`, limit, offset)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []*AuditEntry
	for rows.Next() {
		var e AuditEntry
		var apiKeyID, ip, ua sql.NullString
		if err := rows.Scan(&e.ID, &apiKeyID, &e.TenantID, &e.Method, &e.Path,
			&e.StatusCode, &ip, &ua, &e.CreatedAt); err != nil {
			return nil, err
		}
		e.APIKeyID = apiKeyID.String
		e.IPAddress = ip.String
		e.UserAgent = ua.String
		entries = append(entries, &e)
	}
	return entries, rows.Err()
}
