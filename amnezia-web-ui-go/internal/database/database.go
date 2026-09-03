package database

import (
	"context"
	"crypto/subtle"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/devops-igor/amnezia-web-ui-go/internal/models"
	"github.com/devops-igor/amnezia-web-ui-go/internal/security"
	_ "modernc.org/sqlite" // Pure-Go SQLite database driver
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

// Column allowlists for update methods to prevent SQL injection
var (
	allowedServerColumns = map[string]bool{
		"name":       true,
		"host":       true,
		"ssh_user":   true,
		"ssh_port":   true,
		"ssh_pass":   true,
		"ssh_key":    true,
		"protocols":  true,
		"created_at": true,
	}

	allowedUserColumns = map[string]bool{
		"username":                 true,
		"email":                    true,
		"telegramId":               true,
		"description":              true,
		"password_hash":            true,
		"role":                     true,
		"enabled":                  true,
		"traffic_limit":            true,
		"traffic_used":             true,
		"traffic_total":            true,
		"traffic_total_rx":         true,
		"traffic_total_tx":         true,
		"monthly_rx":               true,
		"monthly_tx":               true,
		"monthly_reset_at":         true,
		"traffic_reset_strategy":   true,
		"share_enabled":            true,
		"share_token":              true,
		"share_password_hash":      true,
		"remnawave_uuid":           true,
		"created_at":               true,
		"last_reset_at":            true,
		"expiration_date":          true,
		"expires_at":               true,
		"awg_mimicry":              true,
		"password_change_required": true,
		"limits":                   true,
	}

	allowedConnectionColumns = map[string]bool{
		"user_id":          true,
		"server_id":        true,
		"protocol":         true,
		"client_id":        true,
		"name":             true,
		"awg_mimicry":      true,
		"last_rx":          true,
		"last_tx":          true,
		"traffic_delta_rx": true,
		"traffic_delta_tx": true,
		"traffic_total_rx": true,
		"traffic_total_tx": true,
		"traffic_total":    true,
		"created_at":       true,
	}

	allowedBackendTunnelColumns = map[string]bool{
		"server_id":          true,
		"interface_name":     true,
		"public_key":         true,
		"private_key":        true,
		"endpoint":           true,
		"status":             true,
		"last_health_check":  true,
		"latency_ms":         true,
		"active_connections": true,
		"created_at":         true,
	}
)

// DB wraps an sql.DB handle and serializes write operations to ensure SQLite thread-safety.
type DB struct {
	dbPath            string
	secretKey         string
	sqlDB             *sql.DB
	writeMu           sync.Mutex
	mu                sync.RWMutex
	reachabilityMu    sync.RWMutex
	reachabilityCache map[int64]models.ReachabilityStatus
}

// Open opens a connection to the SQLite database with WAL mode, busy timeout, and foreign keys enabled.
func Open(dbPath, secretKey string) (*DB, error) {
	if dbPath != ":memory:" && !strings.HasPrefix(dbPath, "file::memory:") && !strings.Contains(dbPath, "mode=memory") {
		dir := filepath.Dir(dbPath)
		if dir != "" && dir != "." {
			if err := CheckDirWritable(dir); err != nil {
				return nil, err
			}
		}
	}

	dsn := fmt.Sprintf("%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)&_pragma=synchronous(NORMAL)", dbPath)
	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open sqlite database: %w", err)
	}

	// SQLite single-writer connection pool bounds
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)

	db := &DB{
		dbPath:            dbPath,
		secretKey:         secretKey,
		sqlDB:             sqlDB,
		reachabilityCache: make(map[int64]models.ReachabilityStatus),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := db.InitSchema(ctx); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("failed to initialize schema: %w", err)
	}

	return db, nil
}

// New is an alias for Open to support multiple calling conventions.
func New(dbPath string, secretKeys ...string) (*DB, error) {
	var secretKey string
	if len(secretKeys) > 0 {
		secretKey = secretKeys[0]
	}
	return Open(dbPath, secretKey)
}

// InitSchema executes DDL and applies default seed settings and migrations.
func (d *DB) InitSchema(ctx context.Context) error {
	d.writeMu.Lock()
	defer d.writeMu.Unlock()

	if _, err := d.sqlDB.ExecContext(ctx, SchemaSQL); err != nil {
		return fmt.Errorf("failed to execute schema DDL: %w", err)
	}

	if err := d.ensureDefaultSettingsLocked(ctx); err != nil {
		return err
	}

	return d.runMigrationsLocked(ctx)
}

// EnsureDefaultSettings ensures all default keys exist in settings table.
func (d *DB) EnsureDefaultSettings(ctx context.Context) error {
	d.writeMu.Lock()
	defer d.writeMu.Unlock()
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

func (d *DB) runMigrationsLocked(ctx context.Context) error {
	if err := d.migratePlaintextCredentials(ctx); err != nil {
		return err
	}
	if err := d.migrateXraySensitiveKeys(ctx); err != nil {
		return err
	}
	if err := d.migratePlaintextSSLKeys(ctx); err != nil {
		return err
	}
	return d.migrateUniqueUsernameIndex(ctx)
}

func (d *DB) migrateUniqueUsernameIndex(ctx context.Context) error {
	_, _ = d.sqlDB.ExecContext(ctx, "CREATE UNIQUE INDEX IF NOT EXISTS idx_users_username ON users(username)")
	return nil
}

func (d *DB) migratePlaintextCredentials(ctx context.Context) error {
	if d.secretKey == "" {
		return nil
	}

	var credsFlag string
	row := d.sqlDB.QueryRowContext(ctx, "SELECT value FROM migration_flags WHERE key = 'credentials_encrypted'")
	if err := row.Scan(&credsFlag); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if credsFlag != "" {
		return nil
	}

	rows, err := d.sqlDB.QueryContext(ctx, "SELECT id, ssh_pass, ssh_key FROM servers")
	if err == nil {
		defer rows.Close()
		type serverCred struct {
			id      int64
			sshPass string
			sshKey  string
		}
		var serversToEncrypt []serverCred
		for rows.Next() {
			var sc serverCred
			var p, k sql.NullString
			if err := rows.Scan(&sc.id, &p, &k); err == nil {
				sc.sshPass = p.String
				sc.sshKey = k.String
				serversToEncrypt = append(serversToEncrypt, sc)
			}
		}
		for _, sc := range serversToEncrypt {
			encPass := sc.sshPass
			encKey := sc.sshKey
			dirty := false
			if sc.sshPass != "" && !security.LooksLikeFernetToken(sc.sshPass) {
				if ep, err := security.EncryptCredential(sc.sshPass, d.secretKey); err == nil {
					encPass = ep
					dirty = true
				}
			}
			if sc.sshKey != "" && !security.LooksLikeFernetToken(sc.sshKey) {
				if ek, err := security.EncryptCredential(sc.sshKey, d.secretKey); err == nil {
					encKey = ek
					dirty = true
				}
			}
			if dirty {
				_, _ = d.sqlDB.ExecContext(ctx, "UPDATE servers SET ssh_pass = ?, ssh_key = ? WHERE id = ?", encPass, encKey, sc.id)
			}
		}
	}

	_, err = d.sqlDB.ExecContext(ctx, "INSERT INTO migration_flags (key, value) VALUES ('credentials_encrypted', '1') ON CONFLICT(key) DO UPDATE SET value = '1'")
	return err
}

func (d *DB) migrateXraySensitiveKeys(ctx context.Context) error {
	var xrayFlag string
	row := d.sqlDB.QueryRowContext(ctx, "SELECT value FROM migration_flags WHERE key = 'xray_private_keys_cleared'")
	if err := row.Scan(&xrayFlag); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if xrayFlag != "" {
		return nil
	}

	rows, err := d.sqlDB.QueryContext(ctx, "SELECT id, protocols FROM servers")
	if err == nil {
		defer rows.Close()
		type srvProto struct {
			id       int64
			cleanedB string
		}
		var toUpdate []srvProto
		for rows.Next() {
			var id int64
			var protoJSON sql.NullString
			if err := rows.Scan(&id, &protoJSON); err == nil && protoJSON.Valid && protoJSON.String != "" {
				var protoMap map[string]any
				if err := json.Unmarshal([]byte(protoJSON.String), &protoMap); err == nil {
					cleaned := security.StripSensitiveProtocolFields(protoMap)
					if cleanedBytes, err := json.Marshal(cleaned); err == nil {
						toUpdate = append(toUpdate, srvProto{id: id, cleanedB: string(cleanedBytes)})
					}
				}
			}
		}
		_ = rows.Close()
		for _, u := range toUpdate {
			_, _ = d.sqlDB.ExecContext(ctx, "UPDATE servers SET protocols = ? WHERE id = ?", u.cleanedB, u.id)
		}
	}

	_, err = d.sqlDB.ExecContext(ctx, "INSERT INTO migration_flags (key, value) VALUES ('xray_private_keys_cleared', '1') ON CONFLICT(key) DO UPDATE SET value = '1'")
	return err
}

func (d *DB) migratePlaintextSSLKeys(ctx context.Context) error {
	if d.secretKey == "" {
		return nil
	}

	var sslFlag string
	row := d.sqlDB.QueryRowContext(ctx, "SELECT value FROM migration_flags WHERE key = 'ssl_keys_encrypted'")
	if err := row.Scan(&sslFlag); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if sslFlag != "" {
		return nil
	}

	var sslVal sql.NullString
	row = d.sqlDB.QueryRowContext(ctx, "SELECT value FROM settings WHERE key = 'ssl'")
	if err := row.Scan(&sslVal); err == nil && sslVal.Valid && sslVal.String != "" {
		var sslMap map[string]any
		if err := json.Unmarshal([]byte(sslVal.String), &sslMap); err == nil {
			dirty := false
			if kt, ok := sslMap["key_text"].(string); ok && kt != "" && !security.LooksLikeFernetToken(kt) {
				if enc, err := security.EncryptCredential(kt, d.secretKey); err == nil {
					sslMap["key_text"] = enc
					dirty = true
				}
			}
			if ct, ok := sslMap["cert_text"].(string); ok && ct != "" && !security.LooksLikeFernetToken(ct) {
				if enc, err := security.EncryptCredential(ct, d.secretKey); err == nil {
					sslMap["cert_text"] = enc
					dirty = true
				}
			}
			if dirty {
				if sslBytes, err := json.Marshal(sslMap); err == nil {
					_, _ = d.sqlDB.ExecContext(ctx, "INSERT INTO settings (key, value) VALUES ('ssl', ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value", string(sslBytes))
				}
			}
		}
	}

	_, err := d.sqlDB.ExecContext(ctx, "INSERT INTO migration_flags (key, value) VALUES ('ssl_keys_encrypted', '1') ON CONFLICT(key) DO UPDATE SET value = '1'")
	return err
}

// WithTransaction executes fn within an exclusive SQLite transaction with write mutex serialization.
func (d *DB) WithTransaction(ctx context.Context, fn func(tx *sql.Tx) error) error {
	d.writeMu.Lock()
	defer d.writeMu.Unlock()

	tx, err := d.sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}

	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

// ExecuteTransaction is an alias for WithTransaction.
func (d *DB) ExecuteTransaction(ctx context.Context, fn func(tx *sql.Tx) error) error {
	return d.WithTransaction(ctx, fn)
}

// Ping verifies database connectivity.
func (d *DB) Ping(ctx context.Context) error {
	return d.sqlDB.PingContext(ctx)
}

// Close gracefully closes the database connection handle.
func (d *DB) Close() error {
	d.writeMu.Lock()
	defer d.writeMu.Unlock()
	return d.sqlDB.Close()
}

// SQLDB returns the underlying *sql.DB handle.
func (d *DB) SQLDB() *sql.DB {
	return d.sqlDB
}

// ExecContext executes a direct query without returning rows.
func (d *DB) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	d.writeMu.Lock()
	defer d.writeMu.Unlock()
	return d.sqlDB.ExecContext(ctx, query, args...)
}

// QueryRowContext executes a direct query expected to return at most one row.
func (d *DB) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.sqlDB.QueryRowContext(ctx, query, args...)
}

// QueryContext executes a direct query that returns rows.
func (d *DB) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.sqlDB.QueryContext(ctx, query, args...)
}

// SecretKey returns the configured database credential secret key.
func (d *DB) SecretKey() string {
	return d.secretKey
}

// Helper functions for time and null conversions

func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05.999999",
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
		"2006-01-02",
	}
	for _, l := range layouts {
		if t, err := time.Parse(l, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}

func formatTimePtr(t *time.Time) *string {
	if t == nil || t.IsZero() {
		return nil
	}
	s := t.Format(time.RFC3339)
	return &s
}

func nullStringToPtr(ns sql.NullString) *string {
	if !ns.Valid {
		return nil
	}
	val := ns.String
	return &val
}

func constantTimeCompare(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
