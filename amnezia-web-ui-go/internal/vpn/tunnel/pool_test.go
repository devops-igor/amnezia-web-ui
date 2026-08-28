package tunnel

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/devops-igor/amnezia-web-ui-go/internal/database"
	"github.com/devops-igor/amnezia-web-ui-go/internal/models"
)

func setupTestDB(t *testing.T) *database.DB {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test_vpn_tunnel.db")
	db, err := database.Open(dbPath, "test-secret-key-1234567890123456")
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})
	return db
}

func TestGenerateCurve25519KeyPair(t *testing.T) {
	pub, priv, err := GenerateCurve25519KeyPair()
	if err != nil {
		t.Fatalf("GenerateCurve25519KeyPair failed: %v", err)
	}
	if len(pub) == 0 || len(priv) == 0 {
		t.Errorf("empty keys generated: pub=%s, priv=%s", pub, priv)
	}
}

func TestTunnelPoolCRUD(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	pool := NewPool(db)

	s1ID, _ := db.CreateServer(ctx, &models.Server{Name: "Server 1", Host: "1.1.1.1"})
	s2ID, _ := db.CreateServer(ctx, &models.Server{Name: "Server 2", Host: "2.2.2.2"})

	// Validation
	if _, err := pool.AddTunnel(ctx, 0, "1.1.1.1:51820", ""); err == nil {
		t.Errorf("expected error for serverID <= 0")
	}
	if _, err := pool.AddTunnel(ctx, s1ID, "", ""); err == nil {
		t.Errorf("expected error for empty endpoint")
	}

	// 1. Add Tunnels
	t1, err := pool.AddTunnel(ctx, s1ID, "1.1.1.1:51820", "pubkey1")
	if err != nil {
		t.Fatalf("AddTunnel 1 failed: %v", err)
	}
	if t1.ServerID != s1ID || t1.InterfaceName != "awg-be-1" || t1.Status != "active" {
		t.Errorf("invalid t1: %+v", t1)
	}

	t2, err := pool.AddTunnel(ctx, s2ID, "2.2.2.2:51820", "")
	if err != nil {
		t.Fatalf("AddTunnel 2 failed: %v", err)
	}
	if t2.PublicKey == "" || t2.PrivateKey == "" {
		t.Errorf("expected auto-generated keys for t2: %+v", t2)
	}

	// Repeat Add updates endpoint
	t1Updated, err := pool.AddTunnel(ctx, s1ID, "1.1.1.1:51822", "pubkey1-new")
	if err != nil || t1Updated.Endpoint != "1.1.1.1:51822" || t1Updated.PublicKey != "pubkey1-new" {
		t.Errorf("repeat AddTunnel mismatch: %+v, err: %v", t1Updated, err)
	}

	// Lookups
	byServer, err := pool.GetTunnel(s1ID)
	if err != nil || byServer.ID != t1.ID {
		t.Errorf("GetTunnel mismatch: %+v, err: %v", byServer, err)
	}
	if _, err := pool.GetTunnel(9999); err != ErrTunnelNotFound {
		t.Errorf("expected ErrTunnelNotFound for non-existent server ID, got %v", err)
	}

	byID, err := pool.GetTunnelByID(t1.ID)
	if err != nil || byID.ServerID != s1ID {
		t.Errorf("GetTunnelByID mismatch: %+v, err: %v", byID, err)
	}
	if _, err := pool.GetTunnelByID(9999); err != ErrTunnelNotFound {
		t.Errorf("expected ErrTunnelNotFound for non-existent tunnel ID, got %v", err)
	}

	byIf, err := pool.GetTunnelByInterface("awg-be-1")
	if err != nil || byIf.ID != t1.ID {
		t.Errorf("GetTunnelByInterface mismatch: %+v, err: %v", byIf, err)
	}
	if _, err := pool.GetTunnelByInterface("non-existent"); err != ErrTunnelNotFound {
		t.Errorf("expected ErrTunnelNotFound for non-existent interface, got %v", err)
	}

	// Remove tunnel
	if err := pool.RemoveTunnel(ctx, s1ID); err != nil {
		t.Fatalf("RemoveTunnel failed: %v", err)
	}
	if _, err := pool.GetTunnel(s1ID); err != ErrTunnelNotFound {
		t.Errorf("expected tunnel to be removed")
	}
	if err := pool.RemoveTunnel(ctx, 9999); err != ErrTunnelNotFound {
		t.Errorf("expected ErrTunnelNotFound on RemoveTunnel non-existent, got %v", err)
	}
}

func TestTunnelPoolStatusAndConnections(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	pool := NewPool(db)

	s1ID, _ := db.CreateServer(ctx, &models.Server{Name: "Server 1", Host: "1.1.1.1"})
	s2ID, _ := db.CreateServer(ctx, &models.Server{Name: "Server 2", Host: "2.2.2.2"})

	_, _ = pool.AddTunnel(ctx, s1ID, "1.1.1.1:51820", "pub1")
	t2, _ := pool.AddTunnel(ctx, s2ID, "2.2.2.2:51820", "pub2")

	// Lists
	if len(pool.ListTunnels()) != 2 || len(pool.GetActiveTunnels()) != 2 {
		t.Errorf("expected 2 tunnels in pool")
	}

	// Status and Latency update
	if err := pool.SetTunnelStatus(ctx, s1ID, "degraded", 600); err != nil {
		t.Fatalf("SetTunnelStatus failed: %v", err)
	}
	if err := pool.SetTunnelStatus(ctx, 9999, "active", 10); err != ErrTunnelNotFound {
		t.Errorf("expected ErrTunnelNotFound on SetTunnelStatus non-existent, got %v", err)
	}

	byServer, _ := pool.GetTunnel(s1ID)
	if byServer.Status != "degraded" || byServer.LatencyMS != 600 {
		t.Errorf("status mismatch: status=%s, lat=%d", byServer.Status, byServer.LatencyMS)
	}

	activeAfterDegrade := pool.GetActiveTunnels()
	if len(activeAfterDegrade) != 1 || activeAfterDegrade[0].ServerID != s2ID {
		t.Errorf("expected 1 active tunnel after degrade, got %d", len(activeAfterDegrade))
	}

	// Connection counts
	pool.IncrementConnections(t2.ID)
	pool.IncrementConnections(t2.ID)
	byServer2, _ := pool.GetTunnel(s2ID)
	if byServer2.ActiveConnections != 2 {
		t.Errorf("expected 2 active connections, got %d", byServer2.ActiveConnections)
	}

	pool.DecrementConnections(t2.ID)
	byServer2, _ = pool.GetTunnel(s2ID)
	if byServer2.ActiveConnections != 1 {
		t.Errorf("expected 1 active connection, got %d", byServer2.ActiveConnections)
	}

	pool.DecrementConnections(t2.ID)
	pool.DecrementConnections(t2.ID) // Decrement past zero
	byServer2, _ = pool.GetTunnel(s2ID)
	if byServer2.ActiveConnections != 0 {
		t.Errorf("expected 0 active connections, got %d", byServer2.ActiveConnections)
	}

	// SyncFromDB
	freshPool := NewPool(db)
	if err := freshPool.SyncFromDB(ctx); err != nil {
		t.Fatalf("SyncFromDB failed: %v", err)
	}
	if len(freshPool.ListTunnels()) != 2 {
		t.Errorf("SyncFromDB mismatch")
	}

	// Close
	if err := pool.Close(); err != nil {
		t.Errorf("Close failed: %v", err)
	}

	// AddTunnel on closed pool returns ErrPoolClosed
	if _, err := pool.AddTunnel(ctx, s1ID, "1.1.1.1:51820", ""); err != ErrPoolClosed {
		t.Errorf("expected ErrPoolClosed on closed pool, got %v", err)
	}
}
