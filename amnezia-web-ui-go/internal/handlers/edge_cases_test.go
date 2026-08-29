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
	"github.com/go-chi/chi/v5"
)

func TestHandlers_EdgeCasesAndErrorBranches(t *testing.T) {
	h, db, _ := setupTestHandlers(t)
	ctx := context.Background()

	// 1. Seed test data
	user := &models.User{
		ID:        "u-edge-1",
		Username:  "edgeuser",
		Role:      models.RoleUser,
		Enabled:   true,
		CreatedAt: time.Now(),
	}
	_, _ = db.CreateUser(ctx, user)

	server := &models.Server{
		Name:      "Edge-Server",
		Host:      "192.168.10.10",
		SSHUser:   "root",
		Protocols: map[string]any{"awg": map[string]any{"port": 55424, "installed": true}},
		CreatedAt: time.Now(),
	}
	sID, _ := db.CreateServer(ctx, server)
	_ = db.SaveKnownHost(ctx, sID, "SHA256:testfingerprint")
	_, _ = db.SaveLeaderboardSnapshot(ctx, time.Now().Year(), int(time.Now().Month()))

	conn := &models.UserConnection{
		ID:         "conn-edge-1",
		UserID:     user.ID,
		ServerID:   sID,
		Protocol:   "awg",
		ClientID:   "client-edge-1",
		Name:       "Edge Conn",
		AWGMimicry: models.AWGMimicryAuto,
		CreatedAt:  time.Now(),
	}
	_, _ = db.CreateConnection(ctx, conn)

	sess := &models.SessionData{
		UserID: user.ID,
		Role:   models.RoleUser,
	}

	t.Run("Server Handlers Missing and Invalid IDs", func(t *testing.T) {
		reqNoID := httptest.NewRequest(http.MethodPost, "/api/servers/delete", nil)
		wNoID := httptest.NewRecorder()
		h.DeleteServerHandler(wNoID, reqNoID)
		if wNoID.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", wNoID.Code)
		}

		req404 := httptest.NewRequest(http.MethodPost, "/api/servers/9999/reboot", nil)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("server_id", "9999")
		req404 = req404.WithContext(context.WithValue(req404.Context(), chi.RouteCtxKey, rctx))
		w404 := httptest.NewRecorder()
		h.RebootServerHandler(w404, req404)
		if w404.Code != http.StatusNotFound {
			t.Errorf("expected 404, got %d", w404.Code)
		}

		wClear := httptest.NewRecorder()
		h.ClearServerHandler(wClear, req404)
		if wClear.Code != http.StatusNotFound {
			t.Errorf("expected 404, got %d", wClear.Code)
		}

		wStats := httptest.NewRecorder()
		h.ServerStatsHandler(wStats, req404)
		if wStats.Code != http.StatusNotFound {
			t.Errorf("expected 404, got %d", wStats.Code)
		}

		wCheck := httptest.NewRecorder()
		h.ServerCheckHandler(wCheck, req404)
		if wCheck.Code != http.StatusNotFound {
			t.Errorf("expected 404, got %d", wCheck.Code)
		}
	})

	t.Run("Server Config and Protocol Edge Cases", func(t *testing.T) {
		// AWG speed limit on non-existent server
		reqNoServer := httptest.NewRequest(http.MethodGet, "/api/servers/9999/awg/speed-limit-config", nil)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("server_id", "9999")
		reqNoServer = reqNoServer.WithContext(context.WithValue(reqNoServer.Context(), chi.RouteCtxKey, rctx))
		wNoServer := httptest.NewRecorder()
		h.GetAWGSpeedLimitConfigHandler(wNoServer, reqNoServer)
		if wNoServer.Code != http.StatusNotFound {
			t.Errorf("expected 404, got %d", wNoServer.Code)
		}

		// Apply default speed limits on non-existent server
		wApplyNoSrv := httptest.NewRecorder()
		h.ApplyDefaultSpeedLimitsHandler(wApplyNoSrv, reqNoServer)
		if wApplyNoSrv.Code != http.StatusNotFound {
			t.Errorf("expected 404, got %d", wApplyNoSrv.Code)
		}

		// Server with no AWG installed
		noAWGSrv := &models.Server{
			Name:      "NoAWG",
			Host:      "1.2.3.4",
			Protocols: map[string]any{},
		}
		noAWGID, _ := db.CreateServer(ctx, noAWGSrv)
		reqNoAWG := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/servers/%d/awg/speed-limit-config", noAWGID), nil)
		rctxNoAWG := chi.NewRouteContext()
		rctxNoAWG.URLParams.Add("server_id", fmt.Sprintf("%d", noAWGID))
		reqNoAWG = reqNoAWG.WithContext(context.WithValue(reqNoAWG.Context(), chi.RouteCtxKey, rctxNoAWG))
		wNoAWG := httptest.NewRecorder()
		h.GetAWGSpeedLimitConfigHandler(wNoAWG, reqNoAWG)
		if wNoAWG.Code != http.StatusBadRequest {
			t.Errorf("expected 400 for non-installed AWG, got %d", wNoAWG.Code)
		}

		wApplyNoAWG := httptest.NewRecorder()
		h.ApplyDefaultSpeedLimitsHandler(wApplyNoAWG, reqNoAWG)
		if wApplyNoAWG.Code != http.StatusBadRequest {
			t.Errorf("expected 400 for non-installed AWG, got %d", wApplyNoAWG.Code)
		}
	})

	t.Run("Server Connection Management Edge Cases", func(t *testing.T) {
		// Edit connection re-assign and rename
		newUID := user.ID
		newName := "Renamed Connection"
		bodyEdit, _ := json.Marshal(models.EditConnectionRequest{
			ClientID: conn.ClientID,
			Protocol: "awg",
			UserID:   &newUID,
			Name:     &newName,
		})
		reqEdit := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/servers/%d/connections/edit", sID), bytes.NewReader(bodyEdit))
		rctxEdit := chi.NewRouteContext()
		rctxEdit.URLParams.Add("server_id", fmt.Sprintf("%d", sID))
		reqEdit = reqEdit.WithContext(context.WithValue(reqEdit.Context(), chi.RouteCtxKey, rctxEdit))
		wEdit := httptest.NewRecorder()
		h.EditServerConnectionHandler(wEdit, reqEdit)
		if wEdit.Code != http.StatusOK {
			t.Errorf("expected 200 for edit connection, got %d", wEdit.Code)
		}

		// Edit connection unassign (empty string userID)
		emptyUID := ""
		bodyUnassign, _ := json.Marshal(models.EditConnectionRequest{
			ClientID: conn.ClientID,
			Protocol: "awg",
			UserID:   &emptyUID,
		})
		reqUnassign := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/servers/%d/connections/edit", sID), bytes.NewReader(bodyUnassign))
		reqUnassign = reqUnassign.WithContext(context.WithValue(reqUnassign.Context(), chi.RouteCtxKey, rctxEdit))
		wUnassign := httptest.NewRecorder()
		h.EditServerConnectionHandler(wUnassign, reqUnassign)
		if wUnassign.Code != http.StatusOK {
			t.Errorf("expected 200 for unassign connection, got %d", wUnassign.Code)
		}

		// Remove connection bad protocol
		bodyRemoveBad, _ := json.Marshal(models.ConnectionActionRequest{
			ClientID: "c-123",
			Protocol: "unknown-proto",
		})
		reqRemoveBad := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/servers/%d/connections/remove", sID), bytes.NewReader(bodyRemoveBad))
		reqRemoveBad = reqRemoveBad.WithContext(context.WithValue(reqRemoveBad.Context(), chi.RouteCtxKey, rctxEdit))
		wRemoveBad := httptest.NewRecorder()
		h.RemoveServerConnectionHandler(wRemoveBad, reqRemoveBad)
		if wRemoveBad.Code != http.StatusBadRequest {
			t.Errorf("expected 400 for unknown protocol, got %d", wRemoveBad.Code)
		}
	})

	t.Run("Backup Download and Restore with KnownHosts", func(t *testing.T) {
		reqDownload := httptest.NewRequest(http.MethodGet, "/api/settings/backup/download", nil)
		wDownload := httptest.NewRecorder()
		h.DownloadBackupHandler(wDownload, reqDownload)
		if wDownload.Code != http.StatusOK {
			t.Fatalf("expected 200 for backup download, got %d", wDownload.Code)
		}

		var backup models.BackupData
		if err := json.Unmarshal(wDownload.Body.Bytes(), &backup); err != nil {
			t.Fatalf("failed to unmarshal backup: %v", err)
		}
		if len(backup.KnownHosts) == 0 {
			t.Errorf("expected known hosts to be exported in backup")
		}

		// Restore backup with known hosts
		restoreMap := h.restoreBackupData(ctx, &backup)
		if restoreMap == nil {
			t.Errorf("expected restoreMap not nil")
		}
	})

	t.Run("User Connection Config and Kit Handlers", func(t *testing.T) {
		// Re-create connection for user
		newC := &models.UserConnection{
			ID:         "conn-cfg-test",
			UserID:     user.ID,
			ServerID:   sID,
			Protocol:   "awg",
			ClientID:   "client-cfg-test",
			Name:       "Cfg Conn",
			AWGMimicry: models.AWGMimicryAuto,
			CreatedAt:  time.Now(),
		}
		_, _ = db.CreateConnection(ctx, newC)

		reqCfg := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/connections/%s/config", newC.ID), nil)
		rctxCfg := chi.NewRouteContext()
		rctxCfg.URLParams.Add("connection_id", newC.ID)
		reqCfg = reqCfg.WithContext(context.WithValue(reqCfg.Context(), chi.RouteCtxKey, rctxCfg))
		reqCfg = reqCfg.WithContext(middleware.WithSession(reqCfg.Context(), sess))
		wCfg := httptest.NewRecorder()
		h.UserGetConnectionConfigHandler(wCfg, reqCfg)
		if wCfg.Code != http.StatusOK {
			t.Errorf("expected 200 for user connection config, got %d", wCfg.Code)
		}

		// Missing connection ID
		reqMissing := httptest.NewRequest(http.MethodPost, "/api/connections/missing-id/config", nil)
		rctxMissing := chi.NewRouteContext()
		rctxMissing.URLParams.Add("connection_id", "missing-id")
		reqMissing = reqMissing.WithContext(context.WithValue(reqMissing.Context(), chi.RouteCtxKey, rctxMissing))
		reqMissing = reqMissing.WithContext(middleware.WithSession(reqMissing.Context(), sess))
		wMissing := httptest.NewRecorder()
		h.UserGetConnectionConfigHandler(wMissing, reqMissing)
		if wMissing.Code != http.StatusNotFound {
			t.Errorf("expected 404 for missing connection config, got %d", wMissing.Code)
		}

		// UserGetConnectionKitHandler
		reqKit := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/connections/%s/kit", newC.ID), nil)
		reqKit = reqKit.WithContext(context.WithValue(reqKit.Context(), chi.RouteCtxKey, rctxCfg))
		reqKit = reqKit.WithContext(middleware.WithSession(reqKit.Context(), sess))
		wKit := httptest.NewRecorder()
		h.UserGetConnectionKitHandler(wKit, reqKit)
		if wKit.Code != http.StatusOK {
			t.Errorf("expected 200 for user connection kit, got %d", wKit.Code)
		}

		// UserRenameConnectionHandler
		bodyRename, _ := json.Marshal(models.RenameConnectionRequest{Name: "Super New Name"})
		reqRename := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/connections/%s/rename", newC.ID), bytes.NewReader(bodyRename))
		reqRename = reqRename.WithContext(context.WithValue(reqRename.Context(), chi.RouteCtxKey, rctxCfg))
		reqRename = reqRename.WithContext(middleware.WithSession(reqRename.Context(), sess))
		wRename := httptest.NewRecorder()
		h.UserRenameConnectionHandler(wRename, reqRename)
		if wRename.Code != http.StatusOK {
			t.Errorf("expected 200 for user rename connection, got %d", wRename.Code)
		}

		// Server connection kit and config
		reqSrvKit := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/servers/%d/connections/kit?client_id=%s&protocol=awg", sID, newC.ClientID), nil)
		rctxSrv := chi.NewRouteContext()
		rctxSrv.URLParams.Add("server_id", fmt.Sprintf("%d", sID))
		reqSrvKit = reqSrvKit.WithContext(context.WithValue(reqSrvKit.Context(), chi.RouteCtxKey, rctxSrv))
		wSrvKit := httptest.NewRecorder()
		h.GetServerConnectionKitHandler(wSrvKit, reqSrvKit)
		if wSrvKit.Code != http.StatusOK {
			t.Errorf("expected 200 for server connection kit, got %d", wSrvKit.Code)
		}

		bodyCfg, _ := json.Marshal(models.ConnectionActionRequest{
			ClientID: newC.ClientID,
			Protocol: "awg",
		})
		reqSrvCfg := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/servers/%d/connections/config", sID), bytes.NewReader(bodyCfg))
		reqSrvCfg = reqSrvCfg.WithContext(context.WithValue(reqSrvCfg.Context(), chi.RouteCtxKey, rctxSrv))
		wSrvCfg := httptest.NewRecorder()
		h.GetServerConnectionConfigHandler(wSrvCfg, reqSrvCfg)
		if wSrvCfg.Code != http.StatusOK {
			t.Errorf("expected 200 for server connection config, got %d", wSrvCfg.Code)
		}
	})

	t.Run("Additional Server Connections and VPN Handlers", func(t *testing.T) {
		rctxRot := chi.NewRouteContext()
		rctxRot.URLParams.Add("server_id", fmt.Sprintf("%d", sID))
		rctxRot.URLParams.Add("client_id", conn.ClientID)

		// Auto trial
		bodyTrial, _ := json.Marshal(map[string]any{
			"username": "trialuser",
			"protocol": "awg",
		})
		reqTrial := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/servers/%d/connections/auto-trial", sID), bytes.NewReader(bodyTrial))
		reqTrial = reqTrial.WithContext(context.WithValue(reqTrial.Context(), chi.RouteCtxKey, rctxRot))
		wTrial := httptest.NewRecorder()
		h.AutoTrialHandler(wTrial, reqTrial)
		if wTrial.Code != http.StatusOK {
			t.Errorf("expected 200 for auto-trial, got %d", wTrial.Code)
		}

		// Toggle server connection
		bodyToggle, _ := json.Marshal(map[string]any{
			"client_id": conn.ClientID,
			"protocol":  "awg",
			"enabled":   false,
		})
		reqToggle := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/servers/%d/connections/toggle", sID), bytes.NewReader(bodyToggle))
		reqToggle = reqToggle.WithContext(context.WithValue(reqToggle.Context(), chi.RouteCtxKey, rctxRot))
		wToggle := httptest.NewRecorder()
		h.ToggleServerConnectionHandler(wToggle, reqToggle)
		if wToggle.Code != http.StatusOK {
			t.Errorf("expected 200 for toggle connection, got %d", wToggle.Code)
		}

		// Leaderboard handler
		reqLeader := httptest.NewRequest(http.MethodGet, "/api/leaderboard?period=monthly", nil)
		wLeader := httptest.NewRecorder()
		h.LeaderboardHandler(wLeader, reqLeader)
		if wLeader.Code != http.StatusOK {
			t.Errorf("expected 200 for monthly leaderboard, got %d", wLeader.Code)
		}

		reqLeaderLast := httptest.NewRequest(http.MethodGet, "/api/leaderboard?period=last-month", nil)
		reqLeaderLast = reqLeaderLast.WithContext(middleware.WithSession(reqLeaderLast.Context(), sess))
		wLeaderLast := httptest.NewRecorder()
		h.LeaderboardHandler(wLeaderLast, reqLeaderLast)
		if wLeaderLast.Code != http.StatusOK {
			t.Errorf("expected 200 for last-month leaderboard, got %d", wLeaderLast.Code)
		}

		reqLeaderAll := httptest.NewRequest(http.MethodGet, "/api/leaderboard?period=all-time", nil)
		reqLeaderAll = reqLeaderAll.WithContext(middleware.WithSession(reqLeaderAll.Context(), sess))
		wLeaderAll := httptest.NewRecorder()
		h.LeaderboardHandler(wLeaderAll, reqLeaderAll)
		if wLeaderAll.Code != http.StatusOK {
			t.Errorf("expected 200 for all-time leaderboard, got %d", wLeaderAll.Code)
		}
	})

	t.Run("Auth Login and Setup Validation Branches", func(t *testing.T) {
		// Login with invalid credentials
		bodyBadCreds, _ := json.Marshal(models.LoginRequest{
			Username: user.Username,
			Password: "WrongPassword123!",
		})
		reqBadLogin := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(bodyBadCreds))
		wBadLogin := httptest.NewRecorder()
		h.APILoginHandler(wBadLogin, reqBadLogin)
		if wBadLogin.Code != http.StatusUnauthorized {
			t.Errorf("expected 401 for bad password, got %d", wBadLogin.Code)
		}

		// Login with disabled user
		disabledUser := &models.User{
			ID:           "u-disabled-test",
			Username:     "disableduser",
			PasswordHash: "$2a$10$xyz",
			Enabled:      false,
		}
		_, _ = db.CreateUser(ctx, disabledUser)
		bodyDisabled, _ := json.Marshal(models.LoginRequest{
			Username: "disableduser",
			Password: "SomePassword123!",
		})
		reqDisLogin := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(bodyDisabled))
		wDisLogin := httptest.NewRecorder()
		h.APILoginHandler(wDisLogin, reqDisLogin)
		if wDisLogin.Code != http.StatusUnauthorized {
			t.Errorf("expected 401 for disabled user login, got %d", wDisLogin.Code)
		}

		// Setup password confirmation mismatch
		bodyMismatch, _ := json.Marshal(models.SetupRequest{
			Username:        "admin2",
			Password:        "Pass12345!A",
			ConfirmPassword: "Pass12345!B",
		})
		reqMismatch := httptest.NewRequest(http.MethodPost, "/api/auth/setup", bytes.NewReader(bodyMismatch))
		wMismatch := httptest.NewRecorder()
		h.APISetupHandler(wMismatch, reqMismatch)
		if wMismatch.Code != http.StatusBadRequest {
			t.Errorf("expected 400 for password mismatch, got %d", wMismatch.Code)
		}
	})

	t.Run("Template Rendering and Kit Builders", func(t *testing.T) {
		// RenderTemplate error case (non-existent template)
		wTmpl := httptest.NewRecorder()
		reqTmpl := httptest.NewRequest(http.MethodGet, "/", nil)
		err := RenderTemplate(wTmpl, reqTmpl, db, "non_existent_page.html", nil)
		if err == nil {
			t.Errorf("expected error for missing template")
		}

		// BuildConnectionKitZip with content
		zipBytes, err := BuildConnectionKitZip("my-telemt", "tg://proxy?server=1.2.3.4", "vpn://testlink")
		if err != nil || len(zipBytes) == 0 {
			t.Errorf("expected successful telemt kit zip build, got err=%v", err)
		}

		// BuildConnectionKitZip with empty name fallback
		zipEmpty, err := BuildConnectionKitZip("", "nameserver 1.2.3.4", "")
		if err != nil || len(zipEmpty) == 0 {
			t.Errorf("expected successful empty name kit zip build, got err=%v", err)
		}
	})

	t.Run("Sync Delete and VPN Controls", func(t *testing.T) {
		// Sync delete with no deleted users
		reqSyncDel := httptest.NewRequest(http.MethodPost, "/api/settings/sync_delete", nil)
		wSyncDel := httptest.NewRecorder()
		h.SyncDeleteHandler(wSyncDel, reqSyncDel)
		if wSyncDel.Code != http.StatusOK {
			t.Errorf("expected 200 for sync delete, got %d", wSyncDel.Code)
		}

		// VPN enable/disable backend on unregistered backend
		rctxVPN := chi.NewRouteContext()
		rctxVPN.URLParams.Add("server_id", fmt.Sprintf("%d", sID))
		reqEnable := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/vpn/backends/%d/enable", sID), nil)
		reqEnable = reqEnable.WithContext(context.WithValue(reqEnable.Context(), chi.RouteCtxKey, rctxVPN))
		wEnable := httptest.NewRecorder()
		h.VPNEnableBackendHandler(wEnable, reqEnable)
		if wEnable.Code != http.StatusInternalServerError {
			t.Errorf("expected 500 for unregistered backend enable, got %d", wEnable.Code)
		}

		wDisable := httptest.NewRecorder()
		h.VPNDisableBackendHandler(wDisable, reqEnable)
		if wDisable.Code != http.StatusInternalServerError {
			t.Errorf("expected 500 for unregistered backend disable, got %d", wDisable.Code)
		}

		// Server config get & save (SSH fails gracefully without mock pool)
		bodyProto, _ := json.Marshal(models.ProtocolRequest{Protocol: "awg"})
		reqGetCfg := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/servers/%d/server_config", sID), bytes.NewReader(bodyProto))
		rctxGetCfg := chi.NewRouteContext()
		rctxGetCfg.URLParams.Add("server_id", fmt.Sprintf("%d", sID))
		reqGetCfg = reqGetCfg.WithContext(context.WithValue(reqGetCfg.Context(), chi.RouteCtxKey, rctxGetCfg))
		wGetCfg := httptest.NewRecorder()
		h.GetServerConfigHandler(wGetCfg, reqGetCfg)
		if wGetCfg.Code != http.StatusBadRequest {
			t.Errorf("expected 400 connection_failed, got %d", wGetCfg.Code)
		}

		bodySaveCfg, _ := json.Marshal(models.ServerConfigSaveRequest{
			Protocol: "awg",
			Config:   "[Interface]\nPrivateKey = xyz\n",
		})
		reqSaveCfg := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/servers/%d/server_config/save", sID), bytes.NewReader(bodySaveCfg))
		reqSaveCfg = reqSaveCfg.WithContext(context.WithValue(reqSaveCfg.Context(), chi.RouteCtxKey, rctxGetCfg))
		wSaveCfg := httptest.NewRecorder()
		h.SaveServerConfigHandler(wSaveCfg, reqSaveCfg)
		if wSaveCfg.Code != http.StatusBadRequest {
			t.Errorf("expected 400 connection_failed, got %d", wSaveCfg.Code)
		}
	})
}
