package dns

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/devops-igor/amnezia-web-ui-go/internal/manager/ssh"
	"github.com/devops-igor/amnezia-web-ui-go/internal/models"
)

const (
	DNSContainerName = "amnezia-dns"
	DNSNetworkName   = "amnezia-dns-net"
	DNSStaticIP      = "172.29.172.254"
	DNSConfigDir     = "/opt/amnezia/dns"
)

// SSHProvider abstracts obtaining an SSHClient for a server.
type SSHProvider interface {
	Get(ctx context.Context, server *models.Server) (ssh.SSHClient, error)
}

// DNSManager implements manager.ProtocolManager for AmneziaDNS (Unbound).
//
//nolint:revive
type DNSManager struct {
	sshPool SSHProvider
	mu      sync.Mutex
}

// NewDNSManager creates a new DNSManager instance.
func NewDNSManager(pool SSHProvider) *DNSManager {
	return &DNSManager{
		sshPool: pool,
	}
}

func (m *DNSManager) Protocol() string {
	return "dns"
}

func (m *DNSManager) getSSHClient(ctx context.Context, server *models.Server) (ssh.SSHClient, error) {
	if server == nil {
		return nil, errors.New("server cannot be nil")
	}
	if m.sshPool == nil {
		return nil, errors.New("ssh pool is not configured")
	}
	return m.sshPool.Get(ctx, server)
}

// Install prepares the host, renders Unbound DoT configuration, builds and starts amnezia-dns container.
func (m *DNSManager) Install(ctx context.Context, server *models.Server, params map[string]any) error {
	client, err := m.getSSHClient(ctx, server)
	if err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// 1. Check Docker
	out, _, code, _ := client.RunCommand(ctx, "docker --version")
	if code != 0 || !strings.Contains(strings.ToLower(out), "docker") {
		return errors.New("docker is not installed on the remote server")
	}

	// 2. Prepare directory
	if _, _, code, err := client.RunSudoCommand(ctx, fmt.Sprintf("mkdir -p %s", DNSConfigDir)); err != nil || code != 0 {
		return fmt.Errorf("failed to create DNS directory: %w", err)
	}

	// 3. Render and upload forward-records.conf and Dockerfile
	dns1, dns2 := DefaultDNS1, DefaultDNS2
	if d1, ok := params["dns1"].(string); ok && d1 != "" {
		dns1 = d1
	}
	if d2, ok := params["dns2"].(string); ok && d2 != "" {
		dns2 = d2
	}

	forwardRecords := RenderForwardRecords(dns1, dns2)
	if err := client.UploadSudoFile(ctx, fmt.Sprintf("%s/forward-records.conf", DNSConfigDir), []byte(forwardRecords), 0644); err != nil {
		return fmt.Errorf("failed to upload forward-records.conf: %w", err)
	}

	dockerfile := RenderDockerfile()
	if err := client.UploadSudoFile(ctx, fmt.Sprintf("%s/Dockerfile", DNSConfigDir), []byte(dockerfile), 0644); err != nil {
		return fmt.Errorf("failed to upload Dockerfile: %w", err)
	}

	// 4. Build Docker image
	if _, errOut, code, err := client.RunSudoCommand(ctx, fmt.Sprintf("docker build -t %s %s", DNSContainerName, DNSConfigDir)); err != nil || code != 0 {
		return fmt.Errorf("failed to build %s image (code %d): %s, %w", DNSContainerName, code, errOut, err)
	}

	// 5. Remove existing container
	_, _, _, _ = client.RunSudoCommand(ctx, fmt.Sprintf("docker stop %s 2>/dev/null || true", DNSContainerName))
	_, _, _, _ = client.RunSudoCommand(ctx, fmt.Sprintf("docker rm -fv %s 2>/dev/null || true", DNSContainerName))

	// 6. Ensure internal network exists
	netScript := fmt.Sprintf("docker network ls | grep -q %s || docker network create --subnet 172.29.172.0/24 %s", DNSNetworkName, DNSNetworkName)
	_, _, _, _ = client.RunSudoCommand(ctx, netScript)

	// 7. Run container
	runCmd := fmt.Sprintf("docker run -d --name %s --restart always --network %s --ip=%s %s",
		DNSContainerName, DNSNetworkName, DNSStaticIP, DNSContainerName)
	if _, errOut, code, err := client.RunSudoCommand(ctx, runCmd); err != nil || code != 0 {
		return fmt.Errorf("failed to run %s container (code %d): %s, %w", DNSContainerName, code, errOut, err)
	}

	// 8. Connect existing VPN containers to DNS network
	vpnContainers := []string{"amnezia-awg", "telemt"}
	for _, c := range vpnContainers {
		connectCmd := fmt.Sprintf("docker ps | grep -q %s && docker network connect %s %s || true", c, DNSNetworkName, c)
		_, _, _, _ = client.RunSudoCommand(ctx, connectCmd)
	}

	return nil
}

// Uninstall stops and removes the DNS container and config folder.
func (m *DNSManager) Uninstall(ctx context.Context, server *models.Server) error {
	client, err := m.getSSHClient(ctx, server)
	if err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	_, _, _, _ = client.RunSudoCommand(ctx, fmt.Sprintf("docker stop %s 2>/dev/null || true", DNSContainerName))
	_, _, _, _ = client.RunSudoCommand(ctx, fmt.Sprintf("docker rm -fv %s 2>/dev/null || true", DNSContainerName))
	_, _, _, _ = client.RunSudoCommand(ctx, fmt.Sprintf("rm -rf %s", DNSConfigDir))
	return nil
}

// GetClients returns an empty slice (DNS operates as a shared service).
func (m *DNSManager) GetClients(ctx context.Context, server *models.Server) ([]map[string]any, error) {
	return []map[string]any{}, nil
}

// AddClient returns the static DNS IP for clients.
func (m *DNSManager) AddClient(ctx context.Context, server *models.Server, clientParams map[string]any) (map[string]any, error) {
	return map[string]any{
		"dns_ip": DNSStaticIP,
	}, nil
}

// RemoveClient is a no-op for DNS.
func (m *DNSManager) RemoveClient(ctx context.Context, server *models.Server, clientID string) error {
	return nil
}

// GetClientConfig returns the DNS server static IP.
func (m *DNSManager) GetClientConfig(ctx context.Context, server *models.Server, clientID string) (string, error) {
	return DNSStaticIP, nil
}

// GetServerStatus queries the amnezia-dns container state.
func (m *DNSManager) GetServerStatus(ctx context.Context, server *models.Server) (map[string]any, error) {
	client, err := m.getSSHClient(ctx, server)
	if err != nil {
		return nil, err
	}

	outAll, errOutAll, codeAll, errAll := client.RunSudoCommand(ctx, fmt.Sprintf("docker ps -a --filter name=^%s$ --format '{{.Names}}'", DNSContainerName))
	if errAll != nil || codeAll != 0 {
		return nil, fmt.Errorf("docker ps -a failed checking %s (code %d): %s, %w", DNSContainerName, codeAll, errOutAll, errAll)
	}

	var exists bool
	for _, line := range strings.Split(strings.TrimSpace(outAll), "\n") {
		if strings.TrimSpace(line) == DNSContainerName {
			exists = true
			break
		}
	}

	var running bool
	if exists {
		outRun, errOutRun, codeRun, errRun := client.RunSudoCommand(ctx, fmt.Sprintf("docker ps --filter name=^%s$ --format '{{.Status}}'", DNSContainerName))
		if errRun != nil || codeRun != 0 {
			return nil, fmt.Errorf("docker ps failed checking %s (code %d): %s, %w", DNSContainerName, codeRun, errOutRun, errRun)
		}
		running = strings.Contains(outRun, "Up")
	}

	return map[string]any{
		"protocol":          "dns",
		"port":              "53",
		"container_exists":  exists,
		"container_running": running,
		"dns_ip":            DNSStaticIP,
	}, nil
}
