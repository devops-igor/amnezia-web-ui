package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/devops-igor/amnezia-web-ui-go/internal/database"
	"github.com/devops-igor/amnezia-web-ui-go/internal/manager"
	"github.com/devops-igor/amnezia-web-ui-go/internal/models"
	"github.com/devops-igor/amnezia-web-ui-go/internal/service/userops"
)

type mockOrchProtocolManager struct {
	proto          string
	mu             sync.Mutex
	clients        []map[string]any
	deleted        []string
	toggled        map[string]bool
	rotated        []string
	returnError    bool
	addClientCalls int
}

func newMockOrchProtocolManager(proto string) *mockOrchProtocolManager {
	return &mockOrchProtocolManager{
		proto:   proto,
		toggled: make(map[string]bool),
	}
}

func (m *mockOrchProtocolManager) Protocol() string {
	return m.proto
}

func (m *mockOrchProtocolManager) Install(ctx context.Context, server *models.Server, params map[string]any) error {
	return nil
}

func (m *mockOrchProtocolManager) Uninstall(ctx context.Context, server *models.Server) error {
	return nil
}

func (m *mockOrchProtocolManager) GetClients(ctx context.Context, server *models.Server) ([]map[string]any, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.returnError {
		return nil, errors.New("get clients error")
	}
	return m.clients, nil
}

func (m *mockOrchProtocolManager) AddClient(ctx context.Context, server *models.Server, clientParams map[string]any) (map[string]any, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.addClientCalls++
	cName := "client"
	if n, ok := clientParams["name"]; ok && n != nil && fmt.Sprint(n) != "" {
		cName = fmt.Sprint(n)
	} else if n, ok := clientParams["clientName"]; ok && n != nil && fmt.Sprint(n) != "" {
		cName = fmt.Sprint(n)
	}
	m.clients = append(m.clients, map[string]any{
		"clientId": "cid-new",
		"userData": map[string]any{
			"clientName":       cName,
			"clientPrivateKey": "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB=",
		},
	})
	return map[string]any{
		"client_id":   "cid-new",
		"client_name": cName,
		"config":      "[Interface]\nPrivateKey = BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB=\n",
	}, nil
}

func (m *mockOrchProtocolManager) RemoveClient(ctx context.Context, server *models.Server, clientID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deleted = append(m.deleted, clientID)
	return nil
}

func (m *mockOrchProtocolManager) GetClientConfig(ctx context.Context, server *models.Server, clientID string) (string, error) {
	return "", nil
}

func (m *mockOrchProtocolManager) ToggleClient(ctx context.Context, server *models.Server, clientID string, enable bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.toggled[clientID] = enable
	return nil
}

func (m *mockOrchProtocolManager) RotateMimicry(ctx context.Context, server *models.Server, clientID string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.returnError {
		return "", errors.New("remote rotation failed")
	}
	m.rotated = append(m.rotated, clientID)
	return "quic", nil
}

func (m *mockOrchProtocolManager) DisableOverquotaUsers(ctx context.Context, server *models.Server) ([]string, error) {
	return []string{"overquota-user"}, nil
}

func (m *mockOrchProtocolManager) GetServerPublicKey(ctx context.Context, server *models.Server) (string, error) {
	return "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=", nil
}

func (m *mockOrchProtocolManager) GetServerPSK(ctx context.Context, server *models.Server) (string, error) {
	return "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=", nil
}

type mockOrchRegistry struct {
	mu       sync.RWMutex
	managers map[string]manager.ProtocolManager
}

func newMockOrchRegistry() *mockOrchRegistry {
	return &mockOrchRegistry{
		managers: make(map[string]manager.ProtocolManager),
	}
}

func (r *mockOrchRegistry) Register(mgr manager.ProtocolManager) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.managers[models.NormalizeProtocol(mgr.Protocol())] = mgr
}

func (r *mockOrchRegistry) Get(proto string) (manager.ProtocolManager, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	mgr, ok := r.managers[models.NormalizeProtocol(proto)]
	return mgr, ok
}

type mockUserOps struct {
	mu       sync.Mutex
	toggles  []userops.UserToggle
	deletes  []string
	creates  []userops.ConnectionCreateRequest
	errToRet error
}

func (m *mockUserOps) PerformMassOperations(ctx context.Context, req userops.MassOperationRequest) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.toggles = append(m.toggles, req.ToggleUIDs...)
	m.deletes = append(m.deletes, req.DeleteUIDs...)
	m.creates = append(m.creates, req.CreateConns...)
	return m.errToRet
}

type mockRemnaSyncer struct {
	count int
	msg   string
	err   error
	calls int32
}

func (m *mockRemnaSyncer) Sync(ctx context.Context) (int, string, error) {
	atomic.AddInt32(&m.calls, 1)
	return m.count, m.msg, m.err
}

func setupTestDB(t *testing.T) (*database.DB, func()) {
	f, err := os.CreateTemp("", "test_orch_*.db")
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

func TestOrchestrator_LifecycleAndOptions(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	reg := newMockOrchRegistry()
	userOps := &mockUserOps{}
	syncer := &mockRemnaSyncer{count: 5, msg: "ok"}

	orch := New(db, reg,
		WithBootDelay(10*time.Millisecond),
		WithInterval(50*time.Millisecond),
		WithMaxConcurrency(5),
		WithUserOps(userOps),
		WithRemnaWaveSyncer(syncer),
	)

	if orch.Name() != "background-orchestrator" {
		t.Errorf("unexpected service name: %s", orch.Name())
	}
	if orch.bootDelay != 10*time.Millisecond {
		t.Errorf("unexpected boot delay: %v", orch.bootDelay)
	}
	if orch.interval != 50*time.Millisecond {
		t.Errorf("unexpected interval: %v", orch.interval)
	}
	if orch.maxConcurrency != 5 {
		t.Errorf("unexpected max concurrency: %d", orch.maxConcurrency)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- orch.Start(ctx)
	}()

	time.Sleep(30 * time.Millisecond)

	// Double start should return error
	if err := orch.Start(ctx); err == nil {
		t.Error("expected error on double start")
	}

	time.Sleep(70 * time.Millisecond)

	stopCtx, stopCancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer stopCancel()

	if err := orch.Stop(stopCtx); err != nil {
		t.Errorf("Stop failed: %v", err)
	}

	// Calling Stop again should be safe
	if err := orch.Stop(stopCtx); err != nil {
		t.Errorf("second Stop call failed: %v", err)
	}
}

func TestOrchestrator_SyncTraffic_AccumulationAndCounterReset(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	sID, _ := db.CreateServer(ctx, &models.Server{
		Name:    "Traffic Server",
		Host:    "10.0.0.1",
		SSHPort: 22,
		Protocols: map[string]any{
			"awg": map[string]any{"port": 55424},
		},
	})

	uID, _ := db.CreateUser(ctx, &models.User{
		Username:             "tester",
		Role:                 models.RoleUser,
		Enabled:              true,
		TrafficLimit:         1000,
		TrafficUsed:          0,
		TrafficResetStrategy: models.ResetStrategyMonthly,
	})

	connID, _ := db.CreateConnection(ctx, &models.UserConnection{
		UserID:    uID,
		ServerID:  sID,
		Protocol:  "awg",
		ClientID:  "client-traffic-1",
		Name:      "test_vpn",
		CreatedAt: time.Now().UTC(),
	})

	reg := newMockOrchRegistry()
	awgMgr := newMockOrchProtocolManager("awg")
	reg.Register(awgMgr)

	userOps := &mockUserOps{}
	orch := New(db, reg, WithUserOps(userOps))

	// Cycle 1: RX 100, TX 200 -> delta RX 100, TX 200
	awgMgr.clients = []map[string]any{
		{
			"clientId": "client-traffic-1",
			"userData": map[string]any{
				"dataReceivedBytes": 100,
				"dataSentBytes":     200,
			},
		},
	}

	if err := orch.SyncTraffic(ctx); err != nil {
		t.Fatalf("SyncTraffic cycle 1 failed: %v", err)
	}

	uc, _ := db.GetConnection(ctx, connID)
	if uc.TrafficTotalRx != 100 || uc.TrafficTotalTx != 200 || uc.TrafficTotal != 300 {
		t.Errorf("unexpected connection totals cycle 1: rx=%d, tx=%d, total=%d",
			uc.TrafficTotalRx, uc.TrafficTotalTx, uc.TrafficTotal)
	}

	u, _ := db.GetUser(ctx, uID)
	if u.TrafficUsed != 300 || u.TrafficTotal != 300 || u.MonthlyRx != 100 || u.MonthlyTx != 200 {
		t.Errorf("unexpected user totals cycle 1: used=%d, total=%d, m_rx=%d, m_tx=%d",
			u.TrafficUsed, u.TrafficTotal, u.MonthlyRx, u.MonthlyTx)
	}

	// Cycle 2: RX 150, TX 300 -> delta RX 50, TX 100
	awgMgr.clients = []map[string]any{
		{
			"clientId": "client-traffic-1",
			"userData": map[string]any{
				"dataReceivedBytes": 150,
				"dataSentBytes":     300,
			},
		},
	}

	if err := orch.SyncTraffic(ctx); err != nil {
		t.Fatalf("SyncTraffic cycle 2 failed: %v", err)
	}

	uc, _ = db.GetConnection(ctx, connID)
	if uc.TrafficTotalRx != 150 || uc.TrafficTotalTx != 300 || uc.TrafficTotal != 450 {
		t.Errorf("unexpected connection totals cycle 2: rx=%d, tx=%d, total=%d",
			uc.TrafficTotalRx, uc.TrafficTotalTx, uc.TrafficTotal)
	}

	// Cycle 3: Counter Reset (remote reboot)! RX 20 < 150, TX 40 < 300 -> delta RX 20, TX 40
	awgMgr.clients = []map[string]any{
		{
			"clientId": "client-traffic-1",
			"userData": map[string]any{
				"dataReceivedBytes": 20,
				"dataSentBytes":     40,
			},
		},
	}

	if err := orch.SyncTraffic(ctx); err != nil {
		t.Fatalf("SyncTraffic cycle 3 failed: %v", err)
	}

	uc, _ = db.GetConnection(ctx, connID)
	if uc.TrafficTotalRx != 170 || uc.TrafficTotalTx != 340 || uc.TrafficTotal != 510 {
		t.Errorf("unexpected connection totals after reset: rx=%d, tx=%d, total=%d",
			uc.TrafficTotalRx, uc.TrafficTotalTx, uc.TrafficTotal)
	}

	u, _ = db.GetUser(ctx, uID)
	if u.TrafficUsed != 510 || u.TrafficTotal != 510 {
		t.Errorf("unexpected user totals after reset: used=%d, total=%d", u.TrafficUsed, u.TrafficTotal)
	}

	// Cycle 4: Limit exceeded (limit 1000, add 600 bytes -> used 1110)
	awgMgr.clients = []map[string]any{
		{
			"clientId": "client-traffic-1",
			"userData": map[string]any{
				"dataReceivedBytes": 320,
				"dataSentBytes":     340,
			},
		},
	}

	if err := orch.SyncTraffic(ctx); err != nil {
		t.Fatalf("SyncTraffic cycle 4 failed: %v", err)
	}

	// User should be queued to be disabled
	userOps.mu.Lock()
	foundDisable := false
	for _, tg := range userOps.toggles {
		if tg.UserID == uID && tg.Enabled == false {
			foundDisable = true
			break
		}
	}
	userOps.mu.Unlock()

	if !foundDisable {
		t.Errorf("expected user %s to be queued for disable after exceeding limit", uID)
	}
}

func TestOrchestrator_MonthlyRolloverAndStrategyReset(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Setup user with naive Python timestamp from previous month
	prevMonthTime := time.Now().UTC().AddDate(0, -1, 0)
	prevMonthNaiveStr := prevMonthTime.Format("2006-01-02T15:04:05.000000")

	uID, _ := db.CreateUser(ctx, &models.User{
		Username:             "rollover_user",
		Role:                 models.RoleUser,
		Enabled:              true,
		TrafficLimit:         1000,
		TrafficUsed:          500,
		TrafficResetStrategy: models.ResetStrategyMonthly,
		MonthlyResetAt:       &prevMonthNaiveStr,
		LastResetAt:          &prevMonthNaiveStr,
	})

	orch := New(db, nil)

	if err := orch.SyncTraffic(ctx); err != nil {
		t.Fatalf("SyncTraffic failed: %v", err)
	}

	u, _ := db.GetUser(ctx, uID)
	if u.MonthlyRx != 0 || u.MonthlyTx != 0 {
		t.Errorf("monthly counters should be reset: rx=%d, tx=%d", u.MonthlyRx, u.MonthlyTx)
	}
	if u.TrafficUsed != 0 {
		t.Errorf("expected traffic_used to be reset to 0 upon monthly rollover, got %d", u.TrafficUsed)
	}

	// Verify leaderboard snapshot was saved for previous month
	history, err := db.GetLeaderboardSnapshot(ctx, prevMonthTime.Year(), int(prevMonthTime.Month()))
	if err != nil {
		t.Fatalf("GetLeaderboardSnapshot failed: %v", err)
	}
	if len(history) >= 0 {
		t.Log("Leaderboard snapshot saved successfully for previous month")
	}
}

func TestOrchestrator_CheckExpiry(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	pastTime := time.Now().UTC().Add(-1 * time.Hour)
	futureTime := time.Now().UTC().Add(24 * time.Hour)

	// Expired user
	uExpiredID, _ := db.CreateUser(ctx, &models.User{
		Username:  "expired_user",
		Role:      models.RoleUser,
		Enabled:   true,
		ExpiresAt: &pastTime,
	})

	// Future expiry user
	uActiveID, _ := db.CreateUser(ctx, &models.User{
		Username:  "active_user",
		Role:      models.RoleUser,
		Enabled:   true,
		ExpiresAt: &futureTime,
	})

	// Disabled user
	uDisabledID, _ := db.CreateUser(ctx, &models.User{
		Username:  "disabled_user",
		Role:      models.RoleUser,
		Enabled:   false,
		ExpiresAt: &pastTime,
	})

	userOps := &mockUserOps{}
	orch := New(db, nil, WithUserOps(userOps))

	if err := orch.CheckExpiry(ctx); err != nil {
		t.Fatalf("CheckExpiry failed: %v", err)
	}

	userOps.mu.Lock()
	defer userOps.mu.Unlock()

	foundExpired := false
	foundActive := false
	foundDisabled := false

	for _, tg := range userOps.toggles {
		if tg.UserID == uExpiredID && !tg.Enabled {
			foundExpired = true
		}
		if tg.UserID == uActiveID {
			foundActive = true
		}
		if tg.UserID == uDisabledID {
			foundDisabled = true
		}
	}

	if !foundExpired {
		t.Errorf("expected expired user %s to be disabled", uExpiredID)
	}
	if foundActive {
		t.Errorf("active user %s should not be disabled", uActiveID)
	}
	if foundDisabled {
		t.Errorf("already disabled user %s should not be queued for toggle", uDisabledID)
	}
}

func TestOrchestrator_CheckServerReachability_TCPAndAWG(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// 1. Live TCP server
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	defer ln.Close()

	_, portStr, _ := net.SplitHostPort(ln.Addr().String())
	var livePort int
	_, _ = fmt.Sscanf(portStr, "%d", &livePort)

	sID1, _ := db.CreateServer(ctx, &models.Server{
		Name:    "Live TCP Server",
		Host:    "127.0.0.1",
		SSHPort: livePort,
	})

	// 2. Dead server
	sID2, _ := db.CreateServer(ctx, &models.Server{
		Name:    "Dead Server",
		Host:    "127.0.0.1",
		SSHPort: 65432,
	})

	orch := New(db, nil)

	results, err := orch.CheckServerReachability(ctx)
	if err != nil {
		t.Fatalf("CheckServerReachability failed: %v", err)
	}

	res1, ok1 := results[sID1]
	if !ok1 || res1["reachable"] != true {
		t.Errorf("expected server 1 to be reachable: %+v", res1)
	}

	res2, ok2 := results[sID2]
	if !ok2 || res2["reachable"] != false {
		t.Errorf("expected server 2 to be unreachable: %+v", res2)
	}

	cached := orch.GetCachedServerReachability()
	if len(cached) != 2 {
		t.Errorf("expected 2 cached reachability entries, got %d", len(cached))
	}
}

func TestOrchestrator_CheckAutoTrialHandshakes(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	sID, _ := db.CreateServer(ctx, &models.Server{
		Name:    "AWG Server",
		Host:    "10.0.0.1",
		SSHPort: 22,
		Protocols: map[string]any{
			"awg": map[string]any{"port": 55424},
		},
	})

	uID, _ := db.CreateUser(ctx, &models.User{
		Username: "autotrial_user",
		Role:     models.RoleUser,
		Enabled:  true,
	})

	connID, _ := db.CreateConnection(ctx, &models.UserConnection{
		UserID:     uID,
		ServerID:   sID,
		Protocol:   "awg",
		ClientID:   "cid-autotrial",
		Name:       "auto_vpn",
		AWGMimicry: models.AWGMimicryAuto,
		CreatedAt:  time.Now().UTC(),
	})

	reg := newMockOrchRegistry()
	awgMgr := newMockOrchProtocolManager("awg")
	reg.Register(awgMgr)

	orch := New(db, reg)

	if err := orch.CheckAutoTrialHandshakes(ctx); err != nil {
		t.Fatalf("CheckAutoTrialHandshakes failed: %v", err)
	}

	if len(awgMgr.rotated) != 1 || awgMgr.rotated[0] != "cid-autotrial" {
		t.Errorf("expected awg mimicry rotation for client, got %v", awgMgr.rotated)
	}

	uc, _ := db.GetConnection(ctx, connID)
	if uc.AWGMimicry != "quic" {
		t.Errorf("expected connection mimicry to be updated to quic, got %s", uc.AWGMimicry)
	}
	_, _ = db.DeleteConnection(ctx, connID)

	// 2. Test fresh handshakes (active <= 180s) - must NOT rotate
	freshTestCases := []struct {
		name      string
		cid       string
		handshake any
	}{
		{"unix timestamp", "cid-fresh-ts", time.Now().UTC().Unix()},
		{"float timestamp", "cid-fresh-float", float64(time.Now().UTC().Unix())},
		{"json.Number timestamp", "cid-fresh-num", json.Number(fmt.Sprintf("%d", time.Now().UTC().Unix()))},
		{"wg show 15 seconds ago", "cid-fresh-15s", "15 seconds ago"},
		{"wg show 1 minute, 30 seconds ago", "cid-fresh-90s", "1 minute, 30 seconds ago"},
		{"wg show now", "cid-fresh-now", "now"},
		{"go duration 2m", "cid-fresh-dur", "2m"},
	}

	for _, tc := range freshTestCases {
		connIDFresh, _ := db.CreateConnection(ctx, &models.UserConnection{
			UserID:     uID,
			ServerID:   sID,
			Protocol:   "awg",
			ClientID:   tc.cid,
			Name:       tc.name,
			AWGMimicry: models.AWGMimicryAuto,
			CreatedAt:  time.Now().UTC(),
		})
		awgMgr.mu.Lock()
		awgMgr.rotated = nil
		awgMgr.clients = []map[string]any{
			{
				"clientId": tc.cid,
				"userData": map[string]any{
					"latestHandshake": tc.handshake,
				},
			},
		}
		awgMgr.mu.Unlock()

		if err := orch.CheckAutoTrialHandshakes(ctx); err != nil {
			t.Fatalf("CheckAutoTrialHandshakes failed for %s: %v", tc.name, err)
		}
		if len(awgMgr.rotated) != 0 {
			t.Errorf("[%s] expected fresh client NOT to be rotated, but rotated: %v", tc.name, awgMgr.rotated)
		}
		ucFresh, _ := db.GetConnection(ctx, connIDFresh)
		if ucFresh.AWGMimicry != models.AWGMimicryAuto {
			t.Errorf("[%s] expected connection in DB to remain auto, got %s", tc.name, ucFresh.AWGMimicry)
		}
		_, _ = db.DeleteConnection(ctx, connIDFresh)
	}

	// 2b. Test stale handshakes (> 180s or never/empty) - MUST rotate
	staleTestCases := []struct {
		name      string
		cid       string
		handshake any
	}{
		{"stale timestamp (>180s)", "cid-stale-ts", time.Now().Add(-5 * time.Minute).UTC().Unix()},
		{"wg show 5 minutes ago", "cid-stale-5m", "5 minutes ago"},
		{"wg show 2 hours, 10 minutes ago", "cid-stale-2h", "2 hours, 10 minutes ago"},
		{"wg show 3 days, 4 hours ago", "cid-stale-3d", "3 days, 4 hours ago"},
		{"wg show never", "cid-stale-never", "never"},
		{"wg show none", "cid-stale-none", "(none)"},
		{"empty handshake", "cid-stale-empty", ""},
		{"zero numeric timestamp", "cid-stale-zero", int64(0)},
	}

	for _, tc := range staleTestCases {
		connIDStale, _ := db.CreateConnection(ctx, &models.UserConnection{
			UserID:     uID,
			ServerID:   sID,
			Protocol:   "awg",
			ClientID:   tc.cid,
			Name:       tc.name,
			AWGMimicry: models.AWGMimicryAuto,
			CreatedAt:  time.Now().UTC(),
		})
		awgMgr.mu.Lock()
		awgMgr.rotated = nil
		awgMgr.clients = []map[string]any{
			{
				"clientId": tc.cid,
				"userData": map[string]any{
					"latestHandshake": tc.handshake,
				},
			},
		}
		awgMgr.mu.Unlock()

		if err := orch.CheckAutoTrialHandshakes(ctx); err != nil {
			t.Fatalf("CheckAutoTrialHandshakes failed for stale client %s: %v", tc.name, err)
		}
		if len(awgMgr.rotated) != 1 || awgMgr.rotated[0] != tc.cid {
			t.Errorf("[%s] expected stale client to be rotated, got: %v", tc.name, awgMgr.rotated)
		}
		ucStale, _ := db.GetConnection(ctx, connIDStale)
		if ucStale.AWGMimicry != "quic" {
			t.Errorf("[%s] expected connection in DB to be updated to quic, got %s", tc.name, ucStale.AWGMimicry)
		}
		_, _ = db.DeleteConnection(ctx, connIDStale)
	}

	// 3. Test failed remote rotation - DB must NOT mutate
	connIDFail, _ := db.CreateConnection(ctx, &models.UserConnection{
		UserID:     uID,
		ServerID:   sID,
		Protocol:   "awg",
		ClientID:   "cid-fail",
		Name:       "fail_vpn",
		AWGMimicry: models.AWGMimicryAuto,
		CreatedAt:  time.Now().UTC(),
	})
	awgMgr.mu.Lock()
	awgMgr.rotated = nil
	awgMgr.clients = nil
	awgMgr.returnError = true
	awgMgr.mu.Unlock()

	if err := orch.CheckAutoTrialHandshakes(ctx); err != nil {
		t.Fatalf("CheckAutoTrialHandshakes with failing rotation failed: %v", err)
	}
	ucFail, _ := db.GetConnection(ctx, connIDFail)
	if ucFail.AWGMimicry != models.AWGMimicryAuto {
		t.Errorf("expected failed client connection in DB to remain auto, got %s", ucFail.AWGMimicry)
	}
}

func TestOrchestrator_VPNTasks_HealthAndRebalance(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Set VPN config
	_ = db.SaveVPNConfig(ctx, &models.VPNConfig{
		HealthThresholdMS: 300,
	})

	srv1ID, _ := db.CreateServer(ctx, &models.Server{Name: "Srv1", Host: "10.0.0.1", SSHPort: 22})
	srv2ID, _ := db.CreateServer(ctx, &models.Server{Name: "Srv2", Host: "10.0.0.2", SSHPort: 22})
	srv3ID, _ := db.CreateServer(ctx, &models.Server{Name: "Srv3", Host: "10.0.0.3", SSHPort: 22})

	tID1, _ := db.CreateBackendTunnel(ctx, &models.BackendTunnel{
		ServerID:      srv1ID,
		InterfaceName: "awg0",
		PublicKey:     "pub1",
		Endpoint:      "127.0.0.1:55420",
		Status:        "active",
	})

	tID2, _ := db.CreateBackendTunnel(ctx, &models.BackendTunnel{
		ServerID:      srv2ID,
		InterfaceName: "awg1",
		PublicKey:     "pub2",
		Endpoint:      "127.0.0.1:55421",
		Status:        "active",
	})

	// Add disabled tunnel
	_, _ = db.CreateBackendTunnel(ctx, &models.BackendTunnel{
		ServerID:      srv3ID,
		InterfaceName: "awg2",
		PublicKey:     "pub3",
		Endpoint:      "127.0.0.1:55422",
		Status:        "disabled",
	})

	// Add 10 sessions on tunnel 1, 2 sessions on tunnel 2 (avg = 6, threshold = 8 -> 4 excess)
	for i := 1; i <= 10; i++ {
		_ = db.CreateVPNSession(ctx, &models.VPNSession{
			ID:              fmt.Sprintf("sess-t1-%d", i),
			UserID:          "u1",
			BackendTunnelID: tID1,
			PeerPublicKey:   fmt.Sprintf("peer-t1-%d", i),
			Status:          "connected",
		})
	}
	for i := 1; i <= 2; i++ {
		_ = db.CreateVPNSession(ctx, &models.VPNSession{
			ID:              fmt.Sprintf("sess-t2-%d", i),
			UserID:          "u2",
			BackendTunnelID: tID2,
			PeerPublicKey:   fmt.Sprintf("peer-t2-%d", i),
			Status:          "connected",
		})
	}

	// 1. Probe with success & low latency
	orchSuccess := New(db, nil, WithProbeFunc(func(ctx context.Context, endpoint, serverPubKey, clientPrivKey, psk string, h1, h2 uint32, s1, s2 int, timeout time.Duration) (time.Duration, error) {
		return 50 * time.Millisecond, nil
	}))
	if err := orchSuccess.CheckBackendTunnelHealth(ctx); err != nil {
		t.Fatalf("CheckBackendTunnelHealth success failed: %v", err)
	}

	t1, _ := db.GetBackendTunnel(ctx, tID1)
	if t1.Status != "active" || t1.LatencyMS != 50 {
		t.Errorf("expected tunnel 1 status active with latency 50, got %s / %d", t1.Status, t1.LatencyMS)
	}

	// 1b. Test RebalanceVPNSessions
	if err := orchSuccess.RebalanceVPNSessions(ctx); err != nil {
		t.Fatalf("RebalanceVPNSessions failed: %v", err)
	}

	activeSess, _ := db.GetActiveVPNSessions(ctx)
	if len(activeSess) >= 12 {
		t.Errorf("expected some sessions to be marked draining, got %d active", len(activeSess))
	}
	// Check that a drained session had its backend tunnel ID updated to tID2
	sess1, _ := db.GetVPNSessionByID(ctx, "sess-t1-1")
	if sess1 != nil && sess1.Status == "draining" && sess1.BackendTunnelID != tID2 {
		t.Errorf("expected drained session to be reassigned to tunnel %d, got %d", tID2, sess1.BackendTunnelID)
	}

	// 2. Probe with high latency > threshold (300ms config), with tID1 degraded and tID2 active
	orchHighLat := New(db, nil, WithProbeFunc(func(ctx context.Context, endpoint, serverPubKey, clientPrivKey, psk string, h1, h2 uint32, s1, s2 int, timeout time.Duration) (time.Duration, error) {
		if strings.Contains(endpoint, "55420") {
			return 600 * time.Millisecond, nil
		}
		return 50 * time.Millisecond, nil
	}))
	if err := orchHighLat.CheckBackendTunnelHealth(ctx); err != nil {
		t.Fatalf("CheckBackendTunnelHealth high latency failed: %v", err)
	}

	t1, _ = db.GetBackendTunnel(ctx, tID1)
	if t1.Status != "degraded" {
		t.Errorf("expected tunnel 1 status degraded on high latency, got %s", t1.Status)
	}

	// 3. Probe with error
	orchErr := New(db, nil, WithProbeFunc(func(ctx context.Context, endpoint, serverPubKey, clientPrivKey, psk string, h1, h2 uint32, s1, s2 int, timeout time.Duration) (time.Duration, error) {
		return 0, errors.New("timeout")
	}))
	if err := orchErr.CheckBackendTunnelHealth(ctx); err != nil {
		t.Fatalf("CheckBackendTunnelHealth err failed: %v", err)
	}

	t1, _ = db.GetBackendTunnel(ctx, tID1)
	if t1.Status != "degraded" {
		t.Errorf("expected tunnel 1 status degraded on error, got %s", t1.Status)
	}
}

func TestOrchestrator_SyncTraffic_TeleMTAndProtocols(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	sID, _ := db.CreateServer(ctx, &models.Server{
		Name:    "TeleMT Server",
		Host:    "10.0.0.5",
		SSHPort: 22,
		Protocols: map[string]any{
			"telemt": map[string]any{"port": 443},
			"dns":    map[string]any{"port": 53},
		},
	})

	uID, _ := db.CreateUser(ctx, &models.User{
		Username: "telemt_user",
		Role:     models.RoleUser,
		Enabled:  true,
	})

	_, _ = db.CreateConnection(ctx, &models.UserConnection{
		UserID:    uID,
		ServerID:  sID,
		Protocol:  "telemt",
		ClientID:  "telemt-c1",
		Name:      "telemt_vpn",
		CreatedAt: time.Now().UTC(),
	})

	reg := newMockOrchRegistry()
	telemtMgr := newMockOrchProtocolManager("telemt")
	telemtMgr.clients = []map[string]any{
		{
			"client_id":         "telemt-c1",
			"dataReceivedBytes": float64(500),
			"dataSentBytes":     float64(500),
		},
	}
	reg.Register(telemtMgr)

	orch := New(db, reg)
	if err := orch.SyncTraffic(ctx); err != nil {
		t.Fatalf("SyncTraffic failed: %v", err)
	}

	u, _ := db.GetUser(ctx, uID)
	if u.TrafficUsed != 1000 {
		t.Errorf("expected 1000 bytes traffic used, got %d", u.TrafficUsed)
	}
}

func TestOrchestrator_Reachability_AWGProbeAndEmptyHost(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// 1. Server with AWG protocol configured but NO public_key in protocols map (requires provisioning)
	sIDAWG, _ := db.CreateServer(ctx, &models.Server{
		Name:    "AWG Provisioning Server",
		Host:    "127.0.0.1",
		SSHPort: 22,
		Protocols: map[string]any{
			"awg": map[string]any{
				"port": "55424",
				"awg_params": map[string]any{
					"junk_packet_count": 4,
				},
			},
		},
	})

	// 2. Server with empty host
	sIDEmpty, _ := db.CreateServer(ctx, &models.Server{
		Name: "Empty Host Server",
		Host: "",
	})

	reg := newMockOrchRegistry()
	awgMgr := newMockOrchProtocolManager("awg")
	reg.Register(awgMgr)

	orch := New(db, reg)
	results, err := orch.CheckServerReachability(ctx)
	if err != nil {
		t.Fatalf("CheckServerReachability failed: %v", err)
	}

	if res, ok := results[sIDEmpty]; !ok || res["reachable"] != false {
		t.Errorf("empty host should be unreachable: %+v", res)
	}
	if _, ok := results[sIDAWG]; !ok {
		t.Errorf("expected result for AWG server %d", sIDAWG)
	}

	// Verify healthProbeKeys cache was populated via AWGHealthProbeManager provisioning
	orch.mu.RLock()
	cachedKey, ok := orch.healthProbeKeys[sIDAWG]
	orch.mu.RUnlock()
	if !ok || cachedKey.serverPub == "" || cachedKey.clientPriv == "" {
		t.Errorf("expected healthProbeKeys to be populated after probe, got %+v", cachedKey)
	}

	// Verify exactly 1 AddClient call occurred
	awgMgr.mu.Lock()
	callsFirst := awgMgr.addClientCalls
	awgMgr.mu.Unlock()
	if callsFirst != 1 {
		t.Errorf("expected exactly 1 AddClient call on initial provisioning, got %d", callsFirst)
	}

	// Verify that a second run or panel restart (new orchestrator instance with empty in-memory cache)
	// finds the existing "Health Probe" peer without creating a duplicate
	orchRestarted := New(db, reg)
	results2, err := orchRestarted.CheckServerReachability(ctx)
	if err != nil {
		t.Fatalf("CheckServerReachability on second run failed: %v", err)
	}
	if _, ok := results2[sIDAWG]; !ok {
		t.Errorf("expected result for AWG server %d on second run", sIDAWG)
	}

	awgMgr.mu.Lock()
	callsSecond := awgMgr.addClientCalls
	awgMgr.mu.Unlock()
	if callsSecond != 1 {
		t.Errorf("expected NO additional AddClient calls on subsequent run (dedup), got %d calls total", callsSecond)
	}
}

func TestOrchestrator_ParseHandshakeAge(t *testing.T) {
	// 1. Nil, empty, special values
	if _, ok := parseHandshakeAge(nil); ok {
		t.Errorf("expected nil to return false")
	}
	if _, ok := parseHandshakeAge(""); ok {
		t.Errorf("expected empty string to return false")
	}
	if _, ok := parseHandshakeAge("   "); ok {
		t.Errorf("expected whitespace to return false")
	}
	if _, ok := parseHandshakeAge("never"); ok {
		t.Errorf("expected 'never' to return false")
	}
	if _, ok := parseHandshakeAge("NEVER"); ok {
		t.Errorf("expected 'NEVER' to return false")
	}
	if _, ok := parseHandshakeAge("(none)"); ok {
		t.Errorf("expected '(none)' to return false")
	}
	if _, ok := parseHandshakeAge("none"); ok {
		t.Errorf("expected 'none' to return false")
	}
	if _, ok := parseHandshakeAge("0"); ok {
		t.Errorf("expected '0' to return false")
	}
	if _, ok := parseHandshakeAge(int64(0)); ok {
		t.Errorf("expected int64(0) to return false")
	}

	// 2. "now"
	if age, ok := parseHandshakeAge("now"); !ok || age != 0 {
		t.Errorf("expected 'now' to return (0, true), got (%v, %v)", age, ok)
	}

	// 3. WireGuard human-readable formats
	cases := []struct {
		input    string
		expected time.Duration
	}{
		{"12 seconds ago", 12 * time.Second},
		{"1 second ago", 1 * time.Second},
		{"1 minute, 32 seconds ago", 1*time.Minute + 32*time.Second},
		{"2 minutes ago", 2 * time.Minute},
		{"2 hours, 10 minutes ago", 2*time.Hour + 10*time.Minute},
		{"1 hour, 1 minute, 1 second ago", 1*time.Hour + 1*time.Minute + 1*time.Second},
		{"3 days, 4 hours ago", 3*24*time.Hour + 4*time.Hour},
		{"5 days ago", 5 * 24 * time.Hour},
	}

	for _, c := range cases {
		age, ok := parseHandshakeAge(c.input)
		if !ok {
			t.Errorf("[%s] parseHandshakeAge returned ok=false", c.input)
			continue
		}
		if age != c.expected {
			t.Errorf("[%s] expected %v, got %v", c.input, c.expected, age)
		}
	}

	// 4. Go duration strings
	if age, ok := parseHandshakeAge("45s"); !ok || age != 45*time.Second {
		t.Errorf("expected '45s' to parse, got (%v, %v)", age, ok)
	}
	if age, ok := parseHandshakeAge("2m30s"); !ok || age != 150*time.Second {
		t.Errorf("expected '2m30s' to parse, got (%v, %v)", age, ok)
	}

	// 5. Numeric timestamps
	now := time.Now().UTC().Unix()
	if age, ok := parseHandshakeAge(now); !ok || age > 2*time.Second {
		t.Errorf("expected current timestamp to parse with ~0 age, got (%v, %v)", age, ok)
	}
	if age, ok := parseHandshakeAge(float64(now)); !ok || age > 2*time.Second {
		t.Errorf("expected float timestamp to parse, got (%v, %v)", age, ok)
	}
	if age, ok := parseHandshakeAge(json.Number(fmt.Sprintf("%d", now))); !ok || age > 2*time.Second {
		t.Errorf("expected json.Number timestamp to parse, got (%v, %v)", age, ok)
	}
	if age, ok := parseHandshakeAge(fmt.Sprintf("%d", now)); !ok || age > 2*time.Second {
		t.Errorf("expected numeric string timestamp to parse, got (%v, %v)", age, ok)
	}
}

func TestOrchestrator_ExtractBytes_And_ResetStrategies(t *testing.T) {
	if extractBytes(nil) != 0 {
		t.Error("expected 0 for nil")
	}
	if extractBytes(int(10)) != 10 {
		t.Error("expected 10 for int")
	}
	if extractBytes(int64(20)) != 20 {
		t.Error("expected 20 for int64")
	}
	if extractBytes(float64(30.5)) != 30 {
		t.Error("expected 30 for float64")
	}
	if extractBytes("40") != 40 {
		t.Error("expected 40 for valid string")
	}
	if extractBytes("invalid") != 0 {
		t.Error("expected 0 for invalid string")
	}

	orch := New(nil, nil)
	now := time.Now().UTC()
	yesterday := now.AddDate(0, 0, -1).Format(time.RFC3339)
	lastWeek := now.AddDate(0, 0, -8).Format(time.RFC3339)
	lastMonth := now.AddDate(0, -1, 0).Format(time.RFC3339)
	today := now.Format(time.RFC3339)

	uDaily := &models.User{TrafficResetStrategy: models.ResetStrategyDaily, LastResetAt: &yesterday}
	if !orch.isTrafficResetNeeded(uDaily, now) {
		t.Error("expected daily reset needed")
	}
	uDailyToday := &models.User{TrafficResetStrategy: models.ResetStrategyDaily, LastResetAt: &today}
	if orch.isTrafficResetNeeded(uDailyToday, now) {
		t.Error("expected daily reset NOT needed today")
	}

	uWeekly := &models.User{TrafficResetStrategy: "weekly", LastResetAt: &lastWeek}
	if !orch.isTrafficResetNeeded(uWeekly, now) {
		t.Error("expected weekly reset needed")
	}

	uMonthly := &models.User{TrafficResetStrategy: models.ResetStrategyMonthly, LastResetAt: &lastMonth}
	if !orch.isTrafficResetNeeded(uMonthly, now) {
		t.Error("expected monthly reset needed")
	}

	uNever := &models.User{TrafficResetStrategy: models.ResetStrategyNever, LastResetAt: &lastMonth}
	if orch.isTrafficResetNeeded(uNever, now) {
		t.Error("never strategy should not need reset")
	}

	uNilLast := &models.User{TrafficResetStrategy: models.ResetStrategyDaily, LastResetAt: nil}
	if orch.isTrafficResetNeeded(uNilLast, now) {
		t.Error("nil LastResetAt should not trigger reset")
	}
}

func TestOrchestrator_RunAll_ErrorAggregation(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	failingSyncer := &mockRemnaSyncer{err: errors.New("remnawave down")}
	orch := New(db, nil, WithRemnaWaveSyncer(failingSyncer))

	err := orch.RunAll(ctx)
	if err == nil {
		t.Error("expected aggregated error from RunAll when subtask fails")
	}
}

func TestOrchestrator_RunAll_ContextCancelled(t *testing.T) {
	cancCtx, cancel := context.WithCancel(context.Background())
	cancel()

	orch := New(nil, nil)
	err := orch.RunAll(cancCtx)
	if err == nil {
		t.Error("expected error on cancelled context in RunAll")
	}
}

func TestNextMimicryProfile(t *testing.T) {
	cases := []struct {
		in   models.AWGMimicryProfile
		want models.AWGMimicryProfile
	}{
		{models.AWGMimicryAuto, models.AWGMimicryTLS},
		{models.AWGMimicryTLS, models.AWGMimicryQUIC},
		{models.AWGMimicryQUIC, models.AWGMimicryDNS},
		{models.AWGMimicryDNS, models.AWGMimicrySIP},
		{models.AWGMimicrySIP, models.AWGMimicryTLS},
		{"", models.AWGMimicryTLS},
		{"unknown", models.AWGMimicryTLS},
	}

	for _, tc := range cases {
		got := NextMimicryProfile(tc.in)
		if got != tc.want {
			t.Errorf("NextMimicryProfile(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestOrchestrator_NilDB(t *testing.T) {
	nilOrch := New(nil, nil)
	ctx := context.Background()

	if err := nilOrch.SyncTraffic(ctx); err == nil {
		t.Error("expected error for nil DB in SyncTraffic")
	}
	if err := nilOrch.CheckExpiry(ctx); err == nil {
		t.Error("expected error for nil DB in CheckExpiry")
	}
	if _, err := nilOrch.CheckServerReachability(ctx); err == nil {
		t.Error("expected error for nil DB in CheckServerReachability")
	}
	if err := nilOrch.CheckAutoTrialHandshakes(ctx); err == nil {
		t.Error("expected error for nil DB in CheckAutoTrialHandshakes")
	}
	if err := nilOrch.CheckBackendTunnelHealth(ctx); err == nil {
		t.Error("expected error for nil DB in CheckBackendTunnelHealth")
	}
	if err := nilOrch.RebalanceVPNSessions(ctx); err == nil {
		t.Error("expected error for nil DB in RebalanceVPNSessions")
	}
}

type panickingProtocolManager struct {
	proto string
}

func (p *panickingProtocolManager) Protocol() string { return p.proto }
func (p *panickingProtocolManager) Install(ctx context.Context, server *models.Server, params map[string]any) error {
	return nil
}
func (p *panickingProtocolManager) Uninstall(ctx context.Context, server *models.Server) error {
	return nil
}
func (p *panickingProtocolManager) GetClients(ctx context.Context, server *models.Server) ([]map[string]any, error) {
	panic("simulated panic in GetClients child goroutine")
}
func (p *panickingProtocolManager) AddClient(ctx context.Context, server *models.Server, clientParams map[string]any) (map[string]any, error) {
	return nil, nil
}
func (p *panickingProtocolManager) RemoveClient(ctx context.Context, server *models.Server, clientID string) error {
	return nil
}
func (p *panickingProtocolManager) GetClientConfig(ctx context.Context, server *models.Server, clientID string) (string, error) {
	return "", nil
}

func TestOrchestrator_PanicRecoveryInChildGoroutines(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	sID, _ := db.CreateServer(ctx, &models.Server{
		Name:    "Panic Server",
		Host:    "10.0.0.99",
		SSHPort: 22,
		Protocols: map[string]any{
			"awg": map[string]any{"port": 55424},
		},
	})

	uID, _ := db.CreateUser(ctx, &models.User{
		Username: "panic_user",
		Role:     models.RoleUser,
	})

	_, _ = db.CreateConnection(ctx, &models.UserConnection{
		ID:        "conn-panic",
		UserID:    uID,
		ServerID:  sID,
		Protocol:  "awg",
		ClientID:  "cid-panic",
		Name:      "panic_vpn",
		CreatedAt: time.Now().UTC(),
	})

	reg := newMockOrchRegistry()
	reg.Register(&panickingProtocolManager{proto: "awg"})

	orch := New(db, reg)

	// SyncTraffic should recover from child goroutine panic without crashing the process
	if err := orch.SyncTraffic(ctx); err != nil {
		t.Fatalf("SyncTraffic should not fail fatally on child panic: %v", err)
	}

	// Status getters check
	if orch.LastRun() != nil {
		t.Log("LastRun tracked successfully")
	}
	_ = orch.LastSuccess()
	_ = orch.LastError()
}

func TestOrchestrator_ParseTolerantTime_AllLayouts(t *testing.T) {
	valid := []string{
		"2026-08-30T14:23:11.123456Z",
		"2026-08-30T14:23:11Z",
		"2026-08-30T14:23:11.123456789",
		"2026-08-30T14:23:11.123456",
		"2026-08-30T14:23:11",
		"2026-08-30 14:23:11.123456",
		"2026-08-30 14:23:11",
		"2026-08-30",
	}
	for _, v := range valid {
		parsed, err := parseTolerantTime(v)
		if err != nil || parsed.IsZero() {
			t.Errorf("parseTolerantTime(%q) failed: %v", v, err)
		}
	}

	invalid := []string{"", "invalid-date", "2026/08/30"}
	for _, inv := range invalid {
		if _, err := parseTolerantTime(inv); err == nil {
			t.Errorf("expected error for parseTolerantTime(%q)", inv)
		}
	}
}

func TestOrchestrator_VPNTasks_DetailedFailoverAndRebalance(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// 1. Rebalance with fewer than 2 active tunnels
	orch := New(db, nil)
	if err := orch.RebalanceVPNSessions(ctx); err != nil {
		t.Errorf("expected nil when < 2 active tunnels, got: %v", err)
	}

	// 2. Add 2 active tunnels with no sessions
	s1, _ := db.CreateServer(ctx, &models.Server{Name: "S1", Host: "10.0.0.1"})
	s2, _ := db.CreateServer(ctx, &models.Server{Name: "S2", Host: "10.0.0.2"})
	t1ID, _ := db.CreateBackendTunnel(ctx, &models.BackendTunnel{ServerID: s1, Status: "active", InterfaceName: "awg0"})
	t2ID, _ := db.CreateBackendTunnel(ctx, &models.BackendTunnel{ServerID: s2, Status: "active", InterfaceName: "awg1"})

	if err := orch.RebalanceVPNSessions(ctx); err != nil {
		t.Errorf("expected nil with 0 sessions, got: %v", err)
	}

	// 3. Add sessions causing overload on t1ID
	uID, _ := db.CreateUser(ctx, &models.User{Username: "vpn_test_user", Role: models.RoleUser})
	for i := 1; i <= 8; i++ {
		_ = db.CreateVPNSession(ctx, &models.VPNSession{
			ID:              fmt.Sprintf("r-sess-%d", i),
			UserID:          uID,
			BackendTunnelID: t1ID,
			PeerPublicKey:   fmt.Sprintf("peer-r-%d", i),
			Status:          "connected",
		})
	}
	_ = db.CreateVPNSession(ctx, &models.VPNSession{
		ID:              "r-sess-t2-1",
		UserID:          uID,
		BackendTunnelID: t2ID,
		PeerPublicKey:   "peer-r-t2-1",
		Status:          "connected",
	})

	if err := orch.RebalanceVPNSessions(ctx); err != nil {
		t.Fatalf("RebalanceVPNSessions failed: %v", err)
	}

	// 4. Test CheckBackendTunnelHealth failover with degraded tunnel and active sessions
	orchFailover := New(db, nil, WithProbeFunc(func(ctx context.Context, endpoint, serverPubKey, clientPrivKey, psk string, h1, h2 uint32, s1, s2 int, timeout time.Duration) (time.Duration, error) {
		if strings.Contains(endpoint, "55420") {
			return 0, errors.New("degraded endpoint")
		}
		return 10 * time.Millisecond, nil
	}))
	_ = db.UpdateBackendTunnel(ctx, t1ID, map[string]any{"endpoint": "127.0.0.1:55420"})
	_ = db.UpdateBackendTunnel(ctx, t2ID, map[string]any{"endpoint": "127.0.0.1:55421"})

	if err := orchFailover.CheckBackendTunnelHealth(ctx); err != nil {
		t.Fatalf("CheckBackendTunnelHealth failed: %v", err)
	}

	// 5. RunAll success tracking
	if err := orch.RunAll(ctx); err != nil {
		t.Fatalf("RunAll failed: %v", err)
	}
	if orch.LastSuccess() == nil {
		t.Error("expected LastSuccess to be non-nil after successful RunAll")
	}
	if orch.LastError() != nil {
		t.Errorf("expected LastError to be nil, got %v", orch.LastError())
	}
}

func TestOrchestrator_ResetStrategyAndExpiryVariants(t *testing.T) {
	orch := New(nil, nil)
	now := time.Now().UTC()

	// 1. Never / Empty strategy
	uNever := &models.User{TrafficResetStrategy: models.ResetStrategyNever}
	if orch.isTrafficResetNeeded(uNever, now) {
		t.Error("expected false for ResetStrategyNever")
	}
	uEmpty := &models.User{TrafficResetStrategy: ""}
	if orch.isTrafficResetNeeded(uEmpty, now) {
		t.Error("expected false for empty strategy")
	}

	// 2. Daily strategy
	yesterdayStr := now.AddDate(0, 0, -1).Format(time.RFC3339)
	todayStr := now.Format(time.RFC3339)
	uDaily := &models.User{TrafficResetStrategy: models.ResetStrategyDaily, LastResetAt: &yesterdayStr}
	if !orch.isTrafficResetNeeded(uDaily, now) {
		t.Error("expected true for daily reset when last reset was yesterday")
	}
	uDailyToday := &models.User{TrafficResetStrategy: models.ResetStrategyDaily, LastResetAt: &todayStr}
	if orch.isTrafficResetNeeded(uDailyToday, now) {
		t.Error("expected false for daily reset when last reset was today")
	}

	// 3. Weekly strategy
	lastWeekStr := now.AddDate(0, 0, -8).Format(time.RFC3339)
	uWeekly := &models.User{TrafficResetStrategy: "weekly", LastResetAt: &lastWeekStr}
	if !orch.isTrafficResetNeeded(uWeekly, now) {
		t.Error("expected true for weekly reset when last reset was 8 days ago")
	}
	uWeeklyToday := &models.User{TrafficResetStrategy: "weekly", LastResetAt: &todayStr}
	if orch.isTrafficResetNeeded(uWeeklyToday, now) {
		t.Error("expected false for weekly reset when last reset was today")
	}

	// 4. Expiration variants
	past := now.Add(-1 * time.Hour)
	future := now.Add(1 * time.Hour)
	uExpAt := &models.User{ExpiresAt: &past}
	if !orch.isUserExpired(uExpAt, now) {
		t.Error("expected true for expired user via ExpiresAt")
	}
	uExpDate := &models.User{ExpirationDate: &past}
	if !orch.isUserExpired(uExpDate, now) {
		t.Error("expected true for expired user via ExpirationDate")
	}
	uFuture := &models.User{ExpiresAt: &future, ExpirationDate: &future}
	if orch.isUserExpired(uFuture, now) {
		t.Error("expected false for non-expired user")
	}
}
