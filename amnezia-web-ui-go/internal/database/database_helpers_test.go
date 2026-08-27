package database

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func TestDatabaseHelpersAndAccessors(t *testing.T) {
	db, secretKey := setupTestDB(t)
	ctx := context.Background()

	// 1. Accessors: SQLDB, SecretKey
	if db.SQLDB() == nil {
		t.Errorf("SQLDB() returned nil")
	}
	if db.SecretKey() != secretKey {
		t.Errorf("SecretKey() = %q, want %q", db.SecretKey(), secretKey)
	}

	// 2. ExecContext, QueryRowContext, QueryContext
	res, err := db.ExecContext(ctx, "INSERT INTO settings (key, value) VALUES ('manual_key', '\"manual_val\"')")
	if err != nil {
		t.Fatalf("ExecContext failed: %v", err)
	}
	ra, _ := res.RowsAffected()
	if ra != 1 {
		t.Errorf("ExecContext rows affected = %d, want 1", ra)
	}

	var rowVal string
	err = db.QueryRowContext(ctx, "SELECT value FROM settings WHERE key = 'manual_key'").Scan(&rowVal)
	if err != nil || rowVal != `"manual_val"` {
		t.Errorf("QueryRowContext failed: val=%q, err=%v", rowVal, err)
	}

	rows, err := db.QueryContext(ctx, "SELECT key FROM settings WHERE key = 'manual_key'")
	if err != nil {
		t.Fatalf("QueryContext failed: %v", err)
	}
	defer rows.Close()
	var keys []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err == nil {
			keys = append(keys, k)
		}
	}
	if len(keys) != 1 || keys[0] != "manual_key" {
		t.Errorf("QueryContext returned %+v, want ['manual_key']", keys)
	}

	// 3. ExecuteTransaction alias
	err = db.ExecuteTransaction(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "INSERT INTO settings (key, value) VALUES ('tx_key', '\"tx_val\"')")
		return err
	})
	if err != nil {
		t.Fatalf("ExecuteTransaction failed: %v", err)
	}
	var txRet string
	_ = db.GetSetting(ctx, "tx_key", &txRet)
	if txRet != "tx_val" {
		t.Errorf("ExecuteTransaction value mismatch: got %q", txRet)
	}

	// 4. EnsureDefaultSettings
	if err := db.EnsureDefaultSettings(ctx); err != nil {
		t.Fatalf("EnsureDefaultSettings failed: %v", err)
	}

	// 5. New alias with and without secretKeys
	tmpDir := t.TempDir()
	db1, err := New(filepath.Join(tmpDir, "new_db1.db"), "custom-secret-key")
	if err != nil {
		t.Fatalf("New with secretKey failed: %v", err)
	}
	_ = db1.Close()

	db2, err := New(filepath.Join(tmpDir, "new_db2.db"))
	if err != nil {
		t.Fatalf("New without secretKey failed: %v", err)
	}
	if db2.SecretKey() != "" {
		t.Errorf("expected empty SecretKey, got %q", db2.SecretKey())
	}
	_ = db2.Close()
}

func TestTimeAndUtilityFunctions(t *testing.T) {
	// 1. parseTime with all supported formats
	formats := []struct {
		input    string
		wantZero bool
		wantYear int
	}{
		{"", true, 0},
		{"invalid-date-string", true, 0},
		{"2026-08-26T12:34:56.789123456Z", false, 2026}, // RFC3339Nano
		{"2026-08-26T12:34:56Z", false, 2026},           // RFC3339
		{"2026-08-26T12:34:56.123456", false, 2026},     // without timezone with fractional
		{"2026-08-26T12:34:56", false, 2026},            // ISO without timezone
		{"2026-08-26 12:34:56", false, 2026},            // standard datetime
		{"2026-08-26", false, 2026},                     // date only
	}

	for _, f := range formats {
		parsed := parseTime(f.input)
		if f.wantZero && !parsed.IsZero() {
			t.Errorf("parseTime(%q) expected zero time, got %v", f.input, parsed)
		}
		if !f.wantZero && (parsed.IsZero() || parsed.Year() != f.wantYear) {
			t.Errorf("parseTime(%q) expected year %d, got %v", f.input, f.wantYear, parsed)
		}
	}

	// 2. formatTime and formatTimePtr
	zeroTime := time.Time{}
	if str := formatTime(zeroTime); str != "" {
		t.Errorf("formatTime(zeroTime) = %q, want empty", str)
	}

	validTime := time.Date(2026, 8, 26, 15, 30, 0, 0, time.UTC)
	if str := formatTime(validTime); str != "2026-08-26T15:30:00Z" {
		t.Errorf("formatTime(validTime) = %q, want '2026-08-26T15:30:00Z'", str)
	}

	if ptr := formatTimePtr(nil); ptr != nil {
		t.Errorf("formatTimePtr(nil) = %v, want nil", ptr)
	}
	if ptr := formatTimePtr(&zeroTime); ptr != nil {
		t.Errorf("formatTimePtr(&zeroTime) = %v, want nil", ptr)
	}
	if ptr := formatTimePtr(&validTime); ptr == nil || *ptr != "2026-08-26T15:30:00Z" {
		t.Errorf("formatTimePtr(&validTime) = %v, want '2026-08-26T15:30:00Z'", ptr)
	}

	// 3. nullStringToPtr
	if ptr := nullStringToPtr(sql.NullString{Valid: false}); ptr != nil {
		t.Errorf("nullStringToPtr(invalid) = %v, want nil", ptr)
	}
	if ptr := nullStringToPtr(sql.NullString{String: "hello", Valid: true}); ptr == nil || *ptr != "hello" {
		t.Errorf("nullStringToPtr(valid) = %v, want 'hello'", ptr)
	}

	// 4. constantTimeCompare
	if !constantTimeCompare("exact_same_string", "exact_same_string") {
		t.Errorf("constantTimeCompare on equal strings returned false")
	}
	if constantTimeCompare("short", "longer_string") {
		t.Errorf("constantTimeCompare on different length strings returned true")
	}
	if constantTimeCompare("same_length_1", "same_length_2") {
		t.Errorf("constantTimeCompare on different strings returned true")
	}
}

func TestDatabaseMigrationHooks(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "mig_test.db")
	secretKey := "secret-key-1234567890abcdef1234567890abcdef"

	// Create a DB manually without flags to test in-flight migrations
	db, err := Open(dbPath, secretKey)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	ctx := context.Background()

	// Clear migration flags to force runMigrationsLocked
	_, _ = db.sqlDB.ExecContext(ctx, "DELETE FROM migration_flags")

	// Insert unencrypted server credentials, xray private keys, unencrypted SSL keys
	_, _ = db.sqlDB.ExecContext(ctx, "INSERT INTO servers (id, name, host, ssh_user, ssh_port, ssh_pass, ssh_key, protocols) VALUES (1, 'Legacy', '1.1.1.1', 'root', 22, 'PlainPass', 'PlainKey', '{\"xray\":{\"port\":443,\"reality_private_key\":\"SENSITIVE\"}}')")
	_, _ = db.sqlDB.ExecContext(ctx, "INSERT OR REPLACE INTO settings (key, value) VALUES ('ssl', '{\"enabled\":true,\"cert_text\":\"PlainCert\",\"key_text\":\"PlainKey\"}')")

	// Run migrations
	if err := db.runMigrationsLocked(ctx); err != nil {
		t.Fatalf("runMigrationsLocked failed: %v", err)
	}

	// Verify server credentials got encrypted
	var encPass, encKey, protoJSON string
	_ = db.sqlDB.QueryRowContext(ctx, "SELECT ssh_pass, ssh_key, protocols FROM servers WHERE id = 1").Scan(&encPass, &encKey, &protoJSON)
	if encPass == "PlainPass" || encKey == "PlainKey" {
		t.Errorf("credentials were not encrypted during migration: pass=%q, key=%q", encPass, encKey)
	}
	srv, _ := db.GetServer(ctx, 1)
	if srv.SSHPass != "PlainPass" || srv.SSHKey != "PlainKey" {
		t.Errorf("decrypted migrated server credentials mismatch: pass=%q, key=%q", srv.SSHPass, srv.SSHKey)
	}

	// Verify xray private key was stripped
	if xmap, ok := srv.Protocols["xray"].(map[string]any); ok && xmap["reality_private_key"] != nil {
		t.Errorf("reality_private_key was not stripped by migration")
	}

	// Verify SSL keys got encrypted
	sslRet, _ := db.GetSSLSettings(ctx)
	if sslRet.CertText != "PlainCert" || sslRet.KeyText != "PlainKey" {
		t.Errorf("SSL migrated credentials mismatch: cert=%q, key=%q", sslRet.CertText, sslRet.KeyText)
	}

	// Test migration when secretKey is empty
	dbNoSecret := &DB{
		sqlDB:     db.sqlDB,
		secretKey: "",
	}
	if err := dbNoSecret.migratePlaintextCredentials(ctx); err != nil {
		t.Errorf("migratePlaintextCredentials without secretKey error = %v", err)
	}
	if err := dbNoSecret.migratePlaintextSSLKeys(ctx); err != nil {
		t.Errorf("migratePlaintextSSLKeys without secretKey error = %v", err)
	}

	_ = db.Close()
}
