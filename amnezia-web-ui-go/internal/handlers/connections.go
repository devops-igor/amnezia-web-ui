package handlers

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/devops-igor/amnezia-web-ui-go/internal/models"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// effectiveMaxConnectionsPerUser returns the per-user override of the connection limit
// when set, otherwise the global limit.
func effectiveMaxConnectionsPerUser(user *models.User, global int) int {
	if user != nil && user.Limits != nil {
		if v, ok := user.Limits["max_connections_per_user"].(float64); ok && int(v) > 0 {
			return int(v)
		}
	}
	return global
}

// effectiveRateLimit returns the per-user override of the connection rate limit
// count/window when set, otherwise the global values.
func effectiveRateLimit(user *models.User, count, window int) (int, int) {
	if user != nil && user.Limits != nil {
		if v, ok := user.Limits["connection_rate_limit_count"].(float64); ok && int(v) > 0 {
			count = int(v)
		}
		if v, ok := user.Limits["connection_rate_limit_window"].(float64); ok && int(v) > 0 {
			window = int(v)
		}
	}
	return count, window
}

// serverReachabilityInfo resolves cached reachability for a server: status string and boolean flag.
func (h *Handlers) serverReachabilityInfo(ctx context.Context, serverID int64) (string, bool) {
	status, err := h.db.GetServerStatus(ctx, serverID)
	if err != nil {
		return "unknown", false
	}
	switch status {
	case models.ReachabilityOnline:
		return string(status), true
	case models.ReachabilityOffline:
		return string(status), false
	default:
		return string(models.ReachabilityUnknown), false
	}
}

// UserGetMyConnectionsHandler returns the authenticated user's active connections.
func (h *Handlers) UserGetMyConnectionsHandler(w http.ResponseWriter, r *http.Request) {
	sess := h.GetSession(r)
	if sess == nil || !sess.IsAuthenticated() {
		h.JSONError(w, http.StatusUnauthorized, "unauthorized", "Authentication required")
		return
	}

	ctx := r.Context()
	conns, err := h.db.GetConnectionsByUserID(ctx, sess.UserID)
	if err != nil {
		h.JSONError(w, http.StatusInternalServerError, "internal_error", "Failed to retrieve connections")
		return
	}

	servers, _ := h.db.GetAllServers(ctx)
	serversMap := make(map[int64]string)
	for _, s := range servers {
		serversMap[s.ID] = s.Name
	}

	enriched := make([]map[string]any, len(conns))
	for i, c := range conns {
		srvName := serversMap[c.ServerID]
		if srvName == "" {
			srvName = fmt.Sprintf("Server #%d", c.ServerID)
		}

		srvStatus, srvReachable := h.serverReachabilityInfo(ctx, c.ServerID)

		enriched[i] = map[string]any{
			"id":               c.ID,
			"user_id":          c.UserID,
			"server_id":        c.ServerID,
			"server_name":      srvName,
			"protocol":         c.Protocol,
			"client_id":        c.ClientID,
			"name":             c.Name,
			"awg_mimicry":      c.AWGMimicry,
			"traffic_total_rx": c.TrafficTotalRx,
			"traffic_total_tx": c.TrafficTotalTx,
			"traffic_total":    c.TrafficTotal,
			"created_at":       c.CreatedAt.Format(time.RFC3339),
			"server_reachable": srvReachable,
			"server_status":    srvStatus,
		}
	}

	var limits models.ConnectionLimits
	_ = h.db.GetSetting(ctx, "limits", &limits)
	if limits.MaxConnectionsPerUser <= 0 {
		limits.MaxConnectionsPerUser = 10
	}

	user, _ := h.db.GetUser(ctx, sess.UserID)
	effectiveMax := effectiveMaxConnectionsPerUser(user, limits.MaxConnectionsPerUser)

	h.JSON(w, http.StatusOK, map[string]any{
		"status":      "ok",
		"connections": enriched,
		"limits": map[string]any{
			"max_connections":     effectiveMax,
			"current_connections": len(conns),
		},
	})
}

// UserAddConnectionHandler provisions a new client connection for the authenticated user.
func (h *Handlers) UserAddConnectionHandler(w http.ResponseWriter, r *http.Request) {
	sess := h.GetSession(r)
	if sess == nil || !sess.IsAuthenticated() {
		h.JSONError(w, http.StatusUnauthorized, "unauthorized", "Authentication required")
		return
	}

	unlock := h.lockUser(sess.UserID)
	defer unlock()

	var req models.MyAddConnectionRequest
	if err := h.DecodeJSON(r, &req); err != nil {
		h.JSONError(w, http.StatusBadRequest, "validation_failed", "Invalid request body")
		return
	}

	if err := req.Validate(); err != nil {
		h.JSONError(w, http.StatusBadRequest, "validation_failed", err.Error())
		return
	}

	ctx := r.Context()
	user, err := h.db.GetUser(ctx, sess.UserID)
	if err != nil || user == nil {
		h.JSONError(w, http.StatusNotFound, "not_found", "User not found")
		return
	}

	// 1. Account status checks
	if !h.checkUserEligible(w, user) {
		return
	}

	// 2. Limits and Rate Limiting (per-user overrides win over global settings)
	userConns, _ := h.db.GetConnectionsByUserID(ctx, user.ID)
	if !h.checkConnectionLimits(w, ctx, user, userConns) {
		return
	}

	// 3. Server and duplicate name check
	server, err := h.db.GetServer(ctx, req.ServerID)
	if err != nil || server == nil {
		h.JSONError(w, http.StatusNotFound, "not_found", "Server not found")
		return
	}

	if h.hasDuplicateConnectionName(userConns, req.Name) {
		h.JSON(w, http.StatusConflict, map[string]any{
			"error":   "duplicate_name",
			"message": "A connection with this name already exists.",
		})
		return
	}

	protoMgr, err := h.GetProtocolManager(req.Protocol)
	if err != nil {
		h.JSONError(w, http.StatusBadRequest, "invalid_protocol", err.Error())
		return
	}

	// Guard: the requested protocol must be installed on this server.
	if !isProtocolInstalled(server, req.Protocol) {
		h.JSONError(w, http.StatusBadRequest, "protocol_not_installed",
			fmt.Sprintf("Protocol %s is not installed on this server", req.Protocol))
		return
	}

	clientParams := map[string]any{
		"name": req.Name,
	}
	h.appendConnectionParams(clientParams, req)

	result, err := protoMgr.AddClient(ctx, server, clientParams)
	if err != nil {
		h.JSONError(w, http.StatusInternalServerError, "add_client_failed", "Failed to create client on server")
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

	newConn := &models.UserConnection{
		ID:         uuid.NewString(),
		UserID:     user.ID,
		ServerID:   req.ServerID,
		Protocol:   req.Protocol,
		ClientID:   clientID,
		Name:       req.Name,
		AWGMimicry: models.AWGMimicryAuto,
		CreatedAt:  time.Now(),
	}
	if req.AWGMimicry != nil {
		newConn.AWGMimicry = models.AWGMimicryProfile(*req.AWGMimicry)
	}

	if _, err := h.db.CreateConnection(ctx, newConn); err != nil {
		h.JSONError(w, http.StatusInternalServerError, "internal_error", "Failed to save connection record")
		return
	}

	_ = h.db.LogConnectionCreation(ctx, user.ID)

	h.audit(r, "connection.user_add", map[string]any{"user_id": user.ID, "server_id": req.ServerID, "protocol": req.Protocol, "client_id": clientID, "name": req.Name})
	h.JSON(w, http.StatusOK, map[string]any{
		"status":     "ok",
		"client_id":  clientID,
		"config":     configStr,
		"vpn_link":   vpnLink,
		"connection": newConn,
	})
}

// UserGetConnectionConfigHandler returns configuration text for an owned connection.
func (h *Handlers) UserGetConnectionConfigHandler(w http.ResponseWriter, r *http.Request) {
	sess := h.GetSession(r)
	if sess == nil || !sess.IsAuthenticated() {
		h.JSONError(w, http.StatusUnauthorized, "unauthorized", "Authentication required")
		return
	}

	connectionID := chi.URLParam(r, "connection_id")
	if connectionID == "" {
		h.JSONError(w, http.StatusBadRequest, "invalid_parameter", "connection_id is required")
		return
	}

	ctx := r.Context()
	conn, err := h.db.GetConnection(ctx, connectionID)
	if err != nil || conn == nil || conn.UserID != sess.UserID {
		h.JSONError(w, http.StatusNotFound, "not_found", "Connection not found")
		return
	}

	server, err := h.db.GetServer(ctx, conn.ServerID)
	if err != nil || server == nil {
		h.JSONError(w, http.StatusNotFound, "not_found", "Server not found")
		return
	}

	protoMgr, err := h.GetProtocolManager(conn.Protocol)
	if err != nil {
		h.JSONError(w, http.StatusBadRequest, "invalid_protocol", err.Error())
		return
	}

	configStr, err := protoMgr.GetClientConfig(ctx, server, conn.ClientID)
	if err != nil {
		h.JSONError(w, http.StatusInternalServerError, "internal_error", "Failed to get config")
		return
	}

	vpnLink := GenerateVPNLink(configStr)
	filename := fmt.Sprintf("%s.conf", conn.Name)

	h.JSON(w, http.StatusOK, map[string]any{
		"status":      "ok",
		"config":      configStr,
		"filename":    filename,
		"vpn_link":    vpnLink,
		"awg_mimicry": conn.AWGMimicry,
	})
}

// UserGetConnectionKitHandler generates and serves a connection kit ZIP archive.
func (h *Handlers) UserGetConnectionKitHandler(w http.ResponseWriter, r *http.Request) {
	sess := h.GetSession(r)
	if sess == nil || !sess.IsAuthenticated() {
		h.JSONError(w, http.StatusUnauthorized, "unauthorized", "Authentication required")
		return
	}

	connectionID := chi.URLParam(r, "connection_id")
	if connectionID == "" {
		h.JSONError(w, http.StatusBadRequest, "invalid_parameter", "connection_id is required")
		return
	}

	ctx := r.Context()
	conn, err := h.db.GetConnection(ctx, connectionID)
	if err != nil || conn == nil || conn.UserID != sess.UserID {
		h.JSONError(w, http.StatusNotFound, "not_found", "Connection not found")
		return
	}

	server, err := h.db.GetServer(ctx, conn.ServerID)
	if err != nil || server == nil {
		h.JSONError(w, http.StatusNotFound, "not_found", "Server not found")
		return
	}

	protoMgr, err := h.GetProtocolManager(conn.Protocol)
	if err != nil {
		h.JSONError(w, http.StatusBadRequest, "invalid_protocol", err.Error())
		return
	}

	configStr, err := protoMgr.GetClientConfig(ctx, server, conn.ClientID)
	if err != nil {
		h.JSONError(w, http.StatusInternalServerError, "internal_error", "Failed to get config")
		return
	}

	vpnLink := GenerateVPNLink(configStr)
	zipBytes, err := BuildConnectionKitZip(conn.Name, configStr, vpnLink)
	if err != nil {
		h.JSONError(w, http.StatusInternalServerError, "internal_error", "Failed to build connection kit archive")
		return
	}

	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s-kit.zip\"", conn.Name))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(zipBytes)
}

// UserRenameConnectionHandler renames an owned client connection.
func (h *Handlers) UserRenameConnectionHandler(w http.ResponseWriter, r *http.Request) {
	sess := h.GetSession(r)
	if sess == nil || !sess.IsAuthenticated() {
		h.JSONError(w, http.StatusUnauthorized, "unauthorized", "Authentication required")
		return
	}

	connectionID := chi.URLParam(r, "connection_id")
	if connectionID == "" {
		h.JSONError(w, http.StatusBadRequest, "invalid_parameter", "connection_id is required")
		return
	}

	var req models.RenameConnectionRequest
	if err := h.DecodeJSON(r, &req); err != nil {
		h.JSONError(w, http.StatusBadRequest, "validation_failed", "Invalid request body")
		return
	}

	if err := req.Validate(); err != nil {
		h.JSONError(w, http.StatusBadRequest, "validation_failed", err.Error())
		return
	}

	ctx := r.Context()
	conn, err := h.db.GetConnection(ctx, connectionID)
	if err != nil || conn == nil || conn.UserID != sess.UserID {
		h.JSONError(w, http.StatusNotFound, "not_found", "Connection not found")
		return
	}

	// Check duplicates
	userConns, err := h.db.GetConnectionsByUserID(ctx, sess.UserID)
	if err == nil {
		for _, c := range userConns {
			if c.ID != connectionID && strings.EqualFold(c.Name, req.Name) {
				h.JSON(w, http.StatusConflict, map[string]any{
					"error":   "duplicate_name",
					"message": "A connection with this name already exists.",
				})
				return
			}
		}
	}

	_, err = h.db.UpdateConnection(ctx, connectionID, map[string]any{
		"name": req.Name,
	})
	if err != nil {
		h.JSONError(w, http.StatusInternalServerError, "internal_error", "Failed to rename connection")
		return
	}

	h.audit(r, "connection.user_rename", map[string]any{"user_id": sess.UserID, "connection_id": connectionID, "old_name": conn.Name, "new_name": req.Name})
	h.JSON(w, http.StatusOK, map[string]any{
		"status": "ok",
		"name":   req.Name,
	})
}

// UserDeleteConnectionHandler removes an owned client connection.
func (h *Handlers) UserDeleteConnectionHandler(w http.ResponseWriter, r *http.Request) {
	sess := h.GetSession(r)
	if sess == nil || !sess.IsAuthenticated() {
		h.JSONError(w, http.StatusUnauthorized, "unauthorized", "Authentication required")
		return
	}

	connectionID := chi.URLParam(r, "connection_id")
	if connectionID == "" {
		h.JSONError(w, http.StatusBadRequest, "invalid_parameter", "connection_id is required")
		return
	}

	ctx := r.Context()
	conn, err := h.db.GetConnection(ctx, connectionID)
	if err != nil || conn == nil || conn.UserID != sess.UserID {
		h.JSONError(w, http.StatusNotFound, "not_found", "Connection not found")
		return
	}

	server, err := h.db.GetServer(ctx, conn.ServerID)
	if err == nil && server != nil {
		if protoMgr, err := h.GetProtocolManager(conn.Protocol); err == nil && protoMgr != nil {
			_ = protoMgr.RemoveClient(ctx, server, conn.ClientID)
		}
	}

	_, _ = h.db.DeleteConnection(ctx, connectionID)

	h.audit(r, "connection.user_delete", map[string]any{"user_id": sess.UserID, "connection_id": connectionID, "name": conn.Name})
	h.JSONOK(w)
}

// hasDuplicateConnectionName reports whether any existing connection already uses the given name (case-insensitive).
func (h *Handlers) hasDuplicateConnectionName(conns []models.UserConnection, name string) bool {
	for _, c := range conns {
		if strings.EqualFold(c.Name, name) {
			return true
		}
	}
	return false
}

// accountExpired reports whether the user account has passed either expiry timestamp.
func (h *Handlers) accountExpired(user *models.User) bool {
	now := time.Now()
	if user.ExpiresAt != nil && now.After(*user.ExpiresAt) {
		return true
	}
	return user.ExpirationDate != nil && now.After(*user.ExpirationDate)
}

// resolveConnectionLimits loads connection limits from settings, applying defaults for unset values.
func (h *Handlers) resolveConnectionLimits(ctx context.Context) models.ConnectionLimits {
	var limits models.ConnectionLimits
	_ = h.db.GetSetting(ctx, "limits", &limits)
	if limits.MaxConnectionsPerUser <= 0 {
		limits.MaxConnectionsPerUser = 10
	}
	if limits.ConnectionRateLimitCount <= 0 {
		limits.ConnectionRateLimitCount = 5
	}
	if limits.ConnectionRateLimitWindow <= 0 {
		limits.ConnectionRateLimitWindow = 60
	}
	return limits
}

// appendConnectionParams copies optional per-protocol client parameters from the request into
// the protocol-manager params map.
func (h *Handlers) appendConnectionParams(params map[string]any, req models.MyAddConnectionRequest) {
	if req.TelemtQuota != nil {
		params["telemt_quota"] = *req.TelemtQuota
	}
	if req.TelemtMaxIPs != nil {
		params["telemt_max_ips"] = *req.TelemtMaxIPs
	}
	if req.TelemtExpiry != nil {
		params["telemt_expiry"] = *req.TelemtExpiry
	}
	if req.AWGSpeedLimitDown != nil {
		params["speed_limit_down"] = *req.AWGSpeedLimitDown
	}
	if req.AWGSpeedLimitUp != nil {
		params["speed_limit_up"] = *req.AWGSpeedLimitUp
	}
	if req.AWGMimicry != nil {
		params["awg_mimicry"] = *req.AWGMimicry
	}
}

func (h *Handlers) checkUserEligible(w http.ResponseWriter, user *models.User) bool {
	if !user.Enabled {
		h.JSONError(w, http.StatusForbidden, "account_disabled", "Account is disabled")
		return false
	}
	if h.accountExpired(user) {
		h.JSONError(w, http.StatusForbidden, "account_expired", "Account expired")
		return false
	}
	if user.TrafficLimit > 0 && user.TrafficUsed >= user.TrafficLimit {
		h.JSONError(w, http.StatusForbidden, "traffic_limit_exceeded", "Traffic limit exceeded")
		return false
	}
	return true
}

func (h *Handlers) checkConnectionLimits(w http.ResponseWriter, ctx context.Context, user *models.User, userConns []models.UserConnection) bool {
	limits := h.resolveConnectionLimits(ctx)
	effectiveMax := effectiveMaxConnectionsPerUser(user, limits.MaxConnectionsPerUser)
	rateCount, rateWindow := effectiveRateLimit(user, limits.ConnectionRateLimitCount, limits.ConnectionRateLimitWindow)

	if len(userConns) >= effectiveMax {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		h.JSON(w, http.StatusPreconditionRequired, map[string]any{
			"error":   "max_connections_reached",
			"detail":  fmt.Sprintf("Maximum connections limit reached (%d)", effectiveMax),
			"limit":   effectiveMax,
			"current": len(userConns),
		})
		return false
	}

	recentLogs, err := h.db.GetRecentConnectionsLog(ctx, user.ID, rateWindow)
	if err == nil && len(recentLogs) >= rateCount {
		retryAfter := rateWindow
		w.Header().Set("Retry-After", fmt.Sprintf("%d", retryAfter))
		h.JSON(w, http.StatusPreconditionRequired, map[string]any{
			"error":       "connection_rate_limit_exceeded",
			"detail":      fmt.Sprintf("Connection rate limit exceeded (%d per %ds)", rateCount, rateWindow),
			"retry_after": retryAfter,
		})
		return false
	}
	return true
}
