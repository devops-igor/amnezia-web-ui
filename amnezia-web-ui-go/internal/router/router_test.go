package router

import (
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
	// Inject CSRF token to pass CSRF middleware
	ctxUser = middleware.WithCSRFToken(ctxUser, "token123")
	reqUser.AddCookie(&http.Cookie{Name: middleware.CSRFCookieName, Value: "token123"})
	reqUser.Header.Set(middleware.CSRFHeaderName, "token123")

	wUser := httptest.NewRecorder()
	r.ServeHTTP(wUser, reqUser.WithContext(ctxUser))
	if wUser.Code != http.StatusForbidden {
		t.Errorf("expected 403 for regular user on admin endpoint, got %d", wUser.Code)
	}

	// 3. Admin user request to /api/servers/add -> 200 OK
	reqAdmin := httptest.NewRequest(http.MethodPost, "/api/servers/add", nil)
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
		t.Errorf("expected 200 for admin user on /api/servers/add, got %d", wAdmin.Code)
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
		input string
		want  string
	}{
		{"", "/"},
		{"/my?filter=active", "/my?filter=active"},
		{"https://evil.com/phish", "/phish"},
		{"http://attacker.com", "/"},
		{"/servers/1", "/servers/1"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := CleanReferer(tt.input)
			if got != tt.want {
				t.Errorf("CleanReferer(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestFormatBytesAndHelpers(t *testing.T) {
	if s := FormatBytes(0); s != "0 B" {
		t.Errorf("FormatBytes(0) = %q, want '0 B'", s)
	}
	if s := FormatBytes(500); s != "500 B" {
		t.Errorf("FormatBytes(500) = %q, want '500 B'", s)
	}
	if s := FormatBytes(1048576); s != "1.00 MB" {
		t.Errorf("FormatBytes(1048576) = %q, want '1.00 MB'", s)
	}
	if s := FormatBytes(-1048576); s != "-1.00 MB" {
		t.Errorf("FormatBytes(-1048576) = %q, want '-1.00 MB'", s)
	}

	tm := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	if s := FormatTime(tm); s != "2026-08-28 12:00:00" {
		t.Errorf("FormatTime failed, got %q", s)
	}
	if s := FormatTime(time.Time{}); s != "" {
		t.Errorf("FormatTime for zero time should return empty string")
	}
}

func TestTemplateRenderingAndPages(t *testing.T) {
	db, cfg := setupTestRouterDB(t)
	r := NewRouter(cfg, db)

	// Admin session for admin pages
	adminSession := &models.SessionData{
		UserID: "admin-1",
		Role:   models.RoleAdmin,
	}

	pages := []struct {
		path         string
		authSession  *models.SessionData
		expectedCode int
	}{
		{"/login", nil, http.StatusOK},
		{"/setup", nil, http.StatusFound}, // Setup is done, redirects to /login
		{"/leaderboard", nil, http.StatusOK},
		{"/share/testtoken123", nil, http.StatusOK},
		{"/my", adminSession, http.StatusOK},
		{"/change-password", adminSession, http.StatusOK},
		{"/", adminSession, http.StatusOK},
		{"/users", adminSession, http.StatusOK},
		{"/settings", adminSession, http.StatusOK},
		{"/server/1", adminSession, http.StatusOK},
		{"/logout", nil, http.StatusFound},
	}

	for _, p := range pages {
		t.Run("Page: "+p.path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, p.path, nil)
			if p.authSession != nil {
				req = req.WithContext(middleware.WithSession(req.Context(), p.authSession))
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code != p.expectedCode {
				t.Errorf("GET %s returned code %d, want %d", p.path, w.Code, p.expectedCode)
			}
		})
	}
}

func TestAPIEndpointCatalogSkeletons(t *testing.T) {
	db, cfg := setupTestRouterDB(t)
	r := NewRouter(cfg, db)

	adminSession := &models.SessionData{
		UserID: "admin-1",
		Role:   models.RoleAdmin,
	}

	endpoints := []struct {
		method  string
		path    string
		session *models.SessionData
	}{
		{http.MethodGet, "/api/auth/captcha", nil},
		{http.MethodPost, "/api/auth/login", nil},
		{http.MethodGet, "/api/leaderboard", nil},
		{http.MethodPost, "/api/share/token123/auth", nil},
		{http.MethodGet, "/api/share/token123/connections", nil},
		{http.MethodPost, "/api/share/token123/config/c1", nil},
		{http.MethodGet, "/api/settings", adminSession},
		{http.MethodPost, "/api/settings/save", adminSession},
		{http.MethodPost, "/api/settings/sync_now", adminSession},
		{http.MethodPost, "/api/settings/sync_delete", adminSession},
		{http.MethodGet, "/api/settings/backup/download", adminSession},
		{http.MethodPost, "/api/settings/backup/restore", adminSession},
		{http.MethodPost, "/api/users/add", adminSession},
		{http.MethodPost, "/api/users/u1/update", adminSession},
		{http.MethodPost, "/api/users/u1/delete", adminSession},
		{http.MethodPost, "/api/users/u1/toggle", adminSession},
		{http.MethodPost, "/api/users/u1/connections/add", adminSession},
		{http.MethodGet, "/api/users/u1/connections", adminSession},
		{http.MethodPost, "/api/users/u1/share/setup", adminSession},
		{http.MethodPost, "/api/servers/add", adminSession},
		{http.MethodPost, "/api/servers/confirm-fingerprint", adminSession},
		{http.MethodPost, "/api/servers/1/delete", adminSession},
		{http.MethodPost, "/api/servers/1/reboot", adminSession},
		{http.MethodPost, "/api/servers/1/clear", adminSession},
		{http.MethodPost, "/api/servers/1/stats", adminSession},
		{http.MethodPost, "/api/servers/1/check", adminSession},
		{http.MethodPost, "/api/servers/1/install", adminSession},
		{http.MethodPost, "/api/servers/1/uninstall", adminSession},
		{http.MethodPost, "/api/servers/1/container/toggle", adminSession},
		{http.MethodPost, "/api/servers/1/server_config", adminSession},
		{http.MethodPost, "/api/servers/1/server_config/save", adminSession},
		{http.MethodGet, "/api/servers/1/connections", adminSession},
		{http.MethodPost, "/api/servers/1/connections/add", adminSession},
		{http.MethodPost, "/api/servers/1/connections/c1/rotate-mimicry", adminSession},
		{http.MethodGet, "/api/servers/1/reachability", adminSession},
		{http.MethodPost, "/api/servers/1/connections/auto-trial", adminSession},
		{http.MethodPost, "/api/servers/1/connections/kit", adminSession},
		{http.MethodPost, "/api/servers/1/connections/remove", adminSession},
		{http.MethodPost, "/api/servers/1/connections/edit", adminSession},
		{http.MethodPost, "/api/servers/1/connections/config", adminSession},
		{http.MethodPost, "/api/servers/1/connections/toggle", adminSession},
		{http.MethodGet, "/api/servers/1/awg/clients", adminSession},
		{http.MethodPatch, "/api/servers/1/connections/speed-limit", adminSession},
		{http.MethodGet, "/api/servers/1/awg/speed-limit-config", adminSession},
		{http.MethodPatch, "/api/servers/1/awg/speed-limit-config", adminSession},
		{http.MethodPost, "/api/servers/1/awg/apply-default-speed-limits", adminSession},
		{http.MethodGet, "/api/vpn/status", adminSession},
		{http.MethodGet, "/api/vpn/backends", adminSession},
		{http.MethodPost, "/api/vpn/backends/1/enable", adminSession},
		{http.MethodPost, "/api/vpn/backends/1/disable", adminSession},
		{http.MethodGet, "/api/vpn/tunnels", adminSession},
		{http.MethodGet, "/api/vpn/config", adminSession},
		{http.MethodPost, "/api/vpn/config", adminSession},
		{http.MethodPost, "/api/vpn/disconnect", adminSession},
		{http.MethodGet, "/api/vpn/my-connection", adminSession},
		{http.MethodGet, "/api/vpn/my-config", adminSession},
	}

	for _, ep := range endpoints {
		t.Run(ep.method+" "+ep.path, func(t *testing.T) {
			req := httptest.NewRequest(ep.method, ep.path, nil)
			ctx := req.Context()
			if ep.session != nil {
				ctx = middleware.WithSession(ctx, ep.session)
			}
			ctx = middleware.WithCSRFToken(ctx, "token123")
			req.AddCookie(&http.Cookie{Name: middleware.CSRFCookieName, Value: "token123"})
			req.Header.Set(middleware.CSRFHeaderName, "token123")

			w := httptest.NewRecorder()
			r.ServeHTTP(w, req.WithContext(ctx))

			if w.Code != http.StatusOK {
				t.Errorf("%s %s returned status %d, want 200", ep.method, ep.path, w.Code)
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

	req := httptest.NewRequest(http.MethodPost, "/api/auth/setup", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 for /api/auth/setup with 0 users, got %d", w.Code)
	}
}
