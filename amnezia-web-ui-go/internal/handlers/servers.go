package handlers

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/devops-igor/amnezia-web-ui-go/internal/manager/awg/tc"
	"github.com/devops-igor/amnezia-web-ui-go/internal/manager/ssh"
	"github.com/devops-igor/amnezia-web-ui-go/internal/models"
	"github.com/go-chi/chi/v5"
)

// AddServerHandler initiates the SSH connection test and fingerprint retrieval.
func (h *Handlers) AddServerHandler(w http.ResponseWriter, r *http.Request) {
	var req models.AddServerRequest
	if err := h.DecodeJSON(r, &req); err != nil {
		h.JSONError(w, http.StatusBadRequest, "validation_failed", "Invalid request body")
		return
	}

	if err := req.Validate(); err != nil {
		h.JSONError(w, http.StatusBadRequest, "validation_failed", err.Error())
		return
	}

	req.Host = strings.TrimSpace(req.Host)
	req.Username = strings.TrimSpace(req.Username)
	if req.Host == "" || req.Username == "" {
		h.JSONError(w, http.StatusBadRequest, "validation_failed", "Host and username are required")
		return
	}
	if req.Password == "" && req.PrivateKey == "" {
		h.JSONError(w, http.StatusBadRequest, "validation_failed", "Password or SSH key is required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	sshCfg := ssh.Config{
		Host:       req.Host,
		Port:       req.SSHPort,
		User:       req.Username,
		Password:   req.Password,
		PrivateKey: req.PrivateKey,
		Timeout:    10 * time.Second,
	}

	client, err := ssh.Dial(ctx, sshCfg)
	if err != nil {
		h.JSONError(w, http.StatusBadRequest, "connection_failed", "SSH connection failed")
		return
	}
	defer client.Close()

	fingerprint := client.CapturedFingerprint()
	serverInfo, _, _, _ := client.RunCommand(ctx, "uname -a 2>/dev/null || cat /etc/os-release 2>/dev/null || echo Linux")
	serverInfo = strings.TrimSpace(serverInfo)

	h.JSON(w, http.StatusOK, map[string]any{
		"status":               "pending_fingerprint_confirmation",
		"fingerprint_required": true,
		"fingerprint":          fingerprint,
		"server_info":          serverInfo,
	})
}

// ConfirmFingerprintHandler persists the server and verified host key fingerprint.
func (h *Handlers) ConfirmFingerprintHandler(w http.ResponseWriter, r *http.Request) {
	var req models.ConfirmFingerprintRequest
	if err := h.DecodeJSON(r, &req); err != nil {
		h.JSONError(w, http.StatusBadRequest, "validation_failed", "Invalid request body")
		return
	}

	if err := req.Validate(); err != nil {
		h.JSONError(w, http.StatusBadRequest, "validation_failed", err.Error())
		return
	}

	req.Host = strings.TrimSpace(req.Host)
	req.Username = strings.TrimSpace(req.Username)
	req.Fingerprint = strings.TrimSpace(req.Fingerprint)

	if req.Host == "" || req.Username == "" {
		h.JSONError(w, http.StatusBadRequest, "validation_failed", "Host and username are required")
		return
	}
	if req.Password == "" && req.PrivateKey == "" {
		h.JSONError(w, http.StatusBadRequest, "validation_failed", "Password or SSH key is required")
		return
	}
	if req.Fingerprint == "" {
		h.JSONError(w, http.StatusBadRequest, "validation_failed", "Fingerprint is required")
		return
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = req.Host
	}

	ctx := r.Context()
	server := &models.Server{
		Name:      name,
		Host:      req.Host,
		SSHPort:   req.SSHPort,
		SSHUser:   req.Username,
		SSHPass:   req.Password,
		SSHKey:    req.PrivateKey,
		Protocols: make(map[string]any),
		CreatedAt: time.Now(),
	}

	serverID, err := h.db.CreateServer(ctx, server)
	if err != nil {
		h.JSONError(w, http.StatusInternalServerError, "internal_error", "Failed to save server")
		return
	}

	_ = h.db.SaveKnownHostFingerprint(ctx, serverID, req.Fingerprint)

	h.audit(r, "server.confirm_fingerprint", map[string]any{"server_id": serverID, "name": name, "host": req.Host})

	h.JSON(w, http.StatusOK, map[string]any{
		"status":    "ok",
		"server_id": serverID,
	})
}

// DeleteServerHandler removes a server and all its associated connections.
func (h *Handlers) DeleteServerHandler(w http.ResponseWriter, r *http.Request) {
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

	if h.sshPool != nil {
		h.sshPool.Remove(serverID)
	}

	if _, err := h.db.DeleteConnectionsByServer(ctx, serverID); err != nil {
		h.JSONError(w, http.StatusInternalServerError, "database_error", "Failed to delete server connections")
		return
	}
	if _, err := h.db.DeleteKnownHost(ctx, serverID); err != nil {
		h.JSONError(w, http.StatusInternalServerError, "database_error", "Failed to delete server known host")
		return
	}
	if _, err := h.db.DeleteServer(ctx, serverID); err != nil {
		h.JSONError(w, http.StatusInternalServerError, "database_error", "Failed to delete server")
		return
	}

	h.audit(r, "server.delete", map[string]any{"server_id": serverID, "name": server.Name})
	h.JSONOK(w)
}

// RebootServerHandler triggers a remote host reboot via SSH.
func (h *Handlers) RebootServerHandler(w http.ResponseWriter, r *http.Request) {
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

	client, err := h.GetSSHClient(ctx, server)
	if err != nil {
		h.JSONError(w, http.StatusBadRequest, "connection_failed", "SSH connection failed")
		return
	}

	if _, _, _, err := client.RunSudoCommand(ctx, "nohup reboot > /dev/null 2>&1 &"); err != nil {
		h.JSONError(w, http.StatusInternalServerError, "operation_failed", "Failed to execute reboot command: "+err.Error())
		return
	}

	if h.sshPool != nil {
		h.sshPool.Remove(serverID)
	}

	h.audit(r, "server.reboot", map[string]any{"server_id": serverID, "name": server.Name})
	h.JSONOK(w)
}

// ClearServerHandler stops all protocol containers and wipes server configuration.
func (h *Handlers) ClearServerHandler(w http.ResponseWriter, r *http.Request) {
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

	client, err := h.GetSSHClient(ctx, server)
	if err != nil {
		h.JSONError(w, http.StatusBadRequest, "connection_failed", "SSH connection failed")
		return
	}

	containers := []string{
		"amnezia-awg",
		"amnezia-awg2",
		"amnezia-awg-legacy",
		"telemt",
		"amnezia-dns",
	}

	for _, c := range containers {
		_, _, _, _ = client.RunSudoCommand(ctx, fmt.Sprintf("docker stop %s || true", c))
		_, _, _, _ = client.RunSudoCommand(ctx, fmt.Sprintf("docker rm %s || true", c))
	}
	_, _, _, _ = client.RunSudoCommand(ctx, "docker network rm amnezia-dns-net || true")
	if _, _, _, err := client.RunSudoCommand(ctx, "rm -rf /opt/amnezia"); err != nil {
		h.JSONError(w, http.StatusInternalServerError, "operation_failed", "Failed to clear server directory: "+err.Error())
		return
	}

	if _, err := h.db.DeleteConnectionsByServer(ctx, serverID); err != nil {
		h.JSONError(w, http.StatusInternalServerError, "database_error", "Failed to delete server connections")
		return
	}
	if err := h.db.UpdateServerProtocols(ctx, serverID, make(map[string]any)); err != nil {
		h.JSONError(w, http.StatusInternalServerError, "database_error", "Failed to clear server protocols")
		return
	}

	h.audit(r, "server.clear", map[string]any{"server_id": serverID, "name": server.Name})
	h.JSONOK(w)
}

// ServerStatsHandler collects telemetry and resource utilization from the remote host.
func (h *Handlers) ServerStatsHandler(w http.ResponseWriter, r *http.Request) {
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

	client, err := h.GetSSHClient(ctx, server)
	if err != nil {
		h.JSONError(w, http.StatusBadRequest, "connection_failed", "SSH connection failed")
		return
	}

	combinedCmd := "echo '===CPU==='; " +
		"top -bn1 | grep 'Cpu(s)' | awk '{print $2}' | cut -d'%' -f1 2>/dev/null || " +
		"awk '{u=$2+$4; t=$2+$4+$5; if(NR==1){pu=u;pt=t} else printf \"%.1f\", (u-pu)/(t-pt)*100}' <(grep 'cpu ' /proc/stat) <(sleep 0.5 && grep 'cpu ' /proc/stat) 2>/dev/null; " +
		"echo ''; " +
		"echo '===RAM==='; " +
		"free -b | awk 'NR==2{printf \"%d %d\", $3, $2}'; " +
		"echo ''; " +
		"echo '===DISK==='; " +
		"df -B1 / | awk 'NR==2{printf \"%d %d\", $3, $2}'; " +
		"echo ''; " +
		"echo '===NET==='; " +
		"DEV=$(ip route | awk '/default/ {print $5}' | head -1); " +
		"cat /proc/net/dev | awk -v dev=\"$DEV:\" '$1==dev{printf \"%d %d\", $2, $10}'; " +
		"echo ''; " +
		"echo '===UPTIME==='; " +
		"uptime -p 2>/dev/null || uptime"

	out, _, _, _ := client.RunCommand(ctx, combinedCmd)
	stats := parseCombinedStats(out)

	h.JSON(w, http.StatusOK, stats)
}

// ServerCheckHandler checks connectivity and Docker protocol status on the server.
func (h *Handlers) ServerCheckHandler(w http.ResponseWriter, r *http.Request) {
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

	client, err := h.GetSSHClient(ctx, server)
	if err != nil {
		h.JSON(w, http.StatusOK, models.ServerCheckResponse{
			Connection:      "failed",
			DockerInstalled: false,
			Protocols:       make(map[string]any),
		})
		return
	}

	// Check docker
	dockerOut, _, code, _ := client.RunCommand(ctx, "docker --version")
	dockerInstalled := code == 0 && strings.Contains(strings.ToLower(dockerOut), "docker")

	protocolsStatus := make(map[string]any)

	// Check AWG
	if h.awgMgr != nil {
		if status, err := h.awgMgr.GetServerStatus(ctx, server); err == nil {
			protocolsStatus["awg"] = status
		}
	}
	// Check MTProxyL
	if h.mtproxylMgr != nil {
		if status, err := h.mtproxylMgr.GetServerStatus(ctx, server); err == nil {
			protocolsStatus["telemt"] = status
		}
	}
	// Check DNS
	if h.dnsMgr != nil {
		if status, err := h.dnsMgr.GetServerStatus(ctx, server); err == nil {
			protocolsStatus["dns"] = status
		}
	}

	h.JSON(w, http.StatusOK, models.ServerCheckResponse{
		Connection:      "ok",
		DockerInstalled: dockerInstalled,
		Protocols:       protocolsStatus,
	})
}

// InstallProtocolHandler deploys a VPN protocol backend to the remote server.
func (h *Handlers) InstallProtocolHandler(w http.ResponseWriter, r *http.Request) {
	serverID, err := parseServerID(r)
	if err != nil {
		h.JSONError(w, http.StatusBadRequest, "invalid_parameter", "Invalid server_id")
		return
	}

	var req models.InstallProtocolRequest
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

	params := map[string]any{
		"port": req.Port,
	}
	if req.TLSEmulation != nil {
		params["tls_emulation"] = *req.TLSEmulation
	}
	if req.TLSDomain != nil {
		params["tls_domain"] = *req.TLSDomain
	}
	if req.MaxConnections != nil {
		params["max_connections"] = *req.MaxConnections
	}
	if req.AWGProfile != nil {
		params["awg_profile"] = string(*req.AWGProfile)
	}
	if req.AWGCPSProtocol != nil {
		params["awg_cps_protocol"] = *req.AWGCPSProtocol
	}

	if err := protoMgr.Install(ctx, server, params); err != nil {
		h.JSONError(w, http.StatusInternalServerError, "install_failed", "Failed to install protocol")
		return
	}

	if server.Protocols == nil {
		server.Protocols = make(map[string]any)
	}
	server.Protocols[req.Protocol] = map[string]any{
		"installed": true,
		"port":      req.Port,
	}
	_ = h.db.UpdateServerProtocols(ctx, serverID, server.Protocols)

	h.audit(r, "server.protocol_install", map[string]any{"server_id": serverID, "protocol": req.Protocol, "port": req.Port})
	h.JSON(w, http.StatusOK, map[string]any{
		"status":   "ok",
		"protocol": req.Protocol,
	})
}

// UninstallProtocolHandler removes a protocol container and removes associated connections.
func (h *Handlers) UninstallProtocolHandler(w http.ResponseWriter, r *http.Request) {
	serverID, err := parseServerID(r)
	if err != nil {
		h.JSONError(w, http.StatusBadRequest, "invalid_parameter", "Invalid server_id")
		return
	}

	var req models.ProtocolRequest
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
	if err == nil && protoMgr != nil {
		_ = protoMgr.Uninstall(ctx, server)
	}

	if server.Protocols != nil {
		delete(server.Protocols, req.Protocol)
		_ = h.db.UpdateServerProtocols(ctx, serverID, server.Protocols)
	}
	_, _ = h.db.DeleteConnectionsByServerAndProtocol(ctx, serverID, req.Protocol)

	h.audit(r, "server.protocol_uninstall", map[string]any{"server_id": serverID, "protocol": req.Protocol})
	h.JSONOK(w)
}

// ToggleContainerHandler starts, stops, or restarts a protocol container.
func (h *Handlers) ToggleContainerHandler(w http.ResponseWriter, r *http.Request) {
	serverID, err := parseServerID(r)
	if err != nil {
		h.JSONError(w, http.StatusBadRequest, "invalid_parameter", "Invalid server_id")
		return
	}

	var req struct {
		Protocol string `json:"protocol"`
		Action   string `json:"action"`
	}
	if err := h.DecodeJSON(r, &req); err != nil {
		h.JSONError(w, http.StatusBadRequest, "validation_failed", "Invalid request body")
		return
	}

	req.Protocol = models.NormalizeProtocol(req.Protocol)
	if !models.IsValidProtocol(req.Protocol) {
		h.JSONError(w, http.StatusBadRequest, "invalid_protocol", "Unknown protocol")
		return
	}

	containerName, ok := models.ContainerNameForProtocol(req.Protocol)
	if !ok {
		h.JSONError(w, http.StatusBadRequest, "invalid_protocol", "Unknown protocol")
		return
	}

	if req.Action == "" {
		req.Action = "restart"
	}
	if req.Action != "start" && req.Action != "stop" && req.Action != "restart" {
		h.JSONError(w, http.StatusBadRequest, "validation_failed", "Invalid action: must be start, stop, or restart")
		return
	}

	ctx := r.Context()
	server, err := h.db.GetServer(ctx, serverID)
	if err != nil || server == nil {
		h.JSONError(w, http.StatusNotFound, "not_found", "Server not found")
		return
	}

	client, err := h.GetSSHClient(ctx, server)
	if err != nil {
		h.JSONError(w, http.StatusBadRequest, "connection_failed", "SSH connection failed")
		return
	}

	var runErr error
	switch req.Action {
	case "start":
		_, _, _, runErr = client.RunSudoCommand(ctx, fmt.Sprintf("docker start %s", containerName))
	case "stop":
		_, _, _, runErr = client.RunSudoCommand(ctx, fmt.Sprintf("docker stop %s", containerName))
	default:
		_, _, _, runErr = client.RunSudoCommand(ctx, fmt.Sprintf("docker restart %s", containerName))
	}
	if runErr != nil {
		h.JSONError(w, http.StatusInternalServerError, "operation_failed", fmt.Sprintf("Failed to %s container %s: %v", req.Action, containerName, runErr))
		return
	}

	h.audit(r, "server.container_toggle", map[string]any{"server_id": serverID, "protocol": req.Protocol, "action": req.Action})
	h.JSON(w, http.StatusOK, map[string]any{
		"status": "ok",
		"state":  req.Action,
	})
}

// GetServerConfigHandler retrieves configuration text for a server protocol.
func (h *Handlers) GetServerConfigHandler(w http.ResponseWriter, r *http.Request) {
	serverID, err := parseServerID(r)
	if err != nil {
		h.JSONError(w, http.StatusBadRequest, "invalid_parameter", "Invalid server_id")
		return
	}

	var req models.ProtocolRequest
	if err := h.DecodeJSON(r, &req); err != nil {
		h.JSONError(w, http.StatusBadRequest, "validation_failed", "Invalid request body")
		return
	}

	req.Protocol = models.NormalizeProtocol(req.Protocol)
	if !models.IsValidProtocol(req.Protocol) {
		h.JSONError(w, http.StatusBadRequest, "invalid_protocol", "Unknown protocol")
		return
	}

	configPath, ok := models.ConfigPathForProtocol(req.Protocol)
	if !ok {
		h.JSONError(w, http.StatusBadRequest, "invalid_protocol", "Unknown protocol")
		return
	}

	ctx := r.Context()
	server, err := h.db.GetServer(ctx, serverID)
	if err != nil || server == nil {
		h.JSONError(w, http.StatusNotFound, "not_found", "Server not found")
		return
	}

	client, err := h.GetSSHClient(ctx, server)
	if err != nil {
		h.JSONError(w, http.StatusBadRequest, "connection_failed", "SSH connection failed")
		return
	}

	out, _, code, err := client.RunSudoCommand(ctx, fmt.Sprintf("cat %s 2>/dev/null", configPath))
	if err != nil || code != 0 {
		out = "# Configuration not found or empty"
	}

	h.JSON(w, http.StatusOK, map[string]any{
		"status": "ok",
		"config": out,
	})
}

// SaveServerConfigHandler overwrites configuration text for a server protocol.
func (h *Handlers) SaveServerConfigHandler(w http.ResponseWriter, r *http.Request) {
	serverID, err := parseServerID(r)
	if err != nil {
		h.JSONError(w, http.StatusBadRequest, "invalid_parameter", "Invalid server_id")
		return
	}

	var req models.ServerConfigSaveRequest
	if err := h.DecodeJSON(r, &req); err != nil {
		h.JSONError(w, http.StatusBadRequest, "validation_failed", "Invalid request body")
		return
	}

	if err := req.Validate(); err != nil {
		h.JSONError(w, http.StatusBadRequest, "validation_failed", err.Error())
		return
	}

	configPath, ok := models.ConfigPathForProtocol(req.Protocol)
	if !ok {
		h.JSONError(w, http.StatusBadRequest, "invalid_protocol", "Unknown protocol")
		return
	}

	ctx := r.Context()
	server, err := h.db.GetServer(ctx, serverID)
	if err != nil || server == nil {
		h.JSONError(w, http.StatusNotFound, "not_found", "Server not found")
		return
	}

	client, err := h.GetSSHClient(ctx, server)
	if err != nil {
		h.JSONError(w, http.StatusBadRequest, "connection_failed", "SSH connection failed")
		return
	}

	if err := client.UploadSudoFile(ctx, configPath, []byte(req.Config), 0600); err != nil {
		h.JSONError(w, http.StatusInternalServerError, "save_failed", "Failed to save config")
		return
	}

	h.audit(r, "server.config_save", map[string]any{"server_id": serverID, "protocol": req.Protocol})
	h.JSONOK(w)
}

// GetServerReachabilityHandler returns server connectivity and latency status.
func (h *Handlers) GetServerReachabilityHandler(w http.ResponseWriter, r *http.Request) {
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

	status, _ := h.db.GetServerStatus(ctx, serverID)
	reachable := status == models.ReachabilityOnline || status == models.ReachabilityUnknown

	// Measure real TCP latency to the server (SSH port) instead of reporting a constant.
	latencyMS := 0
	if reachable {
		start := time.Now()
		// #nosec G704 -- connecting to managed server host for latency probe
		conn, err := net.DialTimeout("tcp", net.JoinHostPort(server.Host, strconv.Itoa(server.SSHPort)), 3*time.Second)
		if err == nil {
			_ = conn.Close()
			latencyMS = int(time.Since(start).Milliseconds())
		} else {
			// Probe target unreachable: downgrade unknown to offline.
			if status == models.ReachabilityUnknown {
				reachable = false
			}
			latencyMS = 0
		}
	}

	h.JSON(w, http.StatusOK, map[string]any{
		"status":       "ok",
		"reachable":    reachable,
		"reachability": string(status),
		"latency_ms":   latencyMS,
	})
}

// SetClientSpeedLimitHandler updates bandwidth rate limits for an individual AWG client.
func (h *Handlers) SetClientSpeedLimitHandler(w http.ResponseWriter, r *http.Request) {
	serverID, err := parseServerID(r)
	if err != nil {
		h.JSONError(w, http.StatusBadRequest, "invalid_parameter", "Invalid server_id")
		return
	}

	var req models.SpeedLimitRequest
	if err := h.DecodeJSON(r, &req); err != nil {
		h.JSONError(w, http.StatusBadRequest, "validation_failed", "Invalid request body")
		return
	}

	if req.ClientID == "" {
		h.JSONError(w, http.StatusBadRequest, "validation_failed", "client_id is required")
		return
	}

	ctx := r.Context()
	server, err := h.db.GetServer(ctx, serverID)
	if err != nil || server == nil {
		h.JSONError(w, http.StatusNotFound, "not_found", "Server not found")
		return
	}

	if h.awgMgr != nil {
		if err := h.awgMgr.EditClient(ctx, server, req.ClientID, map[string]any{
			"speed_limit_down": req.SpeedLimitDown,
			"speed_limit_up":   req.SpeedLimitUp,
		}); err != nil {
			h.JSONError(w, http.StatusInternalServerError, "operation_failed", "Failed to set client speed limit: "+err.Error())
			return
		}
	}

	h.audit(r, "server.speed_limit_set", map[string]any{"server_id": serverID, "client_id": req.ClientID, "down": req.SpeedLimitDown, "up": req.SpeedLimitUp})
	h.JSONOK(w)
}

// GetAWGSpeedLimitConfigHandler returns global and default AWG bandwidth caps.
func (h *Handlers) GetAWGSpeedLimitConfigHandler(w http.ResponseWriter, r *http.Request) {
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

	if !isProtocolInstalled(server, "awg") {
		h.JSONError(w, http.StatusBadRequest, "protocol_not_installed", "AWG protocol is not installed on this server")
		return
	}

	var globalDown, globalUp, defaultDown, defaultUp *int
	if awgData, ok := server.Protocols["awg"].(map[string]any); ok {
		if cfgMap, ok := awgData["awg_speed_limit_config"].(map[string]any); ok {
			if v, ok := cfgMap["global_speed_limit_down"].(float64); ok && v > 0 {
				val := int(v)
				globalDown = &val
			}
			if v, ok := cfgMap["global_speed_limit_up"].(float64); ok && v > 0 {
				val := int(v)
				globalUp = &val
			}
			if v, ok := cfgMap["default_speed_limit_down"].(float64); ok && v > 0 {
				val := int(v)
				defaultDown = &val
			}
			if v, ok := cfgMap["default_speed_limit_up"].(float64); ok && v > 0 {
				val := int(v)
				defaultUp = &val
			}
		}
	}

	h.JSON(w, http.StatusOK, map[string]any{
		"global_speed_limit_down":  globalDown,
		"global_speed_limit_up":    globalUp,
		"default_speed_limit_down": defaultDown,
		"default_speed_limit_up":   defaultUp,
	})
}

// SetAWGSpeedLimitConfigHandler modifies global or default bandwidth caps for AWG.
func (h *Handlers) SetAWGSpeedLimitConfigHandler(w http.ResponseWriter, r *http.Request) {
	serverID, err := parseServerID(r)
	if err != nil {
		h.JSONError(w, http.StatusBadRequest, "invalid_parameter", "Invalid server_id")
		return
	}

	var req models.AwgSpeedLimitConfigRequest
	if err := h.DecodeJSON(r, &req); err != nil {
		h.JSONError(w, http.StatusBadRequest, "validation_failed", "Invalid request body")
		return
	}

	ctx := r.Context()
	server, err := h.db.GetServer(ctx, serverID)
	if err != nil || server == nil {
		h.JSONError(w, http.StatusNotFound, "not_found", "Server not found")
		return
	}

	if !isProtocolInstalled(server, "awg") {
		h.JSONError(w, http.StatusBadRequest, "protocol_not_installed", "AWG protocol is not installed on this server")
		return
	}

	awgData, ok := server.Protocols["awg"].(map[string]any)
	if !ok {
		awgData = make(map[string]any)
	}
	cfgMap, ok := awgData["awg_speed_limit_config"].(map[string]any)
	if !ok {
		cfgMap = make(map[string]any)
	}

	if req.GlobalSpeedLimitDown != nil {
		cfgMap["global_speed_limit_down"] = float64(*req.GlobalSpeedLimitDown)
	}
	if req.GlobalSpeedLimitUp != nil {
		cfgMap["global_speed_limit_up"] = float64(*req.GlobalSpeedLimitUp)
	}
	if req.DefaultSpeedLimitDown != nil {
		cfgMap["default_speed_limit_down"] = float64(*req.DefaultSpeedLimitDown)
	}
	if req.DefaultSpeedLimitUp != nil {
		cfgMap["default_speed_limit_up"] = float64(*req.DefaultSpeedLimitUp)
	}
	awgData["awg_speed_limit_config"] = cfgMap
	server.Protocols["awg"] = awgData
	if err := h.db.UpdateServerProtocols(ctx, serverID, server.Protocols); err != nil {
		h.JSONError(w, http.StatusInternalServerError, "database_error", "Failed to update speed limit config: "+err.Error())
		return
	}

	client, err := h.GetSSHClient(ctx, server)
	if err == nil && client != nil {
		if err := tc.SetGlobalLimit(ctx, client, "amnezia-awg", req.GlobalSpeedLimitDown, req.GlobalSpeedLimitUp); err != nil {
			h.JSONError(w, http.StatusInternalServerError, "operation_failed", "Failed to apply global speed limit on server: "+err.Error())
			return
		}
	}

	h.audit(r, "server.awg_speed_limit_config", map[string]any{"server_id": serverID})
	h.JSONOK(w)
}

// ApplyDefaultSpeedLimitsHandler applies default rate limits across all existing AWG peers.
func (h *Handlers) ApplyDefaultSpeedLimitsHandler(w http.ResponseWriter, r *http.Request) {
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

	if !isProtocolInstalled(server, "awg") {
		h.JSONError(w, http.StatusBadRequest, "protocol_not_installed", "AWG protocol is not installed on this server")
		return
	}

	count := 0
	if h.awgMgr != nil {
		// Read the configured default limits from the server's AWG protocol entry.
		var defaultDown, defaultUp *int
		if awgData, ok := server.Protocols["awg"].(map[string]any); ok {
			if cfg, ok := awgData["awg_speed_limit_config"].(map[string]any); ok {
				if d, ok := cfg["default_speed_limit_down"].(float64); ok {
					v := int(d)
					defaultDown = &v
				}
				if u, ok := cfg["default_speed_limit_up"].(float64); ok {
					v := int(u)
					defaultUp = &v
				}
			}
			if defaultDown == nil {
				if d, ok := awgData["default_speed_limit_down"].(float64); ok {
					v := int(d)
					defaultDown = &v
				} else if d, ok := awgData["speed_limit_down"].(float64); ok {
					v := int(d)
					defaultDown = &v
				}
			}
			if defaultUp == nil {
				if u, ok := awgData["default_speed_limit_up"].(float64); ok {
					v := int(u)
					defaultUp = &v
				} else if u, ok := awgData["speed_limit_up"].(float64); ok {
					v := int(u)
					defaultUp = &v
				}
			}
		}
		if defaultDown == nil && defaultUp == nil {
			h.JSONError(w, http.StatusBadRequest, "no_default_limits", "No default speed limits configured")
			return
		}

		clients, err := h.awgMgr.GetClients(ctx, server)
		if err != nil {
			h.JSONError(w, http.StatusInternalServerError, "internal_error", "Failed to list AWG clients")
			return
		}

		for _, c := range clients {
			clientID, _ := c["clientId"].(string)
			if clientID == "" {
				clientID, _ = c["client_id"].(string)
			}
			if clientID == "" {
				continue
			}
			if err := h.awgMgr.EditClient(ctx, server, clientID, map[string]any{
				"speed_limit_down": defaultDown,
				"speed_limit_up":   defaultUp,
			}); err != nil {
				continue
			}
			count++
		}
	}

	h.audit(r, "server.awg_apply_default_speed_limits", map[string]any{"server_id": serverID, "updated": count})
	h.JSON(w, http.StatusOK, map[string]any{
		"status":  "ok",
		"updated": count,
	})
}

func parseServerID(r *http.Request) (int64, error) {
	idStr := chi.URLParam(r, "server_id")
	if idStr == "" {
		return 0, fmt.Errorf("missing server_id in URL")
	}
	return strconv.ParseInt(idStr, 10, 64)
}

func parseCombinedStats(raw string) models.ServerStatsResponse {
	var resp models.ServerStatsResponse
	sections := make(map[string]string)

	pattern := regexp.MustCompile(`===(CPU|RAM|DISK|NET|UPTIME)===`)
	matches := pattern.FindAllStringIndex(raw, -1)

	for i, m := range matches {
		name := raw[m[0]+3 : m[1]-3]
		start := m[1]
		end := len(raw)
		if i+1 < len(matches) {
			end = matches[i+1][0]
		}
		sections[name] = strings.TrimSpace(raw[start:end])
	}

	if cpuStr, ok := sections["CPU"]; ok {
		if val, err := strconv.ParseFloat(strings.TrimSpace(cpuStr), 64); err == nil {
			resp.CPU = val
		}
	}

	if ramStr, ok := sections["RAM"]; ok {
		parts := strings.Fields(ramStr)
		if len(parts) >= 2 {
			used, _ := strconv.ParseInt(parts[0], 10, 64)
			total, _ := strconv.ParseInt(parts[1], 10, 64)
			resp.RAMUsed = used
			resp.RAMTotal = total
			if total > 0 {
				resp.RAMPercent = float64(used) / float64(total) * 100.0
			}
		}
	}

	if diskStr, ok := sections["DISK"]; ok {
		parts := strings.Fields(diskStr)
		if len(parts) >= 2 {
			used, _ := strconv.ParseInt(parts[0], 10, 64)
			total, _ := strconv.ParseInt(parts[1], 10, 64)
			resp.DiskUsed = used
			resp.DiskTotal = total
			if total > 0 {
				resp.DiskPercent = float64(used) / float64(total) * 100.0
			}
		}
	}

	if netStr, ok := sections["NET"]; ok {
		parts := strings.Fields(netStr)
		if len(parts) >= 2 {
			rx, _ := strconv.ParseInt(parts[0], 10, 64)
			tx, _ := strconv.ParseInt(parts[1], 10, 64)
			resp.NetRx = rx
			resp.NetTx = tx
		}
	}

	if uptimeStr, ok := sections["UPTIME"]; ok {
		resp.Uptime = uptimeStr
	}

	return resp
}

// ListServersHandler returns all configured servers (with sensitive credentials stripped).
func (h *Handlers) ListServersHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	servers, err := h.db.GetAllServers(ctx)
	if err != nil {
		h.JSONError(w, http.StatusInternalServerError, "internal_error", "Failed to retrieve servers")
		return
	}

	sess := h.GetSession(r)
	isAdminOrSupport := sess != nil && (sess.Role == models.RoleAdmin || sess.Role == models.RoleSupport)

	result := make([]models.ServerItemResponse, 0, len(servers))
	for _, s := range servers {
		if !isAdminOrSupport {
			name := s.Name
			if strings.TrimSpace(name) == "" {
				name = fmt.Sprintf("Server #%d", s.ID)
			}
			sanitizedProtocols := make(map[string]any)
			for proto, pVal := range s.Protocols {
				installed := false
				if m, ok := pVal.(map[string]any); ok {
					if inst, ok := m["installed"].(bool); ok {
						installed = inst
					}
				} else if b, ok := pVal.(bool); ok {
					installed = b
				}
				sanitizedProtocols[proto] = map[string]bool{"installed": installed}
			}

			status := string(s.Status)
			if status == "" {
				status = "online"
			}
			reachable := s.Status == models.ReachabilityOnline || s.Status == ""

			result = append(result, models.ServerItemResponse{
				ID:        s.ID,
				Name:      name,
				Host:      "",
				SSHPort:   0,
				Username:  "",
				Protocols: sanitizedProtocols,
				Status:    status,
				Reachable: &reachable,
			})
		} else {
			protoMap := s.Protocols
			if protoMap == nil {
				protoMap = make(map[string]any)
			}
			status := string(s.Status)
			if status == "" {
				status = "unknown"
			}
			var createdAt *time.Time
			if !s.CreatedAt.IsZero() {
				createdAt = &s.CreatedAt
			}
			result = append(result, models.ServerItemResponse{
				ID:        s.ID,
				Name:      s.Name,
				Host:      s.Host,
				SSHPort:   s.SSHPort,
				Username:  s.SSHUser,
				Protocols: protoMap,
				CreatedAt: createdAt,
				Status:    status,
			})
		}
	}

	h.JSON(w, http.StatusOK, result)
}
