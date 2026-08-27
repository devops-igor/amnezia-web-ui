package database

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	_ "modernc.org/sqlite" // Pure-Go SQLite database driver registration
)

//go:embed schema.sql
var SchemaSQL string

// DefaultSettings maps configuration keys to their default JSON values.
var DefaultSettings = map[string]string{
	"schema_version": `"1"`,
	"appearance":     `{"title":"Amnezia","logo":"🛡","subtitle":"Web Panel","language":"en"}`,
	"sync":           `{"remnawave_url":"","remnawave_api_key":"","remnawave_sync":false,"remnawave_sync_users":false,"remnawave_create_conns":false,"remnawave_server_id":0,"remnawave_protocol":"awg"}`,
	"captcha":        `{"enabled":false}`,
	"telegram":       `{}`,
	"ssl":            `{"enabled":false,"domain":"","cert_path":"","key_path":"","cert_text":"","key_text":"","panel_port":5000}`,
	"limits":         `{"max_connections_per_user":10,"connection_rate_limit_count":5,"connection_rate_limit_window":60}`,
	"vpn_config":     `{"algorithm":"least_conn","weights":{},"health_threshold_ms":2000,"listen_port":51820,"subnet_cidr":"10.100.0.0/16","max_total_peers":1000,"max_peers_per_backend":200}`,
}

// DB wraps an sql.DB handle and serializes write operations to ensure thread safety with SQLite.
type DB struct {
	dbPath string
	sqlDB  *sql.DB
	mu     sync.RWMutex
}

// New opens a connection to the SQLite database, applies PRAGMAs, and sets connection pool bounds.
func New(dbPath string) (*DB, error) {
	dir := filepath.Dir(dbPath)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0750); err != nil {
			return nil, fmt.Errorf("failed to create database directory %s: %w", dir, err)
		}
	}

	dsn := fmt.Sprintf("%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)&_pragma=synchronous(NORMAL)", dbPath)
	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open sqlite database: %w", err)
	}

	// SQLite single-writer connection pooling configuration
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)

	db := &DB{
		dbPath: dbPath,
		sqlDB:  sqlDB,
	}

	return db, nil
}

// InitSchema applies the DDL schema and indexes to the database.
func (d *DB) InitSchema(ctx context.Context) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if _, err := d.sqlDB.ExecContext(ctx, SchemaSQL); err != nil {
		return fmt.Errorf("failed to execute schema DDL: %w", err)
	}

	return d.ensureDefaultSettingsLocked(ctx)
}

// EnsureDefaultSettings populates default dynamic settings if missing.
func (d *DB) EnsureDefaultSettings(ctx context.Context) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.ensureDefaultSettingsLocked(ctx)
}

func (d *DB) ensureDefaultSettingsLocked(ctx context.Context) error {
	for key, val := range DefaultSettings {
		_, err := d.sqlDB.ExecContext(ctx, "INSERT OR IGNORE INTO settings (key, value) VALUES (?, ?)", key, val)
		if err != nil {
			return fmt.Errorf("failed to seed default setting %s: %w", key, err)
		}
	}
	return nil
}

// Ping verifies the database connection is alive.
func (d *DB) Ping(ctx context.Context) error {
	return d.sqlDB.PingContext(ctx)
}

// Close closes the underlying SQLite database connection.
func (d *DB) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.sqlDB.Close()
}

// SQLDB returns the underlying *sql.DB instance.
func (d *DB) SQLDB() *sql.DB {
	return d.sqlDB
}

// ExecContext executes a query without returning rows.
func (d *DB) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.sqlDB.ExecContext(ctx, query, args...)
}

// QueryRowContext executes a query expected to return at most one row.
func (d *DB) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.sqlDB.QueryRowContext(ctx, query, args...)
}

// QueryContext executes a query that returns rows.
func (d *DB) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.sqlDB.QueryContext(ctx, query, args...)
}
