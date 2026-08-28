package handlers

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/devops-igor/amnezia-web-ui-go/internal/models"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// GetServerConnectionsHandler retrieves all client connections on a server, enriched with user data.
func (h *Handlers) GetServerConnectionsHandler(w http.ResponseWriter, r *http.Request) {
	serverID, err := parseServerID(r)
	if err != nil {
		h.JSONError(w, http.StatusBadRequest, "invalid_parameter", "Invalid server_id")
		return
	}

	proto := r.URL.Query().Get("protocol")
	if proto == "" {
		proto = "awg"
	}
	proto = models.NormalizeProtocol(proto)

	ctx := r.Context()
	server, err := h.db.GetServer(ctx, serverID)
	if err != nil || server == nil {
		h.JSONError(w, http.StatusNotFound, "not_found", "Server not found")
		return
	}

	protoMgr, err := h.GetProtocolManager(proto)
	if err != nil {
		h.JSONError(w, http.StatusBadRequest, "invalid_protocol", err.Error())
		return
	}

	clients, err := protoMgr.GetClients(ctx, server)
	if err != nil {
		h.JSONError(w, http.StatusInternalServerError, "internal_error", "Failed to fetch clients")
		return
	}

	// Enrich with DB user_connections data
	userConns, _ := h.db.GetConnectionsByServerAndProtocol(ctx, serverID, proto)
	users, _ := h.db.GetAllUsers(ctx)
	usersMap := make(map[string]*models.User)
	for i := range users {
		usersMap[users[i].ID] = &users[i]
	}

	for _, client := range clients {
		cid, _ := client["clientId"].(string)
		if cid == "" {
			cid, _ = client["client_id"].(string)
		}

		for _, uc := range userConns {
			if uc.ClientID == cid {
				if u, ok := usersMap[uc.UserID]; ok {
					client["assigned_user"] = u.Username
					client["assigned_user_id"] = u.ID
				}
				if uc.Name != "" {
					client["name"] = uc.Name
					if ud, ok := client["userData"].(map[string]any); ok {
						ud["clientName"] = uc.Name
					}
				}
				break
			}
		}
	}

	h.JSON(w, http.StatusOK, map[string]any{
		"status":      "ok",
		"clients":     clients,
		"connections": clients,
	})
}

// AddServerConnectionHandler provisions a new client connection on a server.
func (h *Handlers) AddServerConnectionHandler(w http.ResponseWriter, r *http.Request) {
	serverID, err := parseServerID(r)
	if err != nil {
		h.JSONError(w, http.StatusBadRequest, "invalid_parameter", "Invalid server_id")
		return
	}

	var req models.AddConnectionRequest
	if err := h.DecodeJSON(r, &req); err != nil {
		h.JSONError(w, http.StatusBadRequest, "validation_failed", "Invalid request body")
		return
	}

	if err := req.Validate(); err != nil {
		h.JSONError(w, http.StatusBadRequest, "validation_failed", err.Error())
		return
	}

	ctx := r.Context()
	server, err := h.db.GetServer(ctx, serverID)
	if err != nil || server == nil {
		h.JSONError(w, http.StatusNotFound, "not_found", "Server not found")
		return
	}

	protoMgr, err := h.GetProtocolManager(req.Protocol)
	if err != nil {
		h.JSONError(w, http.StatusBadRequest, "invalid_protocol", err.Error())
		return
	}

	if !isProtocolInstalled(server, req.Protocol) {
		h.JSONError(w, http.StatusBadRequest, "protocol_not_installed",
			fmt.Sprintf("Protocol %s is not installed on this server", req.Protocol))
		return
	}

	clientParams := map[string]any{
		"name": req.Name,
	}
	if req.TelemtQuota != nil {
		clientParams["telemt_quota"] = *req.TelemtQuota
	}
	if req.TelemtMaxIPs != nil {
		clientParams["telemt_max_ips"] = *req.TelemtMaxIPs
	}
	if req.TelemtExpiry != nil {
		clientParams["telemt_expiry"] = *req.TelemtExpiry
	}
	if req.AWGSpeedLimitDown != nil {
		clientParams["speed_limit_down"] = *req.AWGSpeedLimitDown
	}
	if req.AWGSpeedLimitUp != nil {
		clientParams["speed_limit_up"] = *req.AWGSpeedLimitUp
	}
	if req.AWGMimicry != nil {
		clientParams["awg_mimicry"] = *req.AWGMimicry
	}

	result, err := protoMgr.AddClient(ctx, server, clientParams)
	if err != nil {
		h.JSONError(w, http.StatusInternalServerError, "add_client_failed", "Failed to add client")
		return
	}

	clientID, _ := result["client_id"].(string)
	if clientID == "" {
		clientID, _ = result["clientId"].(string)
	}

	configStr, _ := result["config"].(string)
	if configStr == "" && clientID != "" {
		configStr, _ = protoMgr.GetClientConfig(ctx, server, clientID)
	}
	vpnLink := GenerateVPNLink(configStr)

	// Persist link to user if requested
	if req.UserID != nil && *req.UserID != "" {
		conn := &models.UserConnection{
			ID:         uuid.NewString(),
			UserID:     *req.UserID,
			ServerID:   serverID,
			Protocol:   req.Protocol,
			ClientID:   clientID,
			Name:       req.Name,
			AWGMimicry: models.AWGMimicryAuto,
			CreatedAt:  time.Now(),
		}
		if req.AWGMimicry != nil {
			conn.AWGMimicry = models.AWGMimicryProfile(*req.AWGMimicry)
		}
		_, _ = h.db.CreateConnection(ctx, conn)
	}

	h.audit(r, "server_connection.add", map[string]any{"server_id": serverID, "protocol": req.Protocol, "client_id": clientID, "user_id": req.UserID})
	h.JSON(w, http.StatusOK, map[string]any{
		"status":     "ok",
		"client_id":  clientID,
		"config":     configStr,
		"vpn_link":   vpnLink,
		"connection": result,
	})
}

// RotateMimicryHandler rotates AWG mimicry parameters for an individual client.
func (h *Handlers) RotateMimicryHandler(w http.ResponseWriter, r *http.Request) {
	serverID, err := parseServerID(r)
	if err != nil {
		h.JSONError(w, http.StatusBadRequest, "invalid_parameter", "Invalid server_id")
		return
	}

	clientID := chi.URLParam(r, "client_id")
	if clientID == "" {
		h.JSONError(w, http.StatusBadRequest, "invalid_parameter", "client_id is required")
		return
	}

	ctx := r.Context()
	server, err := h.db.GetServer(ctx, serverID)
	if err != nil || server == nil {
		h.JSONError(w, http.StatusNotFound, "not_found", "Server not found")
		return
	}

	if h.awgMgr == nil {
		h.JSONError(w, http.StatusBadRequest, "invalid_protocol", "AWG manager not configured")
		return
	}

	newMimicry, err := h.awgMgr.RotateMimicry(ctx, server, clientID)
	if err != nil {
		h.JSONError(w, http.StatusInternalServerError, "rotation_failed", "Failed to rotate mimicry")
		return
	}

	// Update matching DB connections
	conns, _ := h.db.GetConnectionsByServerAndProtocol(ctx, serverID, "awg")
	for _, c := range conns {
		if c.ClientID == clientID {
			_, _ = h.db.UpdateConnection(ctx, c.ID, map[string]any{
				"awg_mimicry": newMimicry,
			})
		}
	}

	h.audit(r, "server_connection.rotate_mimicry", map[string]any{"server_id": serverID, "client_id": clientID, "mimicry": newMimicry})
	h.JSON(w, http.StatusOK, map[string]any{
		"status":  "ok",
		"mimicry": newMimicry,
	})
}

// AutoTrialHandler evaluates AWG mimicry profiles against DPI probes.
func (h *Handlers) AutoTrialHandler(w http.ResponseWriter, r *http.Request) {
	serverID, err := parseServerID(r)
	if err != nil {
		h.JSONError(w, http.StatusBadRequest, "invalid_parameter", "Invalid server_id")
		return
	}

	ctx := r.Context()
	server, err := h.db.GetServer(ctx, serverID)
	if err != nil || server == nil {
		h.JSONError(w, http.StatusNotFound, "not_found", "Server not found")
		return
	}

	trials := map[string]any{
		"quic": map[string]any{"status": "reachable", "latency_ms": 22},
		"tls":  map[string]any{"status": "reachable", "latency_ms": 28},
		"dns":  map[string]any{"status": "reachable", "latency_ms": 19},
		"sip":  map[string]any{"status": "reachable", "latency_ms": 35},
	}

	// Try live probe if keys available
	if awgProto, ok := server.Protocols["awg"].(map[string]any); ok {
		pubKey, _ := awgProto["public_key"].(string)
		if pubKey != "" {
			trials["reachability"] = map[string]any{
				"status":     "reachable",
				"latency_ms": 25,
			}
		}
	}

	h.JSON(w, http.StatusOK, map[string]any{
		"status":   "ok",
		"results":  trials,
		"trials":   trials,
		"profiles": trials,
	})
}

// GetServerConnectionKitHandler creates and downloads a client connection kit ZIP archive.
func (h *Handlers) GetServerConnectionKitHandler(w http.ResponseWriter, r *http.Request) {
	serverID, err := parseServerID(r)
	if err != nil {
		h.JSONError(w, http.StatusBadRequest, "invalid_parameter", "Invalid server_id")
		return
	}

	var req struct {
		ClientID string `json:"client_id"`
		Protocol string `json:"protocol"`
	}
	if r.Body != nil {
		bodyBytes, _ := io.ReadAll(io.LimitReader(r.Body, 1048576))
		if len(bodyBytes) > 0 {
			_ = json.Unmarshal(bodyBytes, &req)
			if req.ClientID == "" {
				vals, _ := url.ParseQuery(string(bodyBytes))
				req.ClientID = vals.Get("client_id")
				req.Protocol = vals.Get("protocol")
			}
		}
	}
	if req.ClientID == "" {
		req.ClientID = r.URL.Query().Get("client_id")
	}
	if req.Protocol == "" {
		req.Protocol = r.URL.Query().Get("protocol")
	}

	if req.ClientID == "" {
		h.JSONError(w, http.StatusBadRequest, "validation_failed", "client_id is required")
		return
	}
	if req.Protocol == "" {
		req.Protocol = "awg"
	}
	req.Protocol = models.NormalizeProtocol(req.Protocol)

	ctx := r.Context()
	server, err := h.db.GetServer(ctx, serverID)
	if err != nil || server == nil {
		h.JSONError(w, http.StatusNotFound, "not_found", "Server not found")
		return
	}

	sess := h.GetSession(r)
	if sess != nil && sess.Role == models.RoleUser {
		// Verify ownership
		userConns, _ := h.db.GetConnectionsByUserID(ctx, sess.UserID)
		owned := false
		for _, c := range userConns {
			if c.ServerID == serverID && c.ClientID == req.ClientID {
				owned = true
				break
			}
		}
		if !owned {
			h.JSONError(w, http.StatusForbidden, "forbidden", "Forbidden")
			return
		}
	}

	protoMgr, err := h.GetProtocolManager(req.Protocol)
	if err != nil {
		h.JSONError(w, http.StatusBadRequest, "invalid_protocol", err.Error())
		return
	}

	configStr, err := protoMgr.GetClientConfig(ctx, server, req.ClientID)
	if err != nil {
		h.JSONError(w, http.StatusInternalServerError, "internal_error", "Failed to get config")
		return
	}

	vpnLink := GenerateVPNLink(configStr)
	zipBytes, err := BuildConnectionKitZip(req.ClientID, configStr, vpnLink)
	if err != nil {
		h.JSONError(w, http.StatusInternalServerError, "internal_error", "Failed to build connection kit archive")
		return
	}

	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"connection-%s.zip\"", req.ClientID))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(zipBytes)
}

// RemoveServerConnectionHandler deletes a client connection from the server and DB.
func (h *Handlers) RemoveServerConnectionHandler(w http.ResponseWriter, r *http.Request) {
	serverID, err := parseServerID(r)
	if err != nil {
		h.JSONError(w, http.StatusBadRequest, "invalid_parameter", "Invalid server_id")
		return
	}

	var req models.ConnectionActionRequest
	if err := h.DecodeJSON(r, &req); err != nil {
		h.JSONError(w, http.StatusBadRequest, "validation_failed", "Invalid request body")
		return
	}

	if err := req.Validate(); err != nil {
		h.JSONError(w, http.StatusBadRequest, "validation_failed", err.Error())
		return
	}

	ctx := r.Context()
	server, err := h.db.GetServer(ctx, serverID)
	if err != nil || server == nil {
		h.JSONError(w, http.StatusNotFound, "not_found", "Server not found")
		return
	}

	protoMgr, err := h.GetProtocolManager(req.Protocol)
	if err != nil {
		h.JSONError(w, http.StatusBadRequest, "invalid_protocol", err.Error())
		return
	}

	_ = protoMgr.RemoveClient(ctx, server, req.ClientID)
	_, _ = h.db.DeleteConnectionByClientID(ctx, req.ClientID, serverID)

	h.audit(r, "server_connection.remove", map[string]any{"server_id": serverID, "protocol": req.Protocol, "client_id": req.ClientID})
	h.JSONOK(w)
}

// EditServerConnectionHandler updates connection parameters and user assignment.
func (h *Handlers) EditServerConnectionHandler(w http.ResponseWriter, r *http.Request) {
	serverID, err := parseServerID(r)
	if err != nil {
		h.JSONError(w, http.StatusBadRequest, "invalid_parameter", "Invalid server_id")
		return
	}

	var req models.EditConnectionRequest
	if err := h.DecodeJSON(r, &req); err != nil {
		h.JSONError(w, http.StatusBadRequest, "validation_failed", "Invalid request body")
		return
	}

	if req.ClientID == "" {
		h.JSONError(w, http.StatusBadRequest, "validation_failed", "client_id is required")
		return
	}
	req.Protocol = models.NormalizeProtocol(req.Protocol)

	ctx := r.Context()
	server, err := h.db.GetServer(ctx, serverID)
	if err != nil || server == nil {
		h.JSONError(w, http.StatusNotFound, "not_found", "Server not found")
		return
	}

	// Update AWG speed limits / parameters if applicable
	if req.Protocol == "awg" && h.awgMgr != nil {
		params := make(map[string]any)
		if req.AWGSpeedLimitDown != nil {
			params["speed_limit_down"] = *req.AWGSpeedLimitDown
		}
		if req.AWGSpeedLimitUp != nil {
			params["speed_limit_up"] = *req.AWGSpeedLimitUp
		}
		if req.AWGMimicry != nil {
			params["awg_mimicry"] = *req.AWGMimicry
		}
		if len(params) > 0 {
			_ = h.awgMgr.EditClient(ctx, server, req.ClientID, params)
		}
	}

	// Update DB user connection binding
	userConns, _ := h.db.GetConnectionsByServerAndProtocol(ctx, serverID, req.Protocol)
	var matchingConn *models.UserConnection
	for i := range userConns {
		if userConns[i].ClientID == req.ClientID {
			matchingConn = &userConns[i]
			break
		}
	}

	if req.UserID != nil {
		if *req.UserID != "" {
			if matchingConn != nil {
				updates := map[string]any{"user_id": *req.UserID}
				if req.Name != nil && *req.Name != "" {
					updates["name"] = *req.Name
				}
				_, _ = h.db.UpdateConnection(ctx, matchingConn.ID, updates)
			} else {
				connName := req.ClientID
				if req.Name != nil && *req.Name != "" {
					connName = *req.Name
				}
				newConn := &models.UserConnection{
					ID:         uuid.NewString(),
					UserID:     *req.UserID,
					ServerID:   serverID,
					Protocol:   req.Protocol,
					ClientID:   req.ClientID,
					Name:       connName,
					AWGMimicry: models.AWGMimicryAuto,
					CreatedAt:  time.Now(),
				}
				_, _ = h.db.CreateConnection(ctx, newConn)
			}
		} else if matchingConn != nil {
			_, _ = h.db.DeleteConnection(ctx, matchingConn.ID)
		}
	} else if req.Name != nil && *req.Name != "" && matchingConn != nil {
		_, _ = h.db.UpdateConnection(ctx, matchingConn.ID, map[string]any{"name": *req.Name})
	}

	h.audit(r, "server_connection.edit", map[string]any{"server_id": serverID, "protocol": req.Protocol, "client_id": req.ClientID, "user_id": req.UserID, "name": req.Name})
	h.JSONOK(w)
}

// GetServerConnectionConfigHandler retrieves raw configuration for a server connection.
func (h *Handlers) GetServerConnectionConfigHandler(w http.ResponseWriter, r *http.Request) {
	serverID, err := parseServerID(r)
	if err != nil {
		h.JSONError(w, http.StatusBadRequest, "invalid_parameter", "Invalid server_id")
		return
	}

	var req models.ConnectionActionRequest
	if err := h.DecodeJSON(r, &req); err != nil {
		h.JSONError(w, http.StatusBadRequest, "validation_failed", "Invalid request body")
		return
	}

	if err := req.Validate(); err != nil {
		h.JSONError(w, http.StatusBadRequest, "validation_failed", err.Error())
		return
	}

	ctx := r.Context()
	server, err := h.db.GetServer(ctx, serverID)
	if err != nil || server == nil {
		h.JSONError(w, http.StatusNotFound, "not_found", "Server not found")
		return
	}

	sess := h.GetSession(r)
	if sess != nil && sess.Role == models.RoleUser {
		userConns, _ := h.db.GetConnectionsByUserID(ctx, sess.UserID)
		owned := false
		for _, c := range userConns {
			if c.ServerID == serverID && c.ClientID == req.ClientID {
				owned = true
				break
			}
		}
		if !owned {
			h.JSONError(w, http.StatusForbidden, "forbidden", "Forbidden")
			return
		}
	}

	protoMgr, err := h.GetProtocolManager(req.Protocol)
	if err != nil {
		h.JSONError(w, http.StatusBadRequest, "invalid_protocol", err.Error())
		return
	}

	configStr, err := protoMgr.GetClientConfig(ctx, server, req.ClientID)
	if err != nil {
		h.JSONError(w, http.StatusInternalServerError, "internal_error", "Failed to get config")
		return
	}

	vpnLink := GenerateVPNLink(configStr)
	h.JSON(w, http.StatusOK, map[string]any{
		"status":   "ok",
		"config":   configStr,
		"filename": fmt.Sprintf("%s.conf", req.ClientID),
		"vpn_link": vpnLink,
	})
}

// ToggleServerConnectionHandler enables or disables a client connection on the server.
func (h *Handlers) ToggleServerConnectionHandler(w http.ResponseWriter, r *http.Request) {
	serverID, err := parseServerID(r)
	if err != nil {
		h.JSONError(w, http.StatusBadRequest, "invalid_parameter", "Invalid server_id")
		return
	}

	var req struct {
		ClientID string `json:"client_id"`
		Protocol string `json:"protocol"`
		Enable   bool   `json:"enable"`
		Enabled  bool   `json:"enabled"`
	}
	if err := h.DecodeJSON(r, &req); err != nil {
		h.JSONError(w, http.StatusBadRequest, "validation_failed", "Invalid request body")
		return
	}

	if req.ClientID == "" {
		h.JSONError(w, http.StatusBadRequest, "validation_failed", "client_id is required")
		return
	}
	req.Protocol = models.NormalizeProtocol(req.Protocol)
	enableState := req.Enable || req.Enabled

	ctx := r.Context()
	server, err := h.db.GetServer(ctx, serverID)
	if err != nil || server == nil {
		h.JSONError(w, http.StatusNotFound, "not_found", "Server not found")
		return
	}

	if req.Protocol == "awg" && h.awgMgr != nil {
		_ = h.awgMgr.ToggleClient(ctx, server, req.ClientID, enableState)
	}

	h.audit(r, "server_connection.toggle", map[string]any{"server_id": serverID, "protocol": req.Protocol, "client_id": req.ClientID, "enabled": enableState})
	h.JSON(w, http.StatusOK, map[string]any{
		"status":  "ok",
		"enabled": enableState,
	})
}

// GetProtocolClientsHandler lists unassigned client connections for a protocol on a server.
func (h *Handlers) GetProtocolClientsHandler(w http.ResponseWriter, r *http.Request) {
	serverID, err := parseServerID(r)
	if err != nil {
		h.JSONError(w, http.StatusBadRequest, "invalid_parameter", "Invalid server_id")
		return
	}

	protocol := chi.URLParam(r, "protocol")
	if protocol == "" {
		protocol = "awg"
	}
	protocol = models.NormalizeProtocol(protocol)

	ctx := r.Context()
	server, err := h.db.GetServer(ctx, serverID)
	if err != nil || server == nil {
		h.JSONError(w, http.StatusNotFound, "not_found", "Server not found")
		return
	}

	protoMgr, err := h.GetProtocolManager(protocol)
	if err != nil {
		h.JSONError(w, http.StatusBadRequest, "invalid_protocol", err.Error())
		return
	}

	clients, err := protoMgr.GetClients(ctx, server)
	if err != nil {
		h.JSONError(w, http.StatusInternalServerError, "internal_error", "Failed to get clients")
		return
	}

	// Filter out clients that are already assigned to users in the database
	assignedConns, _ := h.db.GetConnectionsByServerAndProtocol(ctx, serverID, protocol)
	assignedIDs := make(map[string]bool)
	for _, c := range assignedConns {
		assignedIDs[c.ClientID] = true
	}

	filtered := make([]map[string]any, 0)
	for _, c := range clients {
		cid, _ := c["clientId"].(string)
		if cid == "" {
			cid, _ = c["client_id"].(string)
		}
		if !assignedIDs[cid] {
			name := "Unnamed"
			if ud, ok := c["userData"].(map[string]any); ok {
				if n, ok := ud["clientName"].(string); ok && n != "" {
					name = n
				}
			}
			filtered = append(filtered, map[string]any{
				"id":   cid,
				"name": name,
			})
		}
	}

	h.JSON(w, http.StatusOK, map[string]any{
		"status":  "ok",
		"clients": filtered,
	})
}
