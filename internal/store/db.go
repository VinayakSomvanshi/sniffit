package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"

	// Pure-Go SQLite driver (no CGO required for alpine builds)
	_ "modernc.org/sqlite"
)

// newSQLite opens a SQLite database at the given path.
// Uses WAL mode, busy timeout, and foreign keys for data integrity.
func newSQLite(dbPath string) (*sql.DB, error) {
	// Ensure directory exists
	dir := dbPath
	for i := len(dir) - 1; i >= 0; i-- {
		if dir[i] == '/' {
			dir = dir[:i]
			break
		}
	}
	if dir != "" && dir != dbPath {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("create dir for %s: %w", dbPath, err)
		}
	}

	db, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=ON")
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// SQLite only allows one writer at a time
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := db.PingContext(context.Background()); err != nil {
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	return db, nil
}

// newPostgres opens a PostgreSQL database using DATABASE_URL.
func newPostgres(databaseURL string) (*sql.DB, error) {
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}
	if err := db.PingContext(context.Background()); err != nil {
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	// PostgreSQL benefits from connection pooling
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(5)
	return db, nil
}
