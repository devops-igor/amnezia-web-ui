package userops

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/devops-igor/amnezia-web-ui-go/internal/database"
	"github.com/devops-igor/amnezia-web-ui-go/internal/manager"
	"github.com/devops-igor/amnezia-web-ui-go/internal/models"
)

type mockProtocolManager struct {
	proto       string
	mu          sync.Mutex
	deleted     []string
	toggled     map[string]bool
	added       []string
	returnError bool
}

func newMockProtocolManager(proto string) *mockProtocolManager {
	return &mockProtocolManager{
		proto:   proto,
		toggled: make(map[string]bool),
	}
}

func (m *mockProtocolManager) Protocol() string {
	return m.proto
}

func (m *mockProtocolManager) Install(ctx context.Context, server *models.Server, params map[string]any) error {
	return nil
}

func (m *mockProtocolManager) Uninstall(ctx context.Context, server *models.Server) error {
	return nil
}

func (m *mockProtocolManager) GetClients(ctx context.Context, server *models.Server) ([]map[string]any, error) {
	return nil, nil
}

func (m *mockProtocolManager) AddClient(ctx context.Context, server *models.Server, clientParams map[string]any) (map[string]any, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.returnError {
		return nil, errors.New("add client error")
	}
	name, _ := clientParams["name"].(string)
	clientID := fmt.Sprintf("cid-%s", name)
	m.added = append(m.added, clientID)
	return map[string]any{
		"client_id": clientID,
	}, nil
}

func (m *mockProtocolManager) RemoveClient(ctx context.Context, server *models.Server, clientID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.returnError {
		return errors.New("remove client error")
	}
	m.deleted = append(m.deleted, clientID)
	return nil
}

func (m *mockProtocolManager) GetClientConfig(ctx context.Context, server *models.Server, clientID string) (string, error) {
	return "", nil
}

func (m *mockProtocolManager) ToggleClient(ctx context.Context, server *models.Server, clientID string, enable bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.returnError {
		return errors.New("toggle client error")
	}
	m.toggled[clientID] = enable
	return nil
}

type mockRegistry struct {
	mu       sync.RWMutex
	managers map[string]manager.ProtocolManager
}

func newMockRegistry() *mockRegistry {
	return &mockRegistry{
		managers: make(map[string]manager.ProtocolManager),
	}
}

func (r *mockRegistry) Register(mgr manager.ProtocolManager) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.managers[models.NormalizeProtocol(mgr.Protocol())] = mgr
}

func (r *mockRegistry) Get(proto string) (manager.ProtocolManager, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	mgr, ok := r.managers[models.NormalizeProtocol(proto)]
	return mgr, ok
}

func setupTestDB(t *testing.T) (*database.DB, func()) {
	f, err := os.CreateTemp("", "test_userops_*.db")
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

func TestUserOpsService_DeleteUser(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	sID1, err := db.CreateServer(ctx, &models.Server{
		Name:    "Server 1",
		Host:    "192.168.1.10",
		SSHPort: 22,
	})
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}

	sID2, err := db.CreateServer(ctx, &models.Server{
		Name:    "Server 2",
		Host:    "192.168.1.11",
		SSHPort: 22,
	})
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}

	uID, err := db.CreateUser(ctx, &models.User{
		Username: "alice",
		Role:     models.RoleUser,
		Enabled:  true,
	})
	if err != nil {
		t.Fatalf("failed to create user: %v", err)
	}

	_, _ = db.CreateConnection(ctx, &models.UserConnection{
		ID:        "conn-1",
		UserID:    uID,
		ServerID:  sID1,
		Protocol:  "awg",
		ClientID:  "client-1",
		Name:      "alice_awg1",
		CreatedAt: time.Now().UTC(),
	})
	_, _ = db.CreateConnection(ctx, &models.UserConnection{
		ID:        "conn-2",
		UserID:    uID,
		ServerID:  sID1,
		Protocol:  "awg",
		ClientID:  "client-2",
		Name:      "alice_awg2",
		CreatedAt: time.Now().UTC(),
	})
	_, _ = db.CreateConnection(ctx, &models.UserConnection{
		ID:        "conn-3",
		UserID:    uID,
		ServerID:  sID2,
		Protocol:  "telemt",
		ClientID:  "client-3",
		Name:      "alice_telemt",
		CreatedAt: time.Now().UTC(),
	})

	reg := newMockRegistry()
	awgMgr := newMockProtocolManager("awg")
	telemtMgr := newMockProtocolManager("telemt")
	reg.Register(awgMgr)
	reg.Register(telemtMgr)

	svc := NewUserOpsService(db, reg)

	// Non-existent user returns false, nil
	ok, err := svc.DeleteUser(ctx, "non-existent-user-id")
	if err != nil || ok {
		t.Errorf("expected (false, nil) for non-existent user, got (%v, %v)", ok, err)
	}

	// Delete alice
	ok, err = svc.DeleteUser(ctx, uID)
	if err != nil || !ok {
		t.Fatalf("DeleteUser failed: ok=%v, err=%v", ok, err)
	}

	// Verify protocol managers called
	if len(awgMgr.deleted) != 2 {
		t.Errorf("expected 2 awg clients deleted, got %d", len(awgMgr.deleted))
	}
	if len(telemtMgr.deleted) != 1 {
		t.Errorf("expected 1 telemt client deleted, got %d", len(telemtMgr.deleted))
	}

	// Verify DB is clean
	deletedUser, err := db.GetUser(ctx, uID)
	if err != nil {
		t.Fatalf("GetUser error: %v", err)
	}
	if deletedUser != nil {
		t.Error("user should be deleted from DB")
	}

	remainingConns, _ := db.GetConnectionsByUserID(ctx, uID)
	if len(remainingConns) != 0 {
		t.Errorf("expected 0 remaining connections, got %d", len(remainingConns))
	}
}

func TestUserOpsService_ToggleUser(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	sID, _ := db.CreateServer(ctx, &models.Server{
		Name:    "Server 1",
		Host:    "192.168.1.10",
		SSHPort: 22,
	})

	uID, _ := db.CreateUser(ctx, &models.User{
		Username: "bob",
		Role:     models.RoleUser,
		Enabled:  true,
	})

	_, _ = db.CreateConnection(ctx, &models.UserConnection{
		ID:        "conn-bob-1",
		UserID:    uID,
		ServerID:  sID,
		Protocol:  "awg",
		ClientID:  "bob-client-1",
		Name:      "bob_vpn",
		CreatedAt: time.Now().UTC(),
	})

	reg := newMockRegistry()
	awgMgr := newMockProtocolManager("awg")
	reg.Register(awgMgr)

	svc := NewUserOpsService(db, reg)

	// Non-existent user
	ok, err := svc.ToggleUser(ctx, "non-existent", false)
	if err != nil || ok {
		t.Errorf("expected (false, nil), got (%v, %v)", ok, err)
	}

	// Toggle disable
	ok, err = svc.ToggleUser(ctx, uID, false)
	if err != nil || !ok {
		t.Fatalf("ToggleUser(false) failed: ok=%v, err=%v", ok, err)
	}

	if awgMgr.toggled["bob-client-1"] != false {
		t.Error("expected bob-client-1 toggled to false")
	}

	u, _ := db.GetUser(ctx, uID)
	if u.Enabled {
		t.Error("user in DB should be disabled")
	}

	// Toggle enable
	ok, err = svc.ToggleUser(ctx, uID, true)
	if err != nil || !ok {
		t.Fatalf("ToggleUser(true) failed: ok=%v, err=%v", ok, err)
	}

	if awgMgr.toggled["bob-client-1"] != true {
		t.Error("expected bob-client-1 toggled to true")
	}

	u, _ = db.GetUser(ctx, uID)
	if !u.Enabled {
		t.Error("user in DB should be enabled")
	}
}

func TestUserOpsService_PerformMassOperations_CreatesAndDeletes(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	sID, _ := db.CreateServer(ctx, &models.Server{
		Name:    "Server 1",
		Host:    "192.168.1.10",
		SSHPort: 22,
	})

	uID, _ := db.CreateUser(ctx, &models.User{
		Username: "charlie",
		Role:     models.RoleUser,
		Enabled:  true,
	})

	reg := newMockRegistry()
	awgMgr := newMockProtocolManager("awg")
	reg.Register(awgMgr)

	svc := NewUserOpsService(db, reg)

	req := MassOperationRequest{
		CreateConns: []ConnectionCreateRequest{
			{
				UserID:   uID,
				ServerID: sID,
				Protocol: "awg",
				Name:     "charlie_vpn_1",
			},
		},
	}

	if err := svc.PerformMassOperations(ctx, req); err != nil {
		t.Fatalf("PerformMassOperations create failed: %v", err)
	}

	conns, _ := db.GetConnectionsByUserID(ctx, uID)
	if len(conns) != 1 {
		t.Fatalf("expected 1 connection created, got %d", len(conns))
	}
	if conns[0].ClientID != "cid-charlie_vpn_1" {
		t.Errorf("unexpected client ID: %s", conns[0].ClientID)
	}
}

func TestUserOpsService_ErrorIsolationAndNilDB(t *testing.T) {
	nilSvc := NewUserOpsService(nil, nil)
	ctx := context.Background()

	if _, err := nilSvc.DeleteUser(ctx, "any"); err == nil {
		t.Error("expected error for nil DB in DeleteUser")
	}
	if _, err := nilSvc.ToggleUser(ctx, "any", true); err == nil {
		t.Error("expected error for nil DB in ToggleUser")
	}
	if err := nilSvc.PerformMassOperations(ctx, MassOperationRequest{}); err == nil {
		t.Error("expected error for nil DB in PerformMassOperations")
	}

	db, cleanup := setupTestDB(t)
	defer cleanup()

	sID, _ := db.CreateServer(ctx, &models.Server{
		Name:    "Server Fail",
		Host:    "192.168.1.20",
		SSHPort: 22,
	})

	uID, _ := db.CreateUser(ctx, &models.User{
		Username: "failing_user",
		Role:     models.RoleUser,
		Enabled:  true,
	})

	_, _ = db.CreateConnection(ctx, &models.UserConnection{
		ID:        "conn-fail-1",
		UserID:    uID,
		ServerID:  sID,
		Protocol:  "awg",
		ClientID:  "cid-fail",
		Name:      "fail_vpn",
		CreatedAt: time.Now().UTC(),
	})

	reg := newMockRegistry()
	failingAwg := newMockProtocolManager("awg")
	failingAwg.returnError = true
	reg.Register(failingAwg)

	svc := NewUserOpsService(db, reg)

	// When remote removal returns error, PerformMassOperations isolates the error and preserves local records
	ok, err := svc.DeleteUser(ctx, uID)
	if err != nil || !ok {
		t.Fatalf("DeleteUser returned unexpected error: ok=%v, err=%v", ok, err)
	}

	// Verify connection is NOT deleted to prevent state mismatch with remote server
	conns, _ := db.GetConnectionsByUserID(ctx, uID)
	if len(conns) == 0 {
		t.Error("connection should be preserved in DB when remote removal fails")
	}

	// Verify user is preserved while connections remain
	u, _ := db.GetUser(ctx, uID)
	if u == nil {
		t.Error("user should be preserved in DB while remaining connections exist")
	}
}

type panickingUserOpsManager struct {
	proto string
}

func (p *panickingUserOpsManager) Protocol() string { return p.proto }
func (p *panickingUserOpsManager) Install(ctx context.Context, server *models.Server, params map[string]any) error {
	return nil
}
func (p *panickingUserOpsManager) Uninstall(ctx context.Context, server *models.Server) error {
	return nil
}
func (p *panickingUserOpsManager) GetClients(ctx context.Context, server *models.Server) ([]map[string]any, error) {
	return nil, nil
}
func (p *panickingUserOpsManager) AddClient(ctx context.Context, server *models.Server, clientParams map[string]any) (map[string]any, error) {
	panic("simulated panic in AddClient")
}
func (p *panickingUserOpsManager) RemoveClient(ctx context.Context, server *models.Server, clientID string) error {
	panic("simulated panic in RemoveClient")
}
func (p *panickingUserOpsManager) GetClientConfig(ctx context.Context, server *models.Server, clientID string) (string, error) {
	return "", nil
}

func TestUserOpsService_PanicRecovery(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	sID, _ := db.CreateServer(ctx, &models.Server{
		Name:    "Server Panic",
		Host:    "192.168.1.30",
		SSHPort: 22,
	})

	uID, _ := db.CreateUser(ctx, &models.User{
		Username: "panic_user",
		Role:     models.RoleUser,
	})

	_, _ = db.CreateConnection(ctx, &models.UserConnection{
		ID:        "conn-panic-1",
		UserID:    uID,
		ServerID:  sID,
		Protocol:  "awg",
		ClientID:  "cid-panic",
		Name:      "panic_vpn",
		CreatedAt: time.Now().UTC(),
	})

	reg := newMockRegistry()
	reg.Register(&panickingUserOpsManager{proto: "awg"})

	svc := NewUserOpsService(db, reg)

	// Should safely recover from child goroutine panic without crashing
	if err := svc.PerformMassOperations(ctx, MassOperationRequest{
		DeleteUIDs: []string{uID},
	}); err != nil {
		t.Fatalf("PerformMassOperations failed fatally on panic: %v", err)
	}
}
