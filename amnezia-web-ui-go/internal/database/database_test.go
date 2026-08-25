package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/devops-igor/amnezia-web-ui-go/internal/models"
)

func setupTestDB(t *testing.T) (*DB, string) {
	t.Helper()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_panel.db")
	secretKey := "test-secret-key-1234567890abcdef1234567890abcdef"

	db, err := Open(dbPath, secretKey)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	t.Cleanup(func() {
		_ = db.Close()
	})

	return db, secretKey
}

func TestDatabaseInitSchemaAndPing(t *testing.T) {
	db, _ := setupTestDB(t)
	ctx := context.Background()

	if err := db.Ping(ctx); err != nil {
		t.Fatalf("Ping failed: %v", err)
	}

	tables := []string{
		"servers", "users", "user_connections", "connection_creation_log",
		"settings", "migration_flags", "known_hosts", "leaderboard_snapshots",
		"backend_tunnels", "vpn_sessions",
	}

	for _, tbl := range tables {
		var name string
		query := "SELECT name FROM sqlite_master WHERE type='table' AND name=?"
		err := db.QueryRowContext(ctx, query, tbl).Scan(&name)
		if err != nil {
			t.Errorf("table %s was not created: %v", tbl, err)
		}
	}
}

func TestDatabaseConcurrentReadWrite(t *testing.T) {
	db, _ := setupTestDB(t)
	ctx := context.Background()

	var wg sync.WaitGroup
	workers := 10
	iterations := 20

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				key := fmt.Sprintf("test_key_%d_%d", workerID, j)
				val := fmt.Sprintf(`{"worker":%d,"iter":%d}`, workerID, j)

				if err := db.SetSetting(ctx, key, val); err != nil {
					t.Errorf("worker %d set setting failed: %v", workerID, err)
				}

				var retrieved string
				if err := db.GetSetting(ctx, key, &retrieved); err != nil {
					t.Errorf("worker %d get setting failed: %v", workerID, err)
				}
			}
		}(i)
	}

	wg.Wait()
}

func TestServerCRUD(t *testing.T) {
	db, _ := setupTestDB(t)
	ctx := context.Background()

	srv := &models.Server{
		Name:      "VPS Frankfurt",
		Host:      "198.51.100.1",
		SSHUser:   "root",
		SSHPort:   2222,
		SSHPass:   "SuperSecretSSHPassword123!",
		SSHKey:    "-----BEGIN RSA PRIVATE KEY-----\nMIIEogIBAAKCAQEA0...",
		Protocols: map[string]any{"awg": map[string]any{"port": 51820}},
	}

	id, err := db.CreateServer(ctx, srv)
	if err != nil || id <= 0 {
		t.Fatalf("CreateServer failed: %v", err)
	}

	exists, err := db.ServerExists(ctx, id)
	if err != nil || !exists {
		t.Errorf("ServerExists failed: %v", err)
	}

	count, err := db.GetServerCount(ctx)
	if err != nil || count != 1 {
		t.Errorf("expected 1 server, got %d", count)
	}

	retrieved, err := db.GetServer(ctx, id)
	if err != nil || retrieved == nil || retrieved.Name != srv.Name {
		t.Fatalf("GetServer failed: %+v", retrieved)
	}

	newPass := "NewUpdatedPassword456!"
	err = db.UpdateServer(ctx, id, map[string]any{
		"name":     "VPS Amsterdam",
		"password": newPass,
	})
	if err != nil {
		t.Fatalf("UpdateServer failed: %v", err)
	}

	updated, _ := db.GetServer(ctx, id)
	if updated.Name != "VPS Amsterdam" || updated.SSHPass != newPass {
		t.Errorf("UpdateServer failed to update fields: %+v", updated)
	}

	if err := db.UpdateServer(ctx, id, map[string]any{"malicious_column": "drop table"}); err == nil {
		t.Errorf("expected error updating unknown server column")
	}

	deleted, err := db.DeleteServer(ctx, id)
	if err != nil || !deleted {
		t.Fatalf("DeleteServer failed")
	}
}

func TestServerCredentialsAndProtocols(t *testing.T) {
	db, _ := setupTestDB(t)
	ctx := context.Background()

	srv := &models.Server{
		Name:      "VPS Warsaw",
		Host:      "198.51.100.2",
		SSHPass:   "SecretPass!",
		SSHKey:    "SecretKey!",
		Protocols: map[string]any{"awg": map[string]any{"port": 51820}},
	}

	id, err := db.CreateServer(ctx, srv)
	if err != nil {
		t.Fatalf("CreateServer failed: %v", err)
	}

	var rawPass, rawKey string
	err = db.QueryRowContext(ctx, "SELECT ssh_pass, ssh_key FROM servers WHERE id = ?", id).Scan(&rawPass, &rawKey)
	if err != nil {
		t.Fatalf("query raw credentials failed: %v", err)
	}
	if !strings.HasPrefix(rawPass, "gAAAAA") || !strings.HasPrefix(rawKey, "gAAAAA") {
		t.Errorf("expected raw credentials to be Fernet encrypted")
	}

	retrieved, _ := db.GetServer(ctx, id)
	if retrieved.SSHPass != "SecretPass!" || retrieved.SSHKey != "SecretKey!" {
		t.Errorf("credentials not transparently decrypted")
	}

	newProtos := map[string]any{
		"xray": map[string]any{
			"port":                443,
			"reality_private_key": "SENSITIVE_KEY",
			"reality_public_key":  "PUBKEY",
		},
	}
	_ = db.UpdateServerProtocols(ctx, id, newProtos)
	updatedProto, _ := db.GetServer(ctx, id)
	xrayMap, ok := updatedProto.Protocols["xray"].(map[string]any)
	if !ok || xrayMap["reality_private_key"] != nil {
		t.Errorf("sensitive xray key was not stripped from protocols")
	}

	_ = db.UpdateServerReachability(ctx, id, models.ReachabilityOnline)
	status, _ := db.GetServerStatus(ctx, id)
	if status != models.ReachabilityOnline {
		t.Errorf("expected reachability status online, got %s", status)
	}
}

func TestUserCRUD(t *testing.T) {
	db, _ := setupTestDB(t)
	ctx := context.Background()

	u1 := &models.User{
		Username:             "alice",
		PasswordHash:         "$2b$12$hash1",
		Role:                 models.RoleUser,
		Enabled:              true,
		TrafficLimit:         1000000,
		TrafficUsed:          500000,
		TrafficResetStrategy: models.ResetStrategyMonthly,
		AWGMimicry:           models.AWGMimicryQUIC,
	}

	id1, err := db.CreateUser(ctx, u1)
	if err != nil || id1 == "" {
		t.Fatalf("CreateUser failed: %v", err)
	}

	user1, err := db.GetUser(ctx, id1)
	if err != nil || user1 == nil || user1.Username != "alice" {
		t.Fatalf("GetUser mismatch: %+v", user1)
	}

	byUsername, err := db.GetUserByUsername(ctx, "ALICE")
	if err != nil || byUsername == nil || byUsername.ID != id1 {
		t.Errorf("GetUserByUsername case-insensitive failed: %+v", byUsername)
	}

	totalUsers, _ := db.CountUsers(ctx)
	if totalUsers != 1 {
		t.Errorf("expected 1 user, got %d", totalUsers)
	}

	_, _ = db.ToggleUser(ctx, id1, false)
	userDisabled, _ := db.GetUser(ctx, id1)
	if userDisabled.Enabled {
		t.Errorf("expected user disabled")
	}

	deleted, err := db.DeleteUser(ctx, id1)
	if err != nil || !deleted {
		t.Fatalf("DeleteUser failed")
	}

	// Test RoleSupport
	supportUser := &models.User{
		Username: "support_agent",
		Role:     models.RoleSupport,
		Enabled:  true,
	}
	sID, err := db.CreateUser(ctx, supportUser)
	if err != nil {
		t.Fatalf("CreateUser with RoleSupport failed: %v", err)
	}

	retrievedSupport, err := db.GetUser(ctx, sID)
	if err != nil || retrievedSupport.Role != models.RoleSupport {
		t.Errorf("expected RoleSupport, got %s, err: %v", retrievedSupport.Role, err)
	}
	if !retrievedSupport.IsAdmin() {
		t.Errorf("support user should have IsAdmin() == true")
	}
}

func TestUserQuotaAndExpiration(t *testing.T) {
	db, _ := setupTestDB(t)
	ctx := context.Background()

	pastTime := time.Now().Add(-24 * time.Hour)
	u := &models.User{
		Username:             "bob",
		PasswordHash:         "$2b$12$hash2",
		Role:                 models.RoleUser,
		Enabled:              true,
		TrafficLimit:         500000,
		TrafficUsed:          600000,
		ExpiresAt:            &pastTime,
		TrafficResetStrategy: models.ResetStrategyMonthly,
	}
	id, err := db.CreateUser(ctx, u)
	if err != nil {
		t.Fatalf("CreateUser failed: %v", err)
	}

	overQuota, err := db.GetUsersOverQuota(ctx)
	if err != nil || len(overQuota) != 1 || overQuota[0].ID != id {
		t.Errorf("expected bob over quota, got %v", overQuota)
	}

	expired, err := db.GetExpiredUsers(ctx)
	if err != nil || len(expired) != 1 || expired[0].ID != id {
		t.Errorf("expected bob expired, got %v", expired)
	}
}

func TestUserTrafficAndLeaderboard(t *testing.T) {
	db, _ := setupTestDB(t)
	ctx := context.Background()

	u := &models.User{
		Username:             "charlie",
		Enabled:              true,
		TrafficResetStrategy: models.ResetStrategyMonthly,
	}
	id, _ := db.CreateUser(ctx, u)

	if err := db.UpdateUserTraffic(ctx, id, 1000, 2000); err != nil {
		t.Fatalf("UpdateUserTraffic failed: %v", err)
	}

	userUpdated, _ := db.GetUser(ctx, id)
	if userUpdated.TrafficUsed != 3000 || userUpdated.MonthlyRx != 1000 || userUpdated.MonthlyTx != 2000 {
		t.Errorf("traffic counters incorrect: %+v", userUpdated)
	}

	leaderboard, err := db.GetLeaderboard(ctx, "monthly")
	if err != nil || len(leaderboard) != 1 || leaderboard[0].Username != "charlie" {
		t.Errorf("GetLeaderboard mismatch: %v", leaderboard)
	}

	resetCount, err := db.ResetMonthlyTraffic(ctx)
	if err != nil || resetCount != 1 {
		t.Errorf("ResetMonthlyTraffic failed: %d, err=%v", resetCount, err)
	}
}

func TestConnectionsCRUDAndRateLimiting(t *testing.T) {
	db, _ := setupTestDB(t)
	ctx := context.Background()

	sID, _ := db.CreateServer(ctx, &models.Server{Name: "Server 1", Host: "1.1.1.1"})
	uID, _ := db.CreateUser(ctx, &models.User{Username: "dave"})

	conn := &models.UserConnection{
		UserID:     uID,
		ServerID:   sID,
		Protocol:   "awg",
		ClientID:   "client-public-key-12345",
		Name:       "Phone WG",
		AWGMimicry: models.AWGMimicryTLS,
	}

	connID, err := db.CreateConnection(ctx, conn)
	if err != nil || connID == "" {
		t.Fatalf("CreateConnection failed: %v", err)
	}

	retrieved, err := db.GetConnection(ctx, connID)
	if err != nil || retrieved == nil || retrieved.ClientID != "client-public-key-12345" {
		t.Fatalf("GetConnection mismatch: %+v", retrieved)
	}

	byUser, err := db.GetConnectionsByUserID(ctx, uID)
	if err != nil || len(byUser) != 1 {
		t.Errorf("expected 1 connection for user, got %d", len(byUser))
	}

	byServerProto, err := db.GetConnectionsByServerAndProtocol(ctx, sID, "awg2")
	if err != nil || len(byServerProto) != 1 {
		t.Errorf("expected 1 connection by server/proto, got %d", len(byServerProto))
	}

	if err := db.UpdateConnectionTraffic(ctx, connID, 500, 1500); err != nil {
		t.Fatalf("UpdateConnectionTraffic failed: %v", err)
	}

	_, _ = db.UpdateConnection(ctx, connID, map[string]any{"name": "Laptop AWG"})

	if err := db.LogConnectionCreation(ctx, uID); err != nil {
		t.Fatalf("LogConnectionCreation failed: %v", err)
	}

	recentLogs, err := db.GetRecentConnectionsLog(ctx, uID, 60)
	if err != nil || len(recentLogs) != 1 {
		t.Errorf("expected 1 recent log entry, got %d", len(recentLogs))
	}

	deleted, err := db.DeleteConnectionByClientID(ctx, "client-public-key-12345", sID)
	if err != nil || !deleted {
		t.Errorf("DeleteConnectionByClientID failed")
	}
}

func TestSettingsAndSSLCredentialEncryption(t *testing.T) {
	db, _ := setupTestDB(t)
	ctx := context.Background()

	appearance := models.AppearanceSettings{
		Title:    "Custom Panel",
		Logo:     "🚀",
		Subtitle: "Fast & Secure",
		Language: "ru",
	}

	if err := db.SetSetting(ctx, "appearance", appearance); err != nil {
		t.Fatalf("SetSetting appearance failed: %v", err)
	}

	var retrievedApp models.AppearanceSettings
	if err := db.GetSetting(ctx, "appearance", &retrievedApp); err != nil || retrievedApp.Title != "Custom Panel" {
		t.Fatalf("GetSetting appearance failed: %+v", retrievedApp)
	}

	ssl := &models.SSLSettings{
		Enabled:   true,
		Domain:    "vpn.example.com",
		CertText:  "-----BEGIN CERTIFICATE-----\nMIIB...",
		KeyText:   "-----BEGIN PRIVATE KEY-----\nMIIE...",
		PanelPort: 8443,
	}

	if err := db.SaveSSLSettings(ctx, ssl); err != nil {
		t.Fatalf("SaveSSLSettings failed: %v", err)
	}

	var rawSSLJSON string
	err := db.QueryRowContext(ctx, "SELECT value FROM settings WHERE key = 'ssl'").Scan(&rawSSLJSON)
	if err != nil {
		t.Fatalf("failed to query raw ssl settings: %v", err)
	}

	var rawSSLMap map[string]any
	_ = json.Unmarshal([]byte(rawSSLJSON), &rawSSLMap)
	rawKeyText, _ := rawSSLMap["key_text"].(string)

	if !strings.HasPrefix(rawKeyText, "gAAAAA") {
		t.Errorf("expected raw SSL key_text to be Fernet encrypted, got %q", rawKeyText)
	}

	retrievedSSL, err := db.GetSSLSettings(ctx)
	if err != nil || retrievedSSL == nil || retrievedSSL.KeyText != ssl.KeyText {
		t.Errorf("decrypted SSL mismatch: %+v", retrievedSSL)
	}

	// Test saving SSLSettings as struct value (models.SSLSettings)
	sslValue := models.SSLSettings{
		Enabled:   true,
		Domain:    "value.example.com",
		CertText:  "-----BEGIN VALUE CERTIFICATE-----\n...",
		KeyText:   "-----BEGIN VALUE KEY-----\n...",
		PanelPort: 9443,
	}
	if err := db.SetSetting(ctx, "ssl", sslValue); err != nil {
		t.Fatalf("SetSetting with SSLSettings struct value failed: %v", err)
	}
	err = db.QueryRowContext(ctx, "SELECT value FROM settings WHERE key = 'ssl'").Scan(&rawSSLJSON)
	if err != nil {
		t.Fatalf("failed to query raw ssl settings: %v", err)
	}
	var rawSSLValueMap map[string]any
	_ = json.Unmarshal([]byte(rawSSLJSON), &rawSSLValueMap)
	rawKeyTextValue, _ := rawSSLValueMap["key_text"].(string)
	if !strings.HasPrefix(rawKeyTextValue, "gAAAAA") {
		t.Errorf("expected raw SSL key_text to be Fernet encrypted when saved as struct value, got %q", rawKeyTextValue)
	}
	retrievedSSLValue, err := db.GetSSLSettings(ctx)
	if err != nil || retrievedSSLValue.KeyText != sslValue.KeyText {
		t.Errorf("decrypted SSL from struct value mismatch: %+v", retrievedSSLValue)
	}

	// Test saving SSLSettings as map[string]any
	sslMap := map[string]any{
		"enabled":    true,
		"domain":     "map.example.com",
		"cert_text":  "-----BEGIN MAP CERTIFICATE-----\n...",
		"key_text":   "-----BEGIN MAP KEY-----\n...",
		"panel_port": 10443,
	}
	if err := db.SetSetting(ctx, "ssl", sslMap); err != nil {
		t.Fatalf("SetSetting with SSLSettings map failed: %v", err)
	}
	retrievedSSLMap, err := db.GetSSLSettings(ctx)
	if err != nil || retrievedSSLMap.KeyText != "-----BEGIN MAP KEY-----\n..." {
		t.Errorf("decrypted SSL from map mismatch: %+v", retrievedSSLMap)
	}

	// Test SetSettingsBulk with ssl map
	bulkSettings := map[string]any{
		"ssl": map[string]any{
			"enabled":   true,
			"domain":    "bulk.example.com",
			"cert_text": "CERT_BULK",
			"key_text":  "KEY_BULK",
		},
		"captcha": map[string]any{"enabled": true},
	}
	if err := db.SetSettingsBulk(ctx, bulkSettings); err != nil {
		t.Fatalf("SetSettingsBulk failed: %v", err)
	}
	allSettings, err := db.GetAllSettings(ctx)
	if err != nil {
		t.Fatalf("GetAllSettings failed: %v", err)
	}
	sslFromAll, ok := allSettings["ssl"].(map[string]any)
	if !ok || sslFromAll["key_text"] != "KEY_BULK" {
		t.Errorf("GetAllSettings failed to decrypt ssl bulk: %+v", allSettings["ssl"])
	}

	_ = db.SetSchemaVersion(ctx, 2)
	v, _ := db.GetSchemaVersion(ctx)
	if v != 2 {
		t.Errorf("expected schema version 2, got %d", v)
	}

	_ = db.SetMigrationFlag(ctx, "flag_test", "done")
	flagVal, _ := db.GetMigrationFlag(ctx, "flag_test")
	if flagVal != "done" {
		t.Errorf("expected migration flag 'done', got %q", flagVal)
	}

	// Test DeleteSetting
	if err := db.DeleteSetting(ctx, "captcha"); err != nil {
		t.Fatalf("DeleteSetting failed: %v", err)
	}
}

func TestTimeFormattingAndZeroTime(t *testing.T) {
	// Zero time must format as empty string
	zeroT := time.Time{}
	if got := formatTime(zeroT); got != "" {
		t.Errorf("formatTime(zeroT) = %q, want empty string", got)
	}

	// Non-zero time must format as RFC3339
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	if got := formatTime(now); got != "2026-08-26T12:00:00Z" {
		t.Errorf("formatTime(now) = %q, want %q", got, "2026-08-26T12:00:00Z")
	}

	// formatTimePtr on nil and zero
	if ptr := formatTimePtr(nil); ptr != nil {
		t.Errorf("formatTimePtr(nil) = %v, want nil", ptr)
	}
	if ptr := formatTimePtr(&zeroT); ptr != nil {
		t.Errorf("formatTimePtr(&zeroT) = %v, want nil", ptr)
	}
	if ptr := formatTimePtr(&now); ptr == nil || *ptr != "2026-08-26T12:00:00Z" {
		t.Errorf("formatTimePtr(&now) failed: %v", ptr)
	}

	// parseTime
	if !parseTime("").IsZero() {
		t.Errorf("parseTime(\"\") should be zero time")
	}
	if parsed := parseTime("2026-08-26T12:00:00Z"); parsed.Year() != 2026 {
		t.Errorf("parseTime RFC3339 failed: %v", parsed)
	}
	if parsed := parseTime("2026-08-26"); parsed.Year() != 2026 {
		t.Errorf("parseTime YYYY-MM-DD failed: %v", parsed)
	}
}

func TestKnownHostsVerification(t *testing.T) {
	db, _ := setupTestDB(t)
	ctx := context.Background()

	sID, _ := db.CreateServer(ctx, &models.Server{Name: "Known Host Server", Host: "2.2.2.2"})

	fp := "SHA256:4tM8lYvX1O5aK9bCdEfGhIjKlMnOpQrStUvWxYz1234"
	if err := db.SaveKnownHost(ctx, sID, fp); err != nil {
		t.Fatalf("SaveKnownHost failed: %v", err)
	}

	retrievedFP, err := db.GetKnownHostFingerprint(ctx, sID)
	if err != nil || retrievedFP != fp {
		t.Errorf("expected fingerprint %s, got %s", fp, retrievedFP)
	}

	verified, err := db.IsKnownHostVerified(ctx, sID, fp)
	if err != nil || !verified {
		t.Errorf("IsKnownHostVerified returned false for matching fingerprint")
	}

	verifiedWrong, _ := db.IsKnownHostVerified(ctx, sID, "SHA256:wrong")
	if verifiedWrong {
		t.Errorf("IsKnownHostVerified returned true for wrong fingerprint")
	}

	deleted, err := db.DeleteKnownHost(ctx, sID)
	if err != nil || !deleted {
		t.Errorf("DeleteKnownHost failed")
	}
}

func TestLeaderboardSnapshots(t *testing.T) {
	db, _ := setupTestDB(t)
	ctx := context.Background()

	uID, _ := db.CreateUser(ctx, &models.User{
		Username:             "winner",
		Enabled:              true,
		TrafficResetStrategy: models.ResetStrategyMonthly,
	})
	_ = db.UpdateUserTraffic(ctx, uID, 5000, 10000)

	saved, err := db.SaveLeaderboardSnapshot(ctx, 2026, 8)
	if err != nil || saved != 1 {
		t.Fatalf("SaveLeaderboardSnapshot failed: saved=%d, err=%v", saved, err)
	}

	entries, err := db.GetLeaderboardSnapshot(ctx, 2026, 8)
	if err != nil || len(entries) != 1 || entries[0].Username != "winner" {
		t.Fatalf("GetLeaderboardSnapshot failed: %v", entries)
	}

	history, err := db.GetLeaderboardHistory(ctx, 10)
	if err != nil || len(history) != 1 {
		t.Errorf("GetLeaderboardHistory failed: %v", err)
	}

	_, _ = db.DeleteOldSnapshots(ctx, 0)
}

func TestVPNSubsystemTablesAndSessions(t *testing.T) {
	db, _ := setupTestDB(t)
	ctx := context.Background()

	sID, _ := db.CreateServer(ctx, &models.Server{Name: "Tunnel Host", Host: "3.3.3.3"})
	uID, _ := db.CreateUser(ctx, &models.User{Username: "vpn_user"})

	tunnel := &models.BackendTunnel{
		ServerID:      sID,
		InterfaceName: "awg-be-1",
		PublicKey:     "backend-pubkey-abc",
		PrivateKey:    "backend-privkey-secret",
		Endpoint:      "3.3.3.3:51820",
		Status:        "active",
		LatencyMS:     12,
	}

	tID, err := db.CreateBackendTunnel(ctx, tunnel)
	if err != nil {
		t.Fatalf("CreateBackendTunnel failed: %v", err)
	}

	retrievedTunnel, err := db.GetBackendTunnel(ctx, tID)
	if err != nil || retrievedTunnel == nil || retrievedTunnel.PrivateKey != "backend-privkey-secret" {
		t.Fatalf("GetBackendTunnel failed: %+v", retrievedTunnel)
	}

	session := &models.VPNSession{
		UserID:          uID,
		BackendTunnelID: tID,
		PeerPublicKey:   "client-peer-key-xyz",
		AssignedIP:      "10.100.0.2",
		ConnectedAt:     time.Now(),
		LastSeen:        time.Now(),
		Status:          "connected",
	}

	if err := db.CreateVPNSession(ctx, session); err != nil {
		t.Fatalf("CreateVPNSession failed: %v", err)
	}

	byPeer, err := db.GetVPNSessionByPeerKey(ctx, "client-peer-key-xyz")
	if err != nil || byPeer == nil || byPeer.AssignedIP != "10.100.0.2" {
		t.Fatalf("GetVPNSessionByPeerKey failed: %+v", byPeer)
	}

	activeSessions, err := db.GetActiveVPNSessions(ctx)
	if err != nil || len(activeSessions) != 1 {
		t.Errorf("expected 1 active session, got %d", len(activeSessions))
	}

	_ = db.DeleteVPNSession(ctx, byPeer.ID)
	_ = db.DeleteBackendTunnel(ctx, tID)
}

func TestWithTransactionRollbackAndCommit(t *testing.T) {
	db, _ := setupTestDB(t)
	ctx := context.Background()

	errExpected := fmt.Errorf("intentional rollback error")
	err := db.WithTransaction(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "INSERT INTO settings (key, value) VALUES ('tx_test', 'val1')")
		if err != nil {
			return err
		}
		return errExpected
	})

	if !errors.Is(err, errExpected) {
		t.Errorf("expected intentional error, got %v", err)
	}

	var val string
	_ = db.GetSetting(ctx, "tx_test", &val)
	if val == "val1" {
		t.Errorf("transaction changes were not rolled back on error")
	}

	err = db.WithTransaction(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "INSERT INTO settings (key, value) VALUES ('tx_test', '\"committed_val\"')")
		return err
	})
	if err != nil {
		t.Fatalf("WithTransaction commit failed: %v", err)
	}

	var committed string
	_ = db.GetSetting(ctx, "tx_test", &committed)
	if committed != "committed_val" {
		t.Errorf("expected committed value 'committed_val', got %q", committed)
	}
}

func TestLoadDataAndSaveDataRoundTrip(t *testing.T) {
	db, secretKey := setupTestDB(t)
	ctx := context.Background()

	sID, _ := db.CreateServer(ctx, &models.Server{
		Name:      "Backup Server",
		Host:      "4.4.4.4",
		SSHUser:   "admin",
		SSHPass:   "Password123!",
		Protocols: map[string]any{"awg": map[string]any{"port": 51820}},
	})
	uID, _ := db.CreateUser(ctx, &models.User{Username: "backup_user"})
	_, _ = db.CreateConnection(ctx, &models.UserConnection{
		UserID:   uID,
		ServerID: sID,
		Protocol: "awg",
		ClientID: "backup-pubkey",
	})
	_ = db.SaveKnownHost(ctx, sID, "SHA256:fingerprint_backup")
	_ = db.SetSetting(ctx, "custom_setting", "setting_val")

	backupData, err := db.LoadData(ctx)
	if err != nil {
		t.Fatalf("LoadData failed: %v", err)
	}

	tmpDir := t.TempDir()
	freshDBPath := filepath.Join(tmpDir, "fresh_panel.db")
	freshDB, err := Open(freshDBPath, secretKey)
	if err != nil {
		t.Fatalf("failed to open fresh DB: %v", err)
	}
	defer freshDB.Close()

	if err := freshDB.SaveData(ctx, backupData); err != nil {
		t.Fatalf("SaveData into fresh DB failed: %v", err)
	}

	restoredServers, _ := freshDB.GetAllServers(ctx)
	if len(restoredServers) != 1 || restoredServers[0].Name != "Backup Server" || restoredServers[0].SSHPass != "Password123!" {
		t.Errorf("restored server mismatch: %+v", restoredServers)
	}
}

func TestMigrateFromDataJSON(t *testing.T) {
	tmpDir := t.TempDir()
	dataFilePath := filepath.Join(tmpDir, "data.json")
	dbPath := filepath.Join(tmpDir, "panel.db")
	secretKey := "migration-secret-key-1234567890abcdef"

	legacyData := map[string]any{
		"servers": []map[string]any{
			{
				"id":          1,
				"name":        "Legacy Server",
				"host":        "192.0.2.1",
				"username":    "root",
				"ssh_port":    22,
				"password":    "PlaintextPass123",
				"private_key": "",
				"protocols":   map[string]any{"awg": map[string]any{"port": 51820}},
				"created_at":  "2026-01-01T00:00:00Z",
			},
		},
		"users": []map[string]any{
			{
				"id":            "legacy-user-1",
				"username":      "legacy_admin",
				"password_hash": "$2b$12$legacyhash",
				"role":          "admin",
				"enabled":       true,
				"traffic_limit": 0,
				"traffic_used":  1024,
				"created_at":    "2026-01-01T00:00:00Z",
			},
		},
		"user_connections": []map[string]any{
			{
				"id":         "conn-1",
				"user_id":    "legacy-user-1",
				"server_id":  1,
				"protocol":   "awg",
				"client_id":  "legacy-client-key",
				"name":       "Legacy Client",
				"created_at": "2026-01-01T00:00:00Z",
			},
		},
		"settings": map[string]any{
			"appearance": map[string]any{"title": "Migrated Panel", "language": "en"},
		},
	}

	dataBytes, _ := json.Marshal(legacyData)
	if err := os.WriteFile(dataFilePath, dataBytes, 0600); err != nil {
		t.Fatalf("failed to write data.json: %v", err)
	}

	if err := MigrateFromDataJSON(dataFilePath, dbPath, secretKey); err != nil {
		t.Fatalf("MigrateFromDataJSON failed: %v", err)
	}

	if _, err := os.Stat(dataFilePath); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("expected data.json to be moved/renamed")
	}
	if _, err := os.Stat(dataFilePath + ".bak"); err != nil {
		t.Errorf("expected data.json.bak to exist: %v", err)
	}

	migratedDB, err := Open(dbPath, secretKey)
	if err != nil {
		t.Fatalf("failed to open migrated DB: %v", err)
	}
	defer migratedDB.Close()

	ctx := context.Background()
	srv, err := migratedDB.GetServer(ctx, 1)
	if err != nil || srv == nil || srv.Name != "Legacy Server" || srv.SSHPass != "PlaintextPass123" {
		t.Errorf("migrated server mismatch: %+v", srv)
	}
}
