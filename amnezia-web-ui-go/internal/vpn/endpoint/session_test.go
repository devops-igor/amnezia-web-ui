package endpoint

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/devops-igor/amnezia-web-ui-go/internal/models"
)

func TestSessionManagerCRUD(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	ipam, err := NewIPAM("10.100.0.0/24")
	if err != nil {
		t.Fatalf("NewIPAM failed: %v", err)
	}

	sm := NewSessionManager(db, ipam)

	sID, _ := db.CreateServer(ctx, &models.Server{Name: "VPN Host", Host: "10.0.0.1"})
	tID, _ := db.CreateBackendTunnel(ctx, &models.BackendTunnel{
		ServerID:      sID,
		InterfaceName: "awg-be-1",
		PublicKey:     "tunnel-pubkey",
		PrivateKey:    "tunnel-privkey",
		Endpoint:      "10.0.0.1:51820",
	})
	u1ID, _ := db.CreateUser(ctx, &models.User{Username: "user1"})

	// Validation
	if _, err := sm.CreateSession(ctx, "", "peer1", "10.100.0.2", tID); err == nil {
		t.Errorf("expected error for missing userID")
	}

	// 1. Create Session
	sess1, err := sm.CreateSession(ctx, u1ID, "peer1", "10.100.0.2", tID)
	if err != nil {
		t.Fatalf("CreateSession sess1 failed: %v", err)
	}
	if sess1.ID == "" || sess1.Status != "connected" || sess1.UserID != u1ID {
		t.Errorf("invalid sess1: %+v", sess1)
	}
	if sm.ActiveCount() != 1 {
		t.Errorf("expected active count 1, got %d", sm.ActiveCount())
	}

	// 2. Lookups
	byPeer, ok := sm.GetSession("peer1")
	if !ok || byPeer.ID != sess1.ID {
		t.Errorf("GetSession mismatch: %+v", byPeer)
	}
	if _, ok := sm.GetSession("ghost"); ok {
		t.Errorf("expected ghost session to not be found")
	}

	byID, ok := sm.GetSessionByID(sess1.ID)
	if !ok || byID.PeerPublicKey != "peer1" {
		t.Errorf("GetSessionByID mismatch: %+v", byID)
	}
	if _, ok := sm.GetSessionByID("ghost-id"); ok {
		t.Errorf("expected ghost id to not be found")
	}

	user1Sessions := sm.GetSessionsByUserID(u1ID)
	if len(user1Sessions) != 1 || user1Sessions[0].ID != sess1.ID {
		t.Errorf("GetSessionsByUserID mismatch: len=%d", len(user1Sessions))
	}
	if len(sm.GetSessionsByUserID("ghost-user")) != 0 {
		t.Errorf("expected 0 sessions for ghost-user")
	}

	// 3. Activity and Touch
	sm.UpdateActivity("peer1", 500, 1000)
	byPeer, _ = sm.GetSession("peer1")
	if byPeer.RxBytes != 500 || byPeer.TxBytes != 1000 {
		t.Errorf("UpdateActivity mismatch: rx=%d, tx=%d", byPeer.RxBytes, byPeer.TxBytes)
	}

	sm.TouchSession("peer1")
	sm.TouchSession("ghost")
	sm.UpdateActivity("ghost", 10, 20)

	// Close Session
	if err := sm.CloseSession(ctx, sess1.ID, "disconnected"); err != nil {
		t.Fatalf("CloseSession failed: %v", err)
	}
	if sm.ActiveCount() != 0 {
		t.Errorf("expected active count 0, got %d", sm.ActiveCount())
	}
	if err := sm.CloseSession(ctx, "non-existent", ""); err != ErrSessionNotFound {
		t.Errorf("expected ErrSessionNotFound, got %v", err)
	}
}

func TestSessionManagerTimeoutsAndDrain(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	ipam, _ := NewIPAM("10.100.0.0/24")
	sm := NewSessionManager(db, ipam)

	sID, _ := db.CreateServer(ctx, &models.Server{Name: "VPN Host", Host: "10.0.0.1"})
	tID, _ := db.CreateBackendTunnel(ctx, &models.BackendTunnel{
		ServerID:      sID,
		InterfaceName: "awg-be-1",
		PublicKey:     "tunnel-pubkey",
		PrivateKey:    "tunnel-privkey",
		Endpoint:      "10.0.0.1:51820",
	})
	u1ID, _ := db.CreateUser(ctx, &models.User{Username: "user1"})
	u2ID, _ := db.CreateUser(ctx, &models.User{Username: "user2"})

	sess1, _ := sm.CreateSession(ctx, u1ID, "peer1", "10.100.0.2", tID)
	_, _ = sm.CreateSession(ctx, u2ID, "peer2", "10.100.0.3", tID)

	// Recreate with peer1 replaces old session
	sess1New, err := sm.CreateSession(ctx, u1ID, "peer1", "10.100.0.4", tID)
	if err != nil {
		t.Fatalf("Recreate peer1 session failed: %v", err)
	}
	if sm.ActiveCount() != 2 {
		t.Errorf("expected active count 2 after peer replace, got %d", sm.ActiveCount())
	}
	if _, ok := sm.GetSessionByID(sess1.ID); ok {
		t.Errorf("expected old session ID to be removed")
	}

	// Drain
	if err := sm.Drain(ctx, 5*time.Second); err != nil {
		t.Fatalf("Drain failed: %v", err)
	}
	activeList := sm.ListActiveSessions()
	if len(activeList) != 2 {
		t.Errorf("expected 2 active list sessions, got %d", len(activeList))
	}
	for _, s := range activeList {
		if s.Status != "draining" {
			t.Errorf("expected status draining, got %s", s.Status)
		}
	}

	_ = sm.CloseSession(ctx, sess1New.ID, "disconnected")

	// Timeouts: artificially age peer2 last seen
	peer2Sess, _ := sm.GetSession("peer2")
	peer2Sess.LastSeen = time.Now().UTC().Add(-10 * time.Minute)

	timedOut, err := sm.CheckTimeouts(ctx, 5*time.Minute)
	if err != nil {
		t.Fatalf("CheckTimeouts failed: %v", err)
	}
	if len(timedOut) != 1 || timedOut[0].PeerPublicKey != "peer2" {
		t.Errorf("CheckTimeouts mismatch: len=%d", len(timedOut))
	}
	if sm.ActiveCount() != 0 {
		t.Errorf("expected active count 0 after timeout, got %d", sm.ActiveCount())
	}

	// zero timeout (noop)
	if to, err := sm.CheckTimeouts(ctx, 0); err != nil || len(to) != 0 {
		t.Errorf("expected noop on zero timeout")
	}
}

func TestSessionManagerSyncFromDB(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	ipam, _ := NewIPAM("10.100.0.0/24")

	sID, _ := db.CreateServer(ctx, &models.Server{Name: "VPN Host", Host: "10.0.0.1"})
	tID, _ := db.CreateBackendTunnel(ctx, &models.BackendTunnel{
		ServerID:      sID,
		InterfaceName: "awg-be-1",
		PublicKey:     "tunnel-pubkey",
		PrivateKey:    "tunnel-privkey",
		Endpoint:      "10.0.0.1:51820",
	})
	uID, _ := db.CreateUser(ctx, &models.User{Username: "sync_user"})

	// Pre-insert into DB
	_ = db.CreateVPNSession(ctx, &models.VPNSession{
		ID:              "sync-sess-1",
		UserID:          uID,
		BackendTunnelID: tID,
		PeerPublicKey:   "sync-peer-1",
		AssignedIP:      "10.100.0.10",
		Status:          "connected",
	})

	sm := NewSessionManager(db, ipam)
	if err := sm.SyncFromDB(ctx); err != nil {
		t.Fatalf("SyncFromDB failed: %v", err)
	}

	if sm.ActiveCount() != 1 {
		t.Fatalf("expected 1 active session after sync, got %d", sm.ActiveCount())
	}
	sess, ok := sm.GetSessionByID("sync-sess-1")
	if !ok || sess.PeerPublicKey != "sync-peer-1" {
		t.Errorf("synced session mismatch: %+v", sess)
	}

	if !ipam.IsAllocated(net.ParseIP("10.100.0.10")) {
		t.Errorf("expected 10.100.0.10 to be marked allocated in IPAM after sync")
	}

	// nil DB sync
	smNil := NewSessionManager(nil, ipam)
	if err := smNil.SyncFromDB(ctx); err != nil {
		t.Errorf("SyncFromDB nil db failed: %v", err)
	}
}

func TestSessionManagerPeerReconnectDBSync(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	ipam, _ := NewIPAM("10.100.0.0/24")
	sm := NewSessionManager(db, ipam)

	sID, _ := db.CreateServer(ctx, &models.Server{Name: "VPN Host", Host: "10.0.0.1"})
	tID, _ := db.CreateBackendTunnel(ctx, &models.BackendTunnel{
		ServerID:      sID,
		InterfaceName: "awg-be-1",
		PublicKey:     "tunnel-pubkey",
		PrivateKey:    "tunnel-privkey",
		Endpoint:      "10.0.0.1:51820",
	})
	uID, _ := db.CreateUser(ctx, &models.User{Username: "reconnect_user"})

	peerKey := "reconnect-peer-pubkey-1"

	// 1. Initial connection
	sess1, err := sm.CreateSession(ctx, uID, peerKey, "10.100.0.15", tID)
	if err != nil {
		t.Fatalf("CreateSession 1 failed: %v", err)
	}

	dbSess1, err := db.GetVPNSessionByPeerKey(ctx, peerKey)
	if err != nil || dbSess1 == nil || dbSess1.ID != sess1.ID {
		t.Fatalf("expected DB row id to match sess1.ID (%s), got: %+v", sess1.ID, dbSess1)
	}

	// 2. Peer Reconnect (creates new session with new UUID for same peer key)
	sess2, err := sm.CreateSession(ctx, uID, peerKey, "10.100.0.16", tID)
	if err != nil {
		t.Fatalf("CreateSession 2 failed: %v", err)
	}
	if sess2.ID == sess1.ID {
		t.Fatalf("expected new UUID for reconnected session")
	}

	// 3. Verify DB row ID was synchronized to new session ID
	dbSess2, err := db.GetVPNSessionByPeerKey(ctx, peerKey)
	if err != nil || dbSess2 == nil {
		t.Fatalf("failed to query DB session after reconnect: %v", err)
	}
	if dbSess2.ID != sess2.ID {
		t.Errorf("DB session ID not synchronized on reconnect: db=%s, sess2=%s", dbSess2.ID, sess2.ID)
	}

	// 4. Update traffic with new session ID and verify DB row is updated
	if err := db.UpdateVPNSessionTraffic(ctx, sess2.ID, 4096, 8192); err != nil {
		t.Fatalf("UpdateVPNSessionTraffic failed: %v", err)
	}

	updatedDBSess, err := db.GetVPNSessionByID(ctx, sess2.ID)
	if err != nil || updatedDBSess == nil {
		t.Fatalf("GetVPNSessionByID failed: %v", err)
	}
	if updatedDBSess.RxBytes != 4096 || updatedDBSess.TxBytes != 8192 {
		t.Errorf("traffic mismatch: rx=%d, tx=%d", updatedDBSess.RxBytes, updatedDBSess.TxBytes)
	}
}
