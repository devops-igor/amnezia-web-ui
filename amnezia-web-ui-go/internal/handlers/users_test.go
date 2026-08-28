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
)

func TestUsersHandlers(t *testing.T) {
	h, db, _ := setupTestHandlers(t)
	ctx := context.Background()

	u := &models.User{
		ID:           "test-u-1",
		Username:     "existinguser",
		PasswordHash: "hashedpass",
		Role:         models.RoleUser,
		Enabled:      true,
		CreatedAt:    time.Now(),
	}
	_, _ = db.CreateUser(ctx, u)

	srv := &models.Server{
		Name:      "User-VPN",
		Host:      "192.168.1.80",
		SSHPort:   22,
		SSHUser:   "root",
		Protocols: map[string]any{"awg": map[string]any{"port": 55424, "installed": true}},
		CreatedAt: time.Now(),
	}
	sID, _ := db.CreateServer(ctx, srv)

	adminSess := &models.SessionData{
		UserID: "admin-id",
		Role:   models.RoleAdmin,
	}

	r := setupFullUsersRouter(h)

	t.Run("ListUsersHandler", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/users?search=existing&page=1&size=10", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}

		var resp models.PaginatedUsersResponse
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatalf("decode failed: %v", err)
		}
		if resp.Total < 1 {
			t.Errorf("expected at least 1 user, got %d", resp.Total)
		}

		// Defaults
		reqDef := httptest.NewRequest(http.MethodGet, "/api/users", nil)
		wDef := httptest.NewRecorder()
		r.ServeHTTP(wDef, reqDef)
		if wDef.Code != http.StatusOK {
			t.Errorf("expected 200 for default list, got %d", wDef.Code)
		}
	})

	t.Run("AddUserHandler Duplicate Username", func(t *testing.T) {
		body, _ := json.Marshal(models.AddUserRequest{
			Username: "existinguser",
			Password: "Password123!",
		})
		req := httptest.NewRequest(http.MethodPost, "/api/users/add", bytes.NewReader(body))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 user_exists, got %d", w.Code)
		}
	})

	t.Run("AddUserHandler Success With AutoConnection", func(t *testing.T) {
		protoAWG := "awg"
		connName := "Initial AWG Conn"
		body, _ := json.Marshal(models.AddUserRequest{
			Username:       "brandnewuser",
			Password:       "BrandNewPass123!",
			Role:           models.RoleUser,
			ServerID:       &sID,
			Protocol:       &protoAWG,
			ConnectionName: &connName,
		})
		req := httptest.NewRequest(http.MethodPost, "/api/users/add", bytes.NewReader(body))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d (body: %s)", w.Code, w.Body.String())
		}
	})

	t.Run("UpdateUserHandler", func(t *testing.T) {
		desc := "Updated test user"
		email := "test@example.com"
		tg := "123456"
		lim := float64(5000000)
		strat := string(models.ResetStrategyMonthly)
		newPass := "UpdatedPass123!"
		mim := string(models.AWGMimicryTLS)
		body, _ := json.Marshal(models.UpdateUserRequest{
			Description:          &desc,
			Email:                &email,
			TelegramID:           &tg,
			TrafficLimit:         &lim,
			TrafficResetStrategy: &strat,
			Password:             &newPass,
			AWGMimicry:           &mim,
		})
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/users/%s/update", u.ID), bytes.NewReader(body))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}

		// Bad JSON
		reqBad := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/users/%s/update", u.ID), bytes.NewReader([]byte("bad-json")))
		wBad := httptest.NewRecorder()
		r.ServeHTTP(wBad, reqBad)
		if wBad.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", wBad.Code)
		}
	})

	t.Run("ToggleUserHandler", func(t *testing.T) {
		body, _ := json.Marshal(models.ToggleUserRequest{
			Enabled: false,
		})
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/users/%s/toggle", u.ID), bytes.NewReader(body))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}

		// Missing user
		req404 := httptest.NewRequest(http.MethodPost, "/api/users/unknown/toggle", bytes.NewReader(body))
		w404 := httptest.NewRecorder()
		r.ServeHTTP(w404, req404)
		if w404.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d", w404.Code)
		}
	})

	t.Run("AddUserConnectionHandler Server Not Found", func(t *testing.T) {
		body, _ := json.Marshal(models.AddUserConnectionRequest{
			ServerID: 99999,
			Protocol: "awg",
			Name:     "Test Conn",
		})
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/users/%s/connections/add", u.ID), bytes.NewReader(body))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d", w.Code)
		}
	})

	t.Run("AddUserConnectionHandler New Client Provision", func(t *testing.T) {
		body, _ := json.Marshal(models.AddUserConnectionRequest{
			ServerID: sID,
			Protocol: "awg",
			Name:     "Auto Provision Client",
		})
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/users/%s/connections/add", u.ID), bytes.NewReader(body))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d (body: %s)", w.Code, w.Body.String())
		}
	})

	t.Run("AddUserConnectionHandler With Existing ClientID", func(t *testing.T) {
		cID := "existing-client-1"
		body, _ := json.Marshal(models.AddUserConnectionRequest{
			ServerID: sID,
			Protocol: "awg",
			ClientID: &cID,
			Name:     "Existing Client Assigned",
		})
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/users/%s/connections/add", u.ID), bytes.NewReader(body))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d (body: %s)", w.Code, w.Body.String())
		}
	})

	t.Run("GetUserConnectionsHandler", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/users/%s/connections", u.ID), nil)
		reqCtx := middleware.WithSession(req.Context(), adminSess)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req.WithContext(reqCtx))

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}
	})

	t.Run("GetUserConnectionsHandler Forbidden", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/users/%s/connections", u.ID), nil)
		reqCtx := middleware.WithSession(req.Context(), &models.SessionData{
			UserID: "other-user",
			Role:   models.RoleUser,
		})
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req.WithContext(reqCtx))

		if w.Code != http.StatusForbidden {
			t.Fatalf("expected 403, got %d", w.Code)
		}
	})

	t.Run("SetupUserShareHandler", func(t *testing.T) {
		pass := "SecretShare123!"
		hours := 48
		body, _ := json.Marshal(models.ShareSetupRequest{
			Password:       &pass,
			ExpiresInHours: &hours,
		})
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/users/%s/share/setup", u.ID), bytes.NewReader(body))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}
	})

	t.Run("ListUsersHandler Pagination and Filters", func(t *testing.T) {
		// Search by email
		email := "searchme@example.com"
		eu := &models.User{
			ID:           "search-u-1",
			Username:     "searchmeuser",
			PasswordHash: "hash",
			Role:         models.RoleUser,
			Enabled:      true,
			Email:        &email,
			CreatedAt:    time.Now(),
		}
		_, _ = db.CreateUser(ctx, eu)

		reqEmail := httptest.NewRequest(http.MethodGet, "/api/users?search=searchme@example.com", nil)
		wEmail := httptest.NewRecorder()
		r.ServeHTTP(wEmail, reqEmail)
		if wEmail.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", wEmail.Code)
		}
		if !strings.Contains(wEmail.Body.String(), "searchmeuser") {
			t.Errorf("expected to find user by email search")
		}

		// Search by telegram ID
		tg := "tg-777"
		tu := &models.User{
			ID:           "tg-u-1",
			Username:     "tguser",
			PasswordHash: "hash",
			Role:         models.RoleUser,
			Enabled:      true,
			TelegramID:   &tg,
			CreatedAt:    time.Now(),
		}
		_, _ = db.CreateUser(ctx, tu)

		reqTg := httptest.NewRequest(http.MethodGet, "/api/users?search=tg-777", nil)
		wTg := httptest.NewRecorder()
		r.ServeHTTP(wTg, reqTg)
		if wTg.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", wTg.Code)
		}
		if !strings.Contains(wTg.Body.String(), "tguser") {
			t.Errorf("expected to find user by telegram search")
		}

		// Remnawave source
		rwUUID := "rw-list-1"
		rw := &models.User{
			ID:            "rw-u-1",
			Username:      "rwuser",
			PasswordHash:  "hash",
			Role:          models.RoleUser,
			Enabled:       true,
			RemnaWaveUUID: &rwUUID,
			ExpirationDate: func() *time.Time {
				t := time.Now().Add(24 * time.Hour)
				return &t
			}(),
			ExpiresAt: func() *time.Time {
				t := time.Now().Add(48 * time.Hour)
				return &t
			}(),
			CreatedAt: time.Now(),
		}
		_, _ = db.CreateUser(ctx, rw)

		reqRW := httptest.NewRequest(http.MethodGet, "/api/users?search=rwuser", nil)
		wRW := httptest.NewRecorder()
		r.ServeHTTP(wRW, reqRW)
		if wRW.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", wRW.Code)
		}
		if !strings.Contains(wRW.Body.String(), "Remnawave") {
			t.Errorf("expected Remnawave source in user list")
		}

		// Pagination - page 2 with size 1
		reqPage := httptest.NewRequest(http.MethodGet, "/api/users?page=2&size=1", nil)
		wPage := httptest.NewRecorder()
		r.ServeHTTP(wPage, reqPage)
		if wPage.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", wPage.Code)
		}

		// Out of range page
		reqFar := httptest.NewRequest(http.MethodGet, "/api/users?page=99&size=10", nil)
		wFar := httptest.NewRecorder()
		r.ServeHTTP(wFar, reqFar)
		if wFar.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", wFar.Code)
		}
	})

	t.Run("AddUserConnectionHandler Telemt With Params", func(t *testing.T) {
		quota := "1000"
		maxIPs := 5
		exp := "30"
		body, _ := json.Marshal(models.AddUserConnectionRequest{
			ServerID:     sID,
			Protocol:     "telemt",
			Name:         "Telemt Admin Conn",
			TelemtQuota:  &quota,
			TelemtMaxIPs: &maxIPs,
			TelemtExpiry: &exp,
		})
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/users/%s/connections/add", u.ID), bytes.NewReader(body))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d (body: %s)", w.Code, w.Body.String())
		}
	})

	t.Run("AddUserConnectionHandler Invalid JSON and User", func(t *testing.T) {
		reqBad := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/users/%s/connections/add", u.ID), bytes.NewReader([]byte("bad")))
		wBad := httptest.NewRecorder()
		r.ServeHTTP(wBad, reqBad)
		if wBad.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", wBad.Code)
		}

		body, _ := json.Marshal(models.AddUserConnectionRequest{ServerID: sID, Protocol: "awg", Name: "X"})
		req404 := httptest.NewRequest(http.MethodPost, "/api/users/no-such-user/connections/add", bytes.NewReader(body))
		w404 := httptest.NewRecorder()
		r.ServeHTTP(w404, req404)
		if w404.Code != http.StatusNotFound {
			t.Errorf("expected 404, got %d", w404.Code)
		}
	})

	t.Run("UpdateUserHandler All Fields", func(t *testing.T) {
		// Create fresh user for field updates
		fu := &models.User{
			ID:           "update-all-1",
			Username:     "updateall",
			PasswordHash: "hash",
			Role:         models.RoleUser,
			Enabled:      true,
			CreatedAt:    time.Now(),
		}
		_, _ = db.CreateUser(ctx, fu)

		tg := "tg-999"
		email := "updateall@example.com"
		desc := "full update"
		lim := float64(10)
		strat := string(models.ResetStrategyMonthly)
		expires := time.Now().Add(72 * time.Hour).Format(time.RFC3339)
		mim := string(models.AWGMimicryQUIC)
		body, _ := json.Marshal(models.UpdateUserRequest{
			TelegramID:           &tg,
			Email:                &email,
			Description:          &desc,
			TrafficLimit:         &lim,
			TrafficResetStrategy: &strat,
			ExpiresAt:            &expires,
			AWGMimicry:           &mim,
		})
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/users/%s/update", fu.ID), bytes.NewReader(body))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}

		// Verify fields persisted
		updated, err := db.GetUser(ctx, fu.ID)
		if err != nil || updated == nil {
			t.Fatalf("failed to fetch updated user: %v", err)
		}
		if updated.TelegramID == nil || *updated.TelegramID != tg {
			t.Errorf("expected telegram id to be updated")
		}
		if updated.Email == nil || *updated.Email != email {
			t.Errorf("expected email to be updated")
		}
		if updated.TrafficLimit != int64(10*1024*1024*1024) {
			t.Errorf("expected traffic limit to be updated, got %d", updated.TrafficLimit)
		}
	})

	t.Run("DeleteUserHandler With Connections", func(t *testing.T) {
		// Create user with connections to exercise RemoveClient loop
		delU := &models.User{
			ID:           "delete-conns-u",
			Username:     "deleteconns",
			PasswordHash: "hash",
			Role:         models.RoleUser,
			Enabled:      true,
			CreatedAt:    time.Now(),
		}
		_, _ = db.CreateUser(ctx, delU)

		dc1 := &models.UserConnection{ID: "del-c-1", UserID: delU.ID, ServerID: sID, Protocol: "awg", ClientID: "del-client-1", Name: "Del Conn 1", CreatedAt: time.Now()}
		_, _ = db.CreateConnection(ctx, dc1)
		dc2 := &models.UserConnection{ID: "del-c-2", UserID: delU.ID, ServerID: sID, Protocol: "telemt", ClientID: "del-client-2", Name: "Del Conn 2", CreatedAt: time.Now()}
		_, _ = db.CreateConnection(ctx, dc2)

		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/users/%s/delete", delU.ID), nil)
		reqCtx := middleware.WithSession(req.Context(), &models.SessionData{
			UserID: "some-other-admin",
			Role:   models.RoleAdmin,
		})
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req.WithContext(reqCtx))

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}

		// Verify user and connections removed
		gone, _ := db.GetUser(ctx, delU.ID)
		if gone != nil {
			t.Errorf("expected user to be deleted")
		}
		conns, _ := db.GetConnectionsByUserID(ctx, delU.ID)
		if len(conns) != 0 {
			t.Errorf("expected connections to be deleted, got %d", len(conns))
		}
	})

	t.Run("DeleteUserHandler Cannot Delete Self", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/users/%s/delete", u.ID), nil)
		reqCtx := middleware.WithSession(req.Context(), &models.SessionData{
			UserID: u.ID,
			Role:   models.RoleAdmin,
		})
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req.WithContext(reqCtx))

		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 cannot delete self, got %d", w.Code)
		}
	})

	t.Run("DeleteUserHandler Success", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/users/%s/delete", u.ID), nil)
		reqCtx := middleware.WithSession(req.Context(), &models.SessionData{
			UserID: "other-admin",
			Role:   models.RoleAdmin,
		})
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req.WithContext(reqCtx))

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}
	})

	t.Run("AddUserHandler Invalid Username & Password", func(t *testing.T) {
		body, _ := json.Marshal(models.AddUserRequest{
			Username: "a", // too short
			Password: "short",
		})
		req := httptest.NewRequest(http.MethodPost, "/api/users/add", bytes.NewReader(body))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", w.Code)
		}
	})

	t.Run("AddUserHandler Admin Role", func(t *testing.T) {
		expDate := time.Now().Add(24 * time.Hour).Format(time.RFC3339)
		body, _ := json.Marshal(models.AddUserRequest{
			Username:       "admincreated",
			Password:       "ValidAdminPass123!",
			Role:           models.RoleAdmin,
			ExpiresAt:      &expDate,
			ExpirationDate: &expDate,
		})
		req := httptest.NewRequest(http.MethodPost, "/api/users/add", bytes.NewReader(body))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d (body: %s)", w.Code, w.Body.String())
		}
	})

	t.Run("UpdateUserHandler Not Found", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/users/nonexistent/update", bytes.NewReader([]byte("{}")))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d", w.Code)
		}
	})

	t.Run("ToggleUserHandler Not Found", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/users/nonexistent/toggle", bytes.NewReader([]byte(`{"enabled":true}`)))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d", w.Code)
		}
	})

	t.Run("SetupUserShareHandler Disable Share", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/users/%s/share/setup", u.ID), bytes.NewReader([]byte(`{"enabled":false}`)))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK && w.Code != http.StatusNotFound {
			t.Fatalf("expected 200 or 404, got %d", w.Code)
		}
	})

	t.Run("ToggleUserHandler Toggle With Connections", func(t *testing.T) {
		selfUser := &models.User{
			ID:           "self-admin-1",
			Username:     "selfadmin",
			PasswordHash: "hash",
			Role:         models.RoleAdmin,
			Enabled:      true,
			CreatedAt:    time.Now(),
		}
		_, _ = db.CreateUser(ctx, selfUser)

		// Create connection for this user
		cToggle := &models.UserConnection{
			ID:        "c-toggle-1",
			UserID:    selfUser.ID,
			ServerID:  sID,
			Protocol:  "awg",
			ClientID:  "client-1",
			Name:      "Toggle Device",
			CreatedAt: time.Now(),
		}
		_, _ = db.CreateConnection(ctx, cToggle)

		body, _ := json.Marshal(models.ToggleUserRequest{
			Enabled: false,
		})
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/users/%s/toggle", selfUser.ID), bytes.NewReader(body))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("expected 200 toggle user, got %d", w.Code)
		}
	})

	t.Run("GetUserConnectionsHandler Enriched Output", func(t *testing.T) {
		// Fresh user (earlier subtests may have deleted seeded users)
		fu := &models.User{
			ID:           "conn-enrich-u",
			Username:     "connenrich",
			PasswordHash: "hash",
			Role:         models.RoleUser,
			Enabled:      true,
			CreatedAt:    time.Now(),
		}
		_, _ = db.CreateUser(ctx, fu)

		// Connection with existing server name + connection with missing server
		cKnown := &models.UserConnection{
			ID:        "c-known-1",
			UserID:    fu.ID,
			ServerID:  sID,
			Protocol:  "awg",
			ClientID:  "client-known",
			Name:      "Known Server Conn",
			CreatedAt: time.Now(),
		}
		_, _ = db.CreateConnection(ctx, cKnown)

		cUnknown := &models.UserConnection{
			ID:        "c-unknown-1",
			UserID:    fu.ID,
			ServerID:  424242,
			Protocol:  "awg",
			ClientID:  "client-unknown",
			Name:      "Unknown Server Conn",
			CreatedAt: time.Now(),
		}
		_, _ = db.CreateConnection(ctx, cUnknown)

		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/users/%s/connections", fu.ID), nil)
		reqCtx := middleware.WithSession(req.Context(), adminSess)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req.WithContext(reqCtx))
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}

		body := w.Body.String()
		if !strings.Contains(body, "User-VPN") {
			t.Errorf("expected known server name in output")
		}
		if !strings.Contains(body, "Server #424242") {
			t.Errorf("expected fallback server name in output")
		}
	})

	t.Run("GetUserConnectionsHandler Unauthorized", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/users/%s/connections", u.ID), nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d", w.Code)
		}
	})

	t.Run("SetupUserShareHandler Full Branches", func(t *testing.T) {
		// Fresh user, no share token yet
		su := &models.User{
			ID:           "share-setup-u",
			Username:     "sharesetup",
			PasswordHash: "hash",
			Role:         models.RoleUser,
			Enabled:      true,
			CreatedAt:    time.Now(),
		}
		_, _ = db.CreateUser(ctx, su)

		// First setup: generates token + password hash
		pass := "SharePass123!"
		bodyEnable, _ := json.Marshal(map[string]any{"enabled": true, "password": pass})
		reqEnable := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/users/%s/share/setup", su.ID), bytes.NewReader(bodyEnable))
		wEnable := httptest.NewRecorder()
		r.ServeHTTP(wEnable, reqEnable)
		if wEnable.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", wEnable.Code)
		}

		// Token returned and persisted
		updated, _ := db.GetUser(ctx, su.ID)
		if updated == nil || updated.ShareToken == nil || *updated.ShareToken == "" {
			t.Errorf("expected share token generated")
		}

		// Second setup: reuses existing token, clears password
		bodyClear, _ := json.Marshal(map[string]any{"enabled": true, "password": ""})
		reqClear := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/users/%s/share/setup", su.ID), bytes.NewReader(bodyClear))
		wClear := httptest.NewRecorder()
		r.ServeHTTP(wClear, reqClear)
		if wClear.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", wClear.Code)
		}

		// No enabled field -> defaults true
		bodyDefault := []byte(`{}`)
		reqDefault := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/users/%s/share/setup", su.ID), bytes.NewReader(bodyDefault))
		wDefault := httptest.NewRecorder()
		r.ServeHTTP(wDefault, reqDefault)
		if wDefault.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", wDefault.Code)
		}

		// Bad JSON
		reqBad := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/users/%s/share/setup", su.ID), bytes.NewReader([]byte("bad")))
		wBad := httptest.NewRecorder()
		r.ServeHTTP(wBad, reqBad)
		if wBad.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", wBad.Code)
		}

		// Not found
		req404 := httptest.NewRequest(http.MethodPost, "/api/users/no-user/share/setup", bytes.NewReader(bodyDefault))
		w404 := httptest.NewRecorder()
		r.ServeHTTP(w404, req404)
		if w404.Code != http.StatusNotFound {
			t.Errorf("expected 404, got %d", w404.Code)
		}
	})

	t.Run("AddUserConnectionHandler Bad Protocol", func(t *testing.T) {
		body, _ := json.Marshal(models.AddUserConnectionRequest{ServerID: sID, Protocol: "bogus", Name: "X"})
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/users/%s/connections/add", u.ID), bytes.NewReader(body))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("expected 400 for bad protocol, got %d", w.Code)
		}
	})

	t.Run("AddUserHandler Bad JSON", func(t *testing.T) {
		reqBad := httptest.NewRequest(http.MethodPost, "/api/users/add", bytes.NewReader([]byte("bad")))
		wBad := httptest.NewRecorder()
		r.ServeHTTP(wBad, reqBad)
		if wBad.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", wBad.Code)
		}
	})

	t.Run("AddUserHandler Default Role and Mimicry", func(t *testing.T) {
		mim := string(models.AWGMimicryTLS)
		lim := float64(5)
		body, _ := json.Marshal(map[string]any{
			"username":        "defaultroleuser",
			"password":        "DefaultPass123!",
			"awg_mimicry":     mim,
			"traffic_limit":   lim,
			"expiration_date": time.Now().Add(48 * time.Hour).Format(time.RFC3339),
		})
		req := httptest.NewRequest(http.MethodPost, "/api/users/add", bytes.NewReader(body))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d (body: %s)", w.Code, w.Body.String())
		}

		created, _ := db.GetUserByUsername(ctx, "defaultroleuser")
		if created == nil {
			t.Fatalf("expected user created")
			return
		}
		if created.Role != models.RoleUser {
			t.Errorf("expected default role user, got %s", created.Role)
		}
		if created.AWGMimicry != models.AWGMimicryTLS {
			t.Errorf("expected mimicry TLS, got %s", created.AWGMimicry)
		}
		if created.TrafficLimit != int64(5*1024*1024*1024) {
			t.Errorf("expected traffic limit converted, got %d", created.TrafficLimit)
		}
	})

	t.Run("ToggleUserHandler Bad JSON", func(t *testing.T) {
		reqBad := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/users/%s/toggle", u.ID), bytes.NewReader([]byte("bad")))
		wBad := httptest.NewRecorder()
		r.ServeHTTP(wBad, reqBad)
		if wBad.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", wBad.Code)
		}
	})

	t.Run("AddUserHandler Telemt AutoConnection", func(t *testing.T) {
		protoTelemt := "telemt"
		body, _ := json.Marshal(models.AddUserRequest{
			Username: "telemtuser",
			Password: "TelemtPass123!",
			Role:     models.RoleUser,
			ServerID: &sID,
			Protocol: &protoTelemt,
		})
		req := httptest.NewRequest(http.MethodPost, "/api/users/add", bytes.NewReader(body))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 for telemt add user, got %d (body: %s)", w.Code, w.Body.String())
		}
	})

	t.Run("generateRandomToken", func(t *testing.T) {
		tok := generateRandomToken(16)
		if len(tok) != 32 { // 16 bytes = 32 hex chars
			t.Errorf("expected 32 hex chars, got %d (%s)", len(tok), tok)
		}
	})
}
