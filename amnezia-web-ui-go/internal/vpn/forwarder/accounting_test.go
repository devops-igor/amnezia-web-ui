package forwarder

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/devops-igor/amnezia-web-ui-go/internal/database"
	"github.com/devops-igor/amnezia-web-ui-go/internal/models"
)

func setupTestDB(t *testing.T) *database.DB {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test_vpn_forwarder.db")
	db, err := database.Open(dbPath, "test-secret-key-1234567890123456")
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})
	return db
}

func TestTrafficAccountant(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	sID, _ := db.CreateServer(ctx, &models.Server{Name: "Host", Host: "1.1.1.1"})
	uID, _ := db.CreateUser(ctx, &models.User{Username: "traffic_user"})
	tID, _ := db.CreateBackendTunnel(ctx, &models.BackendTunnel{
		ServerID:      sID,
		InterfaceName: "awg-be-1",
		PublicKey:     "pub1",
		PrivateKey:    "priv1",
		Endpoint:      "1.1.1.1:51820",
	})
	cID, _ := db.CreateConnection(ctx, &models.UserConnection{
		UserID:   uID,
		ServerID: sID,
		Protocol: "awg",
		ClientID: "peer-acc-1",
	})

	sessID := "sess-acc-1"
	_ = db.CreateVPNSession(ctx, &models.VPNSession{
		ID:              sessID,
		UserID:          uID,
		BackendTunnelID: tID,
		PeerPublicKey:   "peer-acc-1",
		AssignedIP:      "10.100.0.10",
		Status:          "connected",
	})

	accountant := NewTrafficAccountant(db, 50*time.Millisecond)

	// Record traffic
	accountant.RecordRx(sessID, cID, 1500)
	accountant.RecordRx(sessID, cID, 500)
	accountant.RecordTx(sessID, cID, 3000)

	// Negative / zero byte records (noop)
	accountant.RecordRx(sessID, cID, 0)
	accountant.RecordTx(sessID, cID, -50)

	sRx, sTx := accountant.GetSessionTraffic(sessID)
	if sRx != 2000 || sTx != 3000 {
		t.Errorf("GetSessionTraffic mismatch: rx=%d, tx=%d", sRx, sTx)
	}

	cRx, cTx := accountant.GetConnectionTraffic(cID)
	if cRx != 2000 || cTx != 3000 {
		t.Errorf("GetConnectionTraffic mismatch: rx=%d, tx=%d", cRx, cTx)
	}

	// Flush to DB
	if err := accountant.Flush(ctx); err != nil {
		t.Fatalf("Flush failed: %v", err)
	}

	// Verify counters reset in memory
	sRxAfter, sTxAfter := accountant.GetSessionTraffic(sessID)
	if sRxAfter != 0 || sTxAfter != 0 {
		t.Errorf("expected 0 in memory after flush, got rx=%d, tx=%d", sRxAfter, sTxAfter)
	}

	// Verify DB state
	dbSess, err := db.GetVPNSessionByID(ctx, sessID)
	if err != nil || dbSess.RxBytes != 2000 || dbSess.TxBytes != 3000 {
		t.Errorf("DB session traffic mismatch: rx=%d, tx=%d, err: %v", dbSess.RxBytes, dbSess.TxBytes, err)
	}

	dbConn, err := db.GetConnection(ctx, cID)
	if err != nil || dbConn.TrafficTotalRx != 2000 || dbConn.TrafficTotalTx != 3000 || dbConn.TrafficTotal != 5000 {
		t.Errorf("DB connection traffic mismatch: rx=%d, tx=%d, total=%d",
			dbConn.TrafficTotalRx, dbConn.TrafficTotalTx, dbConn.TrafficTotal)
	}

	dbUser, err := db.GetUser(ctx, uID)
	if err != nil || dbUser.TrafficTotalRx != 2000 || dbUser.TrafficTotalTx != 3000 || dbUser.TrafficUsed != 5000 {
		t.Errorf("DB user traffic mismatch: rx=%d, tx=%d, used=%d",
			dbUser.TrafficTotalRx, dbUser.TrafficTotalTx, dbUser.TrafficUsed)
	}

	// Start & Stop lifecycle
	accountant.Start(ctx)
	if !accountant.IsRunning() {
		t.Errorf("expected accountant to be running")
	}
	// Double start noop
	accountant.Start(ctx)

	accountant.RecordRx(sessID, cID, 1000)
	time.Sleep(120 * time.Millisecond)

	if err := accountant.Stop(); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}
	if accountant.IsRunning() {
		t.Errorf("expected accountant to not be running after Stop")
	}
	// Double stop noop
	_ = accountant.Stop()

	// Nil DB flush
	nilAccountant := NewTrafficAccountant(nil, 0)
	if err := nilAccountant.Flush(ctx); err != nil {
		t.Errorf("expected nil error on nil DB flush")
	}
}
