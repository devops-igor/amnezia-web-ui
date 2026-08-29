package handlers

import (
	"net/http"

	"github.com/devops-igor/amnezia-web-ui-go/internal/models"
	"github.com/devops-igor/amnezia-web-ui-go/internal/vpn"
)

// VPNStatusHandler returns operational metrics for the VPN subsystem.
func (h *Handlers) VPNStatusHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var status *vpn.Status
	var err error

	if h.vpnSvc != nil {
		status, err = h.vpnSvc.GetStatus(ctx)
	}

	if err != nil || status == nil {
		status = &vpn.Status{
			ListenerRunning:   false,
			ActiveTunnels:     0,
			ConnectedSessions: 0,
		}
	}

	h.JSON(w, http.StatusOK, status)
}

// VPNBackendsHandler returns all configured VPN backend tunnels.
func (h *Handlers) VPNBackendsHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var backends []*models.BackendTunnel

	if h.vpnSvc != nil {
		backends, _ = h.vpnSvc.GetBackends(ctx)
	}
	if backends == nil {
		backends = make([]*models.BackendTunnel, 0)
	}

	h.JSON(w, http.StatusOK, map[string]any{
		"backends": backends,
	})
}

// VPNEnableBackendHandler activates a backend server for load balancing.
func (h *Handlers) VPNEnableBackendHandler(w http.ResponseWriter, r *http.Request) {
	serverID, err := parseServerID(r)
	if err != nil {
		h.JSONError(w, http.StatusBadRequest, "invalid_parameter", "Invalid server_id")
		return
	}

	ctx := r.Context()
	if h.vpnSvc != nil {
		if err := h.vpnSvc.EnableBackend(ctx, serverID); err != nil {
			h.JSONError(w, http.StatusInternalServerError, "internal_error", "Failed to enable backend")
			return
		}
	}

	h.audit(r, "vpn.backend_enable", map[string]any{"server_id": serverID})
	h.JSONOK(w)
}

// VPNDisableBackendHandler deactivates a backend server and initiates peer draining.
func (h *Handlers) VPNDisableBackendHandler(w http.ResponseWriter, r *http.Request) {
	serverID, err := parseServerID(r)
	if err != nil {
		h.JSONError(w, http.StatusBadRequest, "invalid_parameter", "Invalid server_id")
		return
	}

	ctx := r.Context()
	if h.vpnSvc != nil {
		if err := h.vpnSvc.DisableBackend(ctx, serverID); err != nil {
			h.JSONError(w, http.StatusInternalServerError, "internal_error", "Failed to disable backend")
			return
		}
	}

	h.audit(r, "vpn.backend_disable", map[string]any{"server_id": serverID})
	h.JSON(w, http.StatusOK, map[string]any{
		"status":   "ok",
		"draining": true,
	})
}

// VPNTunnelsHandler returns all active VPN tunnels (alias for backends).
func (h *Handlers) VPNTunnelsHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var tunnels []*models.BackendTunnel

	if h.vpnSvc != nil {
		tunnels, _ = h.vpnSvc.GetTunnels(ctx)
	}
	if tunnels == nil {
		tunnels = make([]*models.BackendTunnel, 0)
	}

	h.JSON(w, http.StatusOK, map[string]any{
		"tunnels": tunnels,
	})
}

// VPNGetConfigHandler returns dynamic routing and load balancer configuration.
func (h *Handlers) VPNGetConfigHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var cfg *models.VPNConfig
	var err error

	if h.vpnSvc != nil {
		cfg, err = h.vpnSvc.GetConfig(ctx)
	}
	if err != nil || cfg == nil {
		cfg = &models.VPNConfig{
			Algorithm:          models.LBLeastConnections,
			HealthThresholdMS:  500,
			ListenPort:         51820,
			SubnetCIDR:         "10.100.0.0/16",
			MaxTotalPeers:      1000,
			MaxPeersPerBackend: 250,
			Weights:            make(map[int64]int),
		}
	}

	h.JSON(w, http.StatusOK, cfg)
}

// VPNUpdateConfigHandler applies new routing policy and rebalances existing pools.
func (h *Handlers) VPNUpdateConfigHandler(w http.ResponseWriter, r *http.Request) {
	var cfg models.VPNConfig
	if err := h.DecodeJSON(r, &cfg); err != nil {
		h.JSONError(w, http.StatusBadRequest, "validation_failed", "Invalid request body")
		return
	}

	ctx := r.Context()
	if h.vpnSvc != nil {
		if err := h.vpnSvc.UpdateConfig(ctx, &cfg); err != nil {
			h.JSONError(w, http.StatusInternalServerError, "internal_error", "Failed to update VPN configuration")
			return
		}
	} else if h.db != nil {
		_ = h.db.SaveVPNConfig(ctx, &cfg)
	}

	h.audit(r, "vpn.config_update", map[string]any{"algorithm": string(cfg.Algorithm), "listen_port": cfg.ListenPort})
	h.JSONOK(w)
}

// VPNMyConnectionHandler returns real-time VPN connection state for the session user.
func (h *Handlers) VPNMyConnectionHandler(w http.ResponseWriter, r *http.Request) {
	sess := h.GetSession(r)
	if sess == nil || !sess.IsAuthenticated() {
		h.JSONError(w, http.StatusUnauthorized, "unauthorized", "Authentication required")
		return
	}

	ctx := r.Context()
	var state *vpn.UserVPNState
	if h.vpnSvc != nil {
		state, _ = h.vpnSvc.GetUserConnectionState(ctx, sess.UserID)
	}
	if state == nil {
		state = &vpn.UserVPNState{Connected: false}
	}

	h.JSON(w, http.StatusOK, state)
}

// VPNMyConfigHandler generates a portal VPN configuration file for the session user.
func (h *Handlers) VPNMyConfigHandler(w http.ResponseWriter, r *http.Request) {
	sess := h.GetSession(r)
	if sess == nil || !sess.IsAuthenticated() {
		h.JSONError(w, http.StatusUnauthorized, "unauthorized", "Authentication required")
		return
	}

	ctx := r.Context()
	configStr, filename, err := "", "portal-awg.conf", error(nil)
	if h.vpnSvc != nil {
		configStr, filename, err = h.vpnSvc.GenerateClientConfig(ctx, sess.UserID)
	} else {
		configStr = "[Interface]\n# VPN subsystem offline\n"
	}

	if err != nil {
		h.JSONError(w, http.StatusInternalServerError, "internal_error", "Failed to generate VPN configuration")
		return
	}

	vpnLink := GenerateVPNLink(configStr)
	h.JSON(w, http.StatusOK, map[string]any{
		"status":   "ok",
		"config":   configStr,
		"filename": filename,
		"vpn_link": vpnLink,
	})
}

// VPNDisconnectHandler terminates active user or session VPN connections.
func (h *Handlers) VPNDisconnectHandler(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SessionID string `json:"session_id"`
		UserID    string `json:"user_id"`
	}
	if err := h.DecodeJSON(r, &req); err != nil {
		h.JSONError(w, http.StatusBadRequest, "validation_failed", "Invalid request body")
		return
	}

	ctx := r.Context()
	if h.vpnSvc != nil {
		if req.SessionID != "" {
			_ = h.vpnSvc.DisconnectSession(ctx, req.SessionID)
		} else if req.UserID != "" {
			_ = h.vpnSvc.DisconnectUser(ctx, req.UserID)
		}
	}

	h.audit(r, "vpn.disconnect", map[string]any{"session_id": req.SessionID, "user_id": req.UserID})
	h.JSONOK(w)
}
