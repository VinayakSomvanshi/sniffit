package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Store is the main database access point.
type Store struct {
	db     *sql.DB
	isPG   bool // true if using PostgreSQL
}

// NewStore creates a new Store, initialising the database connection
// and running migrations. DATABASE_URL env var selects PostgreSQL;
// otherwise SQLite at SQLITE_PATH (default: ./sniffit-data/sniffit.db).
func NewStore(ctx context.Context) (*Store, error) {
	dbURL := os.Getenv("DATABASE_URL")
	var db *sql.DB
	var err error
	isPG := false

	if dbURL != "" {
		db, err = newPostgres(dbURL)
		isPG = true
	} else {
		path := os.Getenv("SQLITE_PATH")
		if path == "" {
			path = "./sniffit-data/sniffit.db"
		}
		db, err = newSQLite(path)
	}
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	s := &Store{db: db, isPG: isPG}
	if err := s.migrate(ctx); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return s, nil
}

// Close closes the underlying database connection.
func (s *Store) Close() error {
	return s.db.Close()
}

// isSQLite returns true if using SQLite.
func (s *Store) isSQLite() bool {
	return !s.isPG
}

func parseTimestamp(s string) time.Time {
	t, _ := time.Parse(time.RFC3339, s)
	return t
}

func (s *Store) migrate(ctx context.Context) error {
	// Create schema_migrations table using TEXT and application-side timestamps
	// for SQLite/PostgreSQL compatibility
	_, err := s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			name TEXT PRIMARY KEY,
			applied_at TEXT NOT NULL
		)
	`)
	if err != nil {
		return fmt.Errorf("create migrations table: %w", err)
	}

	// Read applied migrations
	applied := make(map[string]bool)
	rows, err := s.db.QueryContext(ctx, `SELECT name FROM schema_migrations`)
	if err != nil {
		return fmt.Errorf("query migrations: %w", err)
	}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return err
		}
		applied[name] = true
	}
	rows.Close()

	// Read migration files
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("read migrations dir: %w", err)
	}

	var names []string
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".sql" {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	for _, name := range names {
		if applied[name] {
			continue
		}
		content, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", name, err)
		}
		// Replace PostgreSQL placeholders ($1, $2) with SQLite-compatible ? for SQLite
		sqlText := string(content)
		if s.isSQLite() {
			sqlText = replacePlaceholders(sqlText)
		}
		if _, err := s.db.ExecContext(ctx, sqlText); err != nil {
			return fmt.Errorf("apply migration %s: %w", name, err)
		}
		// Record migration using appropriate placeholder
		now := time.Now().UTC().Format(time.RFC3339)
		if s.isSQLite() {
			_, _ = s.db.ExecContext(ctx,
				`INSERT INTO schema_migrations(name, applied_at) VALUES (?, ?)`, name, now)
		} else {
			_, _ = s.db.ExecContext(ctx,
				`INSERT INTO schema_migrations(name, applied_at) VALUES ($1, $2)`, name, now)
		}
		fmt.Printf("[store] applied migration: %s\n", name)
	}

	// Ensure default tenant exists
	now := time.Now().UTC().Format(time.RFC3339)
	if s.isSQLite() {
		_, _ = s.db.ExecContext(ctx,
			`INSERT OR IGNORE INTO tenants (id, name, created_at, updated_at) VALUES (?, ?, ?, ?)`,
			"default", "Default Tenant", now, now)
	} else {
		_, _ = s.db.ExecContext(ctx,
			`INSERT INTO tenants (id, name, created_at, updated_at) VALUES ($1, $2, $3, $4)
			 ON CONFLICT (id) DO NOTHING`,
			"default", "Default Tenant", now, now)
	}

	// Bootstrap: Seed an initial admin API key if none exist
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM api_keys`).Scan(&count); err == nil && count == 0 {
		// Key: sniffit_379582a513e97184f7711c3b71c96689
		const bootstrapHash = "575435a839a4724ad154f62251c42f6d814e1529a063bce2053b77de1bd1caf5"
		if s.isSQLite() {
			_, _ = s.db.ExecContext(ctx, `
				INSERT INTO api_keys (id, key_hash, name, tenant_id, rate_limit, is_admin, created_at)
				VALUES (?, ?, ?, ?, ?, ?, ?)
			`, "admin", bootstrapHash, "Admin Key", "default", 1000, 1, now)
		} else {
			_, _ = s.db.ExecContext(ctx, `
				INSERT INTO api_keys (id, key_hash, name, tenant_id, rate_limit, is_admin, created_at)
				VALUES ($1, $2, $3, $4, $5, $6, $7)
			`, "admin", bootstrapHash, "Admin Key", "default", 1000, 1, now)
		}
		fmt.Println("[store] bootstrapped default admin API key")
	}

	return nil
}

// replacePlaceholders converts $1, $2, etc. to ? for SQLite compatibility.
func replacePlaceholders(sql string) string {
	var result strings.Builder
	n := 1
	for i := 0; i < len(sql); i++ {
		if sql[i] == '$' {
			result.WriteByte('?')
			for i+1 < len(sql) && sql[i+1] >= '0' && sql[i+1] <= '9' {
				i++
			}
		} else {
			result.WriteByte(sql[i])
		}
	}
	_ = n // suppress unused warning
	return result.String()
}
