package handlers

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
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

		body := w.Body.String()
		pattern := fmt.Sprintf(`const SERVER_ID\s*=\s*%d\s*;`, sID)
		matched, _ := regexp.MatchString(pattern, body)
		if !matched {
			t.Fatalf("expected rendered body to match pattern %q, but got:\n%s", pattern, body)
		}
		if strings.Contains(body, "const SERVER_ID =  null ;") || strings.Contains(body, "const SERVER_ID = null;") {
			t.Fatalf("CRITICAL REGRESSION: SERVER_ID rendered as null in /server/%d HTML", sID)
		}

		// Invalid ID redirect
		reqBad := httptest.NewRequest(http.MethodGet, "/server/invalid-id", nil)
		wBad := httptest.NewRecorder()
		r.ServeHTTP(wBad, reqBad)
		if wBad.Code != http.StatusFound {
			t.Errorf("expected 302 for invalid server ID, got %d", wBad.Code)
		}

		// Partial garbage redirect (Regression C verification)
		reqGarbage := httptest.NewRequest(http.MethodGet, "/server/12abc", nil)
		wGarbage := httptest.NewRecorder()
		r.ServeHTTP(wGarbage, reqGarbage)
		if wGarbage.Code != http.StatusFound {
			t.Errorf("expected 302 for partial garbage server ID, got %d", wGarbage.Code)
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
		uConn := &models.User{
			ID:       "u-my-1",
			Username: "myconnuser",
			Role:     models.RoleUser,
			Enabled:  true,
		}
		_, _ = db.CreateUser(ctx, uConn)

		conn := &models.UserConnection{
			ID:        "conn-my-1",
			UserID:    uConn.ID,
			ServerID:  sID,
			Protocol:  "awg",
			ClientID:  "client-my-1",
			Name:      "Primary Wireguard",
			CreatedAt: time.Now(),
		}
		_, _ = db.CreateConnection(ctx, conn)

		// Authenticated
		req := httptest.NewRequest(http.MethodGet, "/my", nil)
		reqCtx := middleware.WithSession(req.Context(), &models.SessionData{
			UserID:   uConn.ID,
			Username: uConn.Username,
			Role:     models.RoleUser,
		})
		w := httptest.NewRecorder()
		h.MyConnectionsPageHandler(w, req.WithContext(reqCtx))
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}

		body := w.Body.String()
		if !strings.Contains(body, "Main-Server") {
			t.Fatalf("expected server name %q in /my rendered HTML, body:\n%s", "Main-Server", body)
		}
		if !strings.Contains(body, `"server_name":"Main-Server"`) {
			t.Fatalf("expected server_name in initialConnections JSON, body:\n%s", body)
		}
		if !strings.Contains(body, "Primary Wireguard") {
			t.Fatalf("expected connection name %q in /my rendered HTML", "Primary Wireguard")
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
		uLeader := &models.User{
			ID:             "u-lead-1",
			Username:       "speedrunner",
			Role:           models.RoleUser,
			Enabled:        true,
			TrafficTotalRx: 5000000,
			TrafficTotalTx: 5000000,
			TrafficTotal:   10000000,
			CreatedAt:      time.Now(),
		}
		_, _ = db.CreateUser(ctx, uLeader)

		// 1. Initial all-time render with user session
		req := httptest.NewRequest(http.MethodGet, "/leaderboard", nil)
		reqCtx := middleware.WithSession(req.Context(), &models.SessionData{
			UserID:   uLeader.ID,
			Username: uLeader.Username,
			Role:     models.RoleUser,
		})
		w := httptest.NewRecorder()
		h.LeaderboardPageHandler(w, req.WithContext(reqCtx))

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}
		body := w.Body.String()
		if !strings.Contains(body, "speedrunner") {
			t.Fatalf("expected seeded user speedrunner in initial leaderboard render, body:\n%s", body)
		}
		if !strings.Contains(body, "9.54 MB") { // 10,000,000 bytes
			t.Fatalf("expected formatted traffic in leaderboard render")
		}
		if !strings.Contains(body, "🥇") && !strings.Contains(body, "#1") {
			t.Fatalf("expected rank 1 in leaderboard render")
		}

		// 2. Monthly period render
		reqMonthly := httptest.NewRequest(http.MethodGet, "/leaderboard?period=monthly", nil)
		wMonthly := httptest.NewRecorder()
		h.LeaderboardPageHandler(wMonthly, reqMonthly)
		if wMonthly.Code != http.StatusOK {
			t.Fatalf("expected 200 for monthly leaderboard, got %d", wMonthly.Code)
		}
		monthlyBody := wMonthly.Body.String()
		currMonthLabel := time.Now().Format("January 2006")
		if !strings.Contains(monthlyBody, currMonthLabel) {
			t.Fatalf("expected monthly label %q in leaderboard render", currMonthLabel)
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
