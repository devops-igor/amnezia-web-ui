package database_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/devops-igor/amnezia-web-ui-go/internal/database"
	"github.com/devops-igor/amnezia-web-ui-go/internal/models"
	"github.com/devops-igor/amnezia-web-ui-go/internal/security"
	_ "modernc.org/sqlite"
)

// LegacyPythonSchemaDDL represents the exact SQLite schema from legacy Python app/core/schema.sql
// which contains the 8 original tables and lacks the additive VPN tables (backend_tunnels, vpn_sessions).
const LegacyPythonSchemaDDL = `
PRAGMA journal_mode=WAL;

CREATE TABLE IF NOT EXISTS servers (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    host TEXT NOT NULL,
    ssh_user TEXT,
    ssh_port INTEGER DEFAULT 22,
    ssh_pass TEXT,
    ssh_key TEXT,
    protocols TEXT,
    created_at TEXT
);

CREATE TABLE IF NOT EXISTS users (
    id TEXT PRIMARY KEY,
    username TEXT NOT NULL,
    email TEXT,
    telegramId TEXT,
    description TEXT,
    password_hash TEXT,
    role TEXT NOT NULL DEFAULT 'user',
    enabled INTEGER NOT NULL DEFAULT 1,
    traffic_limit INTEGER,
    traffic_used INTEGER DEFAULT 0,
    traffic_total INTEGER DEFAULT 0,
    traffic_total_rx INTEGER DEFAULT 0,
    traffic_total_tx INTEGER DEFAULT 0,
    monthly_rx INTEGER DEFAULT 0,
    monthly_tx INTEGER DEFAULT 0,
    monthly_reset_at TEXT,
    traffic_reset_strategy TEXT DEFAULT 'never',
    share_enabled INTEGER DEFAULT 0,
    share_token TEXT,
    share_password_hash TEXT,
    remnawave_uuid TEXT,
    created_at TEXT,
    last_reset_at TEXT,
    expiration_date TEXT,
    expires_at TEXT,
    awg_mimicry TEXT DEFAULT 'auto',
    password_change_required INTEGER NOT NULL DEFAULT 0,
    limits TEXT
);

CREATE TABLE IF NOT EXISTS user_connections (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL,
    server_id INTEGER NOT NULL,
    protocol TEXT NOT NULL,
    client_id TEXT,
    name TEXT,
    awg_mimicry TEXT DEFAULT 'auto',
    last_rx INTEGER DEFAULT 0,
    last_tx INTEGER DEFAULT 0,
    traffic_delta_rx INTEGER DEFAULT 0,
    traffic_delta_tx INTEGER DEFAULT 0,
    traffic_total_rx INTEGER DEFAULT 0,
    traffic_total_tx INTEGER DEFAULT 0,
    traffic_total INTEGER DEFAULT 0,
    created_at TEXT,
    FOREIGN KEY (user_id) REFERENCES users(id)
);

CREATE TABLE IF NOT EXISTS connection_creation_log (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id TEXT NOT NULL,
    created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS settings (
    key TEXT PRIMARY KEY,
    value TEXT
);

CREATE TABLE IF NOT EXISTS migration_flags (
    key TEXT PRIMARY KEY,
    value TEXT
);

CREATE INDEX IF NOT EXISTS idx_user_connections_user_id ON user_connections(user_id);
CREATE INDEX IF NOT EXISTS idx_user_connections_server_id ON user_connections(server_id);
CREATE INDEX IF NOT EXISTS idx_creation_log_user_time ON connection_creation_log(user_id, created_at);

CREATE TABLE IF NOT EXISTS known_hosts (
    server_id INTEGER PRIMARY KEY,
    fingerprint TEXT NOT NULL,
    first_seen TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (server_id) REFERENCES servers(id)
);

CREATE TABLE IF NOT EXISTS leaderboard_snapshots (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    year INTEGER NOT NULL,
    month INTEGER NOT NULL,
    username TEXT NOT NULL,
    rank INTEGER NOT NULL,
    download INTEGER NOT NULL,
    upload INTEGER NOT NULL,
    total INTEGER NOT NULL,
    snapshot_at TEXT NOT NULL,
    UNIQUE(year, month, username)
);
`

func TestLegacySQLiteMigrationCompatibility(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "legacy_panel.db")
	masterSecretKey := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

	// 1. Manually create legacy database with pure Python DDL (no backend_tunnels, no vpn_sessions)
	rawDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("failed to open raw sqlite db: %v", err)
	}

	if _, err := rawDB.Exec(LegacyPythonSchemaDDL); err != nil {
		rawDB.Close()
		t.Fatalf("failed to execute legacy DDL: %v", err)
	}

	// 2. Prepare test credentials and hashes
	// Legacy Fernet encrypted passwords/keys
	plainSSHPass1 := "SuperSecretServerPassword123!"
	encSSHPass1, err := security.EncryptCredential(plainSSHPass1, masterSecretKey)
	if err != nil {
		t.Fatalf("failed to encrypt ssh pass: %v", err)
	}

	plainSSHKey1 := "-----BEGIN OPENSSH PRIVATE KEY-----\nb3BlbnNzaC1rZXktdjEAAAAABG5vbmUAAAAEbm9uZQAAAAAAAAABAAABlwAAAAdzc2gtcn\nNhAAAAAwEAAQAAAYEAv8K...test_key...\n-----END OPENSSH PRIVATE KEY-----"
	encSSHKey1, err := security.EncryptCredential(plainSSHKey1, masterSecretKey)
	if err != nil {
		t.Fatalf("failed to encrypt ssh key: %v", err)
	}

	// Plaintext credentials for server 2 to test auto-migration of unencrypted credentials
	plainSSHPass2 := "PlaintextLegacyPassword456!"
	plainSSHKey2 := "-----BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAKCAQEA0_legacy_plaintext_key\n-----END RSA PRIVATE KEY-----"

	// Insert legacy servers
	_, err = rawDB.Exec(`
		INSERT INTO servers (id, name, host, ssh_user, ssh_port, ssh_pass, ssh_key, protocols, created_at)
		VALUES 
		(1, 'US-East-Legacy', '198.51.100.10', 'root', 22, ?, ?, '{"awg":{"port":51820,"installed":true,"reality_private_key":"REPLACE_ME_SECRET"}}', '2026-01-15T10:00:00Z'),
		(2, 'EU-West-Legacy', '198.51.100.20', 'ubuntu', 2222, ?, ?, '{"openvpn":{"port":1194,"installed":true}}', '2026-02-01T12:00:00Z')
	`, encSSHPass1, encSSHKey1, plainSSHPass2, plainSSHKey2)
	if err != nil {
		rawDB.Close()
		t.Fatalf("failed to seed legacy servers: %v", err)
	}

	// Real Python-generated legacy bcrypt and PBKDF2 hashes
	// Hash 1: Python bcrypt hash for standard password "AdminLegacyPass123!"
	adminPass := "AdminLegacyPass123!"
	adminHash := "$2b$12$648jFKq7gtNPFVI7FvIPMO6X3zTiSuMZaDpp/8r610jYMeV2nJLCy"

	// Hash 2: Python bcrypt hash for >72-byte password truncated via bcrypt.hashpw(pw[:72], gensalt())
	longUserPass := "VeryLongLegacyUserPasswordExceeding72BytesThreshold1234567890!ExtraCharactersHereToMakeItOver100BytesLong"
	longUserHash := "$2b$12$qgLpV6UPGD7.bJzNFE75KO/obhD7JCfH2L1uIraqfIM4L4/5xzswu"

	// Hash 3: Python legacy PBKDF2 hash (salt$hex, 100,000 iterations)
	bobPass := "BobLegacyPBKDF2Pass2026!"
	bobHash := "legacy_bob_salt_1234$e7f038f1ff7c2feaa617ce72eb67e447b78964d78b8723917b87025038704cd6"

	// Insert legacy users
	now := time.Now().UTC().Format(time.RFC3339)
	_, err = rawDB.Exec(`
		INSERT INTO users (
			id, username, email, telegramId, description, password_hash, role, enabled,
			traffic_limit, traffic_used, traffic_total, traffic_total_rx, traffic_total_tx,
			monthly_rx, monthly_tx, monthly_reset_at, traffic_reset_strategy,
			share_enabled, share_token, share_password_hash, remnawave_uuid,
			created_at, last_reset_at, expiration_date, expires_at, awg_mimicry, password_change_required, limits
		) VALUES 
		('u-admin-1', 'admin_legacy', 'admin@example.com', '123456', 'Main Admin', ?, 'admin', 1, 0, 1024, 2048, 1024, 1024, 512, 512, ?, 'monthly', 0, NULL, NULL, NULL, ?, ?, NULL, NULL, 'auto', 0, '{}'),
		('u-user-2', 'alice_longpass', 'alice@example.com', '789012', 'Power User', ?, 'user', 1, 10737418240, 5368709120, 5368709120, 2684354560, 2684354560, 2684354560, 2684354560, ?, 'monthly', 1, 'token-alice-123', '$2b$12$shareHashAlice', 'remna-uuid-alice', ?, ?, '2027-01-01T00:00:00Z', '2027-01-01T00:00:00Z', 'quic', 0, '{"max_connections":5}'),
		('u-user-3', 'bob_legacy', 'bob@example.com', NULL, 'Standard User', ?, 'user', 1, 5368709120, 1048576, 1048576, 524288, 524288, 524288, 524288, ?, 'never', 0, NULL, NULL, NULL, ?, ?, NULL, NULL, 'auto', 1, '{}')
	`, adminHash, now, now, now, longUserHash, now, now, now, bobHash, now, now, now)
	if err != nil {
		rawDB.Close()
		t.Fatalf("failed to seed legacy users: %v", err)
	}

	// Insert legacy user_connections
	_, err = rawDB.Exec(`
		INSERT INTO user_connections (
			id, user_id, server_id, protocol, client_id, name, awg_mimicry,
			last_rx, last_tx, traffic_delta_rx, traffic_delta_tx, traffic_total_rx, traffic_total_tx, traffic_total, created_at
		) VALUES 
		('conn-1', 'u-admin-1', 1, 'awg', 'client-uuid-1', 'Admin AWG Laptop', 'quic', 1000, 2000, 500, 500, 1000, 2000, 3000, ?),
		('conn-2', 'u-user-2', 1, 'awg', 'client-uuid-2', 'Alice AWG Phone', 'dns', 5000, 5000, 1000, 1000, 5000, 5000, 10000, ?),
		('conn-3', 'u-user-3', 2, 'openvpn', 'client-uuid-3', 'Bob OpenVPN', 'auto', 200, 300, 100, 100, 200, 300, 500, ?)
	`, now, now, now)
	if err != nil {
		rawDB.Close()
		t.Fatalf("failed to seed legacy connections: %v", err)
	}

	// Insert connection creation log
	_, err = rawDB.Exec(`
		INSERT INTO connection_creation_log (user_id, created_at)
		VALUES ('u-user-2', ?), ('u-user-3', ?)
	`, now, now)
	if err != nil {
		rawDB.Close()
		t.Fatalf("failed to seed connection creation log: %v", err)
	}

	// Legacy settings with Fernet-encrypted SSL certificate & private key
	plainSSLCert := "-----BEGIN CERTIFICATE-----\nMIIBkDCB+wIJAL...test_legacy_ssl_cert...\n-----END CERTIFICATE-----"
	plainSSLKey := "-----BEGIN PRIVATE KEY-----\nMIGHAgEAMBMGByqGSM49AgEGPC...test_legacy_ssl_key...\n-----END PRIVATE KEY-----"
	encSSLCert, err := security.EncryptCredential(plainSSLCert, masterSecretKey)
	if err != nil {
		t.Fatalf("failed to encrypt ssl cert: %v", err)
	}
	encSSLKey, err := security.EncryptCredential(plainSSLKey, masterSecretKey)
	if err != nil {
		t.Fatalf("failed to encrypt ssl key: %v", err)
	}

	legacySSLJSON := fmt.Sprintf(`{"enabled":true,"domain":"vpn.legacy.org","cert_path":"","key_path":"","cert_text":%q,"key_text":%q,"panel_port":5000}`, encSSLCert, encSSLKey)
	legacyAppearanceJSON := `{"title":"Custom Legacy Panel","logo":"🛡️","subtitle":"Enterprise VPN","language":"ru"}`
	legacySyncJSON := `{"remnawave_url":"https://api.remna.legacy","remnawave_api_key":"secret-remna-key-123","remnawave_sync":true,"remnawave_sync_users":true,"remnawave_create_conns":false,"remnawave_server_id":1,"remnawave_protocol":"awg"}`

	// Seed schema_version as '1' (plain integer string matching legacy Python database output)
	_, err = rawDB.Exec(`
		INSERT INTO settings (key, value) VALUES 
		('appearance', ?),
		('ssl', ?),
		('sync', ?),
		('schema_version', '1')
	`, legacyAppearanceJSON, legacySSLJSON, legacySyncJSON)
	if err != nil {
		rawDB.Close()
		t.Fatalf("failed to seed legacy settings: %v", err)
	}

	// Known hosts
	_, err = rawDB.Exec(`
		INSERT INTO known_hosts (server_id, fingerprint, first_seen)
		VALUES (1, 'SHA256:legacyHostKeyFingerprintServer1111111111111111', ?)
	`, now)
	if err != nil {
		rawDB.Close()
		t.Fatalf("failed to seed known hosts: %v", err)
	}

	// Leaderboard snapshots
	_, err = rawDB.Exec(`
		INSERT INTO leaderboard_snapshots (year, month, username, rank, download, upload, total, snapshot_at)
		VALUES 
		(2025, 12, 'alice_longpass', 1, 10000000000, 5000000000, 15000000000, '2025-12-31T23:59:59Z'),
		(2025, 12, 'bob_legacy', 2, 2000000000, 1000000000, 3000000000, '2025-12-31T23:59:59Z')
	`)
	if err != nil {
		rawDB.Close()
		t.Fatalf("failed to seed leaderboard snapshots: %v", err)
	}

	// Close raw DB connection before opening with Go database package
	if err := rawDB.Close(); err != nil {
		t.Fatalf("failed to close raw DB: %v", err)
	}

	// =========================================================================
	// 3. STAGING SIMULATION: Open legacy database via Go database.Open()
	// =========================================================================
	db, err := database.Open(dbPath, masterSecretKey)
	if err != nil {
		t.Fatalf("database.Open failed on legacy SQLite database: %v", err)
	}
	defer db.Close()

	// =========================================================================
	// 4. VERIFY ADDITIVE SCHEMAS WERE SAFELY APPLIED
	// =========================================================================

	nowTime := time.Now().UTC()
	// Verify backend_tunnels table exists and accepts records
	bt := &models.BackendTunnel{
		ServerID:          1,
		InterfaceName:     "awg-be-1",
		PublicKey:         "PubTestKeyBackendTunnel11111111111111111111=",
		PrivateKey:        "PrivTestKeyBackendTunnel1111111111111111111=",
		Endpoint:          "198.51.100.10:51820",
		Status:            "active",
		LastHealthCheck:   &nowTime,
		LatencyMS:         15,
		ActiveConnections: 1,
		CreatedAt:         nowTime,
	}
	btID, err := db.CreateBackendTunnel(ctx, bt)
	if err != nil {
		t.Fatalf("failed to insert into additive backend_tunnels table: %v", err)
	}
	if btID <= 0 {
		t.Fatalf("expected valid backend tunnel ID > 0, got %d", btID)
	}

	fetchedBT, err := db.GetBackendTunnelByID(ctx, btID)
	if err != nil {
		t.Fatalf("failed to get backend tunnel: %v", err)
	}
	if fetchedBT.InterfaceName != "awg-be-1" || fetchedBT.ServerID != 1 {
		t.Errorf("backend tunnel mismatch: %+v", fetchedBT)
	}

	// Verify vpn_sessions table exists and accepts records
	vpnSess := &models.VPNSession{
		ID:              "sess-uuid-1",
		UserID:          "u-admin-1",
		BackendTunnelID: btID,
		PeerPublicKey:   "PeerPublicKeyTestSession111111111111111111=",
		AssignedIP:      "10.100.0.2",
		ConnectedAt:     nowTime,
		LastSeen:        nowTime,
		RxBytes:         50000,
		TxBytes:         75000,
		Status:          "connected",
	}
	if err := db.CreateVPNSession(ctx, vpnSess); err != nil {
		t.Fatalf("failed to insert into additive vpn_sessions table: %v", err)
	}

	fetchedSess, err := db.GetVPNSessionByID(ctx, "sess-uuid-1")
	if err != nil {
		t.Fatalf("failed to fetch VPN session: %v", err)
	}
	if fetchedSess.AssignedIP != "10.100.0.2" || fetchedSess.UserID != "u-admin-1" {
		t.Errorf("vpn session mismatch: %+v", fetchedSess)
	}

	// Verify vpn_config setting was safely seeded without overwriting existing settings
	vpnCfg, err := db.GetVPNConfig(ctx)
	if err != nil {
		t.Fatalf("failed to get seeded vpn_config: %v", err)
	}
	if vpnCfg.ListenPort != 51820 || vpnCfg.SubnetCIDR != "10.100.0.0/16" {
		t.Errorf("unexpected default vpn_config: %+v", vpnCfg)
	}

	// =========================================================================
	// 5. VERIFY ZERO DATA LOSS & EXACT PRESERVATION OF LEGACY DATA
	// =========================================================================

	// Servers preservation and transparent credential decryption
	srv1, err := db.GetServerByID(ctx, 1)
	if err != nil {
		t.Fatalf("failed to get server 1: %v", err)
	}
	if srv1.Name != "US-East-Legacy" || srv1.Host != "198.51.100.10" || srv1.SSHUser != "root" {
		t.Errorf("server 1 metadata corrupted: %+v", srv1)
	}
	if srv1.SSHPass != plainSSHPass1 {
		t.Errorf("server 1 SSH password failed to decrypt: expected %q, got %q", plainSSHPass1, srv1.SSHPass)
	}
	if srv1.SSHKey != plainSSHKey1 {
		t.Errorf("server 1 SSH private key failed to decrypt: expected %q, got %q", plainSSHKey1, srv1.SSHKey)
	}

	// Server 2 unencrypted credentials migrated to encrypted
	srv2, err := db.GetServerByID(ctx, 2)
	if err != nil {
		t.Fatalf("failed to get server 2: %v", err)
	}
	if srv2.Name != "EU-West-Legacy" || srv2.Host != "198.51.100.20" || srv2.SSHPort != 2222 {
		t.Errorf("server 2 metadata corrupted: %+v", srv2)
	}
	if srv2.SSHPass != plainSSHPass2 {
		t.Errorf("server 2 SSH password mismatch: expected %q, got %q", plainSSHPass2, srv2.SSHPass)
	}
	if srv2.SSHKey != plainSSHKey2 {
		t.Errorf("server 2 SSH private key mismatch: expected %q, got %q", plainSSHKey2, srv2.SSHKey)
	}

	// Users preservation and password hash cross-authentication
	users, err := db.GetAllUsers(ctx)
	if err != nil {
		t.Fatalf("failed to list users: %v", err)
	}
	if len(users) != 3 {
		t.Fatalf("expected 3 users, got %d", len(users))
	}

	userAdmin, err := db.GetUser(ctx, "u-admin-1")
	if err != nil {
		t.Fatalf("failed to get admin user: %v", err)
	}
	if userAdmin.Username != "admin_legacy" || userAdmin.Role != models.RoleAdmin {
		t.Errorf("admin user record corrupted: %+v", userAdmin)
	}
	if !security.CheckPasswordHash(adminPass, userAdmin.PasswordHash) {
		t.Errorf("admin legacy password failed authentication check")
	}

	userAlice, err := db.GetUser(ctx, "u-user-2")
	if err != nil {
		t.Fatalf("failed to get alice user: %v", err)
	}
	if userAlice.Username != "alice_longpass" || userAlice.Role != models.RoleUser || !userAlice.ShareEnabled {
		t.Errorf("alice user record corrupted: %+v", userAlice)
	}
	// Verify legacy >72 byte password authentication with direct compare fallback
	if !security.CheckPasswordHash(longUserPass, userAlice.PasswordHash) {
		t.Errorf("alice >72-byte password failed authentication check")
	}
	if security.CheckPasswordHash("WrongPrefix_"+longUserPass, userAlice.PasswordHash) {
		t.Errorf("alice incorrect password falsely verified")
	}

	userBob, err := db.GetUser(ctx, "u-user-3")
	if err != nil {
		t.Fatalf("failed to get bob user: %v", err)
	}
	if userBob.Username != "bob_legacy" || !userBob.PasswordChangeRequired {
		t.Errorf("bob user record corrupted: %+v", userBob)
	}
	if !security.CheckPasswordHash(bobPass, userBob.PasswordHash) {
		t.Errorf("bob legacy PBKDF2 password failed authentication check")
	}
	if security.CheckPasswordHash("WrongPassword!", userBob.PasswordHash) {
		t.Errorf("bob incorrect password falsely verified")
	}

	// User connections preservation
	conns, err := db.GetAllConnections(ctx)
	if err != nil {
		t.Fatalf("failed to list connections: %v", err)
	}
	if len(conns) != 3 {
		t.Fatalf("expected 3 connections, got %d", len(conns))
	}

	adminConns, err := db.GetConnectionsByUserID(ctx, "u-admin-1")
	if err != nil {
		t.Fatalf("failed to get admin connections: %v", err)
	}
	if len(adminConns) != 1 || adminConns[0].Name != "Admin AWG Laptop" || adminConns[0].Protocol != "awg" {
		t.Errorf("admin connection corrupted: %+v", adminConns)
	}

	// Settings preservation and SSL decryption
	var appSettings struct {
		Title    string `json:"title"`
		Language string `json:"language"`
	}
	if err := db.GetSetting(ctx, "appearance", &appSettings); err != nil {
		t.Fatalf("failed to get appearance settings: %v", err)
	}
	if appSettings.Title != "Custom Legacy Panel" || appSettings.Language != "ru" {
		t.Errorf("appearance settings corrupted: %+v", appSettings)
	}

	var syncSettings struct {
		RemnawaveURL  string `json:"remnawave_url"`
		RemnawaveSync bool   `json:"remnawave_sync"`
	}
	if err := db.GetSetting(ctx, "sync", &syncSettings); err != nil {
		t.Fatalf("failed to get sync settings: %v", err)
	}
	if syncSettings.RemnawaveURL != "https://api.remna.legacy" || !syncSettings.RemnawaveSync {
		t.Errorf("sync settings corrupted: %+v", syncSettings)
	}

	sslSettings, err := db.GetSSLSettings(ctx)
	if err != nil {
		t.Fatalf("failed to get ssl settings: %v", err)
	}
	if !sslSettings.Enabled || sslSettings.Domain != "vpn.legacy.org" {
		t.Errorf("ssl settings metadata corrupted: %+v", sslSettings)
	}
	if sslSettings.CertText != plainSSLCert {
		t.Errorf("ssl cert text failed to decrypt: expected %q, got %q", plainSSLCert, sslSettings.CertText)
	}
	if sslSettings.KeyText != plainSSLKey {
		t.Errorf("ssl key text failed to decrypt: expected %q, got %q", plainSSLKey, sslSettings.KeyText)
	}

	// Known hosts preservation
	fp, err := db.GetKnownHostFingerprint(ctx, 1)
	if err != nil {
		t.Fatalf("failed to get known host fingerprint: %v", err)
	}
	if fp != "SHA256:legacyHostKeyFingerprintServer1111111111111111" {
		t.Errorf("known host fingerprint corrupted: got %q", fp)
	}

	// Leaderboard snapshots preservation
	snaps, err := db.GetLeaderboardSnapshot(ctx, 2025, 12)
	if err != nil {
		t.Fatalf("failed to get leaderboard snapshots: %v", err)
	}
	if len(snaps) != 2 {
		t.Fatalf("expected 2 leaderboard snapshots, got %d", len(snaps))
	}
	if snaps[0].Username != "alice_longpass" || snaps[0].Rank != 1 || snaps[1].Username != "bob_legacy" || snaps[1].Rank != 2 {
		t.Errorf("leaderboard snapshots corrupted: %+v", snaps)
	}

	// Schema version preservation
	schemaVer, err := db.GetSchemaVersion(ctx)
	if err != nil || schemaVer != 1 {
		t.Errorf("schema version corrupted or unreadable: expected 1, got %d (err: %v)", schemaVer, err)
	}

	// =========================================================================
	// 6. CASCADE DELETION VERIFICATION ACROSS LEGACY AND ADDITIVE SCHEMAS
	// =========================================================================

	// Delete server 1 — should cascade delete known_hosts, backend_tunnels, and vpn_sessions
	if _, err := db.DeleteServer(ctx, 1); err != nil {
		t.Fatalf("failed to delete server 1: %v", err)
	}

	// Verify server 1 is deleted
	if s, err := db.GetServerByID(ctx, 1); err != nil || s != nil {
		t.Errorf("expected server 1 to be deleted, got %+v, err: %v", s, err)
	}

	// Verify known_hosts for server 1 is cascade-deleted
	if fp, err := db.GetKnownHostFingerprint(ctx, 1); err != nil || fp != "" {
		t.Errorf("expected known_hosts entry for server 1 to be cascade-deleted, got %q, err: %v", fp, err)
	}

	// Verify backend tunnel for server 1 is cascade-deleted
	if bt, err := db.GetBackendTunnelByID(ctx, btID); err != nil || bt != nil {
		t.Errorf("expected backend tunnel %d to be cascade-deleted, got %+v, err: %v", btID, bt, err)
	}

	// Verify vpn session linked to backend tunnel is cascade-deleted
	if sess, err := db.GetVPNSessionByID(ctx, "sess-uuid-1"); err != nil || sess != nil {
		t.Errorf("expected vpn session sess-uuid-1 to be cascade-deleted, got %+v, err: %v", sess, err)
	}
}

func TestLegacyPlaintextCredentialAutoMigration(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "unmigrated_legacy.db")
	masterSecretKey := "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"

	rawDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("failed to open raw sqlite db: %v", err)
	}

	if _, err := rawDB.Exec(LegacyPythonSchemaDDL); err != nil {
		rawDB.Close()
		t.Fatalf("failed to execute legacy DDL: %v", err)
	}

	// Insert servers with plaintext passwords and Xray private keys in protocols JSON
	plainPass := "UnencryptedServerPass999!"
	plainKey := "-----BEGIN RSA PRIVATE KEY-----\nMIIPlaintextKey...\n-----END RSA PRIVATE KEY-----"
	protoWithXray := `{"xray":{"port":443,"installed":true,"reality_private_key":"SuperSecretXrayPrivateKey","public_key":"pub123"}}`

	_, err = rawDB.Exec(`
		INSERT INTO servers (id, name, host, ssh_user, ssh_port, ssh_pass, ssh_key, protocols, created_at)
		VALUES (10, 'Unmigrated-Server', '203.0.113.50', 'admin', 22, ?, ?, ?, '2026-01-01T00:00:00Z')
	`, plainPass, plainKey, protoWithXray)
	if err != nil {
		rawDB.Close()
		t.Fatalf("failed to seed unmigrated server: %v", err)
	}

	// Insert unencrypted SSL keys in settings
	plainSSLCert := "-----BEGIN CERTIFICATE-----\nMIIUnencryptedSSLCert...\n-----END CERTIFICATE-----"
	plainSSLKey := "-----BEGIN PRIVATE KEY-----\nMIIUnencryptedSSLKey...\n-----END PRIVATE KEY-----"
	unencryptedSSLJSON := fmt.Sprintf(`{"enabled":true,"domain":"secure.vpn.org","cert_text":%q,"key_text":%q}`, plainSSLCert, plainSSLKey)

	_, err = rawDB.Exec(`
		INSERT INTO settings (key, value) VALUES ('ssl', ?)
	`, unencryptedSSLJSON)
	if err != nil {
		rawDB.Close()
		t.Fatalf("failed to seed unencrypted ssl setting: %v", err)
	}

	if err := rawDB.Close(); err != nil {
		t.Fatalf("failed to close raw db: %v", err)
	}

	// Open with Go database package — triggers runMigrationsLocked
	db, err := database.Open(dbPath, masterSecretKey)
	if err != nil {
		t.Fatalf("database.Open failed: %v", err)
	}
	defer db.Close()

	// 1. Verify migration flags were recorded
	credFlag, _ := db.GetMigrationFlag(ctx, "credentials_encrypted")
	xrayFlag, _ := db.GetMigrationFlag(ctx, "xray_private_keys_cleared")
	sslFlag, _ := db.GetMigrationFlag(ctx, "ssl_keys_encrypted")

	if credFlag != "1" || xrayFlag != "1" || sslFlag != "1" {
		t.Errorf("migration flags not set properly: cred=%q, xray=%q, ssl=%q", credFlag, xrayFlag, sslFlag)
	}

	// 2. Verify server credentials decrypt seamlessly
	srv, err := db.GetServerByID(ctx, 10)
	if err != nil {
		t.Fatalf("failed to get server: %v", err)
	}
	if srv.SSHPass != plainPass {
		t.Errorf("server password mismatch: expected %q, got %q", plainPass, srv.SSHPass)
	}
	if srv.SSHKey != plainKey {
		t.Errorf("server private key mismatch: expected %q, got %q", plainKey, srv.SSHKey)
	}

	// 3. Verify sensitive Xray private key was stripped from stored protocols
	if xrayMap, ok := srv.Protocols["xray"].(map[string]any); ok {
		if _, hasPrivKey := xrayMap["reality_private_key"]; hasPrivKey {
			t.Errorf("reality_private_key was not stripped from protocols JSON")
		}
	}

	// 4. Verify SSL settings decrypt seamlessly
	ssl, err := db.GetSSLSettings(ctx)
	if err != nil {
		t.Fatalf("failed to get ssl settings: %v", err)
	}
	if ssl.CertText != plainSSLCert || ssl.KeyText != plainSSLKey {
		t.Errorf("ssl cert/key decrypted mismatch: cert=%q, key=%q", ssl.CertText, ssl.KeyText)
	}
}

func TestLegacyDatabaseDataJSONMigrationRoundtrip(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	dataJSONPath := filepath.Join(tempDir, "data.json")
	dbPath := filepath.Join(tempDir, "migrated_from_json.db")
	masterSecretKey := "fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210"

	// Create legacy data.json file
	legacyJSON := `{
		"servers": [
			{
				"id": 1,
				"name": "JSON-Migrated-Server",
				"host": "192.0.2.1",
				"username": "root",
				"ssh_port": 22,
				"password": "ServerPasswordFromJSON123",
				"private_key": "-----BEGIN RSA KEY-----\nMII...\n-----END RSA KEY-----",
				"protocols": {"awg": {"port": 51820, "installed": true}},
				"created_at": "2026-03-01T00:00:00Z"
			}
		],
		"users": [
			{
				"id": "u-json-1",
				"username": "json_user",
				"email": "json@example.com",
				"password_hash": "$2b$12$u1MSQrJFIcgW/9euUst0POT./x1w8hmB.dX6t.ZP9ct/XvoTSPu6O",
				"role": "user",
				"enabled": true,
				"traffic_limit": 1000000,
				"traffic_used": 50000,
				"created_at": "2026-03-01T00:00:00Z"
			}
		],
		"user_connections": [
			{
				"id": "conn-json-1",
				"user_id": "u-json-1",
				"server_id": 1,
				"protocol": "awg",
				"client_id": "client-uuid-json",
				"name": "JSON Conn",
				"awg_mimicry": "auto",
				"created_at": "2026-03-01T00:00:00Z"
			}
		],
		"settings": {
			"appearance": {"title": "Migrated Panel", "logo": "🛡️", "language": "fr"},
			"limits": {"max_connections_per_user": 5}
		}
	}`

	if err := os.WriteFile(dataJSONPath, []byte(legacyJSON), 0600); err != nil {
		t.Fatalf("failed to write data.json: %v", err)
	}

	// Execute MigrateFromDataJSON
	if err := database.MigrateFromDataJSON(dataJSONPath, dbPath, masterSecretKey); err != nil {
		t.Fatalf("MigrateFromDataJSON failed: %v", err)
	}

	// Verify data.json was renamed to data.json.bak
	if _, err := os.Stat(dataJSONPath + ".bak"); err != nil {
		t.Errorf("expected data.json.bak to exist after migration: %v", err)
	}

	// Open migrated DB and verify contents
	db, err := database.Open(dbPath, masterSecretKey)
	if err != nil {
		t.Fatalf("failed to open migrated DB: %v", err)
	}
	defer db.Close()

	srv, err := db.GetServerByID(ctx, 1)
	if err != nil || srv == nil {
		t.Fatalf("failed to fetch migrated server: %v", err)
	}
	if srv.Name != "JSON-Migrated-Server" || srv.SSHPass != "ServerPasswordFromJSON123" {
		t.Errorf("server fields mismatch: %+v", srv)
	}

	u, err := db.GetUser(ctx, "u-json-1")
	if err != nil || u == nil {
		t.Fatalf("failed to fetch migrated user: %v", err)
	}
	if u.Username != "json_user" || u.Email == nil || *u.Email != "json@example.com" {
		t.Errorf("user fields mismatch: %+v", u)
	}

	// Verify LoadData roundtrip
	backup, err := db.LoadData(ctx)
	if err != nil {
		t.Fatalf("LoadData failed: %v", err)
	}
	if len(backup.Servers) != 1 || len(backup.Users) != 1 || len(backup.UserConnections) != 1 {
		t.Errorf("backup data mismatch: %+v", backup)
	}
}

func TestLegacyBcryptPasswordEdgeCases(t *testing.T) {
	testCases := []struct {
		name       string
		password   string
		legacyHash string
	}{
		{"empty", "", ""},
		{"short", "a", ""},
		{"typical", "Correct-Horse-Battery-Staple-2026!", "$2b$12$ZFcggG8jKwtU72Iw5r52c.fEMPre6DdgFpJ3aEYoTfC4GYOcHELs2"},
		{"unicode", "Пароль🛡️ОченьСекретный2026!", "$2b$12$ML8F.RCv8yFQreNNB4MnyuobGEeEIS7hUG/w8M3Mc.L2ADJk7Koa."},
		{"exact_72_bytes", strings.Repeat("A", 72), "$2b$12$E/hctodEbNQii7NfQnhex.2ECNdwYUmMj0bpOz9n4SlhC4GurOpey"},
		{"73_bytes_boundary", strings.Repeat("B", 73), "$2b$12$NjDBkA2xgl3uyUh/skoZWOJ5p20ZqIqI4JaRjKrTDYAvdgdhY0Ife"},
		{"101_bytes_legacy_long", "VeryLongLegacyPasswordExceeding72BytesThreshold1234567890!ExtraCharactersHereToMakeItOver100BytesLong", "$2b$12$3uddR.vC35.4MVGlGClYq.9IBKJM32DvP60PQaJ9i3wU7nnFqf.su"},
		{"128_bytes_long", strings.Repeat("C", 128), ""},
		{"512_bytes_super_long", strings.Repeat("VeryLongPasswordCharacterSequence!", 15), ""},
		{"legacy_pbkdf2", "LegacyPBKDF2Password123!", "test_salt_12345$ffbf33339ad49954872dc1d1d4c3d4537e3aaf72730b50ca56a6d4ec2c11ba69"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// 1. Verify Go HashPassword roundtrip
			hash, err := security.HashPassword(tc.password)
			if err != nil {
				t.Fatalf("HashPassword failed for %s: %v", tc.name, err)
			}

			if !security.CheckPasswordHash(tc.password, hash) {
				t.Errorf("CheckPasswordHash failed for Go-generated hash on %s", tc.name)
			}

			if security.CheckPasswordHash("WrongPrefix_"+tc.password, hash) {
				t.Errorf("CheckPasswordHash falsely matched incorrect password for %s", tc.name)
			}

			// 2. Verify Genuine Python-generated legacy hash (if provided)
			if tc.legacyHash != "" {
				if !security.CheckPasswordHash(tc.password, tc.legacyHash) {
					t.Errorf("CheckPasswordHash failed for genuine Python-generated hash on %s", tc.name)
				}
				if security.CheckPasswordHash("WrongPrefix_"+tc.password, tc.legacyHash) {
					t.Errorf("CheckPasswordHash falsely verified wrong password for Python hash on %s", tc.name)
				}
			}
		})
	}
}
