package handlers

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/devops-igor/amnezia-web-ui-go/internal/middleware"
	"github.com/devops-igor/amnezia-web-ui-go/internal/models"
	"github.com/go-chi/chi/v5"
)

func TestPageHandlers(t *testing.T) {
	h, db, _ := setupTestHandlers(t)
	ctx := context.Background()

	srv := &models.Server{
		Name:      "Main-Server",
		Host:      "192.168.1.200",
		SSHPort:   22,
		SSHUser:   "root",
		CreatedAt: time.Now(),
	}
	sID, _ := db.CreateServer(ctx, srv)

	adminSess := &models.SessionData{
		UserID: "admin-id",
		Role:   models.RoleAdmin,
	}

	t.Run("IndexPageHandler Admin", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		reqCtx := middleware.WithSession(req.Context(), adminSess)
		w := httptest.NewRecorder()
		h.IndexPageHandler(w, req.WithContext(reqCtx))

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}
	})

	t.Run("IndexPageHandler User Redirect", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		reqCtx := middleware.WithSession(req.Context(), &models.SessionData{
			UserID: "u-1",
			Role:   models.RoleUser,
		})
		w := httptest.NewRecorder()
		h.IndexPageHandler(w, req.WithContext(reqCtx))

		if w.Code != http.StatusFound || w.Header().Get("Location") != "/my" {
			t.Fatalf("expected 302 redirect to /my, got %d", w.Code)
		}
	})

	t.Run("UsersPageHandler", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/users", nil)
		reqCtx := middleware.WithSession(req.Context(), adminSess)
		w := httptest.NewRecorder()
		h.UsersPageHandler(w, req.WithContext(reqCtx))

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}
	})

	t.Run("ServerPageHandler", func(t *testing.T) {
		r := chi.NewRouter()
		r.Get("/server/{server_id}", h.ServerPageHandler)

		// Success
		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/server/%d", sID), nil)
		reqCtx := middleware.WithSession(req.Context(), adminSess)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req.WithContext(reqCtx))
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}

		// Invalid ID redirect
		reqBad := httptest.NewRequest(http.MethodGet, "/server/invalid-id", nil)
		wBad := httptest.NewRecorder()
		r.ServeHTTP(wBad, reqBad)
		if wBad.Code != http.StatusFound {
			t.Errorf("expected 302 for invalid server ID, got %d", wBad.Code)
		}

		// Not found redirect
		reqNF := httptest.NewRequest(http.MethodGet, "/server/99999", nil)
		wNF := httptest.NewRecorder()
		r.ServeHTTP(wNF, reqNF)
		if wNF.Code != http.StatusFound {
			t.Errorf("expected 302 for not found server, got %d", wNF.Code)
		}
	})

	t.Run("SettingsPageHandler", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/settings", nil)
		reqCtx := middleware.WithSession(req.Context(), adminSess)
		w := httptest.NewRecorder()
		h.SettingsPageHandler(w, req.WithContext(reqCtx))

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}
	})

	t.Run("MyConnectionsPageHandler", func(t *testing.T) {
		// Authenticated
		req := httptest.NewRequest(http.MethodGet, "/my", nil)
		reqCtx := middleware.WithSession(req.Context(), &models.SessionData{
			UserID: "u-1",
			Role:   models.RoleUser,
		})
		w := httptest.NewRecorder()
		h.MyConnectionsPageHandler(w, req.WithContext(reqCtx))
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}

		// Unauthenticated
		reqUnauth := httptest.NewRequest(http.MethodGet, "/my", nil)
		wUnauth := httptest.NewRecorder()
		h.MyConnectionsPageHandler(wUnauth, reqUnauth)
		if wUnauth.Code != http.StatusFound || wUnauth.Header().Get("Location") != "/login" {
			t.Errorf("expected 302 redirect to /login, got %d", wUnauth.Code)
		}
	})

	t.Run("ChangePasswordPageHandler", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/change-password", nil)
		w := httptest.NewRecorder()
		h.ChangePasswordPageHandler(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}
	})

	t.Run("LeaderboardPageHandler", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/leaderboard", nil)
		w := httptest.NewRecorder()
		h.LeaderboardPageHandler(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}
	})

	t.Run("SetupPageHandler", func(t *testing.T) {
		// When user exists -> redirects to /login
		u := &models.User{ID: "admin-u", Username: "adm", Role: models.RoleAdmin}
		_, _ = db.CreateUser(ctx, u)

		req := httptest.NewRequest(http.MethodGet, "/setup", nil)
		w := httptest.NewRecorder()
		h.SetupPageHandler(w, req)
		if w.Code != http.StatusFound || w.Header().Get("Location") != "/login" {
			t.Fatalf("expected 302 redirect to /login, got %d", w.Code)
		}

		// Empty DB -> renders setup page
		hEmpty, _, _ := setupTestHandlers(t)
		reqEmpty := httptest.NewRequest(http.MethodGet, "/setup", nil)
		wEmpty := httptest.NewRecorder()
		hEmpty.SetupPageHandler(wEmpty, reqEmpty)
		if wEmpty.Code != http.StatusOK {
			t.Fatalf("expected 200 for empty db setup, got %d", wEmpty.Code)
		}
	})
}
