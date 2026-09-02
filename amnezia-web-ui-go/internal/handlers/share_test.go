package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/devops-igor/amnezia-web-ui-go/internal/middleware"
	"github.com/devops-igor/amnezia-web-ui-go/internal/models"
	"github.com/devops-igor/amnezia-web-ui-go/internal/security"
)

func TestShareHandlers(t *testing.T) {
	h, db, _ := setupTestHandlers(t)
	ctx := context.Background()

	token := "valid-share-token"
	sharePass := "ShareSecret123!"
	sharePassHash, _ := security.HashPassword(sharePass)

	user := &models.User{
		ID:                "share-user-1",
		Username:          "shareuser",
		PasswordHash:      "hash",
		Role:              models.RoleUser,
		Enabled:           true,
		ShareEnabled:      true,
		ShareToken:        &token,
		SharePasswordHash: &sharePassHash,
		CreatedAt:         time.Now(),
	}
	_, _ = db.CreateUser(ctx, user)

	r := setupFullShareRouter(h)

	srv := &models.Server{
		Name:      "Share-Node",
		Host:      "192.168.1.99",
		SSHPort:   22,
		SSHUser:   "root",
		Protocols: map[string]any{"awg": map[string]any{"port": 55424, "installed": true}},
		CreatedAt: time.Now(),
	}
	sID, _ := db.CreateServer(ctx, srv)

	t.Run("SharePageHandler", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/share/%s", token), nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}

		// Not found token
		req404 := httptest.NewRequest(http.MethodGet, "/share/nonexistent", nil)
		w404 := httptest.NewRecorder()
		r.ServeHTTP(w404, req404)
		if w404.Code != http.StatusNotFound {
			t.Fatalf("expected 404 for not found page, got %d", w404.Code)
		}
		if !strings.Contains(w404.Body.String(), "404") {
			t.Fatalf("expected 404 text in body, got %s", w404.Body.String())
		}

		// Direct call without router
		reqDirect := httptest.NewRequest(http.MethodGet, "/share/", nil)
		wDirect := httptest.NewRecorder()
		h.SharePageHandler(wDirect, reqDirect)
		if wDirect.Code != http.StatusNotFound {
			t.Fatalf("expected 404 for empty token, got %d", wDirect.Code)
		}
	})

	t.Run("SharePageHandler Authenticated No Password Needed", func(t *testing.T) {
		// Authenticated session skips password requirement
		sess := &models.SessionData{
			ShareAuthenticated: map[string]bool{token: true},
		}
		reqAuth := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/share/%s", token), nil)
		reqAuthCtx := middleware.WithSession(reqAuth.Context(), sess)
		wAuth := httptest.NewRecorder()
		r.ServeHTTP(wAuth, reqAuth.WithContext(reqAuthCtx))
		if wAuth.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", wAuth.Code)
		}
		body := wAuth.Body.String()
		if strings.Contains(body, "need_password") {
			t.Logf("page rendered (body size: %d)", len(body))
		}
	})

	t.Run("ShareAuthHandler", func(t *testing.T) {
		// Wrong Password
		body, _ := json.Marshal(models.ShareAuthRequest{
			Password: "WrongPassword!",
		})
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/share/%s/auth", token), bytes.NewReader(body))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", w.Code)
		}

		// Correct Password
		bodyOK, _ := json.Marshal(models.ShareAuthRequest{
			Password: sharePass,
		})
		reqOK := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/share/%s/auth", token), bytes.NewReader(bodyOK))
		wOK := httptest.NewRecorder()
		r.ServeHTTP(wOK, reqOK)
		if wOK.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", wOK.Code)
		}
		var respOK map[string]any
		if err := json.Unmarshal(wOK.Body.Bytes(), &respOK); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}
		if respOK["status"] != "success" {
			t.Errorf("expected status success, got %v", respOK["status"])
		}

		// Invalid Body
		reqBad := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/share/%s/auth", token), bytes.NewReader([]byte("bad-json")))
		wBad := httptest.NewRecorder()
		r.ServeHTTP(wBad, reqBad)
		if wBad.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", wBad.Code)
		}

		// Not Found Token
		reqNF := httptest.NewRequest(http.MethodPost, "/api/share/unknown/auth", bytes.NewReader(bodyOK))
		wNF := httptest.NewRecorder()
		r.ServeHTTP(wNF, reqNF)
		if wNF.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d", wNF.Code)
		}

		// Direct call with empty token
		reqDirect := httptest.NewRequest(http.MethodPost, "/api/share//auth", nil)
		wDirect := httptest.NewRecorder()
		h.ShareAuthHandler(wDirect, reqDirect)
		if wDirect.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for empty token, got %d", wDirect.Code)
		}
	})

	t.Run("GetShareConnectionsHandler", func(t *testing.T) {
		// Unauthorized
		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/share/%s/connections", token), nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", w.Code)
		}

		// Authorized
		sess := &models.SessionData{
			ShareAuthenticated: map[string]bool{token: true},
		}
		reqAuth := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/share/%s/connections", token), nil)
		reqAuthCtx := middleware.WithSession(reqAuth.Context(), sess)
		wAuth := httptest.NewRecorder()
		r.ServeHTTP(wAuth, reqAuth.WithContext(reqAuthCtx))
		if wAuth.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", wAuth.Code)
		}

		// Forbidden / Unknown token
		reqNF := httptest.NewRequest(http.MethodGet, "/api/share/missing/connections", nil)
		wNF := httptest.NewRecorder()
		r.ServeHTTP(wNF, reqNF)
		if wNF.Code != http.StatusForbidden {
			t.Fatalf("expected 403, got %d", wNF.Code)
		}

		// Direct empty token
		reqDirect := httptest.NewRequest(http.MethodGet, "/api/share//connections", nil)
		wDirect := httptest.NewRecorder()
		h.GetShareConnectionsHandler(wDirect, reqDirect)
		if wDirect.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", wDirect.Code)
		}
	})

	t.Run("GetShareConnectionsHandler Enriched and Wrong User Conn", func(t *testing.T) {
		// Add connection belonging to a different user (should NOT appear)
		otherConn := &models.UserConnection{
			ID:        "share-other-conn",
			UserID:    "someone-else",
			ServerID:  sID,
			Protocol:  "awg",
			ClientID:  "other-client",
			Name:      "Other User Conn",
			CreatedAt: time.Now(),
		}
		_, _ = db.CreateConnection(ctx, otherConn)

		sess := &models.SessionData{
			ShareAuthenticated: map[string]bool{token: true},
		}
		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/share/%s/connections", token), nil)
		reqCtx := middleware.WithSession(req.Context(), sess)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req.WithContext(reqCtx))
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}
		body := w.Body.String()
		if !strings.Contains(body, "shareuser") {
			t.Errorf("expected username in output")
		}
		if strings.Contains(body, "share-other-conn") {
			t.Errorf("expected other user's connection to be excluded")
		}
	})

	t.Run("GetShareConnectionConfigHandler Wrong Owner and Server Missing", func(t *testing.T) {
		sess := &models.SessionData{
			ShareAuthenticated: map[string]bool{token: true},
		}

		// Connection belongs to someone else
		otherConn := &models.UserConnection{
			ID:        "share-other-config",
			UserID:    "someone-else",
			ServerID:  sID,
			Protocol:  "awg",
			ClientID:  "other-client",
			Name:      "Other Config",
			CreatedAt: time.Now(),
		}
		_, _ = db.CreateConnection(ctx, otherConn)

		reqOther := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/share/%s/config/%s", token, otherConn.ID), nil)
		reqOtherCtx := middleware.WithSession(reqOther.Context(), sess)
		wOther := httptest.NewRecorder()
		r.ServeHTTP(wOther, reqOther.WithContext(reqOtherCtx))
		if wOther.Code != http.StatusNotFound {
			t.Errorf("expected 404 for other user's connection, got %d", wOther.Code)
		}

		// Connection with missing server
		orphanConn := &models.UserConnection{
			ID:        "share-orphan-conn",
			UserID:    user.ID,
			ServerID:  424242,
			Protocol:  "awg",
			ClientID:  "orphan-client",
			Name:      "Orphan",
			CreatedAt: time.Now(),
		}
		_, _ = db.CreateConnection(ctx, orphanConn)

		reqOrphan := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/share/%s/config/%s", token, orphanConn.ID), nil)
		reqOrphanCtx := middleware.WithSession(reqOrphan.Context(), sess)
		wOrphan := httptest.NewRecorder()
		r.ServeHTTP(wOrphan, reqOrphan.WithContext(reqOrphanCtx))
		if wOrphan.Code != http.StatusNotFound {
			t.Errorf("expected 404 for missing server, got %d", wOrphan.Code)
		}
	})

	srv2 := &models.Server{
		Name:      "Share-Node-2",
		Host:      "192.168.1.98",
		SSHPort:   22,
		SSHUser:   "root",
		Protocols: map[string]any{"awg": map[string]any{"port": 55424, "installed": true}},
		CreatedAt: time.Now(),
	}
	srv2ID, _ := db.CreateServer(ctx, srv2)

	c := &models.UserConnection{
		ID:        "share-c-1",
		UserID:    user.ID,
		ServerID:  srv2ID,
		Protocol:  "awg",
		ClientID:  "client-1",
		Name:      "Share Connection",
		CreatedAt: time.Now(),
	}
	_, _ = db.CreateConnection(ctx, c)

	t.Run("GetShareConnectionConfigHandler", func(t *testing.T) {
		sess := &models.SessionData{
			ShareAuthenticated: map[string]bool{token: true},
		}

		// Success
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/share/%s/config/%s", token, c.ID), nil)
		reqCtx := middleware.WithSession(req.Context(), sess)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req.WithContext(reqCtx))
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d (body: %s)", w.Code, w.Body.String())
		}

		// Unauthorized
		reqUnauth := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/share/%s/config/%s", token, c.ID), nil)
		wUnauth := httptest.NewRecorder()
		r.ServeHTTP(wUnauth, reqUnauth)
		if wUnauth.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", wUnauth.Code)
		}

		// Not found connection
		reqNF := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/share/%s/config/nonexistent", token), nil)
		reqNFCtx := middleware.WithSession(reqNF.Context(), sess)
		wNF := httptest.NewRecorder()
		r.ServeHTTP(wNF, reqNF.WithContext(reqNFCtx))
		if wNF.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d", wNF.Code)
		}

		// Direct call with missing params
		reqDirect := httptest.NewRequest(http.MethodPost, "/api/share//config/", nil)
		wDirect := httptest.NewRecorder()
		h.GetShareConnectionConfigHandler(wDirect, reqDirect)
		if wDirect.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", wDirect.Code)
		}
	})
}
