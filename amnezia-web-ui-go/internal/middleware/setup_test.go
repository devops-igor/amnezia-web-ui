package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/devops-igor/amnezia-web-ui-go/internal/database"
	"github.com/devops-igor/amnezia-web-ui-go/internal/models"
)

func createTestDB(t *testing.T) *database.DB {
	t.Helper()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	db, err := database.Open(dbPath, testSecretKey)
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})
	return db
}

func TestSetupRedirectMiddleware(t *testing.T) {
	db := createTestDB(t)
	InvalidateSetupCache()

	okHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	})
	middlewareHandler := SetupRedirect(db)(okHandler)

	// 1. With 0 users in DB:
	// - Allowed routes succeed
	allowedPaths := []string{
		"/setup",
		"/api/auth/setup",
		"/api/health",
		"/api/version",
		"/static/bundle.css",
		"/set_lang/en",
	}
	for _, p := range allowedPaths {
		t.Run("0 Users Allowed: "+p, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, p, nil)
			w := httptest.NewRecorder()
			middlewareHandler.ServeHTTP(w, req)
			if w.Code != http.StatusOK {
				t.Errorf("expected 200 for %s during setup, got %d", p, w.Code)
			}
		})
	}

	// - HTML page request to /dashboard -> 302 to /setup
	reqHTML := httptest.NewRequest(http.MethodGet, "/", nil)
	reqHTML.Header.Set("Accept", "text/html")
	wHTML := httptest.NewRecorder()
	middlewareHandler.ServeHTTP(wHTML, reqHTML)
	if wHTML.Code != http.StatusFound || wHTML.Header().Get("Location") != "/setup" {
		t.Errorf("expected 302 redirect to /setup, got status %d location %q", wHTML.Code, wHTML.Header().Get("Location"))
	}

	// - API request to /api/servers -> 403 Forbidden
	reqAPI := httptest.NewRequest(http.MethodGet, "/api/servers", nil)
	wAPI := httptest.NewRecorder()
	middlewareHandler.ServeHTTP(wAPI, reqAPI)
	if wAPI.Code != http.StatusForbidden {
		t.Errorf("expected 403 for API request during setup, got %d", wAPI.Code)
	}

	// 2. Create an admin user in DB
	ctx := context.Background()
	_, err := db.CreateUser(ctx, &models.User{
		ID:        "admin-uuid-1",
		Username:  "admin",
		Role:      models.RoleAdmin,
		Enabled:   true,
		CreatedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("failed to create user: %v", err)
	}

	// Invalidate cache and test with >0 users
	InvalidateSetupCache()

	// - GET /setup -> 302 redirect to /login
	reqSetupAfter := httptest.NewRequest(http.MethodGet, "/setup", nil)
	wSetupAfter := httptest.NewRecorder()
	middlewareHandler.ServeHTTP(wSetupAfter, reqSetupAfter)
	if wSetupAfter.Code != http.StatusFound || wSetupAfter.Header().Get("Location") != "/login" {
		t.Errorf("expected 302 redirect to /login for /setup once users exist, got %d", wSetupAfter.Code)
	}

	// - POST /api/auth/setup -> 403 Forbidden
	reqAPISetupAfter := httptest.NewRequest(http.MethodPost, "/api/auth/setup", nil)
	wAPISetupAfter := httptest.NewRecorder()
	middlewareHandler.ServeHTTP(wAPISetupAfter, reqAPISetupAfter)
	if wAPISetupAfter.Code != http.StatusForbidden {
		t.Errorf("expected 403 for /api/auth/setup once users exist, got %d", wAPISetupAfter.Code)
	}

	// - Normal route -> 200 OK
	reqNormal := httptest.NewRequest(http.MethodGet, "/api/servers", nil)
	wNormal := httptest.NewRecorder()
	middlewareHandler.ServeHTTP(wNormal, reqNormal)
	if wNormal.Code != http.StatusOK {
		t.Errorf("expected 200 for normal route after setup, got %d", wNormal.Code)
	}

	// Check IsSetupCompleted helper
	if !IsSetupCompleted() {
		t.Errorf("expected IsSetupCompleted() to be true")
	}
	MarkSetupCompleted()
	if !IsSetupCompleted() {
		t.Errorf("expected IsSetupCompleted() to be true after MarkSetupCompleted")
	}
}
