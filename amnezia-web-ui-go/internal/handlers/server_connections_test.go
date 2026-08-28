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

func TestServerConnectionsHandlers(t *testing.T) {
	mockSSH := &testMockSSHClient{}
	h, db, _ := setupTestHandlersWithMockSSH(t, mockSSH)
	ctx := context.Background()

	srv := &models.Server{
		Name:      "Production-AWG",
		Host:      "192.168.1.105",
		SSHPort:   22,
		SSHUser:   "root",
		SSHPass:   "pass123",
		Protocols: map[string]any{"awg": map[string]any{"port": 55424, "installed": true}, "telemt": map[string]any{"port": 443, "installed": true}},
		CreatedAt: time.Now(),
	}
	serverID, err := db.CreateServer(ctx, srv)
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}
	mockSSH.serverID = &serverID

	u := &models.User{
		ID:           "sc-user-1",
		Username:     "scuser",
		PasswordHash: "hash",
		Role:         models.RoleUser,
		Enabled:      true,
		CreatedAt:    time.Now(),
	}
	_, _ = db.CreateUser(ctx, u)

	c := &models.UserConnection{
		ID:         "sc-conn-1",
		UserID:     u.ID,
		ServerID:   serverID,
		Protocol:   "awg",
		ClientID:   "client-1",
		Name:       "Test Device",
		CreatedAt:  time.Now(),
		AWGMimicry: models.AWGMimicryAuto,
	}
	_, _ = db.CreateConnection(ctx, c)

	adminSess := &models.SessionData{
		UserID: "admin-1",
		Role:   models.RoleAdmin,
	}

	otherUserSess := &models.SessionData{
		UserID: "other-user-id",
		Role:   models.RoleUser,
	}

	r := setupFullServerConnectionsRouter(h)

	t.Run("GetServerConnectionsHandler", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/servers/%d/connections?protocol=awg", serverID), nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}

		// Invalid ServerID
		reqBad := httptest.NewRequest(http.MethodGet, "/api/servers/invalid-id/connections", nil)
		wBad := httptest.NewRecorder()
		r.ServeHTTP(wBad, reqBad)
		if wBad.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", wBad.Code)
		}

		// Unknown Server
		req404 := httptest.NewRequest(http.MethodGet, "/api/servers/99999/connections", nil)
		w404 := httptest.NewRecorder()
		r.ServeHTTP(w404, req404)
		if w404.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d", w404.Code)
		}
	})

	t.Run("AddServerConnectionHandler Success & Validation", func(t *testing.T) {
		quota := "1000"
		maxIPs := 2
		exp := "30"
		down := 10
		up := 10
		mim := string(models.AWGMimicryAuto)
		body, _ := json.Marshal(models.AddConnectionRequest{
			Protocol:          "awg",
			Name:              "New Server Conn",
			UserID:            &u.ID,
			TelemtQuota:       &quota,
			TelemtMaxIPs:      &maxIPs,
			TelemtExpiry:      &exp,
			AWGSpeedLimitDown: &down,
			AWGSpeedLimitUp:   &up,
			AWGMimicry:        &mim,
		})
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/servers/%d/connections/add", serverID), bytes.NewReader(body))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d (body: %s)", w.Code, w.Body.String())
		}

		// Invalid body
		reqBad := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/servers/%d/connections/add", serverID), bytes.NewReader([]byte("bad-json")))
		wBad := httptest.NewRecorder()
		r.ServeHTTP(wBad, reqBad)
		if wBad.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", wBad.Code)
		}

		// Invalid protocol
		badProto, _ := json.Marshal(models.AddConnectionRequest{Protocol: "unknown", Name: "bad"})
		reqBadProto := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/servers/%d/connections/add", serverID), bytes.NewReader(badProto))
		wBadProto := httptest.NewRecorder()
		r.ServeHTTP(wBadProto, reqBadProto)
		if wBadProto.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", wBadProto.Code)
		}

		// Valid protocol but not installed on server
		uninstalledProto, _ := json.Marshal(models.AddConnectionRequest{Protocol: "dns", Name: "dns client"})
		reqUninstalled := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/servers/%d/connections/add", serverID), bytes.NewReader(uninstalledProto))
		wUninstalled := httptest.NewRecorder()
		r.ServeHTTP(wUninstalled, reqUninstalled)
		if wUninstalled.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for uninstalled protocol, got %d", wUninstalled.Code)
		}
	})

	t.Run("RotateMimicryHandler", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/servers/%d/connections/client-1/rotate-mimicry", serverID), nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		// awgMgr may not be set or return error
		if w.Code != http.StatusOK && w.Code != http.StatusBadRequest && w.Code != http.StatusInternalServerError {
			t.Fatalf("unexpected code: %d", w.Code)
		}

		// 404 server
		req404 := httptest.NewRequest(http.MethodPost, "/api/servers/99999/connections/client-1/rotate-mimicry", nil)
		w404 := httptest.NewRecorder()
		r.ServeHTTP(w404, req404)
		if w404.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d", w404.Code)
		}
	})

	t.Run("AutoTrialHandler", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/servers/%d/connections/auto-trial", serverID), nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d (body: %s)", w.Code, w.Body.String())
		}
	})

	t.Run("GetServerConnectionKitHandler Branches", func(t *testing.T) {
		// 1. Query Params
		reqQuery := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/servers/%d/connections/kit?client_id=client-1&protocol=awg", serverID), nil)
		reqQueryCtx := middleware.WithSession(reqQuery.Context(), adminSess)
		wQuery := httptest.NewRecorder()
		r.ServeHTTP(wQuery, reqQuery.WithContext(reqQueryCtx))
		if wQuery.Code != http.StatusOK {
			t.Errorf("expected 200 via query params, got %d", wQuery.Code)
		}

		// 2. Form Body
		reqForm := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/servers/%d/connections/kit", serverID), strings.NewReader("client_id=client-1&protocol=awg"))
		reqFormCtx := middleware.WithSession(reqForm.Context(), adminSess)
		wForm := httptest.NewRecorder()
		r.ServeHTTP(wForm, reqForm.WithContext(reqFormCtx))
		if wForm.Code != http.StatusOK {
			t.Errorf("expected 200 via form body, got %d", wForm.Code)
		}

		// 3. Owned User
		ownerSess := &models.SessionData{UserID: u.ID, Role: models.RoleUser}
		bodyOwner, _ := json.Marshal(models.ConnectionActionRequest{Protocol: "awg", ClientID: "client-1"})
		reqOwner := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/servers/%d/connections/kit", serverID), bytes.NewReader(bodyOwner))
		reqOwnerCtx := middleware.WithSession(reqOwner.Context(), ownerSess)
		wOwner := httptest.NewRecorder()
		r.ServeHTTP(wOwner, reqOwner.WithContext(reqOwnerCtx))
		if wOwner.Code != http.StatusOK {
			t.Errorf("expected 200 for owned user kit, got %d", wOwner.Code)
		}

		// 4. Missing Client ID
		reqEmpty := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/servers/%d/connections/kit", serverID), bytes.NewReader([]byte("{}")))
		wEmpty := httptest.NewRecorder()
		r.ServeHTTP(wEmpty, reqEmpty)
		if wEmpty.Code != http.StatusBadRequest {
			t.Errorf("expected 400 for missing client_id, got %d", wEmpty.Code)
		}
	})

	t.Run("GetServerConnectionConfigHandler Owned User", func(t *testing.T) {
		ownerSess := &models.SessionData{UserID: u.ID, Role: models.RoleUser}
		bodyOwner, _ := json.Marshal(models.ConnectionActionRequest{Protocol: "awg", ClientID: "client-1"})
		reqOwner := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/servers/%d/connections/config", serverID), bytes.NewReader(bodyOwner))
		reqOwnerCtx := middleware.WithSession(reqOwner.Context(), ownerSess)
		wOwner := httptest.NewRecorder()
		r.ServeHTTP(wOwner, reqOwner.WithContext(reqOwnerCtx))
		if wOwner.Code != http.StatusOK {
			t.Errorf("expected 200 for owned user config, got %d", wOwner.Code)
		}
	})

	t.Run("GetServerConnectionConfigHandler Bad Body and Bad ID", func(t *testing.T) {
		// Bad JSON body
		reqBad := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/servers/%d/connections/config", serverID), bytes.NewReader([]byte("bad")))
		wBad := httptest.NewRecorder()
		r.ServeHTTP(wBad, reqBad)
		if wBad.Code != http.StatusBadRequest {
			t.Errorf("expected 400 for bad body, got %d", wBad.Code)
		}

		// Validation failure (missing client_id)
		reqNoClient := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/servers/%d/connections/config", serverID), bytes.NewReader([]byte(`{"protocol":"awg"}`)))
		wNoClient := httptest.NewRecorder()
		r.ServeHTTP(wNoClient, reqNoClient)
		if wNoClient.Code != http.StatusBadRequest {
			t.Errorf("expected 400 for missing client_id, got %d", wNoClient.Code)
		}

		// Bad protocol
		reqBadProto := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/servers/%d/connections/config", serverID), bytes.NewReader([]byte(`{"protocol":"nope","client_id":"c1"}`)))
		wBadProto := httptest.NewRecorder()
		r.ServeHTTP(wBadProto, reqBadProto)
		if wBadProto.Code != http.StatusBadRequest {
			t.Errorf("expected 400 for bad protocol, got %d", wBadProto.Code)
		}
	})

	t.Run("GetServerConnectionConfigHandler Server Not Found", func(t *testing.T) {
		body, _ := json.Marshal(models.ConnectionActionRequest{Protocol: "awg", ClientID: "client-1"})
		req404 := httptest.NewRequest(http.MethodPost, "/api/servers/99999/connections/config", bytes.NewReader(body))
		w404 := httptest.NewRecorder()
		r.ServeHTTP(w404, req404)
		if w404.Code != http.StatusNotFound {
			t.Errorf("expected 404, got %d", w404.Code)
		}
	})

	t.Run("RotateMimicryHandler Empty ClientID", func(t *testing.T) {
		reqDirect := httptest.NewRequest(http.MethodPost, "/api/servers/1/connections//rotate-mimicry", nil)
		wDirect := httptest.NewRecorder()
		h.RotateMimicryHandler(wDirect, reqDirect)
		if wDirect.Code != http.StatusBadRequest {
			t.Errorf("expected 400 for empty client_id, got %d", wDirect.Code)
		}
	})

	t.Run("AutoTrialHandler With PublicKey", func(t *testing.T) {
		sPub := &models.Server{
			Name:    "Pub-Server",
			Host:    "1.1.1.10",
			SSHUser: "root",
			Protocols: map[string]any{
				"awg": map[string]any{
					"installed":  true,
					"public_key": "test-pub-key-123",
				},
			},
		}
		sPubID, _ := db.CreateServer(ctx, sPub)

		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/servers/%d/connections/auto-trial", sPubID), nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}
		if !strings.Contains(w.Body.String(), "reachability") {
			t.Errorf("expected reachability in trial results")
		}

		// 404 server
		req404 := httptest.NewRequest(http.MethodPost, "/api/servers/99999/connections/auto-trial", nil)
		w404 := httptest.NewRecorder()
		r.ServeHTTP(w404, req404)
		if w404.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d", w404.Code)
		}
	})

	t.Run("RemoveServerConnectionHandler", func(t *testing.T) {
		body, _ := json.Marshal(models.ConnectionActionRequest{
			Protocol: "awg",
			ClientID: "client-1",
		})
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/servers/%d/connections/remove", serverID), bytes.NewReader(body))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}
	})

	t.Run("EditServerConnectionHandler", func(t *testing.T) {
		down := 20
		up := 20
		mim := string(models.AWGMimicryTLS)
		name := "Edited Device"
		body, _ := json.Marshal(models.EditConnectionRequest{
			Protocol:          "awg",
			ClientID:          "client-1",
			Name:              &name,
			UserID:            &u.ID,
			AWGSpeedLimitDown: &down,
			AWGSpeedLimitUp:   &up,
			AWGMimicry:        &mim,
		})
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/servers/%d/connections/edit", serverID), bytes.NewReader(body))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}
	})

	t.Run("GetServerConnectionConfigHandler Permissions", func(t *testing.T) {
		body, _ := json.Marshal(models.ConnectionActionRequest{
			Protocol: "awg",
			ClientID: "client-1",
		})

		reqAdmin := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/servers/%d/connections/config", serverID), bytes.NewReader(body))
		reqAdminCtx := middleware.WithSession(reqAdmin.Context(), adminSess)
		wAdmin := httptest.NewRecorder()
		r.ServeHTTP(wAdmin, reqAdmin.WithContext(reqAdminCtx))
		if wAdmin.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", wAdmin.Code)
		}

		reqOther := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/servers/%d/connections/config", serverID), bytes.NewReader(body))
		reqOtherCtx := middleware.WithSession(reqOther.Context(), otherUserSess)
		wOther := httptest.NewRecorder()
		r.ServeHTTP(wOther, reqOther.WithContext(reqOtherCtx))
		if wOther.Code != http.StatusForbidden {
			t.Fatalf("expected 403, got %d", wOther.Code)
		}
	})

	t.Run("EditServerConnectionHandler All Branches", func(t *testing.T) {
		// Branch: no matching conn, userID set -> creates new conn
		bodyNewConn, _ := json.Marshal(models.EditConnectionRequest{
			Protocol: "awg",
			ClientID: "client-unassigned",
			UserID:   &u.ID,
			Name:     func() *string { s := "Unassigned Edit"; return &s }(),
		})
		reqNewConn := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/servers/%d/connections/edit", serverID), bytes.NewReader(bodyNewConn))
		wNewConn := httptest.NewRecorder()
		r.ServeHTTP(wNewConn, reqNewConn)
		if wNewConn.Code != http.StatusOK {
			t.Errorf("expected 200 for edit creating conn, got %d", wNewConn.Code)
		}

		// Branch: matching conn, no userID, name only -> updates name
		bodyNameOnly, _ := json.Marshal(models.EditConnectionRequest{
			Protocol: "awg",
			ClientID: "client-1",
			Name:     func() *string { s := "Renamed Only"; return &s }(),
		})
		reqNameOnly := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/servers/%d/connections/edit", serverID), bytes.NewReader(bodyNameOnly))
		wNameOnly := httptest.NewRecorder()
		r.ServeHTTP(wNameOnly, reqNameOnly)
		if wNameOnly.Code != http.StatusOK {
			t.Errorf("expected 200 for edit rename only, got %d", wNameOnly.Code)
		}

		// Branch: empty userID string -> deletes matching conn
		emptyUser := ""
		bodyDelete, _ := json.Marshal(models.EditConnectionRequest{
			Protocol: "awg",
			ClientID: "client-1",
			UserID:   &emptyUser,
		})
		reqDelete := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/servers/%d/connections/edit", serverID), bytes.NewReader(bodyDelete))
		wDelete := httptest.NewRecorder()
		r.ServeHTTP(wDelete, reqDelete)
		if wDelete.Code != http.StatusOK {
			t.Errorf("expected 200 for edit delete binding, got %d", wDelete.Code)
		}

		// Branch: missing client_id -> 400
		reqNoClient := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/servers/%d/connections/edit", serverID), bytes.NewReader([]byte(`{"protocol":"awg"}`)))
		wNoClient := httptest.NewRecorder()
		r.ServeHTTP(wNoClient, reqNoClient)
		if wNoClient.Code != http.StatusBadRequest {
			t.Errorf("expected 400 for missing client_id, got %d", wNoClient.Code)
		}
	})

	t.Run("ToggleServerConnectionHandler", func(t *testing.T) {
		// Enabled = true
		body, _ := json.Marshal(map[string]any{
			"protocol":  "awg",
			"client_id": "client-1",
			"enabled":   true,
		})
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/servers/%d/connections/toggle", serverID), bytes.NewReader(body))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}

		// Enabled = false
		bodyOff, _ := json.Marshal(map[string]any{
			"protocol":  "awg",
			"client_id": "client-1",
			"enabled":   false,
		})
		reqOff := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/servers/%d/connections/toggle", serverID), bytes.NewReader(bodyOff))
		wOff := httptest.NewRecorder()
		r.ServeHTTP(wOff, reqOff)
		if wOff.Code != http.StatusOK {
			t.Fatalf("expected 200 for disable, got %d", wOff.Code)
		}

		// Not found server
		req404 := httptest.NewRequest(http.MethodPost, "/api/servers/99999/connections/toggle", bytes.NewReader(body))
		w404 := httptest.NewRecorder()
		r.ServeHTTP(w404, req404)
		if w404.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d", w404.Code)
		}
	})

	t.Run("ToggleServerConnectionHandler Enable Field and Missing Client", func(t *testing.T) {
		// enable field (instead of enabled)
		bodyEnableField, _ := json.Marshal(map[string]any{
			"protocol":  "awg",
			"client_id": "client-1",
			"enable":    true,
		})
		reqEnableField := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/servers/%d/connections/toggle", serverID), bytes.NewReader(bodyEnableField))
		wEnableField := httptest.NewRecorder()
		r.ServeHTTP(wEnableField, reqEnableField)
		if wEnableField.Code != http.StatusOK {
			t.Errorf("expected 200 for enable field, got %d", wEnableField.Code)
		}

		// missing client_id
		reqNoClient := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/servers/%d/connections/toggle", serverID), bytes.NewReader([]byte(`{"protocol":"awg"}`)))
		wNoClient := httptest.NewRecorder()
		r.ServeHTTP(wNoClient, reqNoClient)
		if wNoClient.Code != http.StatusBadRequest {
			t.Errorf("expected 400 for missing client_id, got %d", wNoClient.Code)
		}
	})

	t.Run("GetServerConnectionsHandler With User Enrichment", func(t *testing.T) {
		// Verify assigned_user / assigned_user_id enrichment
		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/servers/%d/connections?protocol=awg", serverID), nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}
	})

	t.Run("AddServerConnectionHandler Without UserID and Server Not Found", func(t *testing.T) {
		// Without UserID (no DB link)
		bodyNoUser, _ := json.Marshal(models.AddConnectionRequest{
			Protocol: "awg",
			Name:     "Unlinked Server Conn",
		})
		reqNoUser := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/servers/%d/connections/add", serverID), bytes.NewReader(bodyNoUser))
		wNoUser := httptest.NewRecorder()
		r.ServeHTTP(wNoUser, reqNoUser)
		if wNoUser.Code != http.StatusOK {
			t.Errorf("expected 200 for unlinked add, got %d", wNoUser.Code)
		}

		// Server not found
		req404 := httptest.NewRequest(http.MethodPost, "/api/servers/99999/connections/add", bytes.NewReader(bodyNoUser))
		w404 := httptest.NewRecorder()
		r.ServeHTTP(w404, req404)
		if w404.Code != http.StatusNotFound {
			t.Errorf("expected 404, got %d", w404.Code)
		}
	})

	t.Run("GetProtocolClientsHandler", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/servers/%d/awg/clients", serverID), nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}

		// Bad proto
		reqBad := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/servers/%d/unknown_proto/clients", serverID), nil)
		wBad := httptest.NewRecorder()
		r.ServeHTTP(wBad, reqBad)
		if wBad.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", wBad.Code)
		}

		// Not found server
		req404 := httptest.NewRequest(http.MethodGet, "/api/servers/99999/awg/clients", nil)
		w404 := httptest.NewRecorder()
		r.ServeHTTP(w404, req404)
		if w404.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d", w404.Code)
		}
	})

	t.Run("Additional Error Branches", func(t *testing.T) {
		// EditServerConnection with bad body & not found
		reqBadEdit := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/servers/%d/connections/edit", serverID), bytes.NewReader([]byte("invalid")))
		wBadEdit := httptest.NewRecorder()
		r.ServeHTTP(wBadEdit, reqBadEdit)
		if wBadEdit.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", wBadEdit.Code)
		}

		req404Edit := httptest.NewRequest(http.MethodPost, "/api/servers/99999/connections/edit", bytes.NewReader([]byte(`{"protocol":"awg","client_id":"c1"}`)))
		w404Edit := httptest.NewRecorder()
		r.ServeHTTP(w404Edit, req404Edit)
		if w404Edit.Code != http.StatusNotFound {
			t.Errorf("expected 404, got %d", w404Edit.Code)
		}

		// RemoveServerConnection bad body & not found
		reqBadRem := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/servers/%d/connections/remove", serverID), bytes.NewReader([]byte("invalid")))
		wBadRem := httptest.NewRecorder()
		r.ServeHTTP(wBadRem, reqBadRem)
		if wBadRem.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", wBadRem.Code)
		}

		req404Rem := httptest.NewRequest(http.MethodPost, "/api/servers/99999/connections/remove", bytes.NewReader([]byte(`{"protocol":"awg","client_id":"c1"}`)))
		w404Rem := httptest.NewRecorder()
		r.ServeHTTP(w404Rem, req404Rem)
		if w404Rem.Code != http.StatusNotFound {
			t.Errorf("expected 404, got %d", w404Rem.Code)
		}
	})
}
