package handlers

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/devops-igor/amnezia-web-ui-go/internal/database"
	"github.com/devops-igor/amnezia-web-ui-go/internal/models"
	"github.com/devops-igor/amnezia-web-ui-go/internal/security"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// ListUsersHandler returns a paginated and filtered list of users.
func (h *Handlers) ListUsersHandler(w http.ResponseWriter, r *http.Request) {
	search := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("search")))
	page := 1
	if p, err := strconv.Atoi(r.URL.Query().Get("page")); err == nil && p > 0 {
		page = p
	}
	size := 10
	if s, err := strconv.Atoi(r.URL.Query().Get("size")); err == nil && s > 0 && s <= 100 {
		size = s
	}

	ctx := r.Context()
	allUsers, err := h.db.GetAllUsers(ctx)
	if err != nil {
		h.JSONError(w, http.StatusInternalServerError, "internal_error", "Failed to retrieve users")
		return
	}

	allConns, _ := h.db.GetAllConnections(ctx)
	connsCountMap := make(map[string]int)
	for _, c := range allConns {
		connsCountMap[c.UserID]++
	}

	filtered := make([]models.User, 0)
	for _, u := range allUsers {
		if search != "" {
			match := strings.Contains(strings.ToLower(u.Username), search)
			if !match && u.Email != nil {
				match = strings.Contains(strings.ToLower(*u.Email), search)
			}
			if !match && u.TelegramID != nil {
				match = strings.Contains(strings.ToLower(*u.TelegramID), search)
			}
			if !match {
				continue
			}
		}
		filtered = append(filtered, u)
	}

	total := len(filtered)
	start := (page - 1) * size
	if start > total {
		start = total
	}
	end := start + size
	if end > total {
		end = total
	}

	items := filtered[start:end]
	userItems := make([]models.UserItemResponse, len(items))
	for i, u := range items {
		source := "Local"
		if u.RemnaWaveUUID != nil && *u.RemnaWaveUUID != "" {
			source = "Remnawave"
		}

		var expStr, expiresStr *string
		if u.ExpirationDate != nil {
			s := u.ExpirationDate.Format(time.RFC3339)
			expStr = &s
		}
		if u.ExpiresAt != nil {
			s := u.ExpiresAt.Format(time.RFC3339)
			expiresStr = &s
		}

		mimicryStr := string(u.AWGMimicry)
		userItems[i] = models.UserItemResponse{
			ID:                   u.ID,
			Username:             u.Username,
			Role:                 string(u.Role),
			Enabled:              u.Enabled,
			CreatedAt:            u.CreatedAt.Format(time.RFC3339),
			TelegramID:           u.TelegramID,
			Email:                u.Email,
			Description:          u.Description,
			ConnectionsCount:     connsCountMap[u.ID],
			TrafficUsed:          u.TrafficUsed,
			TrafficTotal:         u.TrafficTotal,
			TrafficLimit:         u.TrafficLimit,
			TrafficResetStrategy: string(u.TrafficResetStrategy),
			LastResetAt:          u.LastResetAt,
			ExpirationDate:       expStr,
			ExpiresAt:            expiresStr,
			AWGMimicry:           &mimicryStr,
			ShareEnabled:         u.ShareEnabled,
			ShareToken:           u.ShareToken,
			HasSharePassword:     u.SharePasswordHash != nil && *u.SharePasswordHash != "",
			Source:               source,
		}
	}

	pages := 0
	if size > 0 {
		pages = (total + size - 1) / size
	}

	h.JSON(w, http.StatusOK, models.PaginatedUsersResponse{
		Users: userItems,
		Total: total,
		Page:  page,
		Size:  size,
		Pages: pages,
	})
}

// AddUserHandler creates a new user account and optionally provisions an initial connection.
func (h *Handlers) AddUserHandler(w http.ResponseWriter, r *http.Request) {
	var req models.AddUserRequest
	if err := h.DecodeJSON(r, &req); err != nil {
		h.JSONError(w, http.StatusBadRequest, "validation_failed", "Invalid request body")
		return
	}

	if err := req.Validate(); err != nil {
		h.JSONError(w, http.StatusBadRequest, "validation_failed", err.Error())
		return
	}

	ctx := r.Context()
	existing, err := h.db.GetUserByUsername(ctx, req.Username)
	if err == nil && existing != nil {
		h.JSONError(w, http.StatusBadRequest, "user_exists", h.Translate(r, "user_exists"))
		return
	}

	hash, err := security.HashPassword(req.Password)
	if err != nil {
		h.JSONError(w, http.StatusInternalServerError, "internal_error", "Failed to hash password")
		return
	}

	role := req.Role
	if role == "" {
		role = models.RoleUser
	}

	now := time.Now()
	nowStr := now.Format(time.RFC3339)
	shareToken := generateRandomToken(16)

	user := &models.User{
		ID:                     uuid.NewString(),
		Username:               req.Username,
		PasswordHash:           hash,
		Role:                   role,
		Email:                  req.Email,
		TelegramID:             req.TelegramID,
		Description:            req.Description,
		TrafficLimit:           int64(req.TrafficLimit * 1024 * 1024 * 1024),
		TrafficResetStrategy:   models.TrafficResetStrategy(req.TrafficResetStrategy),
		LastResetAt:            &nowStr,
		AWGMimicry:             models.AWGMimicryAuto,
		Enabled:                true,
		ShareEnabled:           false,
		ShareToken:             &shareToken,
		PasswordChangeRequired: false,
		CreatedAt:              now,
	}

	if user.TrafficResetStrategy == "" {
		user.TrafficResetStrategy = models.ResetStrategyNever
	}
	if req.AWGMimicry != nil {
		user.AWGMimicry = models.AWGMimicryProfile(*req.AWGMimicry)
	}
	h.applyUserExpiry(user, req)

	if _, err := h.db.CreateUser(ctx, user); err != nil {
		if errors.Is(err, database.ErrUserAlreadyExists) || strings.Contains(err.Error(), "user already exists") || strings.Contains(err.Error(), "UNIQUE constraint") {
			h.JSONError(w, http.StatusBadRequest, "user_exists", h.Translate(r, "user_exists"))
			return
		}
		h.JSONError(w, http.StatusInternalServerError, "internal_error", "Failed to create user")
		return
	}

	resp := map[string]any{
		"status":  "ok",
		"user_id": user.ID,
	}

	h.provisionInitialConnection(ctx, user, req, resp)

	h.audit(r, "user.add", map[string]any{"user_id": user.ID, "username": user.Username, "role": string(user.Role)})
	h.JSON(w, http.StatusOK, resp)
}

// applyUserExpiry parses and applies expiration fields onto a new user.
func (h *Handlers) applyUserExpiry(user *models.User, req models.AddUserRequest) {
	if req.ExpiresAt != nil {
		if t, err := time.Parse(time.RFC3339, *req.ExpiresAt); err == nil {
			user.ExpiresAt = &t
			user.ExpirationDate = &t
		}
		return
	}
	if req.ExpirationDate != nil {
		if t, err := time.Parse(time.RFC3339, *req.ExpirationDate); err == nil {
			user.ExpiresAt = &t
			user.ExpirationDate = &t
		}
	}
}

// provisionInitialConnection auto-creates an initial connection for a new user when requested.
func (h *Handlers) provisionInitialConnection(ctx context.Context, user *models.User, req models.AddUserRequest, resp map[string]any) {
	if req.ServerID == nil || req.Protocol == nil || *req.Protocol == "" {
		return
	}
	server, err := h.db.GetServer(ctx, *req.ServerID)
	if err != nil || server == nil {
		return
	}
	protoMgr, err := h.GetProtocolManager(*req.Protocol)
	if err != nil || protoMgr == nil {
		return
	}

	connName := fmt.Sprintf("%s_vpn", user.Username)
	if req.ConnectionName != nil && *req.ConnectionName != "" {
		connName = *req.ConnectionName
	}
	params := map[string]any{"name": connName}
	if req.AWGMimicry != nil {
		params["awg_mimicry"] = *req.AWGMimicry
	}

	connRes, err := protoMgr.AddClient(ctx, server, params)
	if err != nil {
		return
	}
	clientID, _ := connRes["client_id"].(string)
	if clientID == "" {
		clientID, _ = connRes["clientId"].(string)
	}
	configStr, _ := connRes["config"].(string)
	if configStr == "" && clientID != "" {
		configStr, _ = protoMgr.GetClientConfig(ctx, server, clientID)
	}

	newConn := &models.UserConnection{
		ID:         uuid.NewString(),
		UserID:     user.ID,
		ServerID:   server.ID,
		Protocol:   *req.Protocol,
		ClientID:   clientID,
		Name:       connName,
		AWGMimicry: user.AWGMimicry,
		CreatedAt:  time.Now(),
	}
	_, _ = h.db.CreateConnection(ctx, newConn)

	resp["connection_created"] = true
	resp["config"] = configStr
	resp["vpn_link"] = GenerateVPNLink(configStr)
}

// UpdateUserHandler updates user attributes and resets limits if modified.
func (h *Handlers) UpdateUserHandler(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "user_id")
	if userID == "" {
		h.JSONError(w, http.StatusBadRequest, "invalid_parameter", "user_id is required")
		return
	}

	var req models.UpdateUserRequest
	if err := h.DecodeJSON(r, &req); err != nil {
		h.JSONError(w, http.StatusBadRequest, "validation_failed", "Invalid request body")
		return
	}

	if err := req.Validate(); err != nil {
		h.JSONError(w, http.StatusBadRequest, "validation_failed", err.Error())
		return
	}

	ctx := r.Context()
	user, err := h.db.GetUser(ctx, userID)
	if err != nil || user == nil {
		h.JSONError(w, http.StatusNotFound, "not_found", "User not found")
		return
	}

	updates := make(map[string]any)
	if req.TelegramID != nil {
		updates["telegramId"] = *req.TelegramID
	}
	if req.Email != nil {
		updates["email"] = *req.Email
	}
	if req.Description != nil {
		updates["description"] = *req.Description
	}
	if req.TrafficLimit != nil {
		updates["traffic_limit"] = int64(*req.TrafficLimit * 1024 * 1024 * 1024)
	}
	if req.TrafficResetStrategy != nil {
		updates["traffic_reset_strategy"] = *req.TrafficResetStrategy
		updates["last_reset_at"] = time.Now().Format(time.RFC3339)
	}
	if req.ExpiresAt != nil {
		if t, err := time.Parse(time.RFC3339, *req.ExpiresAt); err == nil {
			updates["expires_at"] = t
			updates["expiration_date"] = t
		}
	} else if req.ExpirationDate != nil {
		if t, err := time.Parse(time.RFC3339, *req.ExpirationDate); err == nil {
			updates["expires_at"] = t
			updates["expiration_date"] = t
		}
	}
	if req.AWGMimicry != nil {
		updates["awg_mimicry"] = *req.AWGMimicry
	}
	if req.Password != nil && *req.Password != "" {
		if hash, err := security.HashPassword(*req.Password); err == nil {
			updates["password_hash"] = hash
		}
	}

	if len(updates) > 0 {
		if _, err := h.db.UpdateUser(ctx, userID, updates); err != nil {
			h.JSONError(w, http.StatusInternalServerError, "internal_error", "Failed to update user")
			return
		}
	}

	h.audit(r, "user.update", map[string]any{"user_id": userID, "username": user.Username, "fields": len(updates)})
	h.JSONOK(w)
}

// DeleteUserHandler removes a user and their client connections across all servers.
func (h *Handlers) DeleteUserHandler(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "user_id")
	if userID == "" {
		h.JSONError(w, http.StatusBadRequest, "invalid_parameter", "user_id is required")
		return
	}

	sess := h.GetSession(r)
	if sess != nil && sess.UserID == userID {
		h.JSONError(w, http.StatusBadRequest, "cannot_delete_self", h.Translate(r, "cannot_delete_self"))
		return
	}

	ctx := r.Context()
	user, err := h.db.GetUser(ctx, userID)
	if err != nil || user == nil {
		h.JSONError(w, http.StatusNotFound, "not_found", "User not found")
		return
	}

	// Delete client connections on servers
	conns, _ := h.db.GetConnectionsByUserID(ctx, userID)
	for _, c := range conns {
		if server, err := h.db.GetServer(ctx, c.ServerID); err == nil && server != nil {
			if protoMgr, err := h.GetProtocolManager(c.Protocol); err == nil && protoMgr != nil {
				_ = protoMgr.RemoveClient(ctx, server, c.ClientID)
			}
		}
	}

	if _, err := h.db.DeleteConnectionsByUser(ctx, userID); err != nil {
		h.JSONError(w, http.StatusInternalServerError, "database_error", "Failed to delete user connections")
		return
	}
	if _, err := h.db.DeleteUser(ctx, userID); err != nil {
		h.JSONError(w, http.StatusInternalServerError, "database_error", "Failed to delete user")
		return
	}

	h.audit(r, "user.delete", map[string]any{"user_id": userID, "username": user.Username})
	h.JSONOK(w)
}

// ToggleUserHandler enables or disables a user and toggles their server client connections.
func (h *Handlers) ToggleUserHandler(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "user_id")
	if userID == "" {
		h.JSONError(w, http.StatusBadRequest, "invalid_parameter", "user_id is required")
		return
	}

	var req models.ToggleUserRequest
	if err := h.DecodeJSON(r, &req); err != nil {
		h.JSONError(w, http.StatusBadRequest, "validation_failed", "Invalid request body")
		return
	}

	ctx := r.Context()
	user, err := h.db.GetUser(ctx, userID)
	if err != nil || user == nil {
		h.JSONError(w, http.StatusNotFound, "not_found", "User not found")
		return
	}

	if _, err := h.db.UpdateUser(ctx, userID, map[string]any{
		"enabled": req.Enabled,
	}); err != nil {
		h.JSONError(w, http.StatusInternalServerError, "database_error", "Failed to update user status")
		return
	}

	// Toggle clients on servers
	conns, _ := h.db.GetConnectionsByUserID(ctx, userID)
	for _, c := range conns {
		if c.Protocol == "awg" && h.awgMgr != nil {
			if server, err := h.db.GetServer(ctx, c.ServerID); err == nil && server != nil {
				_ = h.awgMgr.ToggleClient(ctx, server, c.ClientID, req.Enabled)
			}
		}
	}

	h.audit(r, "user.toggle", map[string]any{"user_id": userID, "username": user.Username, "enabled": req.Enabled})
	h.JSON(w, http.StatusOK, map[string]any{
		"status":  "ok",
		"enabled": req.Enabled,
	})
}

// AddUserConnectionHandler assigns a new or existing connection to a specific user.
func (h *Handlers) AddUserConnectionHandler(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "user_id")
	if userID == "" {
		h.JSONError(w, http.StatusBadRequest, "invalid_parameter", "user_id is required")
		return
	}

	var req models.AddUserConnectionRequest
	if err := h.DecodeJSON(r, &req); err != nil {
		h.JSONError(w, http.StatusBadRequest, "validation_failed", "Invalid request body")
		return
	}

	if err := req.Validate(); err != nil {
		h.JSONError(w, http.StatusBadRequest, "validation_failed", err.Error())
		return
	}

	ctx := r.Context()
	user, err := h.db.GetUser(ctx, userID)
	if err != nil || user == nil {
		h.JSONError(w, http.StatusNotFound, "not_found", "User not found")
		return
	}

	server, err := h.db.GetServer(ctx, req.ServerID)
	if err != nil || server == nil {
		h.JSONError(w, http.StatusNotFound, "not_found", "Server not found")
		return
	}

	protoMgr, err := h.GetProtocolManager(req.Protocol)
	if err != nil {
		h.JSONError(w, http.StatusBadRequest, "invalid_protocol", err.Error())
		return
	}

	var clientID, configStr string
	if req.ClientID != nil && *req.ClientID != "" {
		clientID = *req.ClientID
		configStr, _ = protoMgr.GetClientConfig(ctx, server, clientID)
	} else {
		params := map[string]any{
			"name": req.Name,
		}
		if req.TelemtQuota != nil {
			params["telemt_quota"] = *req.TelemtQuota
		}
		if req.TelemtMaxIPs != nil {
			params["telemt_max_ips"] = *req.TelemtMaxIPs
		}
		if req.TelemtExpiry != nil {
			params["telemt_expiry"] = *req.TelemtExpiry
		}
		if req.AWGMimicry != nil {
			params["awg_mimicry"] = *req.AWGMimicry
		}

		res, err := protoMgr.AddClient(ctx, server, params)
		if err != nil {
			h.JSONError(w, http.StatusInternalServerError, "add_client_failed", "Failed to add client")
			return
		}
		clientID, _ = res["client_id"].(string)
		if clientID == "" {
			clientID, _ = res["clientId"].(string)
		}
		configStr, _ = res["config"].(string)
		if configStr == "" && clientID != "" {
			configStr, _ = protoMgr.GetClientConfig(ctx, server, clientID)
		}
	}

	vpnLink := GenerateVPNLink(configStr)
	newConn := &models.UserConnection{
		ID:         uuid.NewString(),
		UserID:     userID,
		ServerID:   req.ServerID,
		Protocol:   req.Protocol,
		ClientID:   clientID,
		Name:       req.Name,
		AWGMimicry: user.AWGMimicry,
		CreatedAt:  time.Now(),
	}
	if req.AWGMimicry != nil {
		newConn.AWGMimicry = models.AWGMimicryProfile(*req.AWGMimicry)
	}

	_, _ = h.db.CreateConnection(ctx, newConn)

	h.audit(r, "user.connection_add", map[string]any{"user_id": userID, "server_id": req.ServerID, "protocol": req.Protocol, "client_id": clientID})
	h.JSON(w, http.StatusOK, map[string]any{
		"status":     "ok",
		"connection": newConn,
		"config":     configStr,
		"vpn_link":   vpnLink,
	})
}

// GetUserConnectionsHandler lists all connections belonging to a user.
func (h *Handlers) GetUserConnectionsHandler(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "user_id")
	if userID == "" {
		h.JSONError(w, http.StatusBadRequest, "invalid_parameter", "user_id is required")
		return
	}

	sess := h.GetSession(r)
	if sess == nil {
		h.JSONError(w, http.StatusUnauthorized, "unauthorized", "Authentication required")
		return
	}
	if sess.Role == models.RoleUser && sess.UserID != userID {
		h.JSONError(w, http.StatusForbidden, "forbidden", "Forbidden")
		return
	}

	ctx := r.Context()
	conns, err := h.db.GetConnectionsByUserID(ctx, userID)
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
		}
	}

	h.JSON(w, http.StatusOK, map[string]any{
		"status":      "ok",
		"connections": enriched,
	})
}

// SetupUserShareHandler configures public share link credentials and availability.
func (h *Handlers) SetupUserShareHandler(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "user_id")
	if userID == "" {
		h.JSONError(w, http.StatusBadRequest, "invalid_parameter", "user_id is required")
		return
	}

	var req struct {
		Enabled  *bool   `json:"enabled"`
		Password *string `json:"password"`
	}
	if err := h.DecodeJSON(r, &req); err != nil {
		h.JSONError(w, http.StatusBadRequest, "validation_failed", "Invalid request body")
		return
	}

	ctx := r.Context()
	user, err := h.db.GetUser(ctx, userID)
	if err != nil || user == nil {
		h.JSONError(w, http.StatusNotFound, "not_found", "User not found")
		return
	}

	updates := make(map[string]any)
	if req.Enabled != nil {
		updates["share_enabled"] = *req.Enabled
	} else {
		updates["share_enabled"] = true
	}

	token := ""
	if user.ShareToken != nil && *user.ShareToken != "" {
		token = *user.ShareToken
	} else {
		token = generateRandomToken(16)
		updates["share_token"] = token
	}

	if req.Password != nil {
		if *req.Password != "" {
			if hash, err := security.HashPassword(*req.Password); err == nil {
				updates["share_password_hash"] = hash
			}
		} else {
			updates["share_password_hash"] = ""
		}
	}

	_, _ = h.db.UpdateUser(ctx, userID, updates)

	h.audit(r, "user.share_setup", map[string]any{"user_id": userID, "username": user.Username, "enabled": updates["share_enabled"]})
	h.JSON(w, http.StatusOK, map[string]any{
		"status":      "ok",
		"share_token": token,
	})
}

func generateRandomToken(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
