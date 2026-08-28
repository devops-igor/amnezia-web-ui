package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/devops-igor/amnezia-web-ui-go/internal/middleware"
	"github.com/devops-igor/amnezia-web-ui-go/internal/models"
)

func TestVPNHandlers(t *testing.T) {
	h, db, _ := setupTestHandlers(t)
	ctx := context.Background()

	// Seed user and server
	u := &models.User{
		ID:           "vpn-user-1",
		Username:     "vpnuser",
		PasswordHash: "hash",
		Role:         models.RoleUser,
		Enabled:      true,
		CreatedAt:    time.Now(),
	}
	_, _ = db.CreateUser(ctx, u)

	srv := &models.Server{
		Name:      "VPN-Node",
		Host:      "192.168.1.50",
		SSHPort:   22,
		SSHUser:   "root",
		SSHPass:   "pass",
		Protocols: map[string]any{"awg": map[string]any{"port": 55424, "installed": true}},
		CreatedAt: time.Now(),
	}
	sID, _ := db.CreateServer(ctx, srv)

	sess := &models.SessionData{
		UserID: u.ID,
		Role:   models.RoleUser,
	}

	r := setupFullVPNRouter(h)

	t.Run("VPNStatusHandler", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/vpn/status", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}
	})

	t.Run("VPNBackendsHandler", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/vpn/backends", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}
	})

	t.Run("VPNTunnelsHandler", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/vpn/tunnels", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}
	})

	t.Run("VPNGetConfigHandler", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/vpn/config", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}
	})

	t.Run("VPNUpdateConfigHandler", func(t *testing.T) {
		cfg := models.VPNConfig{
			Algorithm:          models.LBLeastConnections,
			HealthThresholdMS:  400,
			ListenPort:         51820,
			SubnetCIDR:         "10.100.0.0/16",
			MaxTotalPeers:      500,
			MaxPeersPerBackend: 100,
		}
		body, _ := json.Marshal(cfg)
		req := httptest.NewRequest(http.MethodPost, "/api/vpn/config", bytes.NewReader(body))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}
	})

	t.Run("VPNMyConnectionHandler Unauth", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/vpn/my-connection", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d", w.Code)
		}
	})

	t.Run("VPNMyConnectionHandler Auth", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/vpn/my-connection", nil)
		reqCtx := middleware.WithSession(req.Context(), sess)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req.WithContext(reqCtx))

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}
	})

	t.Run("VPNMyConfigHandler Unauth", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/vpn/my-config", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d", w.Code)
		}
	})

	t.Run("VPNMyConfigHandler Auth", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/vpn/my-config", nil)
		reqCtx := middleware.WithSession(req.Context(), sess)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req.WithContext(reqCtx))

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}
	})

	t.Run("VPNEnableBackendHandler", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/vpn/backends/%d/enable", sID), nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK && w.Code != http.StatusInternalServerError {
			t.Fatalf("expected 200 or 500, got %d", w.Code)
		}
	})

	t.Run("VPNDisableBackendHandler", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/vpn/backends/%d/disable", sID), nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK && w.Code != http.StatusInternalServerError {
			t.Fatalf("expected 200 or 500, got %d", w.Code)
		}
	})

	t.Run("VPNUpdateConfigHandler Invalid JSON", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/vpn/config", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", w.Code)
		}
	})

	t.Run("VPNEnableBackendHandler Invalid ID", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/vpn/backends/invalid/enable", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", w.Code)
		}
	})

	t.Run("VPNDisableBackendHandler Invalid ID", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/vpn/backends/invalid/disable", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", w.Code)
		}
	})

	t.Run("VPNDisconnectHandler", func(t *testing.T) {
		// Session ID
		body, _ := json.Marshal(map[string]any{"session_id": "sess-123"})
		req := httptest.NewRequest(http.MethodPost, "/api/vpn/disconnect", bytes.NewReader(body))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}

		// User ID
		bodyUser, _ := json.Marshal(map[string]any{"user_id": u.ID})
		reqUser := httptest.NewRequest(http.MethodPost, "/api/vpn/disconnect", bytes.NewReader(bodyUser))
		wUser := httptest.NewRecorder()
		r.ServeHTTP(wUser, reqUser)
		if wUser.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", wUser.Code)
		}

		// Invalid Body
		reqBad := httptest.NewRequest(http.MethodPost, "/api/vpn/disconnect", bytes.NewReader([]byte("bad-json")))
		wBad := httptest.NewRecorder()
		r.ServeHTTP(wBad, reqBad)
		if wBad.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", wBad.Code)
		}
	})

	t.Run("Nil VPNService Fallbacks", func(t *testing.T) {
		hNil := NewHandlers(Dependencies{
			Config: h.cfg,
			DB:     db,
		})
		rNil := setupFullVPNRouter(hNil)

		// Status
		reqS := httptest.NewRequest(http.MethodGet, "/api/vpn/status", nil)
		wS := httptest.NewRecorder()
		rNil.ServeHTTP(wS, reqS)
		if wS.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", wS.Code)
		}

		// Backends
		reqB := httptest.NewRequest(http.MethodGet, "/api/vpn/backends", nil)
		wB := httptest.NewRecorder()
		rNil.ServeHTTP(wB, reqB)
		if wB.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", wB.Code)
		}

		// Tunnels
		reqT := httptest.NewRequest(http.MethodGet, "/api/vpn/tunnels", nil)
		wT := httptest.NewRecorder()
		rNil.ServeHTTP(wT, reqT)
		if wT.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", wT.Code)
		}

		// Config
		reqC := httptest.NewRequest(http.MethodGet, "/api/vpn/config", nil)
		wC := httptest.NewRecorder()
		rNil.ServeHTTP(wC, reqC)
		if wC.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", wC.Code)
		}

		// Update Config (saves to db)
		cfg := models.VPNConfig{ListenPort: 51821}
		body, _ := json.Marshal(cfg)
		reqUC := httptest.NewRequest(http.MethodPost, "/api/vpn/config", bytes.NewReader(body))
		wUC := httptest.NewRecorder()
		rNil.ServeHTTP(wUC, reqUC)
		if wUC.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", wUC.Code)
		}

		// MyConnection
		reqMC := httptest.NewRequest(http.MethodGet, "/api/vpn/my-connection", nil)
		reqMCCtx := middleware.WithSession(reqMC.Context(), sess)
		wMC := httptest.NewRecorder()
		rNil.ServeHTTP(wMC, reqMC.WithContext(reqMCCtx))
		if wMC.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", wMC.Code)
		}

		// MyConfig
		reqMCfg := httptest.NewRequest(http.MethodGet, "/api/vpn/my-config", nil)
		reqMCfgCtx := middleware.WithSession(reqMCfg.Context(), sess)
		wMCfg := httptest.NewRecorder()
		rNil.ServeHTTP(wMCfg, reqMCfg.WithContext(reqMCfgCtx))
		if wMCfg.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", wMCfg.Code)
		}
	})
}
