package store

import (
	"context"
	"time"
)

// Tenant represents a tenant in the system.
type Tenant struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ListTenants returns all tenants.
func (s *Store) ListTenants(ctx context.Context) ([]*Tenant, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, created_at, updated_at FROM tenants ORDER BY created_at ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tenants []*Tenant
	for rows.Next() {
		var t Tenant
		if err := rows.Scan(&t.ID, &t.Name, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, err
		}
		tenants = append(tenants, &t)
	}
	return tenants, rows.Err()
}

// CreateTenant creates a new tenant.
func (s *Store) CreateTenant(ctx context.Context, id, name string) (*Tenant, error) {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO tenants (id, name) VALUES (?, ?)
	`, id, name)
	if err != nil {
		return nil, err
	}
	return &Tenant{ID: id, Name: name, CreatedAt: time.Now(), UpdatedAt: time.Now()}, nil
}

// DeleteTenant deletes a tenant and all its data (cascade).
func (s *Store) DeleteTenant(ctx context.Context, id string) error {
	if id == "default" {
		return nil // Cannot delete default tenant
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM tenants WHERE id = ?`, id)
	return err
}
