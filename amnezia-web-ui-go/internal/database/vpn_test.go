package database

import (
	"context"
	"testing"
	"time"

	"github.com/devops-igor/amnezia-web-ui-go/internal/models"
	"github.com/devops-igor/amnezia-web-ui-go/internal/security"
)

func TestVPNEmptyAndNotFound(t *testing.T) {
	db, _ := setupTestDB(t)
	ctx := context.Background()

	tunnels, err := db.GetBackendTunnels(ctx)
	if err != nil || len(tunnels) != 0 {
		t.Fatalf("GetBackendTunnels empty DB = (%v, %v), want ([], nil)", tunnels, err)
	}

	allTunnels, err := db.GetAllBackendTunnels(ctx)
	if err != nil || len(allTunnels) != 0 {
		t.Errorf("GetAllBackendTunnels empty DB failed: %v", err)
	}

	nonExistentT, err := db.GetBackendTunnel(ctx, 9999)
	if err != nil || nonExistentT != nil {
		t.Errorf("GetBackendTunnel(9999) = (%v, %v), want (nil, nil)", nonExistentT, err)
	}

	nonExistentTByID, err := db.GetBackendTunnelByID(ctx, 9999)
	if err != nil || nonExistentTByID != nil {
		t.Errorf("GetBackendTunnelByID(9999) = (%v, %v), want (nil, nil)", nonExistentTByID, err)
	}

	nonExistentSession, err := db.GetVPNSessionByPeerKey(ctx, "ghost-peer-key")
	if err != nil || nonExistentSession != nil {
		t.Errorf("GetVPNSessionByPeerKey(ghost) = (%v, %v), want (nil, nil)", nonExistentSession, err)
	}

	activeSessionsEmpty, err := db.GetActiveVPNSessions(ctx)
	if err != nil || len(activeSessionsEmpty) != 0 {
		t.Errorf("GetActiveVPNSessions empty = (%v, %v), want (empty, nil)", activeSessionsEmpty, err)
	}
}

func TestVPNBackendTunnelsCreateAndGet(t *testing.T) {
	db, secretKey := setupTestDB(t)
	ctx := context.Background()

	sID, _ := db.CreateServer(ctx, &models.Server{Name: "VPN Host", Host: "10.10.10.1"})
	healthCheckTime := time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)
	encPrivKey, _ := security.EncryptCredential("FERNET_PRIV_KEY", secretKey)

	t1 := &models.BackendTunnel{
		ServerID:          sID,
		InterfaceName:     "awg-be-1",
		PublicKey:         "pubkey-tunnel-1",
		PrivateKey:        "secret-privkey-1",
		Endpoint:          "10.10.10.1:51820",
		Status:            "active",
		LastHealthCheck:   &healthCheckTime,
		LatencyMS:         15,
		ActiveConnections: 5,
		CreatedAt:         time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
	}
	t1ID, err := db.CreateBackendTunnel(ctx, t1)
	if err != nil || t1ID <= 0 {
		t.Fatalf("CreateBackendTunnel t1 failed: %v", err)
	}

	t2 := &models.BackendTunnel{
		ServerID:      sID,
		InterfaceName: "awg-be-2",
		PublicKey:     "pubkey-tunnel-2",
		PrivateKey:    encPrivKey,
		Endpoint:      "10.10.10.1:51821",
	}
	_, _ = db.CreateBackendTunnel(ctx, t2)

	retrieved, _ := db.GetBackendTunnelByID(ctx, t1ID)
	if retrieved.PrivateKey != "secret-privkey-1" || retrieved.Status != "active" {
		t.Errorf("Backend tunnel 1 mismatch: %+v", retrieved)
	}

	all, _ := db.GetAllBackendTunnels(ctx)
	if len(all) != 2 {
		t.Errorf("expected 2 backend tunnels, got %d", len(all))
	}
}

func TestVPNBackendTunnelsUpdateAndStatus(t *testing.T) {
	db, _ := setupTestDB(t)
	ctx := context.Background()

	sID, _ := db.CreateServer(ctx, &models.Server{Name: "VPN Host", Host: "10.10.10.1"})
	tID, _ := db.CreateBackendTunnel(ctx, &models.BackendTunnel{
		ServerID:      sID,
		InterfaceName: "awg-be",
		PublicKey:     "pubkey",
		PrivateKey:    "privkey",
		Endpoint:      "10.10.10.1:51820",
	})

	if err := db.UpdateBackendTunnel(ctx, tID, map[string]any{"invalid_col": 123}); err == nil {
		t.Errorf("expected error updating invalid column")
	}
	if err := db.UpdateBackendTunnel(ctx, tID, map[string]any{}); err != nil {
		t.Errorf("UpdateBackendTunnel empty map failed: %v", err)
	}

	newHealth := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	err := db.UpdateBackendTunnel(ctx, tID, map[string]any{
		"interface_name":    "awg-be-renamed",
		"private_key":       "new-plaintext-privkey",
		"latency_ms":        42,
		"last_health_check": &newHealth,
	})
	if err != nil {
		t.Fatalf("UpdateBackendTunnel failed: %v", err)
	}

	_ = db.UpdateBackendTunnel(ctx, tID, map[string]any{"last_health_check": newHealth})

	if err := db.UpdateBackendTunnelStatus(ctx, tID, "degraded", 88); err != nil {
		t.Fatalf("UpdateBackendTunnelStatus failed: %v", err)
	}
	tStatus, _ := db.GetBackendTunnel(ctx, tID)
	if tStatus.Status != "degraded" || tStatus.LatencyMS != 88 {
		t.Errorf("UpdateBackendTunnelStatus mismatch: status=%s, latency=%d", tStatus.Status, tStatus.LatencyMS)
	}
}

func TestVPNSessionsCRUDAndTraffic(t *testing.T) {
	db, _ := setupTestDB(t)
	ctx := context.Background()

	sID, _ := db.CreateServer(ctx, &models.Server{Name: "VPN Host", Host: "10.10.10.1"})
	uID, _ := db.CreateUser(ctx, &models.User{Username: "vpn_user"})
	tID, _ := db.CreateBackendTunnel(ctx, &models.BackendTunnel{
		ServerID:      sID,
		InterfaceName: "awg-be",
		PublicKey:     "pubkey",
		PrivateKey:    "privkey",
		Endpoint:      "10.10.10.1:51820",
	})

	sess1 := &models.VPNSession{
		ID:              "custom-session-1",
		UserID:          uID,
		BackendTunnelID: tID,
		PeerPublicKey:   "peer-key-alice",
		AssignedIP:      "10.100.0.10",
		ConnectedAt:     time.Date(2026, 8, 26, 1, 0, 0, 0, time.UTC),
		LastSeen:        time.Date(2026, 8, 26, 1, 5, 0, 0, time.UTC),
		RxBytes:         1024,
		TxBytes:         2048,
		Status:          "connected",
	}
	if err := db.CreateVPNSession(ctx, sess1); err != nil {
		t.Fatalf("CreateVPNSession sess1 failed: %v", err)
	}

	retrievedSess, _ := db.GetVPNSessionByPeerKey(ctx, "peer-key-alice")
	if retrievedSess == nil || retrievedSess.ID != "custom-session-1" {
		t.Errorf("GetVPNSessionByPeerKey mismatch: %+v", retrievedSess)
	}

	sess2 := &models.VPNSession{
		UserID:          uID,
		BackendTunnelID: tID,
		PeerPublicKey:   "peer-key-bob",
		AssignedIP:      "10.100.0.11",
	}
	_ = db.CreateVPNSession(ctx, sess2)

	sess1Update := &models.VPNSession{
		UserID:          uID,
		BackendTunnelID: tID,
		PeerPublicKey:   "peer-key-alice",
		AssignedIP:      "10.100.0.20",
		Status:          "disconnected",
	}
	_ = db.CreateVPNSession(ctx, sess1Update)

	bobSess, _ := db.GetVPNSessionByPeerKey(ctx, "peer-key-bob")
	if err := db.UpdateVPNSessionTraffic(ctx, bobSess.ID, 5000, 10000); err != nil {
		t.Fatalf("UpdateVPNSessionTraffic failed: %v", err)
	}

	activeSess, err := db.GetActiveVPNSessions(ctx)
	if err != nil || len(activeSess) != 1 || activeSess[0].PeerPublicKey != "peer-key-bob" {
		t.Fatalf("GetActiveVPNSessions failed: len=%d, err=%v", len(activeSess), err)
	}
}

func TestVPNDeletionAndNullScanning(t *testing.T) {
	db, _ := setupTestDB(t)
	ctx := context.Background()

	sID, _ := db.CreateServer(ctx, &models.Server{Name: "VPN Host", Host: "10.10.10.1"})
	uID, _ := db.CreateUser(ctx, &models.User{Username: "vpn_user"})
	tID, _ := db.CreateBackendTunnel(ctx, &models.BackendTunnel{
		ServerID:      sID,
		InterfaceName: "awg-be",
		PublicKey:     "pubkey",
		PrivateKey:    "privkey",
		Endpoint:      "10.10.10.1:51820",
	})
	_ = db.CreateVPNSession(ctx, &models.VPNSession{
		UserID:          uID,
		BackendTunnelID: tID,
		PeerPublicKey:   "peer-key-del",
		AssignedIP:      "10.100.0.99",
	})

	delSess, _ := db.GetVPNSessionByPeerKey(ctx, "peer-key-del")
	if err := db.DeleteVPNSession(ctx, delSess.ID); err != nil {
		t.Fatalf("DeleteVPNSession failed: %v", err)
	}

	if err := db.DeleteBackendTunnel(ctx, tID); err != nil {
		t.Fatalf("DeleteBackendTunnel failed: %v", err)
	}

	_, _ = db.sqlDB.ExecContext(ctx, "INSERT INTO backend_tunnels (id, server_id, interface_name, public_key, private_key, endpoint, created_at) VALUES (777, ?, 'awg-null-ts', 'pubkey-null-ts', 'priv', '10.0.0.1:51820', '2026-08-01T00:00:00Z')", sID)
	tNullTS, err := db.GetBackendTunnel(ctx, 777)
	if err != nil || tNullTS == nil || tNullTS.CreatedAt.IsZero() || tNullTS.LastHealthCheck != nil {
		t.Errorf("expected nil last_health_check in tNullTS, got: %+v", tNullTS)
	}

	_, _ = db.sqlDB.ExecContext(ctx, "INSERT INTO vpn_sessions (id, user_id, backend_tunnel_id, peer_public_key, assigned_ip, connected_at, last_seen) VALUES ('sess-null-ts', ?, 777, 'peer-null-ts', '10.100.0.99', '2026-08-01T00:00:00Z', '2026-08-01T00:00:00Z')", uID)
	sessNullTS, err := db.GetVPNSessionByPeerKey(ctx, "peer-null-ts")
	if err != nil || sessNullTS == nil || sessNullTS.ConnectedAt.IsZero() || sessNullTS.LastSeen.IsZero() {
		t.Errorf("expected valid timestamps in sessNullTS, got: %+v", sessNullTS)
	}
}

func TestVPNConfig(t *testing.T) {
	db, _ := setupTestDB(t)
	ctx := context.Background()

	// Default config
	cfg, err := db.GetVPNConfig(ctx)
	if err != nil {
		t.Fatalf("GetVPNConfig failed: %v", err)
	}
	if cfg.Algorithm != models.LBLeastConnections || cfg.ListenPort != 51820 || cfg.SubnetCIDR != "10.100.0.0/16" {
		t.Errorf("unexpected default cfg: %+v", cfg)
	}

	// Update config
	cfg.Algorithm = models.LBWeighted
	cfg.ListenPort = 51822
	cfg.Weights = map[int64]int{1: 50, 2: 50}
	if err := db.SaveVPNConfig(ctx, cfg); err != nil {
		t.Fatalf("SaveVPNConfig failed: %v", err)
	}
	if err := db.SaveVPNConfig(ctx, nil); err == nil {
		t.Errorf("expected error saving nil cfg")
	}

	loadedCfg, err := db.GetVPNConfig(ctx)
	if err != nil || loadedCfg.Algorithm != models.LBWeighted || loadedCfg.ListenPort != 51822 || loadedCfg.Weights[1] != 50 {
		t.Errorf("loaded cfg mismatch: %+v, err: %v", loadedCfg, err)
	}
}

func TestVPNQueries(t *testing.T) {
	db, _ := setupTestDB(t)
	ctx := context.Background()

	// Backend tunnel by server ID
	sID, _ := db.CreateServer(ctx, &models.Server{Name: "Server 1", Host: "1.2.3.4"})
	tID, err := db.CreateBackendTunnel(ctx, &models.BackendTunnel{
		ServerID:      sID,
		InterfaceName: "awg-be-1",
		PublicKey:     "pubkey1",
		PrivateKey:    "privkey1",
		Endpoint:      "1.2.3.4:51820",
	})
	if err != nil {
		t.Fatalf("CreateBackendTunnel failed: %v", err)
	}

	byServerID, err := db.GetBackendTunnelByServerID(ctx, sID)
	if err != nil || byServerID == nil || byServerID.ID != tID {
		t.Errorf("GetBackendTunnelByServerID mismatch: %+v, err: %v", byServerID, err)
	}

	nonExistent, err := db.GetBackendTunnelByServerID(ctx, 99999)
	if err != nil || nonExistent != nil {
		t.Errorf("expected nil for non-existent server ID, got: %+v, err: %v", nonExistent, err)
	}

	// VPNSessionByID and VPNSessionsByUserID
	uID, _ := db.CreateUser(ctx, &models.User{Username: "sess_user"})
	sessID := "session-uuid-123"
	if err := db.CreateVPNSession(ctx, &models.VPNSession{
		ID:              sessID,
		UserID:          uID,
		BackendTunnelID: tID,
		PeerPublicKey:   "peer123",
		AssignedIP:      "10.100.0.15",
	}); err != nil {
		t.Fatalf("CreateVPNSession failed: %v", err)
	}

	byID, err := db.GetVPNSessionByID(ctx, sessID)
	if err != nil || byID == nil || byID.ID != sessID {
		t.Errorf("GetVPNSessionByID mismatch: %+v, err: %v", byID, err)
	}

	nonExistentSess, err := db.GetVPNSessionByID(ctx, "non-existent")
	if err != nil || nonExistentSess != nil {
		t.Errorf("expected nil for non-existent session ID, got: %+v, err: %v", nonExistentSess, err)
	}

	userSessions, err := db.GetVPNSessionsByUserID(ctx, uID)
	if err != nil || len(userSessions) != 1 || userSessions[0].ID != sessID {
		t.Errorf("GetVPNSessionsByUserID mismatch: len=%d, err=%v", len(userSessions), err)
	}

	emptyUserSessions, err := db.GetVPNSessionsByUserID(ctx, "non-existent-user")
	if err != nil || len(emptyUserSessions) != 0 {
		t.Errorf("expected empty user sessions, got: %d, err: %v", len(emptyUserSessions), err)
	}
}
