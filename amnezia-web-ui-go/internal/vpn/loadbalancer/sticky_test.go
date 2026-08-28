package loadbalancer

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
	dbPath := filepath.Join(dir, "test_vpn_lb.db")
	db, err := database.Open(dbPath, "test-secret-key-1234567890123456")
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})
	return db
}

func TestStickySessionManager(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	caps := CapacityConfig{MaxTotalPeers: 100, MaxPeersPerBackend: 50}
	base := NewLeastConnectionsBalancer(caps)
	sticky := NewStickySessionManager(db, base, caps)

	tunnels := []*models.BackendTunnel{
		{ID: 1, Status: "active", ActiveConnections: 10},
		{ID: 2, Status: "active", ActiveConnections: 5},
	}

	// 1. Nil request check
	if _, _, err := sticky.GetOrAssignBackend(ctx, nil); err == nil {
		t.Errorf("expected error for nil request")
	}

	// 2. First request (new assignment)
	reqUserA := &RoutingRequest{
		UserID:           "user-a",
		PeerPublicKey:    "peer-a",
		AvailableTunnels: tunnels,
	}
	backend1, isNew, err := sticky.GetOrAssignBackend(ctx, reqUserA)
	if err != nil || !isNew || backend1.ID != 2 {
		t.Fatalf("expected new assignment to tunnel 2, got %+v (isNew: %v, err: %v)", backend1, isNew, err)
	}

	// 3. Second request for same user (sticky hit)
	// Even if tunnel 1 now has 0 connections, user-a should stick to tunnel 2!
	tunnels[0].ActiveConnections = 0
	backend2, isNew, err := sticky.GetOrAssignBackend(ctx, reqUserA)
	if err != nil || isNew || backend2.ID != 2 {
		t.Fatalf("expected sticky hit on tunnel 2, got %+v (isNew: %v, err: %v)", backend2, isNew, err)
	}

	// 4. Query affinity
	tid, ok := sticky.GetAffinity("user-a")
	if !ok || tid != 2 {
		t.Errorf("GetAffinity mismatch: tid=%d, ok=%v", tid, ok)
	}
	if _, ok := sticky.GetAffinity("ghost"); ok {
		t.Errorf("expected ghost to have no affinity")
	}

	// 5. Failover when assigned backend degrades
	tunnels[1].Status = "degraded" // Tunnel 2 degraded
	backendFailover, isNew, err := sticky.GetOrAssignBackend(ctx, reqUserA)
	if err != nil || !isNew || backendFailover.ID != 1 {
		t.Fatalf("expected failover to tunnel 1, got %+v (isNew: %v, err: %v)", backendFailover, isNew, err)
	}

	// 6. Manual affinities and clearing
	sticky.AssignAffinity("user-b", 1)
	sticky.AssignPeerAffinity("peer-b", 1)
	if tid, _ := sticky.GetAffinity("user-b"); tid != 1 {
		t.Errorf("expected user-b affinity to be 1")
	}
	sticky.ClearAffinity("user-b")
	if _, ok := sticky.GetAffinity("user-b"); ok {
		t.Errorf("expected user-b affinity cleared")
	}
	sticky.ClearPeerAffinity("peer-b")

	// 7. HandleFailover with DB Sessions
	sID, _ := db.CreateServer(ctx, &models.Server{Name: "Host", Host: "1.1.1.1"})
	t1ID, _ := db.CreateBackendTunnel(ctx, &models.BackendTunnel{
		ServerID:      sID,
		InterfaceName: "awg-be-1",
		PublicKey:     "pub1",
		PrivateKey:    "priv1",
		Endpoint:      "1.1.1.1:51820",
		Status:        "active",
	})
	t2ID, _ := db.CreateBackendTunnel(ctx, &models.BackendTunnel{
		ServerID:      sID,
		InterfaceName: "awg-be-2",
		PublicKey:     "pub2",
		PrivateKey:    "priv2",
		Endpoint:      "1.1.1.1:51821",
		Status:        "active",
	})
	uID, _ := db.CreateUser(ctx, &models.User{Username: "failover_user"})

	_ = db.CreateVPNSession(ctx, &models.VPNSession{
		ID:              "sess-failover-1",
		UserID:          uID,
		BackendTunnelID: t1ID,
		PeerPublicKey:   "peer-failover-1",
		AssignedIP:      "10.100.0.99",
		Status:          "connected",
	})

	sticky.AssignAffinity(uID, t1ID)
	sticky.AssignPeerAffinity("peer-failover-1", t1ID)

	healthyPool := []*models.BackendTunnel{
		{ID: t2ID, Status: "active", ActiveConnections: 0},
	}

	migrated, err := sticky.HandleFailover(ctx, t1ID, healthyPool)
	if err != nil || migrated < 1 {
		t.Fatalf("HandleFailover failed: migrated=%d, err=%v", migrated, err)
	}

	// Verify DB session was updated to t2ID
	updatedSess, _ := db.GetVPNSessionByID(ctx, "sess-failover-1")
	if updatedSess.BackendTunnelID != t2ID {
		t.Errorf("expected session backend tunnel ID to be updated to %d, got %d", t2ID, updatedSess.BackendTunnelID)
	}

	// HandleFailover when no healthy backends exist
	if _, err := sticky.HandleFailover(ctx, t2ID, nil); err != ErrNoActiveBackends {
		t.Errorf("expected ErrNoActiveBackends when no healthy tunnels for failover, got %v", err)
	}
}
