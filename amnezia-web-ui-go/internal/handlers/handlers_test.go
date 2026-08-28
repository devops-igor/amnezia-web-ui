package handlers

import (
	"archive/zip"
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/devops-igor/amnezia-web-ui-go/internal/config"
	"github.com/devops-igor/amnezia-web-ui-go/internal/database"
	"github.com/devops-igor/amnezia-web-ui-go/internal/manager"
	"github.com/devops-igor/amnezia-web-ui-go/internal/manager/awg"
	"github.com/devops-igor/amnezia-web-ui-go/internal/manager/dns"
	"github.com/devops-igor/amnezia-web-ui-go/internal/manager/mtproxyl"
	"github.com/devops-igor/amnezia-web-ui-go/internal/manager/ssh"
	"github.com/devops-igor/amnezia-web-ui-go/internal/models"
	"github.com/devops-igor/amnezia-web-ui-go/internal/vpn"
	"github.com/go-chi/chi/v5"
	gossh "golang.org/x/crypto/ssh"
)

const testSecretKey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func setupTestHandlers(t *testing.T) (*Handlers, *database.DB, *config.Config) {
	t.Helper()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "handlers_test.db")
	db, err := database.Open(dbPath, testSecretKey)
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	cfg := &config.Config{
		AppVersion: "1.0.0",
		Host:       "127.0.0.1",
		Port:       5000,
		SecretKey:  testSecretKey,
	}

	sshPool := ssh.NewSSHClientPool(ssh.PoolConfig{
		IdleTimeout:     5 * time.Minute,
		KeepAlivePeriod: 30 * time.Second,
	}, db)

	vpnSvc, _ := vpn.NewVPNService(db, nil)

	reg := manager.NewRegistry()
	reg.Register(&mockProtocolManager{protocol: "awg"})
	reg.Register(&mockProtocolManager{protocol: "telemt"})
	reg.Register(&mockProtocolManager{protocol: "dns"})

	h := NewHandlers(Dependencies{
		Config:     cfg,
		DB:         db,
		Registry:   reg,
		SSHPool:    sshPool,
		VPNService: vpnSvc,
	})

	return h, db, cfg
}

func setupTestHandlersWithMockSSH(t *testing.T, client *testMockSSHClient) (*Handlers, *database.DB, *config.Config) {
	t.Helper()
	h, db, cfg := setupTestHandlers(t)
	mockPool := &testMockSSHPool{client: client}
	h.sshPool = mockPool
	h.awgMgr = awg.NewAWGManager(mockPool)
	h.mtproxylMgr = mtproxyl.NewMTProxyLManager(mockPool)
	h.dnsMgr = dns.NewDNSManager(mockPool)
	h.registry = manager.NewRegistry()
	h.registry.Register(h.awgMgr)
	h.registry.Register(h.mtproxylMgr)
	h.registry.Register(h.dnsMgr)
	return h, db, cfg
}

type mockProtocolManager struct {
	protocol          string
	installFn         func(ctx context.Context, server *models.Server, params map[string]any) error
	uninstallFn       func(ctx context.Context, server *models.Server) error
	getClientsFn      func(ctx context.Context, server *models.Server) ([]map[string]any, error)
	addClientFn       func(ctx context.Context, server *models.Server, clientParams map[string]any) (map[string]any, error)
	removeClientFn    func(ctx context.Context, server *models.Server, clientID string) error
	getClientConfigFn func(ctx context.Context, server *models.Server, clientID string) (string, error)
}

func (m *mockProtocolManager) Protocol() string {
	return m.protocol
}

func (m *mockProtocolManager) Install(ctx context.Context, server *models.Server, params map[string]any) error {
	if m.installFn != nil {
		return m.installFn(ctx, server, params)
	}
	return nil
}

func (m *mockProtocolManager) Uninstall(ctx context.Context, server *models.Server) error {
	if m.uninstallFn != nil {
		return m.uninstallFn(ctx, server)
	}
	return nil
}

func (m *mockProtocolManager) GetClients(ctx context.Context, server *models.Server) ([]map[string]any, error) {
	if m.getClientsFn != nil {
		return m.getClientsFn(ctx, server)
	}
	return []map[string]any{
		{
			"clientId":   "client-1",
			"client_id":  "client-1",
			"clientName": "Test Client",
			"userData":   map[string]any{"clientName": "Test Client"},
		},
	}, nil
}

func (m *mockProtocolManager) AddClient(ctx context.Context, server *models.Server, clientParams map[string]any) (map[string]any, error) {
	if m.addClientFn != nil {
		return m.addClientFn(ctx, server, clientParams)
	}
	return map[string]any{
		"clientId":  "new-client-1",
		"client_id": "new-client-1",
		"config":    "[Interface]\nPrivateKey=testkey\nAddress=10.0.0.5/32\n",
	}, nil
}

func (m *mockProtocolManager) RemoveClient(ctx context.Context, server *models.Server, clientID string) error {
	if m.removeClientFn != nil {
		return m.removeClientFn(ctx, server, clientID)
	}
	return nil
}

func (m *mockProtocolManager) GetClientConfig(ctx context.Context, server *models.Server, clientID string) (string, error) {
	if m.getClientConfigFn != nil {
		return m.getClientConfigFn(ctx, server, clientID)
	}
	return "[Interface]\nPrivateKey=testkey\nAddress=10.0.0.5/32\n", nil
}

type testMockSSHClient struct {
	serverID   *int64
	cmdFunc    func(ctx context.Context, cmd string) (string, string, int, error)
	downloadFn func(ctx context.Context, path string) ([]byte, error)
	uploadFn   func(ctx context.Context, path string, content []byte) error
}

func (m *testMockSSHClient) RunCommand(ctx context.Context, cmd string) (string, string, int, error) {
	if m.cmdFunc != nil {
		return m.cmdFunc(ctx, cmd)
	}
	if strings.Contains(cmd, "clientsTable") {
		return `[{"clientId":"client-1","client_id":"client-1","userData":{"clientName":"Test Client","clientPrivateKey":"c29tZXByaXZhdGVrZXk="},"config":"[Interface]\nAddress = 10.8.0.2/24\n"}]`, "", 0, nil
	}
	if strings.Contains(cmd, "docker --version") {
		return "Docker version 24.0.5, build cedb786", "", 0, nil
	}
	if strings.Contains(cmd, "top -bn1") {
		return "top - 12:00:00 up 10 days\nTasks: 100 total\n%Cpu(s):  5.0 us,  2.0 sy,  0.0 ni, 93.0 id\nMiB Mem :   2000.0 total,    500.0 free,   1000.0 used\n/dev/sda1       50G   20G   30G  40% /", "", 0, nil
	}
	if strings.Contains(cmd, "tc qdisc") {
		return "qdisc tbf 1: dev eth0 root", "", 0, nil
	}
	return "ok", "", 0, nil
}

func (m *testMockSSHClient) RunScript(ctx context.Context, script string) (string, string, int, error) {
	return m.RunCommand(ctx, script)
}

func (m *testMockSSHClient) RunSudoScript(ctx context.Context, script string) (string, string, int, error) {
	return m.RunCommand(ctx, script)
}

func (m *testMockSSHClient) RunSudoCommand(ctx context.Context, cmd string) (string, string, int, error) {
	return m.RunCommand(ctx, cmd)
}

func (m *testMockSSHClient) UploadFile(ctx context.Context, remotePath string, content []byte, mode os.FileMode) error {
	if m.uploadFn != nil {
		return m.uploadFn(ctx, remotePath, content)
	}
	return nil
}

func (m *testMockSSHClient) UploadSudoFile(ctx context.Context, remotePath string, content []byte, mode os.FileMode) error {
	if m.uploadFn != nil {
		return m.uploadFn(ctx, remotePath, content)
	}
	return nil
}

func (m *testMockSSHClient) DownloadFile(ctx context.Context, remotePath string) ([]byte, error) {
	if m.downloadFn != nil {
		return m.downloadFn(ctx, remotePath)
	}
	if strings.Contains(remotePath, "clientsTable") {
		return []byte(`[{"clientId":"client-1","client_id":"client-1","userData":{"clientName":"Test Client"},"config":"[Interface]\nAddress = 10.8.0.2/24\n"}]`), nil
	}
	return []byte("[Interface]\nAddress = 10.8.0.1/24\nPrivateKey = privkey\n"), nil
}

func (m *testMockSSHClient) FileExists(ctx context.Context, remotePath string) (bool, error) {
	return true, nil
}

func (m *testMockSSHClient) TestConnection(ctx context.Context) (string, error) {
	return "fingerprint:12345", nil
}

func (m *testMockSSHClient) Close() error {
	return nil
}

func (m *testMockSSHClient) IsAlive() bool {
	return true
}

func (m *testMockSSHClient) GetUnderlyingClient() *gossh.Client {
	return nil
}

func (m *testMockSSHClient) GetHost() string {
	return "127.0.0.1"
}

func (m *testMockSSHClient) GetPort() int {
	return 22
}

func (m *testMockSSHClient) GetUser() string {
	return "root"
}

func (m *testMockSSHClient) GetServerID() *int64 {
	return m.serverID
}

func (m *testMockSSHClient) GetLastActive() time.Time {
	return time.Now()
}

type testMockSSHPool struct {
	client *testMockSSHClient
	err    error
}

func (p *testMockSSHPool) Get(ctx context.Context, server *models.Server) (ssh.SSHClient, error) {
	if p.err != nil {
		return nil, p.err
	}
	return p.client, nil
}

func (p *testMockSSHPool) Remove(serverID int64) {}

func setupFullServerRouter(h *Handlers) *chi.Mux {
	r := chi.NewRouter()
	r.Post("/api/servers/add", h.AddServerHandler)
	r.Post("/api/servers/confirm-fingerprint", h.ConfirmFingerprintHandler)
	r.Post("/api/servers/{server_id}/delete", h.DeleteServerHandler)
	r.Post("/api/servers/{server_id}/reboot", h.RebootServerHandler)
	r.Post("/api/servers/{server_id}/clear", h.ClearServerHandler)
	r.Post("/api/servers/{server_id}/stats", h.ServerStatsHandler)
	r.Post("/api/servers/{server_id}/check", h.ServerCheckHandler)
	r.Post("/api/servers/{server_id}/install", h.InstallProtocolHandler)
	r.Post("/api/servers/{server_id}/uninstall", h.UninstallProtocolHandler)
	r.Post("/api/servers/{server_id}/container/toggle", h.ToggleContainerHandler)
	r.Post("/api/servers/{server_id}/server_config", h.GetServerConfigHandler)
	r.Post("/api/servers/{server_id}/server_config/save", h.SaveServerConfigHandler)
	r.Get("/api/servers/{server_id}/reachability", h.GetServerReachabilityHandler)
	r.Patch("/api/servers/{server_id}/connections/speed-limit", h.SetClientSpeedLimitHandler)
	r.Get("/api/servers/{server_id}/awg/speed-limit-config", h.GetAWGSpeedLimitConfigHandler)
	r.Patch("/api/servers/{server_id}/awg/speed-limit-config", h.SetAWGSpeedLimitConfigHandler)
	r.Post("/api/servers/{server_id}/awg/apply-default-speed-limits", h.ApplyDefaultSpeedLimitsHandler)
	return r
}

func setupFullServerConnectionsRouter(h *Handlers) *chi.Mux {
	r := chi.NewRouter()
	r.Get("/api/servers/{server_id}/connections", h.GetServerConnectionsHandler)
	r.Post("/api/servers/{server_id}/connections/add", h.AddServerConnectionHandler)
	r.Post("/api/servers/{server_id}/connections/{client_id}/rotate-mimicry", h.RotateMimicryHandler)
	r.Post("/api/servers/{server_id}/connections/auto-trial", h.AutoTrialHandler)
	r.Post("/api/servers/{server_id}/connections/kit", h.GetServerConnectionKitHandler)
	r.Post("/api/servers/{server_id}/connections/remove", h.RemoveServerConnectionHandler)
	r.Post("/api/servers/{server_id}/connections/edit", h.EditServerConnectionHandler)
	r.Post("/api/servers/{server_id}/connections/config", h.GetServerConnectionConfigHandler)
	r.Post("/api/servers/{server_id}/connections/toggle", h.ToggleServerConnectionHandler)
	r.Get("/api/servers/{server_id}/{protocol}/clients", h.GetProtocolClientsHandler)
	return r
}

func setupFullUsersRouter(h *Handlers) *chi.Mux {
	r := chi.NewRouter()
	r.Get("/api/users", h.ListUsersHandler)
	r.Post("/api/users/add", h.AddUserHandler)
	r.Post("/api/users/{user_id}/update", h.UpdateUserHandler)
	r.Post("/api/users/{user_id}/delete", h.DeleteUserHandler)
	r.Post("/api/users/{user_id}/toggle", h.ToggleUserHandler)
	r.Post("/api/users/{user_id}/connections/add", h.AddUserConnectionHandler)
	r.Get("/api/users/{user_id}/connections", h.GetUserConnectionsHandler)
	r.Post("/api/users/{user_id}/share/setup", h.SetupUserShareHandler)
	return r
}

func setupFullConnectionsRouter(h *Handlers) *chi.Mux {
	r := chi.NewRouter()
	r.Get("/api/my/connections", h.UserGetMyConnectionsHandler)
	r.Post("/api/connections/add", h.UserAddConnectionHandler)
	r.Post("/api/connections/{connection_id}/config", h.UserGetConnectionConfigHandler)
	r.Post("/api/connections/{connection_id}/kit", h.UserGetConnectionKitHandler)
	r.Post("/api/connections/{connection_id}/rename", h.UserRenameConnectionHandler)
	r.Post("/api/connections/{connection_id}/delete", h.UserDeleteConnectionHandler)
	return r
}

func setupFullSettingsRouter(h *Handlers) *chi.Mux {
	r := chi.NewRouter()
	r.Get("/api/settings", h.GetSettingsHandler)
	r.Post("/api/settings/save", h.SaveSettingsHandler)
	r.Post("/api/settings/sync_now", h.SyncNowHandler)
	r.Post("/api/settings/sync_delete", h.SyncDeleteHandler)
	r.Get("/api/settings/backup/download", h.DownloadBackupHandler)
	r.Post("/api/settings/backup/restore", h.RestoreBackupHandler)
	return r
}

func setupFullShareRouter(h *Handlers) *chi.Mux {
	r := chi.NewRouter()
	r.Get("/share/{token}", h.SharePageHandler)
	r.Post("/api/share/{token}/auth", h.ShareAuthHandler)
	r.Get("/api/share/{token}/connections", h.GetShareConnectionsHandler)
	r.Post("/api/share/{token}/config/{connection_id}", h.GetShareConnectionConfigHandler)
	return r
}

func setupFullVPNRouter(h *Handlers) *chi.Mux {
	r := chi.NewRouter()
	r.Get("/api/vpn/status", h.VPNStatusHandler)
	r.Get("/api/vpn/backends", h.VPNBackendsHandler)
	r.Post("/api/vpn/backends/{server_id}/enable", h.VPNEnableBackendHandler)
	r.Post("/api/vpn/backends/{server_id}/disable", h.VPNDisableBackendHandler)
	r.Get("/api/vpn/tunnels", h.VPNTunnelsHandler)
	r.Get("/api/vpn/config", h.VPNGetConfigHandler)
	r.Post("/api/vpn/config", h.VPNUpdateConfigHandler)
	r.Get("/api/vpn/my-connection", h.VPNMyConnectionHandler)
	r.Get("/api/vpn/my-config", h.VPNMyConfigHandler)
	r.Post("/api/vpn/disconnect", h.VPNDisconnectHandler)
	return r
}

func TestHandlersHelpers(t *testing.T) {
	h, db, _ := setupTestHandlers(t)

	// JSON / JSONOK
	w := httptest.NewRecorder()
	h.JSONOK(w, map[string]any{"custom": "value"})
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"status":"ok"`) || !strings.Contains(w.Body.String(), `"custom":"value"`) {
		t.Errorf("unexpected body: %s", w.Body.String())
	}

	// JSONError
	wErr := httptest.NewRecorder()
	h.JSONError(wErr, http.StatusBadRequest, "err_code", "detail message")
	if wErr.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", wErr.Code)
	}

	// JSONErrorWithFlag
	wFlag := httptest.NewRecorder()
	h.JSONErrorWithFlag(wFlag, http.StatusForbidden, "pwd_req", "Change pwd", true)
	if wFlag.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", wFlag.Code)
	}

	// DecodeJSON empty
	reqEmpty := httptest.NewRequest(http.MethodPost, "/", nil)
	reqEmpty.Body = nil
	if err := h.DecodeJSON(reqEmpty, &map[string]any{}); err == nil {
		t.Errorf("expected error for empty body")
	}

	// DecodeJSON valid
	jsonBody := bytes.NewReader([]byte(`{"foo":"bar"}`))
	reqValid := httptest.NewRequest(http.MethodPost, "/", jsonBody)
	var decoded map[string]string
	if err := h.DecodeJSON(reqValid, &decoded); err != nil || decoded["foo"] != "bar" {
		t.Errorf("DecodeJSON failed: %v", err)
	}

	// GetLang
	reqLang := httptest.NewRequest(http.MethodGet, "/", nil)
	if lang := h.GetLang(reqLang); lang != "en" {
		t.Errorf("expected default en, got %s", lang)
	}
	reqLang.AddCookie(&http.Cookie{Name: "lang", Value: "ru"})
	if lang := h.GetLang(reqLang); lang != "ru" {
		t.Errorf("expected ru, got %s", lang)
	}

	// Translate
	_ = config.LoadTranslations()
	tr := h.Translate(reqLang, "app_title")
	if tr == "" {
		t.Errorf("expected non-empty translation")
	}

	// GenerateVPNLink
	vpnLink := GenerateVPNLink("[Interface]\nPrivateKey=abc\n")
	if !strings.HasPrefix(vpnLink, "vpn://") {
		t.Errorf("expected vpn:// prefix, got %s", vpnLink)
	}

	// BuildConnectionKitZip
	zipBytes, err := BuildConnectionKitZip("test-client", "[Interface]\nAddress=10.0.0.2/32\n", vpnLink)
	if err != nil || len(zipBytes) == 0 {
		t.Fatalf("BuildConnectionKitZip failed: %v", err)
	}

	// Verify zip contents
	zr, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		t.Fatalf("failed to read zip: %v", err)
	}
	if len(zr.File) < 2 {
		t.Errorf("expected at least 2 files in zip, got %d", len(zr.File))
	}

	t.Run("NewHandlers Auto Registration", func(t *testing.T) {
		// Exercised via setupTestHandlersWithMockSSH: nil registry with managers set
		mockPool := &testMockSSHPool{client: &testMockSSHClient{}}
		hAuto := NewHandlers(Dependencies{
			Config:          h.cfg,
			DB:              db,
			SSHPool:         mockPool,
			AWGManager:      awg.NewAWGManager(mockPool),
			MTProxyLManager: mtproxyl.NewMTProxyLManager(mockPool),
			DNSManager:      dns.NewDNSManager(mockPool),
		})
		if hAuto.registry == nil {
			t.Fatalf("expected registry to be auto-created")
		}
		for _, p := range []string{"awg", "telemt", "dns"} {
			if _, err := hAuto.GetProtocolManager(p); err != nil {
				t.Errorf("expected manager registered for %s, got err: %v", p, err)
			}
		}

		// NewHandlers with nothing set
		hEmpty := NewHandlers(Dependencies{})
		if hEmpty.registry == nil {
			t.Errorf("expected empty registry created")
		}
	})

	t.Run("BuildConnectionKitZip Special Chars", func(t *testing.T) {
		zipBytes, err := BuildConnectionKitZip("client/with\\special", "[Interface]\n", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		zr, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
		if err != nil {
			t.Fatalf("failed to read zip: %v", err)
		}
		if len(zr.File) != 1 {
			t.Errorf("expected 1 file (conf only, no vpn link), got %d", len(zr.File))
		}
		if zr.File[0].Name != "client_with_special.conf" {
			t.Errorf("expected sanitized name, got %s", zr.File[0].Name)
		}
	})

	t.Run("GetProtocolManager", func(t *testing.T) {
		if mgr, err := h.GetProtocolManager("awg"); err != nil || mgr == nil {
			t.Errorf("expected awg manager, got err: %v", err)
		}
		if mgr, err := h.GetProtocolManager("telemt"); err != nil || mgr == nil {
			t.Errorf("expected telemt manager, got err: %v", err)
		}
		if mgr, err := h.GetProtocolManager("dns"); err != nil || mgr == nil {
			t.Errorf("expected dns manager, got err: %v", err)
		}
		if _, err := h.GetProtocolManager("unknown_proto"); err == nil {
			t.Errorf("expected error for unknown protocol")
		}
	})

	// GetLang with panel_lang
	reqPanelLang := httptest.NewRequest(http.MethodGet, "/", nil)
	reqPanelLang.AddCookie(&http.Cookie{Name: "panel_lang", Value: "ru"})
	if lang := h.GetLang(reqPanelLang); lang != "ru" {
		t.Errorf("expected ru from panel_lang, got %s", lang)
	}

	// BuildConnectionKitZip empty name & no vpn link
	zipSimple, err := BuildConnectionKitZip("", "[Interface]\nAddress=10.0.0.3/32\n", "")
	if err != nil || len(zipSimple) == 0 {
		t.Errorf("BuildConnectionKitZip failed: %v", err)
	}

	// GetProtocolManager without registry
	hNoReg := &Handlers{
		awgMgr:      awg.NewAWGManager(nil),
		mtproxylMgr: mtproxyl.NewMTProxyLManager(nil),
		dnsMgr:      dns.NewDNSManager(nil),
	}
	if _, err := hNoReg.GetProtocolManager("awg"); err != nil {
		t.Errorf("expected awg without registry, got err: %v", err)
	}
	if _, err := hNoReg.GetProtocolManager("telemt"); err != nil {
		t.Errorf("expected telemt without registry, got err: %v", err)
	}
	if _, err := hNoReg.GetProtocolManager("dns"); err != nil {
		t.Errorf("expected dns without registry, got err: %v", err)
	}

	// GetSSHClient with nil sshPool or nil server
	hNoPool := &Handlers{}
	if _, err := hNoPool.GetSSHClient(context.Background(), &models.Server{}); err == nil {
		t.Errorf("expected error when sshPool is nil")
	}
	if _, err := h.GetSSHClient(context.Background(), nil); err == nil {
		t.Errorf("expected error when server is nil")
	}

	// isProtocolInstalled tests
	if isProtocolInstalled(nil, "awg") {
		t.Errorf("expected false for nil server")
	}
	if isProtocolInstalled(&models.Server{}, "awg") {
		t.Errorf("expected false for server with nil protocols")
	}
	sInst := &models.Server{
		Protocols: map[string]any{
			"awg": map[string]any{"installed": true},
			"dns": map[string]any{"installed": false},
		},
	}
	if !isProtocolInstalled(sInst, "awg") {
		t.Errorf("expected true for installed awg")
	}
	if !isProtocolInstalled(sInst, "awg2") {
		t.Errorf("expected true for normalized awg2")
	}
	if isProtocolInstalled(sInst, "dns") {
		t.Errorf("expected false for uninstalled dns")
	}
	if isProtocolInstalled(sInst, "telemt") {
		t.Errorf("expected false for missing telemt")
	}

	// serverReachabilityInfo tests
	sIDTest, _ := db.CreateServer(context.Background(), &models.Server{Name: "Reach-Test", Host: "1.2.3.4", SSHUser: "root"})
	_ = db.UpdateServerReachability(context.Background(), sIDTest, models.ReachabilityOnline)
	if st, reach := h.serverReachabilityInfo(context.Background(), sIDTest); st != "online" || !reach {
		t.Errorf("expected online/true, got %s/%v", st, reach)
	}
	_ = db.UpdateServerReachability(context.Background(), sIDTest, models.ReachabilityOffline)
	if st, reach := h.serverReachabilityInfo(context.Background(), sIDTest); st != "offline" || reach {
		t.Errorf("expected offline/false, got %s/%v", st, reach)
	}
	_ = db.UpdateServerReachability(context.Background(), sIDTest, models.ReachabilityUnknown)
	if st, reach := h.serverReachabilityInfo(context.Background(), sIDTest); st != "unknown" || reach {
		t.Errorf("expected unknown/false, got %s/%v", st, reach)
	}

	// effectiveRateLimit and effectiveMaxConnectionsPerUser tests
	if cnt, win := effectiveRateLimit(nil, 5, 60); cnt != 5 || win != 60 {
		t.Errorf("expected default 5/60, got %d/%d", cnt, win)
	}
	uLimits := &models.User{
		Limits: map[string]any{
			"connection_rate_limit_count":  float64(20),
			"connection_rate_limit_window": float64(120),
			"max_connections_per_user":     float64(50),
		},
	}
	if cnt, win := effectiveRateLimit(uLimits, 5, 60); cnt != 20 || win != 120 {
		t.Errorf("expected custom 20/120, got %d/%d", cnt, win)
	}
	if maxC := effectiveMaxConnectionsPerUser(nil, 10); maxC != 10 {
		t.Errorf("expected default 10, got %d", maxC)
	}
	if maxC := effectiveMaxConnectionsPerUser(uLimits, 10); maxC != 50 {
		t.Errorf("expected custom 50, got %d", maxC)
	}
}
