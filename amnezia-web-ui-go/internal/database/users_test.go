package database

import (
	"context"
	"testing"
	"time"

	"github.com/devops-igor/amnezia-web-ui-go/internal/models"
)

func TestUsersEmptyAndNotFound(t *testing.T) {
	db, _ := setupTestDB(t)
	ctx := context.Background()

	allUsers, err := db.GetAllUsers(ctx)
	if err != nil || len(allUsers) != 0 {
		t.Fatalf("GetAllUsers empty DB = (%v, %v), want ([], nil)", allUsers, err)
	}

	c, err := db.CountUsers(ctx)
	if err != nil || c != 0 {
		t.Errorf("CountUsers empty DB = %d, err = %v, want 0, nil", c, err)
	}

	uNonExistent, err := db.GetUser(ctx, "non-existent-uuid")
	if err != nil || uNonExistent != nil {
		t.Errorf("GetUser(non-existent) = (%v, %v), want (nil, nil)", uNonExistent, err)
	}

	uByIDAlias, err := db.GetUserByID(ctx, "non-existent-uuid")
	if err != nil || uByIDAlias != nil {
		t.Errorf("GetUserByID(non-existent) = (%v, %v), want (nil, nil)", uByIDAlias, err)
	}

	uByUsername, err := db.GetUserByUsername(ctx, "ghost")
	if err != nil || uByUsername != nil {
		t.Errorf("GetUserByUsername(ghost) = (%v, %v), want (nil, nil)", uByUsername, err)
	}

	uByShareToken, err := db.GetUserByShareToken(ctx, "invalid-token")
	if err != nil || uByShareToken != nil {
		t.Errorf("GetUserByShareToken(invalid) = (%v, %v), want (nil, nil)", uByShareToken, err)
	}

	uByRemna, err := db.GetUserByRemnaWaveUUID(ctx, "invalid-remna-uuid")
	if err != nil || uByRemna != nil {
		t.Errorf("GetUserByRemnaWaveUUID(invalid) = (%v, %v), want (nil, nil)", uByRemna, err)
	}
}

func TestUsersCreateAndRetrieve(t *testing.T) {
	db, _ := setupTestDB(t)
	ctx := context.Background()

	emailStr := "alice@example.com"
	telStr := "123456789"
	descStr := "Main administrator account"
	tokenStr := "share-token-alice-12345"
	passHashStr := "$2b$12$sharepasswordhash"
	remnaStr := "remna-uuid-alice-98765"
	monthResetStr := "2026-08-01T00:00:00Z"
	lastResetStr := "2026-08-01T00:00:00Z"
	expDate := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	expiresAt := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)

	u1 := &models.User{
		ID:                     "custom-alice-id",
		Username:               " Alice ",
		Email:                  &emailStr,
		TelegramID:             &telStr,
		Description:            &descStr,
		PasswordHash:           "$2b$12$hashAlice",
		Role:                   models.RoleAdmin,
		Enabled:                true,
		TrafficLimit:           10000000,
		TrafficUsed:            2000000,
		TrafficTotal:           5000000,
		TrafficTotalRx:         2000000,
		TrafficTotalTx:         3000000,
		MonthlyRx:              1000000,
		MonthlyTx:              1000000,
		MonthlyResetAt:         &monthResetStr,
		TrafficResetStrategy:   models.ResetStrategyMonthly,
		ShareEnabled:           true,
		ShareToken:             &tokenStr,
		SharePasswordHash:      &passHashStr,
		RemnaWaveUUID:          &remnaStr,
		CreatedAt:              time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		LastResetAt:            &lastResetStr,
		ExpirationDate:         &expDate,
		ExpiresAt:              &expiresAt,
		AWGMimicry:             models.AWGMimicryTLS,
		PasswordChangeRequired: true,
		Limits:                 map[string]any{"max_ips": 3},
	}

	id1, err := db.CreateUser(ctx, u1)
	if err != nil || id1 != "custom-alice-id" {
		t.Fatalf("CreateUser u1 failed: id=%s, err=%v", id1, err)
	}

	retrievedAlice, err := db.GetUser(ctx, id1)
	if err != nil || retrievedAlice == nil {
		t.Fatalf("GetUser(alice) failed: %v", err)
	}
	if retrievedAlice.Username != "alice" || retrievedAlice.Role != models.RoleAdmin {
		t.Errorf("Alice data mismatch: %+v", retrievedAlice)
	}

	byShare, err := db.GetUserByShareToken(ctx, tokenStr)
	if err != nil || byShare == nil || byShare.ID != id1 {
		t.Errorf("GetUserByShareToken failed: %+v, err=%v", byShare, err)
	}

	byRemna, err := db.GetUserByRemnaWaveUUID(ctx, remnaStr)
	if err != nil || byRemna == nil || byRemna.ID != id1 {
		t.Errorf("GetUserByRemnaWaveUUID failed: %+v, err=%v", byRemna, err)
	}

	u2 := &models.User{Username: "bob_default"}
	id2, _ := db.CreateUser(ctx, u2)
	retrievedBob, _ := db.GetUser(ctx, id2)
	if retrievedBob.Role != models.RoleUser || retrievedBob.TrafficResetStrategy != models.ResetStrategyNever || retrievedBob.AWGMimicry != models.AWGMimicryAuto {
		t.Errorf("Bob default fields mismatch: %+v", retrievedBob)
	}
}

func TestUsersUpdateAndLimits(t *testing.T) {
	db, _ := setupTestDB(t)
	ctx := context.Background()

	uID, _ := db.CreateUser(ctx, &models.User{Username: "update_user"})

	upGhost, err := db.UpdateUser(ctx, "ghost-id", map[string]any{"username": "ghost"})
	if err != nil || upGhost {
		t.Errorf("UpdateUser(ghost) = (%v, %v), want (false, nil)", upGhost, err)
	}

	if _, err := db.UpdateUser(ctx, uID, map[string]any{"invalid_col": "bad"}); err == nil {
		t.Errorf("expected error updating invalid column")
	}

	if ok, err := db.UpdateUser(ctx, uID, map[string]any{}); err != nil || !ok {
		t.Errorf("UpdateUser empty map failed: (%v, %v)", ok, err)
	}

	newExp := time.Date(2028, 6, 1, 0, 0, 0, 0, time.UTC)
	ok, err := db.UpdateUser(ctx, uID, map[string]any{
		"username":                 " Updated_User ",
		"email":                    "updated@example.com",
		"enabled":                  true,
		"share_enabled":            true,
		"password_change_required": false,
		"limits":                   map[string]any{"quota_mb": 5000},
		"expiration_date":          &newExp,
		"expires_at":               newExp,
	})
	if err != nil || !ok {
		t.Fatalf("UpdateUser failed: %v", err)
	}

	updated, _ := db.GetUser(ctx, uID)
	if updated.Username != "updated_user" || *updated.Email != "updated@example.com" {
		t.Errorf("Updated fields mismatch: %+v", updated)
	}

	if err := db.UpdateUserLimits(ctx, uID, map[string]any{"custom_limit": 100}); err != nil {
		t.Fatalf("UpdateUserLimits failed: %v", err)
	}

	futureExpiry := time.Now().Add(48 * time.Hour)
	if err := db.UpdateUserExpiry(ctx, uID, &futureExpiry); err != nil {
		t.Fatalf("UpdateUserExpiry failed: %v", err)
	}
	if err := db.UpdateUserExpiry(ctx, uID, nil); err != nil {
		t.Fatalf("UpdateUserExpiry(nil) failed: %v", err)
	}

	_, _ = db.ToggleUser(ctx, uID, false)
	toggledOff, _ := db.GetUser(ctx, uID)
	if toggledOff.Enabled {
		t.Errorf("expected disabled user")
	}
}

func TestUsersTrafficLeaderboardAndReset(t *testing.T) {
	db, _ := setupTestDB(t)
	ctx := context.Background()

	u1ID, _ := db.CreateUser(ctx, &models.User{Username: "alice_traffic", Enabled: true, TrafficResetStrategy: models.ResetStrategyMonthly})
	u2ID, _ := db.CreateUser(ctx, &models.User{Username: "bob_traffic", Enabled: true, TrafficResetStrategy: models.ResetStrategyNever})

	if err := db.UpdateUserTraffic(ctx, u1ID, 500, 1500); err != nil {
		t.Fatalf("UpdateUserTraffic alice failed: %v", err)
	}
	if err := db.UpdateUserTraffic(ctx, u2ID, 1000, 3000); err != nil {
		t.Fatalf("UpdateUserTraffic bob failed: %v", err)
	}

	lbMonthly, err := db.GetLeaderboard(ctx, "monthly")
	if err != nil || len(lbMonthly) != 2 {
		t.Fatalf("GetLeaderboard(monthly) failed: len=%d, err=%v", len(lbMonthly), err)
	}

	lbTotal, err := db.GetLeaderboard(ctx, "total")
	if err != nil || len(lbTotal) != 2 {
		t.Fatalf("GetLeaderboard(total) failed: len=%d, err=%v", len(lbTotal), err)
	}

	resetRows, err := db.ResetMonthlyTraffic(ctx)
	if err != nil || resetRows != 1 {
		t.Fatalf("ResetMonthlyTraffic affected %d rows, want 1, err=%v", resetRows, err)
	}
}

func TestUsersQuotaAndExpiration(t *testing.T) {
	db, _ := setupTestDB(t)
	ctx := context.Background()

	_, _ = db.CreateUser(ctx, &models.User{
		Username:     "quota_user",
		TrafficLimit: 1000,
		TrafficUsed:  2000,
		Enabled:      true,
	})
	overQuota, err := db.GetUsersOverQuota(ctx)
	if err != nil || len(overQuota) != 1 || overQuota[0].Username != "quota_user" {
		t.Errorf("GetUsersOverQuota failed: %+v, err=%v", overQuota, err)
	}

	pastTime := time.Now().Add(-10 * time.Hour)
	_, _ = db.CreateUser(ctx, &models.User{
		Username:  "expired_user",
		ExpiresAt: &pastTime,
		Enabled:   true,
	})
	expired, err := db.GetExpiredUsers(ctx)
	if err != nil || len(expired) != 1 || expired[0].Username != "expired_user" {
		t.Errorf("GetExpiredUsers failed: %+v, err=%v", expired, err)
	}
}

func TestUsersDeleteAndCascade(t *testing.T) {
	db, _ := setupTestDB(t)
	ctx := context.Background()

	delUID, _ := db.CreateUser(ctx, &models.User{Username: "delete_cascade_user"})
	sID, _ := db.CreateServer(ctx, &models.Server{Name: "Cascade Server", Host: "1.2.3.4"})
	_, _ = db.CreateConnection(ctx, &models.UserConnection{UserID: delUID, ServerID: sID, Protocol: "awg"})
	_ = db.LogConnectionCreation(ctx, delUID)
	_ = db.CreateVPNSession(ctx, &models.VPNSession{UserID: delUID, PeerPublicKey: "peer-del-key"})

	delRes, err := db.DeleteUser(ctx, delUID)
	if err != nil || !delRes {
		t.Fatalf("DeleteUser failed: %v", err)
	}

	delCheck, _ := db.GetUser(ctx, delUID)
	if delCheck != nil {
		t.Errorf("deleted user still exists")
	}

	delGhost, err := db.DeleteUser(ctx, "ghost-id")
	if err != nil || delGhost {
		t.Errorf("DeleteUser(ghost) = (%v, %v), want (false, nil)", delGhost, err)
	}
}

func TestUsersDateAndNullScanningEdgeCases(t *testing.T) {
	db, _ := setupTestDB(t)
	ctx := context.Background()

	_, _ = db.sqlDB.ExecContext(ctx, `INSERT INTO users (id, username, password_hash, expiration_date) VALUES ('u-expdate-only', 'expdate_only', 'h', '2027-01-01T00:00:00Z')`)
	uExpDateOnly, err := db.GetUser(ctx, "u-expdate-only")
	if err != nil || uExpDateOnly == nil || uExpDateOnly.ExpirationDate == nil || uExpDateOnly.ExpiresAt == nil {
		t.Errorf("uExpDateOnly reciprocal population failed: %+v", uExpDateOnly)
	}

	_, _ = db.sqlDB.ExecContext(ctx, `INSERT INTO users (id, username, password_hash, expires_at) VALUES ('u-expiresat-only', 'expiresat_only', 'h', '2027-01-01T00:00:00Z')`)
	uExpiresAtOnly, err := db.GetUser(ctx, "u-expiresat-only")
	if err != nil || uExpiresAtOnly == nil || uExpiresAtOnly.ExpirationDate == nil || uExpiresAtOnly.ExpiresAt == nil {
		t.Errorf("uExpiresAtOnly reciprocal population failed: %+v", uExpiresAtOnly)
	}

	_, _ = db.sqlDB.ExecContext(ctx, `INSERT INTO users (id, username, password_hash, role, traffic_reset_strategy, awg_mimicry, limits) VALUES ('u-empty-enums', 'empty_enums', 'h', '', '', '', 'invalid-json{')`)
	uEmptyEnums, err := db.GetUser(ctx, "u-empty-enums")
	if err != nil || uEmptyEnums == nil || uEmptyEnums.Role != models.RoleUser || uEmptyEnums.TrafficResetStrategy != models.ResetStrategyNever || uEmptyEnums.AWGMimicry != models.AWGMimicryAuto {
		t.Errorf("default fallback for empty enum strings failed: %+v", uEmptyEnums)
	}

	badLimits := map[string]any{"bad": make(chan int)}
	if _, err := db.UpdateUser(ctx, "u-empty-enums", map[string]any{"limits": badLimits}); err == nil {
		t.Errorf("expected error updating user with unmarshalable limits")
	}
}
