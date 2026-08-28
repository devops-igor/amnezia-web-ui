package mtproxyl

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/devops-igor/amnezia-web-ui-go/internal/manager/ssh"
	"github.com/devops-igor/amnezia-web-ui-go/internal/models"
)

const (
	DefaultCLIPath = "/usr/local/bin/mtproxyl"
	// #nosec G101
	DefaultSecretsPath  = "/opt/mtproxyl/secrets.conf"
	DefaultSettingsPath = "/opt/mtproxyl/settings.conf"
)

var (
	usernameSanitizeRegex = regexp.MustCompile(`[^a-zA-Z0-9_-]`)
	tgLinkRegex           = regexp.MustCompile(`tg://\S+`)
)

// SSHProvider abstracts obtaining an SSHClient for a server.
type SSHProvider interface {
	Get(ctx context.Context, server *models.Server) (ssh.SSHClient, error)
}

// MTProxyLManager implements manager.ProtocolManager for MTProxyL (telemt).
//
//nolint:revive
type MTProxyLManager struct {
	sshPool     SSHProvider
	secretsFile *SecretsFile
	mu          sync.Mutex
}

// NewMTProxyLManager creates a new MTProxyLManager instance.
func NewMTProxyLManager(pool SSHProvider) *MTProxyLManager {
	return &MTProxyLManager{
		sshPool:     pool,
		secretsFile: NewSecretsFile(),
	}
}

func (m *MTProxyLManager) Protocol() string {
	return "telemt"
}

func (m *MTProxyLManager) getSSHClient(ctx context.Context, server *models.Server) (ssh.SSHClient, error) {
	if server == nil {
		return nil, errors.New("server cannot be nil")
	}
	if m.sshPool == nil {
		return nil, errors.New("ssh pool is not configured")
	}
	return m.sshPool.Get(ctx, server)
}

// Install installs MTProxyL CLI if missing, sets port, FakeTLS domain, and starts the service.
func (m *MTProxyLManager) Install(ctx context.Context, server *models.Server, params map[string]any) error {
	client, err := m.getSSHClient(ctx, server)
	if err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	port := "443"
	if p, ok := params["port"]; ok && fmt.Sprint(p) != "" {
		port = fmt.Sprint(p)
	}

	// 1. Check if MTProxyL binary exists
	out, _, _, _ := client.RunCommand(ctx, fmt.Sprintf("test -f %s && echo found || echo not_found", DefaultCLIPath))
	if !strings.Contains(out, "found") {
		installScript := "wget -qO /tmp/mtproxyl-install.sh https://raw.githubusercontent.com/Liafanx/MTProxyL/main/install.sh && bash /tmp/mtproxyl-install.sh"
		if _, errOut, code, err := client.RunSudoCommand(ctx, installScript); err != nil || code != 0 {
			return fmt.Errorf("failed to install MTProxyL (code %d): %s, %w", code, errOut, err)
		}
	}

	// 2. Check for BunkerWeb conflict on port 443
	if port == "443" {
		bwOut, _, _, _ := client.RunCommand(ctx, "docker ps --filter name=^bunkerweb$ --format '{{.Names}}'")
		if strings.Contains(bwOut, "bunkerweb") {
			port = "18443"
		}
	}

	// 3. Configure port
	if _, errOut, code, err := client.RunCommand(ctx, fmt.Sprintf("%s port %s", DefaultCLIPath, port)); err != nil || code != 0 {
		return fmt.Errorf("failed to set port %s (code %d): %s, %w", port, code, errOut, err)
	}

	// 4. Configure FakeTLS domain if present
	if domain, ok := params["tls_domain"].(string); ok && domain != "" {
		_, _, _, _ = client.RunCommand(ctx, fmt.Sprintf("%s domain %s", DefaultCLIPath, domain))
	} else if tlsDomain, ok := params["tlsDomain"].(string); ok && tlsDomain != "" {
		_, _, _, _ = client.RunCommand(ctx, fmt.Sprintf("%s domain %s", DefaultCLIPath, tlsDomain))
	}

	// 5. Start proxy
	_, _, _, _ = client.RunCommand(ctx, fmt.Sprintf("%s start", DefaultCLIPath))
	return nil
}

// Uninstall stops the MTProxyL service.
func (m *MTProxyLManager) Uninstall(ctx context.Context, server *models.Server) error {
	client, err := m.getSSHClient(ctx, server)
	if err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	_, _, _, _ = client.RunCommand(ctx, fmt.Sprintf("%s stop", DefaultCLIPath))
	return nil
}

// GetClients returns all registered clients from secrets.conf enriched with live traffic and connections.
func (m *MTProxyLManager) GetClients(ctx context.Context, server *models.Server) ([]map[string]any, error) {
	client, err := m.getSSHClient(ctx, server)
	if err != nil {
		return nil, err
	}

	out, _, code, err := client.RunCommand(ctx, fmt.Sprintf("cat %s 2>/dev/null", DefaultSecretsPath))
	if err != nil || code != 0 || strings.TrimSpace(out) == "" {
		return []map[string]any{}, nil
	}

	entries, err := m.secretsFile.Parse(out)
	if err != nil {
		return nil, err
	}

	trafficOut, _, _, _ := client.RunCommand(ctx, fmt.Sprintf("%s traffic 2>/dev/null", DefaultCLIPath))
	trafficStats, _ := ParseTraffic(trafficOut)

	connOut, _, _, _ := client.RunCommand(ctx, fmt.Sprintf("%s connections 2>/dev/null", DefaultCLIPath))
	conns, _ := ParseConnections(connOut)

	var results []map[string]any
	for _, e := range entries {
		var totalOctets int64
		var currentConns int

		if stat, ok := trafficStats[e.Label]; ok {
			totalOctets = stat.TotalBytes
		}
		if c, ok := conns[e.Label]; ok {
			currentConns = c
		}

		var quotaAny any
		if e.QuotaBytes > 0 {
			quotaAny = e.QuotaBytes
		}

		var expiryAny any
		if e.Expires != "" && e.Expires != "0" {
			expiryAny = e.Expires
		}

		var activeIPsAny any
		if e.MaxIPs > 0 {
			activeIPsAny = e.MaxIPs
		}

		userData := map[string]any{
			"clientName":          e.Label,
			"token":               e.Secret,
			"tg_link":             "",
			"total_octets":        totalOctets,
			"current_connections": currentConns,
			"active_ips":          activeIPsAny,
			"quota":               quotaAny,
			"expiry":              expiryAny,
		}

		results = append(results, map[string]any{
			"clientId":     e.Label,
			"clientName":   e.Label,
			"enabled":      e.Enabled,
			"creationDate": e.CreatedTS,
			"userData":     userData,
		})
	}

	return results, nil
}

// AddClient adds a new MTProxy secret and returns the tg:// connection link.
func (m *MTProxyLManager) AddClient(ctx context.Context, server *models.Server, clientParams map[string]any) (map[string]any, error) {
	client, err := m.getSSHClient(ctx, server)
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	rawName := "user"
	if n, ok := clientParams["name"]; ok && fmt.Sprint(n) != "" {
		rawName = fmt.Sprint(n)
	}

	// Sanitize username: [a-zA-Z0-9_-], max 32 chars
	username := usernameSanitizeRegex.ReplaceAllString(strings.ReplaceAll(rawName, " ", "_"), "")
	if username == "" {
		username = "user"
	}
	if len(username) > 32 {
		username = username[:32]
	}

	// Run secret add
	out, errOut, code, err := client.RunCommand(ctx, fmt.Sprintf("%s secret add %s", DefaultCLIPath, username))
	if err != nil || code != 0 {
		return nil, fmt.Errorf("failed to add secret (code %d): %s, %w", code, errOut, err)
	}

	// Format and apply limits if given
	var maxConns, maxIPs int
	var quotaBytes int64
	expires := "0"

	if v, ok := clientParams["telemt_quota"]; ok && v != nil {
		if q, err := strconv.ParseInt(fmt.Sprint(v), 10, 64); err == nil && q > 0 {
			quotaBytes = q
		}
	}
	if v, ok := clientParams["telemt_max_ips"]; ok && v != nil {
		if ips, err := strconv.Atoi(fmt.Sprint(v)); err == nil && ips > 0 {
			maxIPs = ips
		}
	}
	if v, ok := clientParams["telemt_expiry"]; ok && fmt.Sprint(v) != "" {
		expires = fmt.Sprint(v)
	}

	if maxIPs > 0 || quotaBytes > 0 || expires != "0" {
		limitCmd := fmt.Sprintf("%s secret setlimits %s %d %d %d %s", DefaultCLIPath, username, maxConns, maxIPs, quotaBytes, expires)
		_, _, _, _ = client.RunCommand(ctx, limitCmd)
	}

	// Extract tg:// link
	link := ""
	if match := tgLinkRegex.FindString(out); match != "" {
		link = match
	} else {
		linkOut, _, _, _ := client.RunCommand(ctx, fmt.Sprintf("%s secret link %s", DefaultCLIPath, username))
		link = tgLinkRegex.FindString(linkOut)
	}

	return map[string]any{
		"client_id": username,
		"config":    link,
		"vpn_link":  link,
	}, nil
}

// RemoveClient removes an MTProxy secret.
func (m *MTProxyLManager) RemoveClient(ctx context.Context, server *models.Server, clientID string) error {
	client, err := m.getSSHClient(ctx, server)
	if err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	_, errOut, code, err := client.RunCommand(ctx, fmt.Sprintf("%s secret remove %s", DefaultCLIPath, clientID))
	if err != nil || code != 0 {
		return fmt.Errorf("failed to remove secret %s (code %d): %s, %w", clientID, code, errOut, err)
	}
	return nil
}

// GetClientConfig returns the tg:// connection link for a secret.
func (m *MTProxyLManager) GetClientConfig(ctx context.Context, server *models.Server, clientID string) (string, error) {
	client, err := m.getSSHClient(ctx, server)
	if err != nil {
		return "", err
	}

	out, errOut, code, err := client.RunCommand(ctx, fmt.Sprintf("%s secret link %s", DefaultCLIPath, clientID))
	if err != nil || code != 0 {
		return "", fmt.Errorf("failed to get client config (code %d): %s, %w", code, errOut, err)
	}

	link := tgLinkRegex.FindString(out)
	return link, nil
}

// ToggleClient enables or disables a secret.
func (m *MTProxyLManager) ToggleClient(ctx context.Context, server *models.Server, clientID string, enable bool) error {
	client, err := m.getSSHClient(ctx, server)
	if err != nil {
		return err
	}

	action := "disable"
	if enable {
		action = "enable"
	}

	_, errOut, code, err := client.RunCommand(ctx, fmt.Sprintf("%s secret %s %s", DefaultCLIPath, action, clientID))
	if err != nil || code != 0 {
		return fmt.Errorf("failed to %s secret %s (code %d): %s, %w", action, clientID, code, errOut, err)
	}
	return nil
}

// EditClient updates limits on an existing secret.
func (m *MTProxyLManager) EditClient(ctx context.Context, server *models.Server, clientID string, params map[string]any) error {
	client, err := m.getSSHClient(ctx, server)
	if err != nil {
		return err
	}

	var maxConns, maxIPs int
	var quotaBytes int64
	expires := "0"

	if v, ok := params["telemt_quota"]; ok && v != nil {
		if q, err := strconv.ParseInt(fmt.Sprint(v), 10, 64); err == nil {
			quotaBytes = q
		}
	}
	if v, ok := params["telemt_max_ips"]; ok && v != nil {
		if ips, err := strconv.Atoi(fmt.Sprint(v)); err == nil {
			maxIPs = ips
		}
	}
	if v, ok := params["telemt_expiry"]; ok && fmt.Sprint(v) != "" {
		expires = fmt.Sprint(v)
	}

	limitCmd := fmt.Sprintf("%s secret setlimits %s %d %d %d %s", DefaultCLIPath, clientID, maxConns, maxIPs, quotaBytes, expires)
	_, errOut, code, err := client.RunCommand(ctx, limitCmd)
	if err != nil || code != 0 {
		return fmt.Errorf("failed to update limits for %s (code %d): %s, %w", clientID, code, errOut, err)
	}
	return nil
}

// GetServerStatus queries `mtproxyl status --json` and secrets count.
func (m *MTProxyLManager) GetServerStatus(ctx context.Context, server *models.Server) (map[string]any, error) {
	client, err := m.getSSHClient(ctx, server)
	if err != nil {
		return nil, err
	}

	out, _, code, err := client.RunCommand(ctx, fmt.Sprintf("%s status --json 2>/dev/null", DefaultCLIPath))
	if err != nil || code != 0 || strings.TrimSpace(out) == "" {
		return map[string]any{
			"container_exists":  false,
			"container_running": false,
		}, nil
	}

	var statusData map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &statusData); err != nil {
		return map[string]any{
			"container_exists":  false,
			"container_running": false,
		}, nil
	}

	isRunning := statusData["status"] == "running"
	result := map[string]any{
		"container_exists":  true,
		"container_running": isRunning,
	}

	if isRunning {
		result["port"] = fmt.Sprint(statusData["port"])
		domain, _ := statusData["domain"].(string)
		result["awg_params"] = map[string]any{
			"tls_emulation":   domain != "",
			"tls_domain":      domain,
			"max_connections": 0,
		}

		if secOut, _, _, _ := client.RunCommand(ctx, fmt.Sprintf("cat %s 2>/dev/null", DefaultSecretsPath)); secOut != "" {
			if entries, err := m.secretsFile.Parse(secOut); err == nil {
				result["clients_count"] = len(entries)
			}
		}
	}

	return result, nil
}
