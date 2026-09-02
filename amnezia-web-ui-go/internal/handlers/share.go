package handlers

import (
	"fmt"
	"net/http"

	"github.com/devops-igor/amnezia-web-ui-go/internal/middleware"
	"github.com/devops-igor/amnezia-web-ui-go/internal/models"
	"github.com/devops-igor/amnezia-web-ui-go/internal/security"
	"github.com/go-chi/chi/v5"
)

// SharePageHandler renders the public share landing page for a user.
func (h *Handlers) SharePageHandler(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	if token == "" {
		http.NotFound(w, r)
		return
	}

	ctx := r.Context()
	user, err := h.db.GetUserByShareToken(ctx, token)
	if err != nil || user == nil || !user.ShareEnabled {
		w.WriteHeader(http.StatusNotFound)
		_ = RenderTemplate(w, r, h.db, "user_share.html", map[string]any{
			"not_found": true,
			"token":     token,
		})
		return
	}

	sess := h.GetSession(r)
	needPassword := user.SharePasswordHash != nil && *user.SharePasswordHash != ""
	if needPassword && sess != nil && sess.ShareAuthenticated != nil && sess.ShareAuthenticated[token] {
		needPassword = false
	}

	_ = RenderTemplate(w, r, h.db, "user_share.html", map[string]any{
		"token":         token,
		"share_user":    user,
		"need_password": needPassword,
	})
}

// ShareAuthHandler verifies the access password for a protected public share link.
func (h *Handlers) ShareAuthHandler(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	if token == "" {
		h.JSONError(w, http.StatusBadRequest, "invalid_parameter", "Token is required")
		return
	}

	var req models.ShareAuthRequest
	if err := h.DecodeJSON(r, &req); err != nil {
		h.JSONError(w, http.StatusBadRequest, "validation_failed", "Invalid request body")
		return
	}

	ctx := r.Context()
	user, err := h.db.GetUserByShareToken(ctx, token)
	if err != nil || user == nil || !user.ShareEnabled {
		h.JSONError(w, http.StatusNotFound, "not_found", "Share link not found or expired")
		return
	}

	if user.SharePasswordHash != nil && *user.SharePasswordHash != "" {
		if !security.CheckPasswordHash(req.Password, *user.SharePasswordHash) {
			h.JSONError(w, http.StatusUnauthorized, "invalid_password", h.Translate(r, "wrong_share_password"))
			return
		}
	}

	sess := h.GetSession(r)
	if sess == nil {
		sess = &models.SessionData{
			ShareAuthenticated: make(map[string]bool),
		}
	}
	if sess.ShareAuthenticated == nil {
		sess.ShareAuthenticated = make(map[string]bool)
	}
	sess.ShareAuthenticated[token] = true

	if h.cfg != nil && h.cfg.SecretKey != "" {
		_ = middleware.SetSessionCookie(w, sess, h.cfg.SecretKey, false, 86400)
	}

	h.JSON(w, http.StatusOK, map[string]any{"status": "success"})
}

// GetShareConnectionsHandler returns connection list accessible through the share token.
func (h *Handlers) GetShareConnectionsHandler(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	if token == "" {
		h.JSONError(w, http.StatusBadRequest, "invalid_parameter", "Token is required")
		return
	}

	ctx := r.Context()
	user, err := h.db.GetUserByShareToken(ctx, token)
	if err != nil || user == nil || !user.ShareEnabled {
		h.JSONError(w, http.StatusForbidden, "forbidden", "Forbidden")
		return
	}

	sess := h.GetSession(r)
	if user.SharePasswordHash != nil && *user.SharePasswordHash != "" {
		if sess == nil || sess.ShareAuthenticated == nil || !sess.ShareAuthenticated[token] {
			h.JSONError(w, http.StatusUnauthorized, "unauthorized", "Unauthorized")
			return
		}
	}

	conns, err := h.db.GetConnectionsByUserID(ctx, user.ID)
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
			"id":          c.ID,
			"name":        c.Name,
			"protocol":    c.Protocol,
			"server_name": srvName,
			"server_id":   c.ServerID,
			"client_id":   c.ClientID,
		}
	}

	h.JSON(w, http.StatusOK, map[string]any{
		"status":      "ok",
		"username":    user.Username,
		"connections": enriched,
	})
}

// GetShareConnectionConfigHandler retrieves client config for a connection via share token.
func (h *Handlers) GetShareConnectionConfigHandler(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	connectionID := chi.URLParam(r, "connection_id")
	if token == "" || connectionID == "" {
		h.JSONError(w, http.StatusBadRequest, "invalid_parameter", "Invalid request parameters")
		return
	}

	ctx := r.Context()
	user, err := h.db.GetUserByShareToken(ctx, token)
	if err != nil || user == nil || !user.ShareEnabled {
		h.JSONError(w, http.StatusForbidden, "forbidden", "Forbidden")
		return
	}

	sess := h.GetSession(r)
	if user.SharePasswordHash != nil && *user.SharePasswordHash != "" {
		if sess == nil || sess.ShareAuthenticated == nil || !sess.ShareAuthenticated[token] {
			h.JSONError(w, http.StatusUnauthorized, "unauthorized", "Unauthorized")
			return
		}
	}

	conn, err := h.db.GetConnection(ctx, connectionID)
	if err != nil || conn == nil || conn.UserID != user.ID {
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
	h.JSON(w, http.StatusOK, map[string]any{
		"status":   "ok",
		"config":   configStr,
		"filename": fmt.Sprintf("%s.conf", conn.Name),
		"vpn_link": vpnLink,
	})
}
