package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/devops-igor/amnezia-web-ui-go/internal/models"
	"github.com/devops-igor/amnezia-web-ui-go/internal/security"
)

const testSecretKey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestSessionMiddleware(t *testing.T) {
	sessionData := &models.SessionData{
		UserID:   "user-999",
		Username: "alice",
		Role:     models.RoleAdmin,
	}

	encodedCookie, err := security.EncodeSession(sessionData.ToMap(), testSecretKey)
	if err != nil {
		t.Fatalf("failed to encode session: %v", err)
	}

	middlewareFunc := Session(testSecretKey)

	var extractedSession *models.SessionData
	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		extractedSession = GetSession(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	// Case 1: Valid signed cookie
	req := httptest.NewRequest(http.MethodGet, "/api/users", nil)
	req.AddCookie(&http.Cookie{
		Name:  SessionCookieName,
		Value: encodedCookie,
	})
	w := httptest.NewRecorder()
	middlewareFunc(testHandler).ServeHTTP(w, req)

	if extractedSession == nil || extractedSession.UserID != "user-999" || extractedSession.Role != models.RoleAdmin {
		t.Errorf("expected session to be extracted, got %+v", extractedSession)
	}

	// Case 2: Tampered cookie
	extractedSession = nil
	req = httptest.NewRequest(http.MethodGet, "/api/users", nil)
	req.AddCookie(&http.Cookie{
		Name:  SessionCookieName,
		Value: encodedCookie + "tampered",
	})
	w = httptest.NewRecorder()
	middlewareFunc(testHandler).ServeHTTP(w, req)

	if extractedSession != nil {
		t.Errorf("expected nil session for tampered cookie, got %+v", extractedSession)
	}

	// Case 3: Missing cookie
	extractedSession = nil
	req = httptest.NewRequest(http.MethodGet, "/api/users", nil)
	w = httptest.NewRecorder()
	middlewareFunc(testHandler).ServeHTTP(w, req)

	if extractedSession != nil {
		t.Errorf("expected nil session for missing cookie, got %+v", extractedSession)
	}
}

func TestSetAndClearSessionCookie(t *testing.T) {
	w := httptest.NewRecorder()
	sessionData := &models.SessionData{
		UserID:   "u-1",
		Username: "bob",
		Role:     models.RoleUser,
	}

	if err := SetSessionCookie(w, sessionData, testSecretKey, true, 3600); err != nil {
		t.Fatalf("SetSessionCookie failed: %v", err)
	}

	cookies := w.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != SessionCookieName || cookies[0].Value == "" {
		t.Fatalf("expected 1 session cookie, got %+v", cookies)
	}
	if !cookies[0].HttpOnly || !cookies[0].Secure {
		t.Errorf("expected cookie to be HttpOnly and Secure")
	}

	// Clear cookie
	wClear := httptest.NewRecorder()
	ClearSessionCookie(wClear)
	clearCookies := wClear.Result().Cookies()
	if len(clearCookies) != 1 || clearCookies[0].MaxAge != -1 || clearCookies[0].Value != "" {
		t.Errorf("ClearSessionCookie failed, got %+v", clearCookies)
	}
}

func TestRequireAuth(t *testing.T) {
	okHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	})
	protected := RequireAuth(okHandler)

	// API unauthenticated -> 401 JSON
	reqAPI := httptest.NewRequest(http.MethodGet, "/api/connections", nil)
	wAPI := httptest.NewRecorder()
	protected.ServeHTTP(wAPI, reqAPI)
	if wAPI.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for unauth API request, got %d", wAPI.Code)
	}
	var errResp ErrorResponse
	_ = json.Unmarshal(wAPI.Body.Bytes(), &errResp)
	if errResp.Error != "unauthorized" {
		t.Errorf("expected error=unauthorized, got %q", errResp.Error)
	}

	// HTML unauthenticated -> 302 to /login
	reqHTML := httptest.NewRequest(http.MethodGet, "/my", nil)
	reqHTML.Header.Set("Accept", "text/html")
	wHTML := httptest.NewRecorder()
	protected.ServeHTTP(wHTML, reqHTML)
	if wHTML.Code != http.StatusFound {
		t.Errorf("expected 302 redirect for unauth HTML request, got %d", wHTML.Code)
	}
	if loc := wHTML.Header().Get("Location"); loc != "/login" {
		t.Errorf("expected redirect to /login, got %q", loc)
	}

	// Authenticated -> 200 OK
	reqAuth := httptest.NewRequest(http.MethodGet, "/api/connections", nil)
	ctx := WithSession(reqAuth.Context(), &models.SessionData{UserID: "u-1", Role: models.RoleUser})
	wAuth := httptest.NewRecorder()
	protected.ServeHTTP(wAuth, reqAuth.WithContext(ctx))
	if wAuth.Code != http.StatusOK {
		t.Errorf("expected 200 for authenticated user, got %d", wAuth.Code)
	}
}

func TestRequireAdmin(t *testing.T) {
	okHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	adminProtected := RequireAdmin(okHandler)

	// Unauthenticated -> 401
	reqUnauth := httptest.NewRequest(http.MethodGet, "/api/servers", nil)
	wUnauth := httptest.NewRecorder()
	adminProtected.ServeHTTP(wUnauth, reqUnauth)
	if wUnauth.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for unauthenticated, got %d", wUnauth.Code)
	}

	// Authenticated regular user -> 403
	reqUser := httptest.NewRequest(http.MethodGet, "/api/servers", nil)
	ctxUser := WithSession(reqUser.Context(), &models.SessionData{UserID: "u-1", Role: models.RoleUser})
	wUser := httptest.NewRecorder()
	adminProtected.ServeHTTP(wUser, reqUser.WithContext(ctxUser))
	if wUser.Code != http.StatusForbidden {
		t.Errorf("expected 403 for regular user, got %d", wUser.Code)
	}

	// Authenticated admin user -> 200
	reqAdmin := httptest.NewRequest(http.MethodGet, "/api/servers", nil)
	ctxAdmin := WithSession(reqAdmin.Context(), &models.SessionData{UserID: "admin-1", Role: models.RoleAdmin})
	wAdmin := httptest.NewRecorder()
	adminProtected.ServeHTTP(wAdmin, reqAdmin.WithContext(ctxAdmin))
	if wAdmin.Code != http.StatusOK {
		t.Errorf("expected 200 for admin user, got %d", wAdmin.Code)
	}
}

func TestRequireAdminOrSupport(t *testing.T) {
	okHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	supportProtected := RequireAdminOrSupport(okHandler)

	// User role -> 403
	reqUser := httptest.NewRequest(http.MethodGet, "/api/servers", nil)
	ctxUser := WithSession(reqUser.Context(), &models.SessionData{UserID: "u-1", Role: models.RoleUser})
	wUser := httptest.NewRecorder()
	supportProtected.ServeHTTP(wUser, reqUser.WithContext(ctxUser))
	if wUser.Code != http.StatusForbidden {
		t.Errorf("expected 403 for user role, got %d", wUser.Code)
	}

	// Support role -> 200
	reqSupport := httptest.NewRequest(http.MethodGet, "/api/servers", nil)
	ctxSupport := WithSession(reqSupport.Context(), &models.SessionData{UserID: "supp-1", Role: models.RoleSupport})
	wSupport := httptest.NewRecorder()
	supportProtected.ServeHTTP(wSupport, reqSupport.WithContext(ctxSupport))
	if wSupport.Code != http.StatusOK {
		t.Errorf("expected 200 for support role, got %d", wSupport.Code)
	}

	// Admin role -> 200
	reqAdmin := httptest.NewRequest(http.MethodGet, "/api/servers", nil)
	ctxAdmin := WithSession(reqAdmin.Context(), &models.SessionData{UserID: "adm-1", Role: models.RoleAdmin})
	wAdmin := httptest.NewRecorder()
	supportProtected.ServeHTTP(wAdmin, reqAdmin.WithContext(ctxAdmin))
	if wAdmin.Code != http.StatusOK {
		t.Errorf("expected 200 for admin role, got %d", wAdmin.Code)
	}
}
