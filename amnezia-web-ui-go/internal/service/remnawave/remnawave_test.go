package remnawave

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/devops-igor/amnezia-web-ui-go/internal/database"
	"github.com/devops-igor/amnezia-web-ui-go/internal/models"
	"github.com/devops-igor/amnezia-web-ui-go/internal/service/userops"
)

type mockMassOps struct {
	mu          sync.Mutex
	deleteUIDs  []string
	toggleUIDs  []userops.UserToggle
	createConns []userops.ConnectionCreateRequest
}

func (m *mockMassOps) PerformMassOperations(ctx context.Context, req userops.MassOperationRequest) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deleteUIDs = append(m.deleteUIDs, req.DeleteUIDs...)
	m.toggleUIDs = append(m.toggleUIDs, req.ToggleUIDs...)
	m.createConns = append(m.createConns, req.CreateConns...)
	return nil
}

func setupTestDB(t *testing.T) (*database.DB, func()) {
	f, err := os.CreateTemp("", "test_remnawave_*.db")
	if err != nil {
		t.Fatalf("failed to create temp db: %v", err)
	}
	dbPath := f.Name()
	_ = f.Close()

	db, err := database.New(dbPath, "test-secret-key")
	if err != nil {
		_ = os.Remove(dbPath)
		t.Fatalf("failed to open test db: %v", err)
	}

	cleanup := func() {
		_ = db.Close()
		_ = os.Remove(dbPath)
	}

	return db, cleanup
}

func TestRemnaWave_HTTPClient_PaginationAndRetries(t *testing.T) {
	var attempts int32

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth != "Bearer secret-token" {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		currentAttempt := atomic.AddInt32(&attempts, 1)
		// First request fails with 500 to test retry
		if currentAttempt == 1 {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}

		start := r.URL.Query().Get("start")
		var users []User
		total := 3

		if start == "0" {
			users = []User{
				{UUID: "u-1", Username: "user1", Status: "ACTIVE", Email: "u1@test.com"},
				{UUID: "u-2", Username: "user2", Status: "DISABLED", TelegramID: int64(7352456128)},
			}
		} else {
			users = []User{
				{UUID: "u-3", Username: "user3", Status: "ACTIVE", Description: "Third user"},
			}
		}

		resp := map[string]any{
			"response": map[string]any{
				"users": users,
				"total": total,
			},
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	client := NewClient(
		WithMaxRetries(2),
		WithBaseDelay(5*time.Millisecond),
	)

	users, err := client.GetUsers(context.Background(), ts.URL, "secret-token", 2)
	if err != nil {
		t.Fatalf("GetUsers failed: %v", err)
	}

	if len(users) != 3 {
		t.Fatalf("expected 3 users across pages, got %d", len(users))
	}
	if users[0].Username != "user1" || users[1].Username != "user2" || users[2].Username != "user3" {
		t.Errorf("unexpected users returned: %+v", users)
	}
	if users[1].TelegramIDString() == nil || *users[1].TelegramIDString() != "7352456128" {
		t.Errorf("unexpected telegram ID: %v", users[1].TelegramIDString())
	}
}

func TestRemnaWave_HTTPClient_Errors(t *testing.T) {
	customHTTP := &http.Client{Timeout: 5 * time.Second}
	client := NewClient(
		WithHTTPClient(customHTTP),
		WithMaxRetries(1),
		WithBaseDelay(1*time.Millisecond),
	)
	ctx := context.Background()

	if _, err := client.GetUsers(ctx, "", "token", 50); err == nil {
		t.Error("expected error for empty base URL")
	}
	if _, err := client.GetUsers(ctx, "http://localhost", "", 50); err == nil {
		t.Error("expected error for empty API key")
	}

	// 404 handler
	ts404 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Not found", http.StatusNotFound)
	}))
	defer ts404.Close()

	if _, err := client.GetUsers(ctx, ts404.URL, "token", 50); err == nil {
		t.Error("expected error for 404 endpoint")
	}

	// Invalid JSON response
	tsInvalidJSON := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{invalid-json`))
	}))
	defer tsInvalidJSON.Close()

	if _, err := client.GetUsers(ctx, tsInvalidJSON.URL, "token", 50); err == nil {
		t.Error("expected error for invalid JSON")
	}

	// 500 error exhausting all retries
	ts500 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "server error", http.StatusInternalServerError)
	}))
	defer ts500.Close()

	if _, err := client.GetUsers(ctx, ts500.URL, "token", 50); err == nil {
		t.Error("expected error when all retries are exhausted")
	}

	// Cancelled context
	cancCtx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.GetUsers(cancCtx, ts404.URL, "token", 50); err == nil {
		t.Error("expected error on cancelled context")
	}

	// TelegramIDString tests
	uEmpty := User{TelegramID: nil}
	if uEmpty.TelegramIDString() != nil {
		t.Error("expected nil for empty TelegramID")
	}
	uNilStr := User{TelegramID: "<nil>"}
	if uNilStr.TelegramIDString() != nil {
		t.Error("expected nil for <nil> TelegramID")
	}
	uSpace := User{TelegramID: "   "}
	if uSpace.TelegramIDString() != nil {
		t.Error("expected nil for whitespace TelegramID")
	}

	// 10-digit realistic IDs
	uInt64 := User{TelegramID: int64(7352456128)}
	if s := uInt64.TelegramIDString(); s == nil || *s != "7352456128" {
		t.Errorf("expected 7352456128 for int64, got %v", s)
	}

	uFloat64 := User{TelegramID: float64(7352456128)}
	if s := uFloat64.TelegramIDString(); s == nil || *s != "7352456128" {
		t.Errorf("expected 7352456128 for float64, got %v", s)
	}

	uNumber := User{TelegramID: json.Number("7352456128")}
	if s := uNumber.TelegramIDString(); s == nil || *s != "7352456128" {
		t.Errorf("expected 7352456128 for json.Number, got %v", s)
	}

	uInt := User{TelegramID: int(123456)}
	if s := uInt.TelegramIDString(); s == nil || *s != "123456" {
		t.Errorf("expected 123456 for int, got %v", s)
	}

	uFloatDec := User{TelegramID: float64(123.45)}
	if s := uFloatDec.TelegramIDString(); s == nil || *s != "123.45" {
		t.Errorf("expected 123.45 for float with decimals, got %v", s)
	}
}

func TestRemnaWave_Syncer_NilDB(t *testing.T) {
	s := NewSyncer(nil, nil, nil)
	if _, _, err := s.Sync(context.Background()); err == nil {
		t.Error("expected error for nil DB in Syncer")
	}
}

func TestRemnaWave_Syncer_DisabledOrMissingConfig(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	syncer := NewSyncer(db, nil, nil)

	// 1. Sync is disabled in settings
	_ = db.SetSetting(ctx, "sync", &models.SyncSettings{
		RemnawaveSyncUsers: false,
	})

	count, msg, err := syncer.Sync(ctx)
	if err != nil || count != 0 || msg != "Synchronization is disabled in settings" {
		t.Errorf("unexpected result for disabled sync: count=%d, msg=%q, err=%v", count, msg, err)
	}

	// 2. Sync is enabled but URL is missing
	_ = db.SetSetting(ctx, "sync", &models.SyncSettings{
		RemnawaveSyncUsers: true,
		RemnawaveURL:       "",
		RemnawaveAPIKey:    "some-key",
	})

	count, msg, err = syncer.Sync(ctx)
	if err != nil || count != 0 || msg != "Remnawave URL or API Key not configured" {
		t.Errorf("unexpected result for missing URL: count=%d, msg=%q, err=%v", count, msg, err)
	}
}

func TestRemnaWave_Syncer_FullReconciliation(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	sID, _ := db.CreateServer(ctx, &models.Server{
		Name:    "VPN Server",
		Host:    "10.0.0.1",
		SSHPort: 22,
	})

	// User 1: Exists locally with RemnaWave UUID, but deleted in RemnaWave (should be removed)
	u1UUID := "rw-uuid-deleted"
	u1ID, _ := db.CreateUser(ctx, &models.User{
		Username:      "deleted_user",
		RemnaWaveUUID: &u1UUID,
		Role:          models.RoleUser,
		Enabled:       true,
	})

	// User 2: Exists locally and in RemnaWave (status changed from active to disabled, metadata updated)
	u2UUID := "rw-uuid-active-to-disabled"
	u2ID, _ := db.CreateUser(ctx, &models.User{
		Username:      "updated_user",
		RemnaWaveUUID: &u2UUID,
		Role:          models.RoleUser,
		Enabled:       true,
	})

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		users := []User{
			{
				UUID:        u2UUID,
				Username:    "updated_user_newname",
				Status:      "DISABLED",
				Email:       "updated@example.com",
				Description: "Updated description",
			},
			{
				UUID:        "rw-uuid-brand-new",
				Username:    "brand_new_user",
				Status:      "ACTIVE",
				TelegramID:  123456,
				Description: "Newly synced user",
			},
		}

		resp := map[string]any{
			"response": map[string]any{
				"users": users,
				"total": len(users),
			},
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	_ = db.SetSetting(ctx, "sync", &models.SyncSettings{
		RemnawaveSyncUsers:   true,
		RemnawaveURL:         ts.URL,
		RemnawaveAPIKey:      "test-api-key",
		RemnawaveCreateConns: true,
		RemnawaveServerID:    sID,
		RemnawaveProtocol:    "awg",
	})

	mockOps := &mockMassOps{}
	client := NewClient(WithMaxRetries(0))
	syncer := NewSyncer(db, client, mockOps)

	count, msg, err := syncer.Sync(ctx)
	if err != nil {
		t.Fatalf("Sync failed: %v", err)
	}

	if count != 2 {
		t.Errorf("expected 2 synced users, got %d", count)
	}
	if msg != "Successfully synchronized with Remnawave" {
		t.Errorf("unexpected message: %s", msg)
	}

	// Verify User 1 was queued for deletion
	mockOps.mu.Lock()
	defer mockOps.mu.Unlock()

	foundU1Delete := false
	for _, id := range mockOps.deleteUIDs {
		if id == u1ID {
			foundU1Delete = true
			break
		}
	}
	if !foundU1Delete {
		t.Errorf("expected user 1 (%s) to be queued for deletion", u1ID)
	}

	// Verify User 2 was updated in DB and queued for toggle to disabled
	u2, _ := db.GetUser(ctx, u2ID)
	if u2.Username != "updated_user_newname" {
		t.Errorf("expected updated username, got %s", u2.Username)
	}
	if u2.Email == nil || *u2.Email != "updated@example.com" {
		t.Errorf("expected updated email, got %v", u2.Email)
	}

	foundU2Toggle := false
	for _, tg := range mockOps.toggleUIDs {
		if tg.UserID == u2ID && tg.Enabled == false {
			foundU2Toggle = true
			break
		}
	}
	if !foundU2Toggle {
		t.Errorf("expected user 2 to be queued for disable toggle")
	}

	// Verify brand new user was created in DB and connection creation queued
	newU, err := db.GetUserByRemnaWaveUUID(ctx, "rw-uuid-brand-new")
	if err != nil || newU == nil {
		t.Fatalf("expected brand new user to be created in DB: %v", err)
		return
	}
	if newU.Username != "brand_new_user" {
		t.Errorf("unexpected username: %s", newU.Username)
	}
	if newU.TelegramID == nil || *newU.TelegramID != "123456" {
		t.Errorf("unexpected telegram ID: %v", newU.TelegramID)
	}

	foundConnCreate := false
	for _, cc := range mockOps.createConns {
		if cc.UserID == newU.ID && cc.ServerID == sID && cc.Protocol == "awg" {
			foundConnCreate = true
			break
		}
	}
	if !foundConnCreate {
		t.Errorf("expected connection create request for brand new user on server %d", sID)
	}
}
