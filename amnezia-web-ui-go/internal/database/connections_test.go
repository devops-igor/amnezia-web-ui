package database

import (
	"context"
	"testing"
	"time"

	"github.com/devops-igor/amnezia-web-ui-go/internal/models"
)

func TestConnectionsEmptyAndNotFound(t *testing.T) {
	db, _ := setupTestDB(t)
	ctx := context.Background()

	allConns, err := db.GetAllConnections(ctx)
	if err != nil || len(allConns) != 0 {
		t.Fatalf("GetAllConnections on empty DB = (%v, %v), want ([], nil)", allConns, err)
	}

	connNonExistent, err := db.GetConnection(ctx, "non-existent-id")
	if err != nil || connNonExistent != nil {
		t.Errorf("GetConnection(non-existent) = (%v, %v), want (nil, nil)", connNonExistent, err)
	}

	connByIDAlias, err := db.GetConnectionByID(ctx, "non-existent-id")
	if err != nil || connByIDAlias != nil {
		t.Errorf("GetConnectionByID(non-existent) = (%v, %v), want (nil, nil)", connByIDAlias, err)
	}

	byTokenNonExistent, err := db.GetConnectionByToken(ctx, "non-existent-token")
	if err != nil || byTokenNonExistent != nil {
		t.Errorf("GetConnectionByToken(non-existent) = (%v, %v), want (nil, nil)", byTokenNonExistent, err)
	}

	byShareTokenNonExistent, err := db.GetConnectionsByToken(ctx, "non-existent-share-token")
	if err != nil || len(byShareTokenNonExistent) != 0 {
		t.Errorf("GetConnectionsByToken(non-existent) = (%v, %v), want (nil/empty, nil)", byShareTokenNonExistent, err)
	}
}

func TestConnectionsCreateAndGet(t *testing.T) {
	db, _ := setupTestDB(t)
	ctx := context.Background()

	sID, _ := db.CreateServer(ctx, &models.Server{Name: "Server 1", Host: "10.0.1.1"})
	uID, _ := db.CreateUser(ctx, &models.User{Username: "user1"})

	c1 := &models.UserConnection{
		ID:             "custom-c1-id",
		UserID:         uID,
		ServerID:       sID,
		Protocol:       "awg2",
		ClientID:       "client-pubkey-1",
		Name:           "Phone Wireguard",
		AWGMimicry:     models.AWGMimicryQUIC,
		LastRx:         100,
		LastTx:         200,
		TrafficDeltaRx: 10,
		TrafficDeltaTx: 20,
		TrafficTotalRx: 1000,
		TrafficTotalTx: 2000,
		TrafficTotal:   3000,
		CreatedAt:      time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}

	c1ID, err := db.CreateConnection(ctx, c1)
	if err != nil || c1ID != "custom-c1-id" {
		t.Fatalf("CreateConnection failed: %v", err)
	}

	c2 := &models.UserConnection{
		UserID:     uID,
		ServerID:   sID,
		Protocol:   "telemt",
		ClientID:   "client-pubkey-2",
		AWGMimicry: "",
	}
	c2ID, _ := db.CreateConnection(ctx, c2)

	all, _ := db.GetAllConnections(ctx)
	if len(all) != 2 {
		t.Errorf("expected 2 connections, got %d", len(all))
	}

	retrievedC1, _ := db.GetConnectionByID(ctx, c1ID)
	if retrievedC1.Protocol != "awg" || retrievedC1.AWGMimicry != models.AWGMimicryQUIC {
		t.Errorf("c1 mismatch: %+v", retrievedC1)
	}

	retrievedC2, _ := db.GetConnection(ctx, c2ID)
	if retrievedC2.AWGMimicry != models.AWGMimicryAuto {
		t.Errorf("expected AWGMimicryAuto for c2, got: %s", retrievedC2.AWGMimicry)
	}
}

func TestConnectionsQueryByFilters(t *testing.T) {
	db, _ := setupTestDB(t)
	ctx := context.Background()

	s1ID, _ := db.CreateServer(ctx, &models.Server{Name: "Server 1", Host: "10.0.1.1"})
	s2ID, _ := db.CreateServer(ctx, &models.Server{Name: "Server 2", Host: "10.0.2.1"})

	shareToken := "user-share-token"
	uID, _ := db.CreateUser(ctx, &models.User{Username: "filter_user", ShareToken: &shareToken, ShareEnabled: true})

	_, _ = db.CreateConnection(ctx, &models.UserConnection{UserID: uID, ServerID: s1ID, Protocol: "awg", ClientID: "c1"})
	_, _ = db.CreateConnection(ctx, &models.UserConnection{UserID: uID, ServerID: s2ID, Protocol: "telemt", ClientID: "c2"})

	uConns, err := db.GetConnectionsByUserID(ctx, uID)
	if err != nil || len(uConns) != 2 {
		t.Errorf("GetConnectionsByUserID failed: len=%d, err=%v", len(uConns), err)
	}

	uConnsAlias, err := db.GetConnectionsByUser(ctx, uID)
	if err != nil || len(uConnsAlias) != 2 {
		t.Errorf("GetConnectionsByUser failed: len=%d, err=%v", len(uConnsAlias), err)
	}

	s1Conns, err := db.GetConnectionsByServerID(ctx, s1ID)
	if err != nil || len(s1Conns) != 1 {
		t.Errorf("GetConnectionsByServerID failed: len=%d, err=%v", len(s1Conns), err)
	}

	syncConns, err := db.GetConnectionsForSync(ctx, s1ID)
	if err != nil || len(syncConns) != 1 {
		t.Errorf("GetConnectionsForSync failed: len=%d, err=%v", len(syncConns), err)
	}

	s1AWGConns, err := db.GetConnectionsByServerAndProtocol(ctx, s1ID, "awg2")
	if err != nil || len(s1AWGConns) != 1 {
		t.Errorf("GetConnectionsByServerAndProtocol failed: len=%d, err=%v", len(s1AWGConns), err)
	}

	byToken, err := db.GetConnectionByToken(ctx, "c1")
	if err != nil || byToken == nil || byToken.ClientID != "c1" {
		t.Errorf("GetConnectionByToken failed: %+v, err=%v", byToken, err)
	}

	byShareToken, err := db.GetConnectionsByToken(ctx, shareToken)
	if err != nil || len(byShareToken) != 2 {
		t.Errorf("GetConnectionsByToken failed: len=%d, err=%v", len(byShareToken), err)
	}
}

func TestConnectionsUpdateAndTraffic(t *testing.T) {
	db, _ := setupTestDB(t)
	ctx := context.Background()

	sID, _ := db.CreateServer(ctx, &models.Server{Name: "S1", Host: "1.1.1.1"})
	uID, _ := db.CreateUser(ctx, &models.User{Username: "traffic_user"})
	cID, _ := db.CreateConnection(ctx, &models.UserConnection{UserID: uID, ServerID: sID, Protocol: "awg"})

	upGhost, err := db.UpdateConnection(ctx, "ghost-conn", map[string]any{"name": "ghost"})
	if err != nil || upGhost {
		t.Errorf("UpdateConnection(ghost) = (%v, %v), want (false, nil)", upGhost, err)
	}

	if _, err := db.UpdateConnection(ctx, cID, map[string]any{"invalid_col": 123}); err == nil {
		t.Errorf("expected error on invalid connection column")
	}

	if ok, err := db.UpdateConnection(ctx, cID, map[string]any{}); err != nil || !ok {
		t.Errorf("UpdateConnection empty map failed: (%v, %v)", ok, err)
	}

	ok, err := db.UpdateConnection(ctx, cID, map[string]any{
		"name":        "Updated Phone Wireguard",
		"protocol":    "awg_legacy",
		"awg_mimicry": "sip",
	})
	if err != nil || !ok {
		t.Fatalf("UpdateConnection failed: %v", err)
	}

	if err := db.UpdateConnectionTraffic(ctx, cID, 50, 100); err != nil {
		t.Fatalf("UpdateConnectionTraffic failed: %v", err)
	}

	toggled, err := db.ToggleConnection(ctx, cID, true)
	if err != nil || !toggled {
		t.Errorf("ToggleConnection failed: (%v, %v)", toggled, err)
	}
	toggledGhost, err := db.ToggleConnection(ctx, "ghost-id", true)
	if err != nil || toggledGhost {
		t.Errorf("ToggleConnection(ghost) = (%v, %v), want (false, nil)", toggledGhost, err)
	}
}

func TestConnectionsRateLimitingAndPruning(t *testing.T) {
	db, _ := setupTestDB(t)
	ctx := context.Background()

	uID, _ := db.CreateUser(ctx, &models.User{Username: "rate_limit_user"})

	for i := 0; i < 5; i++ {
		if err := db.LogConnectionCreation(ctx, uID); err != nil {
			t.Fatalf("LogConnectionCreation failed: %v", err)
		}
	}
	logs, err := db.GetRecentConnectionsLog(ctx, uID, 3600)
	if err != nil || len(logs) != 5 {
		t.Errorf("GetRecentConnectionsLog expected 5, got %d, err=%v", len(logs), err)
	}

	if err := db.PruneConnectionLog(ctx, 2); err != nil {
		t.Fatalf("PruneConnectionLog failed: %v", err)
	}
	prunedLogs, err := db.GetRecentConnectionsLog(ctx, uID, 3600)
	if err != nil || len(prunedLogs) != 2 {
		t.Errorf("GetRecentConnectionsLog after prune expected 2, got %d, err=%v", len(prunedLogs), err)
	}
}

func TestConnectionsDeleteVariants(t *testing.T) {
	db, _ := setupTestDB(t)
	ctx := context.Background()

	sID, _ := db.CreateServer(ctx, &models.Server{Name: "S1", Host: "1.1.1.1"})
	uID, _ := db.CreateUser(ctx, &models.User{Username: "del_user"})

	c1ID, _ := db.CreateConnection(ctx, &models.UserConnection{UserID: uID, ServerID: sID, Protocol: "awg", ClientID: "c1"})
	_, _ = db.CreateConnection(ctx, &models.UserConnection{UserID: uID, ServerID: sID, Protocol: "awg", ClientID: "c2"})

	del1, err := db.DeleteConnection(ctx, c1ID)
	if err != nil || !del1 {
		t.Errorf("DeleteConnection failed: (%v, %v)", del1, err)
	}

	delByClient, err := db.DeleteConnectionByClientID(ctx, "c2", sID)
	if err != nil || !delByClient {
		t.Errorf("DeleteConnectionByClientID failed: (%v, %v)", delByClient, err)
	}

	_, _ = db.CreateConnection(ctx, &models.UserConnection{UserID: uID, ServerID: sID, Protocol: "awg", ClientID: "c3"})
	delProtoCount, err := db.DeleteConnectionsByServerAndProtocol(ctx, sID, "awg")
	if err != nil || delProtoCount != 1 {
		t.Errorf("DeleteConnectionsByServerAndProtocol expected 1, got %d, err=%v", delProtoCount, err)
	}

	_, _ = db.CreateConnection(ctx, &models.UserConnection{UserID: uID, ServerID: sID, Protocol: "telemt", ClientID: "c4"})
	delUserCount, err := db.DeleteConnectionsByUser(ctx, uID)
	if err != nil || delUserCount != 1 {
		t.Errorf("DeleteConnectionsByUser expected 1, got %d, err=%v", delUserCount, err)
	}

	_, _ = db.CreateConnection(ctx, &models.UserConnection{UserID: uID, ServerID: sID, Protocol: "telemt", ClientID: "c5"})
	delServerCount, err := db.DeleteConnectionsByServer(ctx, sID)
	if err != nil || delServerCount != 1 {
		t.Errorf("DeleteConnectionsByServer expected 1, got %d, err=%v", delServerCount, err)
	}
}

func TestConnectionsNullScanning(t *testing.T) {
	db, _ := setupTestDB(t)
	ctx := context.Background()

	sID, _ := db.CreateServer(ctx, &models.Server{Name: "S1", Host: "1.1.1.1"})
	uID, _ := db.CreateUser(ctx, &models.User{Username: "null_user"})

	_, _ = db.sqlDB.ExecContext(ctx, "INSERT INTO user_connections (id, user_id, server_id, protocol) VALUES ('conn-null-fields', ?, ?, 'awg')", uID, sID)
	cNull, err := db.GetConnection(ctx, "conn-null-fields")
	if err != nil || cNull == nil || !cNull.CreatedAt.IsZero() || cNull.ClientID != "" || cNull.AWGMimicry != models.AWGMimicryAuto {
		t.Errorf("expected default/empty fields in cNull, got: %+v", cNull)
	}
}
