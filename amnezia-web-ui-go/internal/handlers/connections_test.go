package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/devops-igor/amnezia-web-ui-go/internal/middleware"
	"github.com/devops-igor/amnezia-web-ui-go/internal/models"
)

func TestConnectionsHandlers(t *testing.T) {
	mockSSH := &testMockSSHClient{}
	h, db, _ := setupTestHandlersWithMockSSH(t, mockSSH)
	ctx := context.Background()

	// Seed user and server
	u := &models.User{
		ID:           "u-conn-1",
		Username:     "connuser",
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
		Protocols: map[string]any{"awg": map[string]any{"port": 55424, "installed": true}, "telemt": map[string]any{"port": 443, "installed": true}},
		CreatedAt: time.Now(),
	}
	sID, _ := db.CreateServer(ctx, srv)
	mockSSH.serverID = &sID

	c1 := &models.UserConnection{
		ID:         "c-1",
		UserID:     u.ID,
		ServerID:   sID,
		Protocol:   "awg",
		ClientID:   "client-1",
		Name:       "Home Laptop",
		AWGMimicry: models.AWGMimicryAuto,
		CreatedAt:  time.Now(),
	}
	_, _ = db.CreateConnection(ctx, c1)

	sess := &models.SessionData{
		UserID: u.ID,
		Role:   models.RoleUser,
	}

	r := setupFullConnectionsRouter(h)

	t.Run("UserGetMyConnectionsHandler Authenticated & Unauthenticated", func(t *testing.T) {
		_ = db.UpdateServerReachability(ctx, sID, models.ReachabilityOnline)
		_, _ = db.UpdateUser(ctx, u.ID, map[string]any{
			"limits": map[string]any{
				"max_connections_per_user": float64(15),
			},
		})
		defer func() {
			_, _ = db.UpdateUser(ctx, u.ID, map[string]any{
				"limits": map[string]any{},
			})
		}()

		req := httptest.NewRequest(http.MethodGet, "/api/my/connections", nil)
		reqCtx := middleware.WithSession(req.Context(), sess)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req.WithContext(reqCtx))
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}

		var resp struct {
			Status      string           `json:"status"`
			Connections []map[string]any `json:"connections"`
			Limits      struct {
				MaxConnections     int `json:"max_connections"`
				CurrentConnections int `json:"current_connections"`
			} `json:"limits"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}
		if len(resp.Connections) == 0 {
			t.Fatalf("expected at least 1 connection")
		}
		if resp.Connections[0]["server_status"] != "online" {
			t.Errorf("expected server_status online, got %v", resp.Connections[0]["server_status"])
		}
		if reachable, ok := resp.Connections[0]["server_reachable"].(bool); !ok || !reachable {
			t.Errorf("expected server_reachable true, got %v", resp.Connections[0]["server_reachable"])
		}
		if resp.Limits.MaxConnections != 15 {
			t.Errorf("expected max_connections 15, got %d", resp.Limits.MaxConnections)
		}

		reqUnauth := httptest.NewRequest(http.MethodGet, "/api/my/connections", nil)
		wUnauth := httptest.NewRecorder()
		r.ServeHTTP(wUnauth, reqUnauth)
		if wUnauth.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", wUnauth.Code)
		}
	})

	t.Run("UserAddConnectionHandler Unauthenticated & Bad Request", func(t *testing.T) {
		reqUnauth := httptest.NewRequest(http.MethodPost, "/api/connections/add", bytes.NewReader([]byte("{}")))
		wUnauth := httptest.NewRecorder()
		r.ServeHTTP(wUnauth, reqUnauth)
		if wUnauth.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", wUnauth.Code)
		}

		reqBad := httptest.NewRequest(http.MethodPost, "/api/connections/add", bytes.NewReader([]byte("invalid-json")))
		reqBadCtx := middleware.WithSession(reqBad.Context(), sess)
		wBad := httptest.NewRecorder()
		r.ServeHTTP(wBad, reqBad.WithContext(reqBadCtx))
		if wBad.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", wBad.Code)
		}
	})

	t.Run("UserAddConnectionHandler Server Missing and Invalid", func(t *testing.T) {
		// Nonexistent server
		bodyNoSrv, _ := json.Marshal(models.MyAddConnectionRequest{ServerID: 987654, Protocol: "awg", Name: "Ghost Conn"})
		reqNoSrv := httptest.NewRequest(http.MethodPost, "/api/connections/add", bytes.NewReader(bodyNoSrv))
		reqNoSrvCtx := middleware.WithSession(reqNoSrv.Context(), sess)
		wNoSrv := httptest.NewRecorder()
		r.ServeHTTP(wNoSrv, reqNoSrv.WithContext(reqNoSrvCtx))
		if wNoSrv.Code != http.StatusNotFound {
			t.Errorf("expected 404 for missing server, got %d", wNoSrv.Code)
		}

		// Invalid protocol
		bodyBadProto, _ := json.Marshal(models.MyAddConnectionRequest{ServerID: sID, Protocol: "notaproto", Name: "Bad Proto Conn"})
		reqBadProto := httptest.NewRequest(http.MethodPost, "/api/connections/add", bytes.NewReader(bodyBadProto))
		reqBadProtoCtx := middleware.WithSession(reqBadProto.Context(), sess)
		wBadProto := httptest.NewRecorder()
		r.ServeHTTP(wBadProto, reqBadProto.WithContext(reqBadProtoCtx))
		if wBadProto.Code != http.StatusBadRequest {
			t.Errorf("expected 400 for bad protocol, got %d", wBadProto.Code)
		}

		// Valid protocol but not installed on this server
		bodyUninstalled, _ := json.Marshal(models.MyAddConnectionRequest{ServerID: sID, Protocol: "dns", Name: "DNS Conn"})
		reqUninstalled := httptest.NewRequest(http.MethodPost, "/api/connections/add", bytes.NewReader(bodyUninstalled))
		reqUninstalledCtx := middleware.WithSession(reqUninstalled.Context(), sess)
		wUninstalled := httptest.NewRecorder()
		r.ServeHTTP(wUninstalled, reqUninstalled.WithContext(reqUninstalledCtx))
		if wUninstalled.Code != http.StatusBadRequest {
			t.Errorf("expected 400 for uninstalled protocol, got %d", wUninstalled.Code)
		}

		// ExpirationDate-based expiry check
		pastExp := time.Now().Add(-24 * time.Hour)
		expU := &models.User{
			ID:             "u-expdate",
			Username:       "expdateuser",
			PasswordHash:   "hash",
			Role:           models.RoleUser,
			Enabled:        true,
			ExpirationDate: &pastExp,
			CreatedAt:      time.Now(),
		}
		_, _ = db.CreateUser(ctx, expU)

		expSess := &models.SessionData{UserID: expU.ID, Role: models.RoleUser}
		bodyExp, _ := json.Marshal(models.MyAddConnectionRequest{ServerID: sID, Protocol: "awg", Name: "Expired Date Conn"})
		reqExp := httptest.NewRequest(http.MethodPost, "/api/connections/add", bytes.NewReader(bodyExp))
		reqExpCtx := middleware.WithSession(reqExp.Context(), expSess)
		wExp := httptest.NewRecorder()
		r.ServeHTTP(wExp, reqExp.WithContext(reqExpCtx))
		if wExp.Code != http.StatusForbidden {
			t.Errorf("expected 403 for expiration_date, got %d", wExp.Code)
		}
	})

	t.Run("UserGetMyConnectionsHandler Unauthenticated", func(t *testing.T) {
		reqUnauth := httptest.NewRequest(http.MethodGet, "/api/my/connections", nil)
		wUnauth := httptest.NewRecorder()
		r.ServeHTTP(wUnauth, reqUnauth)
		if wUnauth.Code != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d", wUnauth.Code)
		}
	})

	t.Run("UserAddConnectionHandler With All Params Success", func(t *testing.T) {
		down := 100
		up := 200
		mim := string(models.AWGMimicryTLS)
		body, _ := json.Marshal(models.MyAddConnectionRequest{
			ServerID:          sID,
			Protocol:          "awg",
			Name:              "Fully Paramed Conn",
			AWGSpeedLimitDown: &down,
			AWGSpeedLimitUp:   &up,
			AWGMimicry:        &mim,
		})
		req := httptest.NewRequest(http.MethodPost, "/api/connections/add", bytes.NewReader(body))
		reqCtx := middleware.WithSession(req.Context(), sess)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req.WithContext(reqCtx))
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d (body: %s)", w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), "vpn_link") {
			t.Errorf("expected vpn_link in response")
		}
	})

	t.Run("UserAddConnectionHandler Duplicate Name", func(t *testing.T) {
		body, _ := json.Marshal(models.MyAddConnectionRequest{
			ServerID: sID,
			Protocol: "awg",
			Name:     "Home Laptop",
		})
		req := httptest.NewRequest(http.MethodPost, "/api/connections/add", bytes.NewReader(body))
		reqCtx := middleware.WithSession(req.Context(), sess)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req.WithContext(reqCtx))

		if w.Code != http.StatusConflict {
			t.Fatalf("expected 409 duplicate name, got %d", w.Code)
		}
	})

	t.Run("UserAddConnectionHandler Disabled User", func(t *testing.T) {
		disUser := &models.User{
			ID:           "u-dis",
			Username:     "disuser",
			PasswordHash: "hash",
			Role:         models.RoleUser,
			Enabled:      false,
			CreatedAt:    time.Now(),
		}
		_, _ = db.CreateUser(ctx, disUser)

		disSess := &models.SessionData{UserID: disUser.ID, Role: models.RoleUser}
		body, _ := json.Marshal(models.MyAddConnectionRequest{
			ServerID: sID,
			Protocol: "awg",
			Name:     "Phone",
		})
		req := httptest.NewRequest(http.MethodPost, "/api/connections/add", bytes.NewReader(body))
		reqCtx := middleware.WithSession(req.Context(), disSess)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req.WithContext(reqCtx))

		if w.Code != http.StatusForbidden {
			t.Fatalf("expected 403 account disabled, got %d", w.Code)
		}
	})

	t.Run("UserAddConnectionHandler Expired User", func(t *testing.T) {
		past := time.Now().Add(-1 * time.Hour)
		expUser := &models.User{
			ID:           "u-exp",
			Username:     "expuser",
			PasswordHash: "hash",
			Role:         models.RoleUser,
			Enabled:      true,
			ExpiresAt:    &past,
			CreatedAt:    time.Now(),
		}
		_, _ = db.CreateUser(ctx, expUser)

		expSess := &models.SessionData{UserID: expUser.ID, Role: models.RoleUser}
		body, _ := json.Marshal(models.MyAddConnectionRequest{
			ServerID: sID,
			Protocol: "awg",
			Name:     "Phone",
		})
		req := httptest.NewRequest(http.MethodPost, "/api/connections/add", bytes.NewReader(body))
		reqCtx := middleware.WithSession(req.Context(), expSess)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req.WithContext(reqCtx))

		if w.Code != http.StatusForbidden {
			t.Fatalf("expected 403 account expired, got %d", w.Code)
		}
	})

	t.Run("UserAddConnectionHandler Traffic Limit Exceeded", func(t *testing.T) {
		trafUser := &models.User{
			ID:           "u-traf",
			Username:     "trafuser",
			PasswordHash: "hash",
			Role:         models.RoleUser,
			Enabled:      true,
			TrafficLimit: 1000,
			TrafficUsed:  2000,
			CreatedAt:    time.Now(),
		}
		_, _ = db.CreateUser(ctx, trafUser)

		trafSess := &models.SessionData{UserID: trafUser.ID, Role: models.RoleUser}
		body, _ := json.Marshal(models.MyAddConnectionRequest{
			ServerID: sID,
			Protocol: "awg",
			Name:     "Phone",
		})
		req := httptest.NewRequest(http.MethodPost, "/api/connections/add", bytes.NewReader(body))
		reqCtx := middleware.WithSession(req.Context(), trafSess)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req.WithContext(reqCtx))

		if w.Code != http.StatusForbidden {
			t.Fatalf("expected 403 traffic limit exceeded, got %d", w.Code)
		}
	})

	t.Run("UserAddConnectionHandler Max Connections Reached", func(t *testing.T) {
		_ = db.SetSetting(ctx, "limits", models.ConnectionLimits{MaxConnectionsPerUser: 1})
		defer func() {
			_ = db.SetSetting(ctx, "limits", models.ConnectionLimits{MaxConnectionsPerUser: 10})
		}()

		body, _ := json.Marshal(models.MyAddConnectionRequest{
			ServerID: sID,
			Protocol: "awg",
			Name:     "Another Device",
		})
		req := httptest.NewRequest(http.MethodPost, "/api/connections/add", bytes.NewReader(body))
		reqCtx := middleware.WithSession(req.Context(), sess)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req.WithContext(reqCtx))

		if w.Code != http.StatusPreconditionRequired {
			t.Fatalf("expected 428 max connections reached, got %d", w.Code)
		}
	})

	t.Run("UserAddConnectionHandler Rate Limit Exceeded", func(t *testing.T) {
		_ = db.SetSetting(ctx, "limits", models.ConnectionLimits{
			MaxConnectionsPerUser:     100,
			ConnectionRateLimitCount:  1,
			ConnectionRateLimitWindow: 3600,
		})
		_ = db.LogConnectionCreation(ctx, u.ID)

		body, _ := json.Marshal(models.MyAddConnectionRequest{
			ServerID: sID,
			Protocol: "awg",
			Name:     "Device Rate Limit",
		})
		req := httptest.NewRequest(http.MethodPost, "/api/connections/add", bytes.NewReader(body))
		reqCtx := middleware.WithSession(req.Context(), sess)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req.WithContext(reqCtx))

		if w.Code != http.StatusPreconditionRequired {
			t.Fatalf("expected 428 rate limit, got %d", w.Code)
		}
	})

	t.Run("UserGetConnectionConfigHandler Ownership", func(t *testing.T) {
		// Connection owned by a different user -> 404 (not found for this user)
		otherConn := &models.UserConnection{
			ID:        "other-owner-conn",
			UserID:    "someone-else-id",
			ServerID:  sID,
			Protocol:  "awg",
			ClientID:  "other-owner-client",
			Name:      "Not Yours",
			CreatedAt: time.Now(),
		}
		_, _ = db.CreateConnection(ctx, otherConn)

		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/connections/%s/config", otherConn.ID), nil)
		reqCtx := middleware.WithSession(req.Context(), sess)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req.WithContext(reqCtx))
		if w.Code != http.StatusNotFound {
			t.Errorf("expected 404 for other owner's config, got %d", w.Code)
		}
	})

	t.Run("UserGetConnectionKitHandler Zip Content", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/connections/%s/kit", c1.ID), nil)
		reqCtx := middleware.WithSession(req.Context(), sess)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req.WithContext(reqCtx))

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}
		if w.Header().Get("Content-Type") != "application/zip" {
			t.Errorf("expected application/zip, got %s", w.Header().Get("Content-Type"))
		}
		if !strings.Contains(w.Header().Get("Content-Disposition"), "-kit.zip") {
			t.Errorf("expected kit zip Content-Disposition, got %s", w.Header().Get("Content-Disposition"))
		}
	})

	t.Run("UserGetConnectionConfigHandler Not Found", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/connections/nonexistent/config", nil)
		reqCtx := middleware.WithSession(req.Context(), sess)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req.WithContext(reqCtx))

		if w.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d", w.Code)
		}
	})

	t.Run("UserGetConnectionKitHandler Not Found", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/connections/nonexistent/kit", nil)
		reqCtx := middleware.WithSession(req.Context(), sess)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req.WithContext(reqCtx))

		if w.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d", w.Code)
		}
	})

	t.Run("UserAddConnectionHandler Success", func(t *testing.T) {
		_ = db.SetSetting(ctx, "limits", models.ConnectionLimits{
			MaxConnectionsPerUser:     100,
			ConnectionRateLimitCount:  100,
			ConnectionRateLimitWindow: 3600,
		})
		body, _ := json.Marshal(models.MyAddConnectionRequest{
			ServerID: sID,
			Protocol: "awg",
			Name:     "Brand New Device",
		})
		req := httptest.NewRequest(http.MethodPost, "/api/connections/add", bytes.NewReader(body))
		reqCtx := middleware.WithSession(req.Context(), sess)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req.WithContext(reqCtx))

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d (body: %s)", w.Code, w.Body.String())
		}
	})

	t.Run("UserGetConnectionConfigHandler Success", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/connections/%s/config", c1.ID), nil)
		reqCtx := middleware.WithSession(req.Context(), sess)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req.WithContext(reqCtx))

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}
	})

	t.Run("UserGetConnectionConfigHandler Edge Cases", func(t *testing.T) {
		// Connection referencing missing server -> 404
		cNoSrv := &models.UserConnection{
			ID:        "c-no-srv-1",
			UserID:    u.ID,
			ServerID:  99999,
			Protocol:  "awg",
			ClientID:  "client-nosrv",
			Name:      "No Server Conn",
			CreatedAt: time.Now(),
		}
		_, _ = db.CreateConnection(ctx, cNoSrv)

		reqNoSrv := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/connections/%s/config", cNoSrv.ID), nil)
		reqNoSrvCtx := middleware.WithSession(reqNoSrv.Context(), sess)
		wNoSrv := httptest.NewRecorder()
		r.ServeHTTP(wNoSrv, reqNoSrv.WithContext(reqNoSrvCtx))
		if wNoSrv.Code != http.StatusNotFound {
			t.Errorf("expected 404 for config with missing server, got %d", wNoSrv.Code)
		}

		// Connection with unsupported protocol -> 400
		cBadProto := &models.UserConnection{
			ID:        "c-bad-proto-1",
			UserID:    u.ID,
			ServerID:  sID,
			Protocol:  "unsupported_proto",
			ClientID:  "client-badproto",
			Name:      "Bad Proto Conn",
			CreatedAt: time.Now(),
		}
		_, _ = db.CreateConnection(ctx, cBadProto)

		reqBadProto := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/connections/%s/config", cBadProto.ID), nil)
		reqBadProtoCtx := middleware.WithSession(reqBadProto.Context(), sess)
		wBadProto := httptest.NewRecorder()
		r.ServeHTTP(wBadProto, reqBadProto.WithContext(reqBadProtoCtx))
		if wBadProto.Code != http.StatusBadRequest {
			t.Errorf("expected 400 for config with unsupported proto, got %d", wBadProto.Code)
		}

		// Kit handler with missing server -> 404
		reqKitNoSrv := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/connections/%s/kit", cNoSrv.ID), nil)
		reqKitNoSrvCtx := middleware.WithSession(reqKitNoSrv.Context(), sess)
		wKitNoSrv := httptest.NewRecorder()
		r.ServeHTTP(wKitNoSrv, reqKitNoSrv.WithContext(reqKitNoSrvCtx))
		if wKitNoSrv.Code != http.StatusNotFound {
			t.Errorf("expected 404 for kit with missing server, got %d", wKitNoSrv.Code)
		}

		// Kit handler with unsupported proto -> 400
		reqKitBadProto := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/connections/%s/kit", cBadProto.ID), nil)
		reqKitBadProtoCtx := middleware.WithSession(reqKitBadProto.Context(), sess)
		wKitBadProto := httptest.NewRecorder()
		r.ServeHTTP(wKitBadProto, reqKitBadProto.WithContext(reqKitBadProtoCtx))
		if wKitBadProto.Code != http.StatusBadRequest {
			t.Errorf("expected 400 for kit with unsupported proto, got %d", wKitBadProto.Code)
		}
	})

	t.Run("UserGetConnectionKitHandler Success", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/connections/%s/kit", c1.ID), nil)
		reqCtx := middleware.WithSession(req.Context(), sess)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req.WithContext(reqCtx))

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}
	})

	t.Run("UserRenameConnectionHandler", func(t *testing.T) {
		body, _ := json.Marshal(models.RenameConnectionRequest{
			Name: "Work MacBook",
		})
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/connections/%s/rename", c1.ID), bytes.NewReader(body))
		reqCtx := middleware.WithSession(req.Context(), sess)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req.WithContext(reqCtx))

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}

		// Bad body
		reqBad := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/connections/%s/rename", c1.ID), bytes.NewReader([]byte("bad-json")))
		reqBadCtx := middleware.WithSession(reqBad.Context(), sess)
		wBad := httptest.NewRecorder()
		r.ServeHTTP(wBad, reqBad.WithContext(reqBadCtx))
		if wBad.Code != http.StatusBadRequest {
			t.Errorf("expected 400 for bad json, got %d", wBad.Code)
		}

		// Empty name
		emptyName, _ := json.Marshal(models.RenameConnectionRequest{Name: ""})
		reqEmpty := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/connections/%s/rename", c1.ID), bytes.NewReader(emptyName))
		reqEmptyCtx := middleware.WithSession(reqEmpty.Context(), sess)
		wEmpty := httptest.NewRecorder()
		r.ServeHTTP(wEmpty, reqEmpty.WithContext(reqEmptyCtx))
		if wEmpty.Code != http.StatusBadRequest {
			t.Errorf("expected 400 for empty name, got %d", wEmpty.Code)
		}

		// Duplicate name (case-insensitive) - create second connection
		cDup := &models.UserConnection{
			ID:        "c-dup-1",
			UserID:    u.ID,
			ServerID:  sID,
			Protocol:  "awg",
			ClientID:  "client-dup",
			Name:      "Existing Dup Name",
			CreatedAt: time.Now(),
		}
		_, _ = db.CreateConnection(ctx, cDup)

		dupName, _ := json.Marshal(models.RenameConnectionRequest{Name: "existing dup name"})
		reqDup := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/connections/%s/rename", c1.ID), bytes.NewReader(dupName))
		reqDupCtx := middleware.WithSession(reqDup.Context(), sess)
		wDup := httptest.NewRecorder()
		r.ServeHTTP(wDup, reqDup.WithContext(reqDupCtx))
		if wDup.Code != http.StatusConflict {
			t.Errorf("expected 409 for duplicate name, got %d", wDup.Code)
		}

		// Bad ID
		req404 := httptest.NewRequest(http.MethodPost, "/api/connections/missing-id/rename", bytes.NewReader(body))
		req404Ctx := middleware.WithSession(req404.Context(), sess)
		w404 := httptest.NewRecorder()
		r.ServeHTTP(w404, req404.WithContext(req404Ctx))
		if w404.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d", w404.Code)
		}
	})

	t.Run("User Connections Telemt Protocol", func(t *testing.T) {
		quota := "500"
		maxIPs := 3
		exp := "60"
		bodyTelemt, _ := json.Marshal(models.MyAddConnectionRequest{
			ServerID:     sID,
			Protocol:     "telemt",
			Name:         "Telemt Connection",
			TelemtQuota:  &quota,
			TelemtMaxIPs: &maxIPs,
			TelemtExpiry: &exp,
		})
		reqTelemt := httptest.NewRequest(http.MethodPost, "/api/connections/add", bytes.NewReader(bodyTelemt))
		reqTelemtCtx := middleware.WithSession(reqTelemt.Context(), sess)
		wTelemt := httptest.NewRecorder()
		r.ServeHTTP(wTelemt, reqTelemt.WithContext(reqTelemtCtx))
		if wTelemt.Code != http.StatusOK {
			t.Fatalf("expected 200 for telemt add, got %d", wTelemt.Code)
		}
	})

	t.Run("UserDeleteConnectionHandler", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/connections/%s/delete", c1.ID), nil)
		reqCtx := middleware.WithSession(req.Context(), sess)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req.WithContext(reqCtx))

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}

		// Bad ID
		req404 := httptest.NewRequest(http.MethodPost, "/api/connections/missing-id/delete", nil)
		req404Ctx := middleware.WithSession(req404.Context(), sess)
		w404 := httptest.NewRecorder()
		r.ServeHTTP(w404, req404.WithContext(req404Ctx))
		if w404.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d", w404.Code)
		}
	})
}

func TestConnectionLimit_ConcurrentAdds(t *testing.T) {
	mockSSH := &testMockSSHClient{}
	h, db, _ := setupTestHandlersWithMockSSH(t, mockSSH)
	ctx := context.Background()

	// 1. Create a user with max_connections_per_user = 3 and disabled creation rate limiting
	limit := 3
	u := &models.User{
		ID:           "u-limit-test",
		Username:     "limituser",
		PasswordHash: "hash",
		Role:         models.RoleUser,
		Enabled:      true,
		Limits: map[string]any{
			"max_connections_per_user":     float64(limit),
			"connection_rate_limit_count":  float64(1000),
			"connection_rate_limit_window": float64(1),
		},
		CreatedAt: time.Now(),
	}
	_, err := db.CreateUser(ctx, u)
	if err != nil {
		t.Fatalf("failed to create user: %v", err)
	}

	// 2. Create server
	srv := &models.Server{
		Name:      "VPN-Limit-Node",
		Host:      "192.168.1.55",
		SSHPort:   22,
		SSHUser:   "root",
		Protocols: map[string]any{"awg": map[string]any{"port": 55424, "installed": true}},
		CreatedAt: time.Now(),
	}
	sID, _ := db.CreateServer(ctx, srv)
	mockSSH.serverID = &sID

	sess := &models.SessionData{
		UserID: u.ID,
		Role:   models.RoleUser,
	}

	r := setupFullConnectionsRouter(h)

	// 3. Fire 10 concurrent requests to /api/connections/add
	concurrentAdds := 10
	var successCount atomic.Int32
	var limitHitCount atomic.Int32
	var wg sync.WaitGroup

	for i := 0; i < concurrentAdds; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			body, _ := json.Marshal(models.MyAddConnectionRequest{
				ServerID: sID,
				Protocol: "awg",
				Name:     fmt.Sprintf("Concurrent Conn %d", idx),
			})
			req := httptest.NewRequest(http.MethodPost, "/api/connections/add", bytes.NewReader(body))
			reqCtx := middleware.WithSession(req.Context(), sess)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req.WithContext(reqCtx))

			if w.Code == http.StatusOK {
				successCount.Add(1)
			} else if w.Code == http.StatusTooManyRequests || w.Code == http.StatusBadRequest {
				limitHitCount.Add(1)
			}
		}(i)
	}
	wg.Wait()

	// 4. Assert that exactly 3 connections were created in DB (never exceeding limit)
	conns, err := db.GetConnectionsByUserID(ctx, u.ID)
	if err != nil {
		t.Fatalf("failed to get user connections: %v", err)
	}

	if len(conns) > limit {
		t.Fatalf("TOCTOU race detected! Connection count %d exceeded max limit %d", len(conns), limit)
	}
	if len(conns) != limit {
		t.Fatalf("expected exactly %d connections created, got %d", limit, len(conns))
	}
	if successCount.Load() != int32(limit) {
		t.Errorf("expected %d successful adds, got %d (rejected: %d)", limit, successCount.Load(), limitHitCount.Load())
	}
}
