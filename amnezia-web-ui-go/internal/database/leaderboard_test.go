package database

import (
	"context"
	"testing"
	"time"

	"github.com/devops-igor/amnezia-web-ui-go/internal/models"
)

func TestLeaderboardEmpty(t *testing.T) {
	db, _ := setupTestDB(t)
	ctx := context.Background()

	entriesEmpty, err := db.GetLeaderboardSnapshot(ctx, 2026, 8)
	if err != nil || len(entriesEmpty) != 0 {
		t.Fatalf("GetLeaderboardSnapshot empty DB = (%v, %v), want ([], nil)", entriesEmpty, err)
	}

	historyEmpty, err := db.GetLeaderboardHistory(ctx, 0)
	if err != nil || len(historyEmpty) != 0 {
		t.Fatalf("GetLeaderboardHistory empty DB = (%v, %v), want ([], nil)", historyEmpty, err)
	}

	savedZero, err := db.SaveLeaderboardSnapshot(ctx, 2026, 8)
	if err != nil || savedZero != 0 {
		t.Errorf("SaveLeaderboardSnapshot empty = (%d, %v), want (0, nil)", savedZero, err)
	}
}

func TestLeaderboardSnapshotsAndHistory(t *testing.T) {
	db, _ := setupTestDB(t)
	ctx := context.Background()

	u1ID, _ := db.CreateUser(ctx, &models.User{Username: "top_user_1", Enabled: true, TrafficResetStrategy: models.ResetStrategyMonthly})
	u2ID, _ := db.CreateUser(ctx, &models.User{Username: "top_user_2", Enabled: true, TrafficResetStrategy: models.ResetStrategyMonthly})
	u3ID, _ := db.CreateUser(ctx, &models.User{Username: "disabled_user", Enabled: false, TrafficResetStrategy: models.ResetStrategyMonthly})

	_ = db.UpdateUserTraffic(ctx, u1ID, 10000, 20000)
	_ = db.UpdateUserTraffic(ctx, u2ID, 5000, 10000)
	_ = db.UpdateUserTraffic(ctx, u3ID, 50000, 50000)

	savedCount, err := db.SaveLeaderboardSnapshot(ctx, 2026, 8)
	if err != nil || savedCount != 2 {
		t.Fatalf("SaveLeaderboardSnapshot expected 2, got %d, err=%v", savedCount, err)
	}

	snapEntries, err := db.GetLeaderboardSnapshot(ctx, 2026, 8)
	if err != nil || len(snapEntries) != 2 {
		t.Fatalf("GetLeaderboardSnapshot expected 2, got %d, err=%v", len(snapEntries), err)
	}
	if snapEntries[0].Username != "top_user_1" || snapEntries[0].Rank != 1 || snapEntries[0].Total != 30000 {
		t.Errorf("Snapshot rank 1 mismatch: %+v", snapEntries[0])
	}
	if snapEntries[1].Username != "top_user_2" || snapEntries[1].Rank != 2 || snapEntries[1].Total != 15000 {
		t.Errorf("Snapshot rank 2 mismatch: %+v", snapEntries[1])
	}

	_, _ = db.SaveLeaderboardSnapshot(ctx, 2026, 7)

	historyDefaultLimit, err := db.GetLeaderboardHistory(ctx, -1)
	if err != nil || len(historyDefaultLimit) != 4 {
		t.Fatalf("GetLeaderboardHistory(-1) expected 4, got %d, err=%v", len(historyDefaultLimit), err)
	}

	historyLimited, err := db.GetLeaderboardHistory(ctx, 2)
	if err != nil || len(historyLimited) != 2 {
		t.Fatalf("GetLeaderboardHistory(2) expected 2, got %d, err=%v", len(historyLimited), err)
	}
}

func TestLeaderboardSnapshotPruning(t *testing.T) {
	db, _ := setupTestDB(t)
	ctx := context.Background()

	uID, _ := db.CreateUser(ctx, &models.User{Username: "prune_user", Enabled: true, TrafficResetStrategy: models.ResetStrategyMonthly})
	_ = db.UpdateUserTraffic(ctx, uID, 1000, 2000)

	now := time.Now()
	currYear, currMonth := now.Year(), int(now.Month())
	oldDate := now.AddDate(0, -2, 0)
	oldYear, oldMonth := oldDate.Year(), int(oldDate.Month())

	_, _ = db.SaveLeaderboardSnapshot(ctx, currYear, currMonth)
	_, _ = db.SaveLeaderboardSnapshot(ctx, oldYear, oldMonth)

	deletedOld, err := db.DeleteOldSnapshots(ctx, 1)
	if err != nil || deletedOld != 1 {
		t.Errorf("DeleteOldSnapshots(1) = (%d, %v), want (1, nil)", deletedOld, err)
	}

	historyAfterDelete, _ := db.GetLeaderboardHistory(ctx, 100)
	if len(historyAfterDelete) != 1 {
		t.Errorf("expected 1 snapshot remaining after pruning, got %d", len(historyAfterDelete))
	}
}
