package reconciliation

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/devops-igor/amnezia-web-ui-go/internal/database"
	"github.com/devops-igor/amnezia-web-ui-go/internal/manager"
	"github.com/devops-igor/amnezia-web-ui-go/internal/models"
)

type mockStatusProtocolManager struct {
	proto           string
	containerExists bool
	returnError     bool
}

func (m *mockStatusProtocolManager) Protocol() string {
	return m.proto
}

func (m *mockStatusProtocolManager) Install(ctx context.Context, server *models.Server, params map[string]any) error {
	return nil
}

func (m *mockStatusProtocolManager) Uninstall(ctx context.Context, server *models.Server) error {
	return nil
}

func (m *mockStatusProtocolManager) GetClients(ctx context.Context, server *models.Server) ([]map[string]any, error) {
	return nil, nil
}

func (m *mockStatusProtocolManager) AddClient(ctx context.Context, server *models.Server, clientParams map[string]any) (map[string]any, error) {
	return nil, nil
}

func (m *mockStatusProtocolManager) RemoveClient(ctx context.Context, server *models.Server, clientID string) error {
	return nil
}

func (m *mockStatusProtocolManager) GetClientConfig(ctx context.Context, server *models.Server, clientID string) (string, error) {
	return "", nil
}

func (m *mockStatusProtocolManager) GetServerStatus(ctx context.Context, server *models.Server) (map[string]any, error) {
	if m.returnError {
		return nil, errors.New("remote check error")
	}
	return map[string]any{
		"container_exists": m.containerExists,
	}, nil
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
	f, err := os.CreateTemp("", "test_reconciliation_*.db")
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

func TestReconciler_Phase1_DBOnlyCleanup(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	sID, _ := db.CreateServer(ctx, &models.Server{
		Name:    "Server 1",
		Host:    "10.0.0.1",
		SSHPort: 22,
		Protocols: map[string]any{
			"awg": map[string]any{"port": 55424},
		},
	})

	uID, _ := db.CreateUser(ctx, &models.User{
		Username: "user1",
		Role:     models.RoleUser,
	})

	// AWG connection (valid)
	_, _ = db.CreateConnection(ctx, &models.UserConnection{
		ID:        "conn-awg",
		UserID:    uID,
		ServerID:  sID,
		Protocol:  "awg",
		ClientID:  "cid-awg",
		Name:      "u1_awg",
		CreatedAt: time.Now().UTC(),
	})

	// TeleMT connection (orphaned because telemt is not in server.Protocols)
	_, _ = db.CreateConnection(ctx, &models.UserConnection{
		ID:        "conn-telemt",
		UserID:    uID,
		ServerID:  sID,
		Protocol:  "telemt",
		ClientID:  "cid-telemt",
		Name:      "u1_telemt",
		CreatedAt: time.Now().UTC(),
	})

	r := New(db, nil)

	if err := r.CleanupStaleProtocols(ctx); err != nil {
		t.Fatalf("CleanupStaleProtocols failed: %v", err)
	}

	conns, _ := db.GetConnectionsByServerID(ctx, sID)
	if len(conns) != 1 {
		t.Fatalf("expected 1 connection remaining, got %d", len(conns))
	}
	if conns[0].Protocol != "awg" {
		t.Errorf("expected remaining connection protocol awg, got %s", conns[0].Protocol)
	}
}

func TestReconciler_Phase2_SSHBasedCleanup(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	sID, _ := db.CreateServer(ctx, &models.Server{
		Name:    "Server 1",
		Host:    "10.0.0.1",
		SSHPort: 22,
		Protocols: map[string]any{
			"awg":    map[string]any{"port": 55424},
			"telemt": map[string]any{"port": 443},
		},
	})

	uID, _ := db.CreateUser(ctx, &models.User{
		Username: "user2",
		Role:     models.RoleUser,
	})

	_, _ = db.CreateConnection(ctx, &models.UserConnection{
		ID:        "conn-awg",
		UserID:    uID,
		ServerID:  sID,
		Protocol:  "awg",
		ClientID:  "cid-awg",
		Name:      "u2_awg",
		CreatedAt: time.Now().UTC(),
	})
	_, _ = db.CreateConnection(ctx, &models.UserConnection{
		ID:        "conn-telemt",
		UserID:    uID,
		ServerID:  sID,
		Protocol:  "telemt",
		ClientID:  "cid-telemt",
		Name:      "u2_telemt",
		CreatedAt: time.Now().UTC(),
	})

	reg := newMockRegistry()
	// AWG exists
	reg.Register(&mockStatusProtocolManager{proto: "awg", containerExists: true})
	// TeleMT container is missing on host!
	reg.Register(&mockStatusProtocolManager{proto: "telemt", containerExists: false})

	r := New(db, reg)

	if err := r.CleanupStaleProtocols(ctx); err != nil {
		t.Fatalf("CleanupStaleProtocols failed: %v", err)
	}

	// TeleMT connection should be removed
	conns, _ := db.GetConnectionsByServerID(ctx, sID)
	if len(conns) != 1 {
		t.Fatalf("expected 1 connection remaining, got %d", len(conns))
	}
	if conns[0].Protocol != "awg" {
		t.Errorf("expected awg connection, got %s", conns[0].Protocol)
	}

	// Server.Protocols should no longer contain telemt
	srv, _ := db.GetServer(ctx, sID)
	if _, ok := srv.Protocols["telemt"]; ok {
		t.Error("telemt should have been removed from server.Protocols")
	}
	if _, ok := srv.Protocols["awg"]; !ok {
		t.Error("awg should still be in server.Protocols")
	}
}

func TestReconciler_ErrorIsolationAndEdgeCases(t *testing.T) {
	ctx := context.Background()

	// Nil DB returns error
	nilR := New(nil, nil)
	if err := nilR.CleanupStaleProtocols(ctx); err == nil {
		t.Error("expected error for nil DB")
	}

	db, cleanup := setupTestDB(t)
	defer cleanup()

	sID1, _ := db.CreateServer(ctx, &models.Server{
		Name:    "Server 1 (Fails)",
		Host:    "10.0.0.1",
		SSHPort: 22,
		Protocols: map[string]any{
			"awg": map[string]any{"port": 55424},
		},
	})

	sID2, _ := db.CreateServer(ctx, &models.Server{
		Name:    "Server 2 (OK)",
		Host:    "10.0.0.2",
		SSHPort: 22,
		Protocols: map[string]any{
			"awg": map[string]any{"port": 55424},
		},
	})

	uID, _ := db.CreateUser(ctx, &models.User{
		Username: "user3",
		Role:     models.RoleUser,
	})

	_, _ = db.CreateConnection(ctx, &models.UserConnection{
		ID:        "conn-s1",
		UserID:    uID,
		ServerID:  sID1,
		Protocol:  "awg",
		ClientID:  "cid-1",
		Name:      "u3_s1",
		CreatedAt: time.Now().UTC(),
	})
	_, _ = db.CreateConnection(ctx, &models.UserConnection{
		ID:        "conn-s2",
		UserID:    uID,
		ServerID:  sID2,
		Protocol:  "awg",
		ClientID:  "cid-2",
		Name:      "u3_s2",
		CreatedAt: time.Now().UTC(),
	})

	reg := newMockRegistry()
	// Manager that errors
	reg.Register(&mockStatusProtocolManager{proto: "awg", returnError: true})

	r := New(db, reg)

	// Should not crash or return fatal error
	if err := r.CleanupStaleProtocols(ctx); err != nil {
		t.Errorf("unexpected fatal error during reconciliation with failing server: %v", err)
	}

	// Connections must NOT be deleted when remote check errors
	connsS1, _ := db.GetConnectionsByServerID(ctx, sID1)
	if len(connsS1) != 1 {
		t.Errorf("expected connection on sID1 to be preserved on error, got %d", len(connsS1))
	}
	srv1, _ := db.GetServer(ctx, sID1)
	if _, ok := srv1.Protocols["awg"]; !ok {
		t.Errorf("expected awg protocol to be preserved on sID1 on error")
	}
}

func TestReconciler_LegacyAWG2Preserved(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	sID, _ := db.CreateServer(ctx, &models.Server{
		Name:    "Legacy AWG2 Server",
		Host:    "10.0.0.5",
		SSHPort: 22,
		Protocols: map[string]any{
			"awg": map[string]any{"port": 55424, "container": "amnezia-awg2"},
		},
	})

	uID, _ := db.CreateUser(ctx, &models.User{
		Username: "legacy_user",
		Role:     models.RoleUser,
	})

	_, _ = db.CreateConnection(ctx, &models.UserConnection{
		ID:        "conn-awg2",
		UserID:    uID,
		ServerID:  sID,
		Protocol:  "awg",
		ClientID:  "cid-awg2",
		Name:      "u_awg2",
		CreatedAt: time.Now().UTC(),
	})

	reg := newMockRegistry()
	// Manager reports container exists (as amnezia-awg2)
	reg.Register(&mockStatusProtocolManager{proto: "awg", containerExists: true})

	r := New(db, reg)
	if err := r.CleanupStaleProtocols(ctx); err != nil {
		t.Fatalf("CleanupStaleProtocols failed: %v", err)
	}

	conns, _ := db.GetConnectionsByServerID(ctx, sID)
	if len(conns) != 1 {
		t.Fatalf("expected connection to be preserved for amnezia-awg2 server, got %d", len(conns))
	}
	srv, _ := db.GetServer(ctx, sID)
	if _, ok := srv.Protocols["awg"]; !ok {
		t.Errorf("expected awg protocol to be preserved for amnezia-awg2 server")
	}
}

type mockNonCheckerManager struct {
	proto string
}

func (m *mockNonCheckerManager) Protocol() string { return m.proto }
func (m *mockNonCheckerManager) Install(ctx context.Context, server *models.Server, params map[string]any) error {
	return nil
}
func (m *mockNonCheckerManager) Uninstall(ctx context.Context, server *models.Server) error {
	return nil
}
func (m *mockNonCheckerManager) GetClients(ctx context.Context, server *models.Server) ([]map[string]any, error) {
	return nil, nil
}
func (m *mockNonCheckerManager) AddClient(ctx context.Context, server *models.Server, clientParams map[string]any) (map[string]any, error) {
	return nil, nil
}
func (m *mockNonCheckerManager) RemoveClient(ctx context.Context, server *models.Server, clientID string) error {
	return nil
}
func (m *mockNonCheckerManager) GetClientConfig(ctx context.Context, server *models.Server, clientID string) (string, error) {
	return "", nil
}

type mockStatusErrorManager struct {
	proto string
}

func (m *mockStatusErrorManager) Protocol() string { return m.proto }
func (m *mockStatusErrorManager) Install(ctx context.Context, server *models.Server, params map[string]any) error {
	return nil
}
func (m *mockStatusErrorManager) Uninstall(ctx context.Context, server *models.Server) error {
	return nil
}
func (m *mockStatusErrorManager) GetClients(ctx context.Context, server *models.Server) ([]map[string]any, error) {
	return nil, nil
}
func (m *mockStatusErrorManager) AddClient(ctx context.Context, server *models.Server, clientParams map[string]any) (map[string]any, error) {
	return nil, nil
}
func (m *mockStatusErrorManager) RemoveClient(ctx context.Context, server *models.Server, clientID string) error {
	return nil
}
func (m *mockStatusErrorManager) GetClientConfig(ctx context.Context, server *models.Server, clientID string) (string, error) {
	return "", nil
}
func (m *mockStatusErrorManager) GetServerStatus(ctx context.Context, server *models.Server) (map[string]any, error) {
	return map[string]any{
		"error":            "daemon crashed",
		"container_exists": false,
	}, nil
}

func TestReconciler_AdditionalBranches(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// 1. Server with empty protocols
	sIDEmpty, _ := db.CreateServer(ctx, &models.Server{
		Name:      "Empty Server",
		Host:      "10.0.0.10",
		SSHPort:   22,
		Protocols: map[string]any{},
	})

	// 2. Server with non-checker protocol and unregistered protocol and status error
	sIDMulti, _ := db.CreateServer(ctx, &models.Server{
		Name:    "Multi Server",
		Host:    "10.0.0.11",
		SSHPort: 22,
		Protocols: map[string]any{
			"unregistered": map[string]any{"port": 1111},
			"nonchecker":   map[string]any{"port": 2222},
			"statuserror":  map[string]any{"port": 3333},
		},
	})

	reg := newMockRegistry()
	reg.Register(&mockNonCheckerManager{proto: "nonchecker"})
	reg.Register(&mockStatusErrorManager{proto: "statuserror"})

	r := New(db, reg)
	if err := r.CleanupStaleProtocols(ctx); err != nil {
		t.Fatalf("CleanupStaleProtocols failed: %v", err)
	}

	srv, _ := db.GetServer(ctx, sIDMulti)
	if len(srv.Protocols) != 3 {
		t.Errorf("expected all 3 protocols preserved, got %d", len(srv.Protocols))
	}
	_ = sIDEmpty
}

func TestReconciler_DNSTeleMTErrorsPreserveProtocols(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	sID, _ := db.CreateServer(ctx, &models.Server{
		Name:    "DNS and TeleMT Server",
		Host:    "10.0.0.50",
		SSHPort: 22,
		Protocols: map[string]any{
			"dns":    map[string]any{"port": 53},
			"telemt": map[string]any{"port": 443},
		},
	})

	uID, _ := db.CreateUser(ctx, &models.User{
		Username: "dual_user",
		Role:     models.RoleUser,
	})

	_, _ = db.CreateConnection(ctx, &models.UserConnection{
		ID:        "conn-dns",
		UserID:    uID,
		ServerID:  sID,
		Protocol:  "dns",
		ClientID:  "cid-dns",
		Name:      "u_dns",
		CreatedAt: time.Now().UTC(),
	})
	_, _ = db.CreateConnection(ctx, &models.UserConnection{
		ID:        "conn-telemt",
		UserID:    uID,
		ServerID:  sID,
		Protocol:  "telemt",
		ClientID:  "cid-telemt",
		Name:      "u_telemt",
		CreatedAt: time.Now().UTC(),
	})

	reg := newMockRegistry()
	// Both managers report transient errors (e.g. docker daemon stopped / CLI error)
	reg.Register(&mockStatusProtocolManager{proto: "dns", returnError: true})
	reg.Register(&mockStatusProtocolManager{proto: "telemt", returnError: true})

	r := New(db, reg)
	if err := r.CleanupStaleProtocols(ctx); err != nil {
		t.Fatalf("CleanupStaleProtocols failed: %v", err)
	}

	// Connections must be preserved
	conns, _ := db.GetConnectionsByServerID(ctx, sID)
	if len(conns) != 2 {
		t.Fatalf("expected 2 connections preserved for DNS and TeleMT, got %d", len(conns))
	}

	// Protocols must be preserved
	srv, _ := db.GetServer(ctx, sID)
	if _, ok := srv.Protocols["dns"]; !ok {
		t.Errorf("expected dns protocol to be preserved on error")
	}
	if _, ok := srv.Protocols["telemt"]; !ok {
		t.Errorf("expected telemt protocol to be preserved on error")
	}
}
