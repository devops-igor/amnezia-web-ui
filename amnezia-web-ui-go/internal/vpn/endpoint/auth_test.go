package endpoint

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
	dbPath := filepath.Join(dir, "test_vpn_auth.db")
	db, err := database.Open(dbPath, "test-secret-key-1234567890123456")
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})
	return db
}

func TestDBAuthenticator(t *testing.T) {
	db := setupTestDB(t)
	auth := NewDBAuthenticator(db)
	ctx := context.Background()

	// Nil DB test
	nilAuth := NewDBAuthenticator(nil)
	if _, _, err := nilAuth.AuthenticatePeer(ctx, "any"); err == nil {
		t.Errorf("expected error for nil DB")
	}

	// Empty peer key
	if _, _, err := auth.AuthenticatePeer(ctx, ""); err != ErrPeerNotFound {
		t.Errorf("expected ErrPeerNotFound, got %v", err)
	}

	// Non-existent peer key
	if _, _, err := auth.AuthenticatePeer(ctx, "non-existent-peer-key"); err != ErrPeerNotFound {
		t.Errorf("expected ErrPeerNotFound, got %v", err)
	}

	// Create Server
	sID, err := db.CreateServer(ctx, &models.Server{Name: "Server 1", Host: "10.0.0.1"})
	if err != nil {
		t.Fatalf("CreateServer failed: %v", err)
	}

	// 1. Valid Active User
	u1ID, err := db.CreateUser(ctx, &models.User{
		Username: "valid_user",
		Enabled:  true,
	})
	if err != nil {
		t.Fatalf("CreateUser failed: %v", err)
	}

	peerKey1 := "valid-peer-public-key"
	_, err = db.CreateConnection(ctx, &models.UserConnection{
		UserID:   u1ID,
		ServerID: sID,
		Protocol: "awg",
		ClientID: peerKey1,
		Name:     "laptop",
	})
	if err != nil {
		t.Fatalf("CreateConnection failed: %v", err)
	}

	u1, c1, err := auth.AuthenticatePeer(ctx, peerKey1)
	if err != nil {
		t.Fatalf("AuthenticatePeer failed for valid user: %v", err)
	}
	if u1.ID != u1ID || c1.ClientID != peerKey1 {
		t.Errorf("mismatch user or conn: u=%+v, c=%+v", u1, c1)
	}

	// 2. Non-AWG protocol
	peerKeyNonAWG := "telemt-peer-key"
	_, err = db.CreateConnection(ctx, &models.UserConnection{
		UserID:   u1ID,
		ServerID: sID,
		Protocol: "telemt",
		ClientID: peerKeyNonAWG,
		Name:     "tg",
	})
	if err != nil {
		t.Fatalf("CreateConnection failed: %v", err)
	}
	if _, _, err := auth.AuthenticatePeer(ctx, peerKeyNonAWG); err != ErrInvalidProtocol {
		t.Errorf("expected ErrInvalidProtocol, got %v", err)
	}

	// 3. Disabled User
	u2ID, _ := db.CreateUser(ctx, &models.User{
		Username: "disabled_user",
		Enabled:  false,
	})
	peerKeyDisabled := "disabled-peer-key"
	_, _ = db.CreateConnection(ctx, &models.UserConnection{
		UserID:   u2ID,
		ServerID: sID,
		Protocol: "awg",
		ClientID: peerKeyDisabled,
	})
	if _, _, err := auth.AuthenticatePeer(ctx, peerKeyDisabled); err != ErrUserDisabled {
		t.Errorf("expected ErrUserDisabled, got %v", err)
	}

	// 4. Expired User via ExpiresAt
	past := time.Now().UTC().Add(-1 * time.Hour)
	u3ID, _ := db.CreateUser(ctx, &models.User{
		Username:  "expired_user_1",
		Enabled:   true,
		ExpiresAt: &past,
	})
	peerKeyExpired1 := "expired-peer-key-1"
	_, _ = db.CreateConnection(ctx, &models.UserConnection{
		UserID:   u3ID,
		ServerID: sID,
		Protocol: "awg",
		ClientID: peerKeyExpired1,
	})
	if _, _, err := auth.AuthenticatePeer(ctx, peerKeyExpired1); err != ErrUserExpired {
		t.Errorf("expected ErrUserExpired, got %v", err)
	}

	// 5. Expired User via ExpirationDate
	u4ID, _ := db.CreateUser(ctx, &models.User{
		Username:       "expired_user_2",
		Enabled:        true,
		ExpirationDate: &past,
	})
	peerKeyExpired2 := "expired-peer-key-2"
	_, _ = db.CreateConnection(ctx, &models.UserConnection{
		UserID:   u4ID,
		ServerID: sID,
		Protocol: "awg",
		ClientID: peerKeyExpired2,
	})
	if _, _, err := auth.AuthenticatePeer(ctx, peerKeyExpired2); err != ErrUserExpired {
		t.Errorf("expected ErrUserExpired, got %v", err)
	}

	// 6. Over Traffic Limit
	u5ID, _ := db.CreateUser(ctx, &models.User{
		Username:     "overlimit_user",
		Enabled:      true,
		TrafficLimit: 1000,
		TrafficUsed:  1000,
	})
	peerKeyOverlimit := "overlimit-peer-key"
	_, _ = db.CreateConnection(ctx, &models.UserConnection{
		UserID:   u5ID,
		ServerID: sID,
		Protocol: "awg",
		ClientID: peerKeyOverlimit,
	})
	if _, _, err := auth.AuthenticatePeer(ctx, peerKeyOverlimit); err != ErrTrafficLimitExceeded {
		t.Errorf("expected ErrTrafficLimitExceeded, got %v", err)
	}

	// 7. Orphaned connection (user deleted without cascade)
	u6ID, _ := db.CreateUser(ctx, &models.User{Username: "temp_user"})
	peerKeyOrphan := "orphan-peer-key"
	_, err = db.CreateConnection(ctx, &models.UserConnection{
		UserID:   u6ID,
		ServerID: sID,
		Protocol: "awg",
		ClientID: peerKeyOrphan,
	})
	if err != nil {
		t.Fatalf("CreateConnection failed: %v", err)
	}
	// Delete user directly via sql without triggering cascade
	_, _ = db.ExecContext(ctx, "PRAGMA foreign_keys=OFF")
	_, _ = db.ExecContext(ctx, "DELETE FROM users WHERE id = ?", u6ID)
	_, _ = db.ExecContext(ctx, "PRAGMA foreign_keys=ON")

	if _, _, err := auth.AuthenticatePeer(ctx, peerKeyOrphan); err != ErrUserNotFound {
		t.Errorf("expected ErrUserNotFound, got %v", err)
	}
}
