package router

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/devops-igor/amnezia-web-ui-go/internal/config"
	"github.com/devops-igor/amnezia-web-ui-go/internal/database"
	"github.com/devops-igor/amnezia-web-ui-go/internal/middleware"
	"github.com/devops-igor/amnezia-web-ui-go/internal/models"
	"strings"
)

const testSecretKey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func setupTestRouterDB(t *testing.T) (*database.DB, *config.Config) {
	t.Helper()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "router_test.db")
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

	// Create an admin user so setup redirect does not block normal routes
	ctx := context.Background()
	_, err = db.CreateUser(ctx, &models.User{
		ID:        "admin-id",
		Username:  "admin",
		Role:      models.RoleAdmin,
		Enabled:   true,
		CreatedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("failed to seed admin user: %v", err)
	}
	middleware.InvalidateSetupCache()

	return db, cfg
}

func TestRouterHealthEndpoint(t *testing.T) {
	db, cfg := setupTestRouterDB(t)
	r := NewRouter(cfg, db)

	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}

	var resp HealthResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Status != "ok" || resp.Version != "1.0.0" {
		t.Errorf("expected status 'ok' and version '1.0.0', got %+v", resp)
	}
}

func TestRouterVersionEndpoint(t *testing.T) {
	db, cfg := setupTestRouterDB(t)
	r := NewRouter(cfg, db)

	req := httptest.NewRequest(http.MethodGet, "/api/version", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}

	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp["version"] != "1.0.0" {
		t.Errorf("expected version '1.0.0', got %q", resp["version"])
	}
}

func TestRouterStaticAssetServing(t *testing.T) {
	db, cfg := setupTestRouterDB(t)
	r := NewRouter(cfg, db)

	req := httptest.NewRequest(http.MethodGet, "/static/favicon.svg", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200 for static asset, got %d", w.Code)
	}

	if cc := w.Header().Get("Cache-Control"); cc != "public, max-age=86400" {
		t.Errorf("expected Cache-Control header, got %q", cc)
	}
}

func TestRouterAuthProtectedRoutes(t *testing.T) {
	db, cfg := setupTestRouterDB(t)
	ctx := context.Background()
	_, _ = db.CreateUser(ctx, &models.User{
		ID:        "user-1",
		Username:  "user1",
		Role:      models.RoleUser,
		Enabled:   true,
		CreatedAt: time.Now(),
	})
	_, _ = db.CreateUser(ctx, &models.User{
		ID:        "admin-1",
		Username:  "admin1",
		Role:      models.RoleAdmin,
		Enabled:   true,
		CreatedAt: time.Now(),
	})
	r := NewRouter(cfg, db)

	// 1. Unauthenticated request to /api/connections/add with CSRF token -> 401
	reqUnauth := httptest.NewRequest(http.MethodPost, "/api/connections/add", nil)
	ctxUnauth := middleware.WithCSRFToken(reqUnauth.Context(), "token123")
	reqUnauth.AddCookie(&http.Cookie{Name: middleware.CSRFCookieName, Value: "token123"})
	reqUnauth.Header.Set(middleware.CSRFHeaderName, "token123")

	wUnauth := httptest.NewRecorder()
	r.ServeHTTP(wUnauth, reqUnauth.WithContext(ctxUnauth))
	if wUnauth.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for unauth /api/connections/add, got %d", wUnauth.Code)
	}

	// 2. Regular user request to /api/servers/add -> 403 Forbidden
	reqUser := httptest.NewRequest(http.MethodPost, "/api/servers/add", nil)
	ctxUser := middleware.WithSession(reqUser.Context(), &models.SessionData{
		UserID: "user-1",
		Role:   models.RoleUser,
	})
	ctxUser = middleware.WithCSRFToken(ctxUser, "token123")
	reqUser.AddCookie(&http.Cookie{Name: middleware.CSRFCookieName, Value: "token123"})
	reqUser.Header.Set(middleware.CSRFHeaderName, "token123")

	wUser := httptest.NewRecorder()
	r.ServeHTTP(wUser, reqUser.WithContext(ctxUser))
	if wUser.Code != http.StatusForbidden {
		t.Errorf("expected 403 for regular user on admin endpoint, got %d", wUser.Code)
	}

	// 3. Admin user request to /api/settings -> 200 OK
	reqAdmin := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	ctxAdmin := middleware.WithSession(reqAdmin.Context(), &models.SessionData{
		UserID: "admin-1",
		Role:   models.RoleAdmin,
	})
	ctxAdmin = middleware.WithCSRFToken(ctxAdmin, "token123")
	reqAdmin.AddCookie(&http.Cookie{Name: middleware.CSRFCookieName, Value: "token123"})
	reqAdmin.Header.Set(middleware.CSRFHeaderName, "token123")

	wAdmin := httptest.NewRecorder()
	r.ServeHTTP(wAdmin, reqAdmin.WithContext(ctxAdmin))
	if wAdmin.Code != http.StatusOK {
		t.Errorf("expected 200 for admin user on /api/settings, got %d", wAdmin.Code)
	}
}

func TestSetLangEndpoint(t *testing.T) {
	db, cfg := setupTestRouterDB(t)
	r := NewRouter(cfg, db)

	req := httptest.NewRequest(http.MethodGet, "/set_lang/ru", nil)
	req.Header.Set("Referer", "/dashboard")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusFound {
		t.Errorf("expected 302 redirect for set_lang, got %d", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/dashboard" {
		t.Errorf("expected redirect to /dashboard, got %q", loc)
	}

	cookies := w.Result().Cookies()
	var langCookieFound bool
	for _, c := range cookies {
		if c.Name == "lang" && c.Value == "ru" {
			langCookieFound = true
			break
		}
	}
	if !langCookieFound {
		t.Errorf("expected lang cookie to be set to ru")
	}
}

func TestCleanReferer(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"empty", "", "/"},
		{"relative path", "/settings", "/settings"},
		{"relative path with query", "/server/1?tab=logs", "/server/1?tab=logs"},
		{"absolute http url", "http://evil.com/hack", "/hack"},
		{"absolute https url", "https://example.com/dashboard?view=grid", "/dashboard?view=grid"},
		{"invalid url", "://invalid-url", "/"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CleanReferer(tt.input)
			if got != tt.want {
				t.Errorf("CleanReferer(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		bytes int64
		want  string
	}{
		{0, "0 B"},
		{500, "500 B"},
		{1024, "1.00 KB"},
		{1536, "1.50 KB"},
		{1048576, "1.00 MB"},
		{1073741824, "1.00 GB"},
		{-1073741824, "-1.00 GB"},
	}

	for _, tt := range tests {
		got := FormatBytes(tt.bytes)
		if got != tt.want {
			t.Errorf("FormatBytes(%d) = %q, want %q", tt.bytes, got, tt.want)
		}
	}
}

func TestFormatTime(t *testing.T) {
	if got := FormatTime(time.Time{}); got != "" {
		t.Errorf("expected empty string for zero time, got %q", got)
	}

	fixed := time.Date(2026, 8, 28, 15, 4, 5, 0, time.UTC)
	if got := FormatTime(fixed); got != "2026-08-28 15:04:05" {
		t.Errorf("expected '2026-08-28 15:04:05', got %q", got)
	}
}

func TestRouterEndpointDispatch(t *testing.T) {
	db, cfg := setupTestRouterDB(t)
	r := NewRouter(cfg, db)

	adminSession := &models.SessionData{
		UserID: "admin-id",
		Role:   models.RoleAdmin,
	}

	endpoints := []struct {
		method         string
		path           string
		session        *models.SessionData
		body           any
		expectedStatus int
	}{
		{http.MethodGet, "/api/health", nil, nil, http.StatusOK},
		{http.MethodGet, "/api/version", nil, nil, http.StatusOK},
		{http.MethodGet, "/api/auth/captcha", nil, nil, http.StatusOK},
		{http.MethodGet, "/api/leaderboard", nil, nil, http.StatusOK},
		{http.MethodGet, "/api/settings", adminSession, nil, http.StatusOK},
		{http.MethodGet, "/api/users", adminSession, nil, http.StatusOK},
		{http.MethodGet, "/api/vpn/status", adminSession, nil, http.StatusOK},
		{http.MethodGet, "/api/vpn/backends", adminSession, nil, http.StatusOK},
		{http.MethodGet, "/api/vpn/tunnels", adminSession, nil, http.StatusOK},
		{http.MethodGet, "/api/vpn/config", adminSession, nil, http.StatusOK},
		{http.MethodGet, "/api/my/connections", adminSession, nil, http.StatusOK},
		{http.MethodGet, "/api/vpn/my-connection", adminSession, nil, http.StatusOK},
	}

	for _, ep := range endpoints {
		t.Run(ep.method+" "+ep.path, func(t *testing.T) {
			var bodyBuf *bytes.Buffer
			if ep.body != nil {
				b, _ := json.Marshal(ep.body)
				bodyBuf = bytes.NewBuffer(b)
			} else {
				bodyBuf = bytes.NewBuffer(nil)
			}

			req := httptest.NewRequest(ep.method, ep.path, bodyBuf)
			ctx := req.Context()
			if ep.session != nil {
				ctx = middleware.WithSession(ctx, ep.session)
			}
			ctx = middleware.WithCSRFToken(ctx, "token123")
			req.AddCookie(&http.Cookie{Name: middleware.CSRFCookieName, Value: "token123"})
			req.Header.Set(middleware.CSRFHeaderName, "token123")

			w := httptest.NewRecorder()
			r.ServeHTTP(w, req.WithContext(ctx))

			if w.Code != ep.expectedStatus {
				t.Errorf("%s %s returned status %d, want %d", ep.method, ep.path, w.Code, ep.expectedStatus)
			}
		})
	}
}

func TestAuthSetupEndpointWithZeroUsers(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "fresh_setup.db")
	db, err := database.Open(dbPath, testSecretKey)
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}
	defer db.Close()
	middleware.InvalidateSetupCache()

	cfg := &config.Config{
		AppVersion: "1.0.0",
		Host:       "127.0.0.1",
		Port:       5000,
		SecretKey:  testSecretKey,
	}
	r := NewRouter(cfg, db)

	body, _ := json.Marshal(models.SetupRequest{
		Username:        "newadmin",
		Password:        "SecurePassword123!",
		ConfirmPassword: "SecurePassword123!",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/auth/setup", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 for /api/auth/setup with 0 users, got %d (body: %s)", w.Code, w.Body.String())
	}
}

func TestRouterServersRoleBasedAccess(t *testing.T) {
	db, cfg := setupTestRouterDB(t)
	ctx := context.Background()

	// Seed regular user
	_, err := db.CreateUser(ctx, &models.User{
		ID:        "reg-user-1",
		Username:  "regularuser",
		Role:      models.RoleUser,
		Enabled:   true,
		CreatedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("failed to seed regular user: %v", err)
	}

	// Seed managed server with protocols
	sID, err := db.CreateServer(ctx, &models.Server{
		Name:      "Production Host 1",
		Host:      "192.168.1.50",
		SSHUser:   "root",
		SSHPort:   2222,
		SSHPass:   "SuperSecretPass!",
		SSHKey:    "PrivateKeyData",
		CreatedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("failed to seed test server: %v", err)
	}
	_ = db.UpdateServerProtocols(ctx, sID, map[string]any{
		"awg": map[string]any{
			"installed":  true,
			"port":       51820,
			"awg_params": map[string]any{"Jc": 3, "S1": 15},
		},
		"dns": map[string]any{
			"installed": true,
		},
	})
	_ = db.UpdateServerReachability(ctx, sID, models.ReachabilityOnline)

	r := NewRouter(cfg, db)

	// 1. Regular user calling GET /api/servers -> 200 OK + sanitized
	for _, path := range []string{"/api/servers", "/api/servers/"} {
		t.Run("Regular User "+path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			userCtx := middleware.WithSession(req.Context(), &models.SessionData{
				UserID:   "reg-user-1",
				Username: "regularuser",
				Role:     models.RoleUser,
			})
			req = req.WithContext(userCtx)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Fatalf("expected 200 OK for regular user on %s, got %d (body: %s)", path, w.Code, w.Body.String())
			}

			var servers []models.ServerItemResponse
			if err := json.Unmarshal(w.Body.Bytes(), &servers); err != nil {
				t.Fatalf("failed to decode response: %v", err)
			}
			if len(servers) != 1 {
				t.Fatalf("expected 1 server, got %d", len(servers))
			}
			s := servers[0]
			if s.Host != "" || s.SSHPort != 0 || s.Username != "" {
				t.Errorf("expected sensitive host/port/username stripped for regular user, got %+v", s)
			}
			if s.CreatedAt != nil {
				t.Errorf("expected created_at omitted for regular user, got %+v", s.CreatedAt)
			}
			if s.Status != "online" {
				t.Errorf("expected status 'online', got %q", s.Status)
			}
			if s.Reachable == nil || !*s.Reachable {
				t.Errorf("expected reachable true, got %v", s.Reachable)
			}
			awgProto, ok := s.Protocols["awg"].(map[string]any)
			if !ok || awgProto["installed"] != true {
				t.Fatalf("expected awg to have installed: true, got %+v", s.Protocols["awg"])
			}
			if _, hasPort := awgProto["port"]; hasPort {
				t.Errorf("leaked internal port to regular user: %+v", awgProto)
			}
			if _, hasParams := awgProto["awg_params"]; hasParams {
				t.Errorf("leaked awg_params to regular user: %+v", awgProto)
			}
		})
	}

	// 2. Admin user calling GET /api/servers -> 200 OK + full details
	for _, path := range []string{"/api/servers", "/api/servers/"} {
		t.Run("Admin User "+path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			adminCtx := middleware.WithSession(req.Context(), &models.SessionData{
				UserID:   "admin-id",
				Username: "admin",
				Role:     models.RoleAdmin,
			})
			req = req.WithContext(adminCtx)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Fatalf("expected 200 OK for admin on %s, got %d (body: %s)", path, w.Code, w.Body.String())
			}

			var servers []models.ServerItemResponse
			if err := json.Unmarshal(w.Body.Bytes(), &servers); err != nil {
				t.Fatalf("failed to decode admin response: %v", err)
			}
			if len(servers) != 1 {
				t.Fatalf("expected 1 server, got %d", len(servers))
			}
			s := servers[0]
			if s.Host != "192.168.1.50" || s.SSHPort != 2222 || s.Username != "root" {
				t.Errorf("expected full host/port/username for admin, got %+v", s)
			}
			if s.CreatedAt == nil {
				t.Errorf("expected created_at present for admin, got nil")
			}
			if s.Status == "" {
				t.Errorf("expected status present for admin, got empty")
			}
			bodyStr := w.Body.String()
			if strings.Contains(bodyStr, "SuperSecretPass") || strings.Contains(bodyStr, "PrivateKeyData") {
				t.Errorf("expected no decrypted credentials in admin response: %s", bodyStr)
			}
		})
	}

	// 3. Unauthenticated client calling GET /api/servers -> 401 Unauthorized
	for _, path := range []string{"/api/servers", "/api/servers/"} {
		t.Run("Unauthenticated "+path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code != http.StatusUnauthorized {
				t.Errorf("expected 401 Unauthorized for unauth on %s, got %d", path, w.Code)
			}
		})
	}

	// 4. Regular user calling mutating endpoint POST /api/servers/add -> 403 Forbidden
	t.Run("Regular User POST /api/servers/add", func(t *testing.T) {
		body, _ := json.Marshal(map[string]any{
			"host":     "10.0.0.1",
			"ssh_user": "root",
			"ssh_port": 22,
			"ssh_pass": "pass",
		})
		req := httptest.NewRequest(http.MethodPost, "/api/servers/add", bytes.NewReader(body))
		userCtx := middleware.WithSession(req.Context(), &models.SessionData{
			UserID:   "reg-user-1",
			Username: "regularuser",
			Role:     models.RoleUser,
		})
		userCtx = middleware.WithCSRFToken(userCtx, "csrf123")
		req.AddCookie(&http.Cookie{Name: middleware.CSRFCookieName, Value: "csrf123"})
		req.Header.Set(middleware.CSRFHeaderName, "csrf123")
		req.Header.Set("Content-Type", "application/json")
		req = req.WithContext(userCtx)

		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusForbidden {
			t.Errorf("expected 403 Forbidden for regular user on /api/servers/add, got %d (body: %s)", w.Code, w.Body.String())
		}
	})

	// 5. Unauthenticated client calling POST /api/servers/add -> 401 Unauthorized
	t.Run("Unauthenticated POST /api/servers/add", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/servers/add", nil)
		ctx := middleware.WithCSRFToken(req.Context(), "csrf123")
		req.AddCookie(&http.Cookie{Name: middleware.CSRFCookieName, Value: "csrf123"})
		req.Header.Set(middleware.CSRFHeaderName, "csrf123")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req.WithContext(ctx))

		if w.Code != http.StatusUnauthorized {
			t.Errorf("expected 401 Unauthorized for unauthenticated on /api/servers/add, got %d", w.Code)
		}
	})
}
