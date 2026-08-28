package awg

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/devops-igor/amnezia-web-ui-go/internal/manager/awg/cps"
	"github.com/devops-igor/amnezia-web-ui-go/internal/manager/awg/tc"
	"github.com/devops-igor/amnezia-web-ui-go/internal/manager/ssh"
	"github.com/devops-igor/amnezia-web-ui-go/internal/models"
)

var (
	AWGContainerNames = []string{"amnezia-awg", "amnezia-awg2", "amnezia-awg-legacy"}
)

// SSHProvider abstracts obtaining an SSHClient for a server.
type SSHProvider interface {
	Get(ctx context.Context, server *models.Server) (ssh.SSHClient, error)
}

// AWGManager implements manager.ProtocolManager for AmneziaWG.
//
//nolint:revive
type AWGManager struct {
	sshPool SSHProvider
	mu      sync.Mutex
}

// NewAWGManager creates a new AWGManager instance.
func NewAWGManager(pool SSHProvider) *AWGManager {
	return &AWGManager{
		sshPool: pool,
	}
}

func (m *AWGManager) Protocol() string {
	return "awg"
}

func (m *AWGManager) getSSHClient(ctx context.Context, server *models.Server) (ssh.SSHClient, error) {
	if server == nil {
		return nil, errors.New("server cannot be nil")
	}
	if m.sshPool == nil {
		return nil, errors.New("ssh pool is not configured")
	}
	return m.sshPool.Get(ctx, server)
}

func (m *AWGManager) containerName() string {
	return "amnezia-awg"
}

func (m *AWGManager) configPath() string {
	return "/opt/amnezia/awg/awg0.conf"
}

func (m *AWGManager) clientsTablePath() string {
	return "/opt/amnezia/awg/clientsTable"
}

func (m *AWGManager) interfaceName() string {
	return "awg0"
}

func (m *AWGManager) wgBinary() string {
	return "awg"
}

func ensureDockerInstalled(ctx context.Context, client ssh.SSHClient) error {
	out, _, code, _ := client.RunCommand(ctx, "docker --version")
	if code == 0 && strings.Contains(strings.ToLower(out), "docker") {
		return nil
	}
	dockerScript := `
if which apt-get > /dev/null 2>&1; then pm=$(which apt-get); silent_inst="-yq install"; check_pkgs="-yq update"; docker_pkg="docker.io"; dist="debian";
elif which dnf > /dev/null 2>&1; then pm=$(which dnf); silent_inst="-yq install"; check_pkgs="-yq check-update"; docker_pkg="docker"; dist="fedora";
elif which yum > /dev/null 2>&1; then pm=$(which yum); silent_inst="-y -q install"; check_pkgs="-y -q check-update"; docker_pkg="docker"; dist="centos";
else echo "Packet manager not found"; exit 1; fi;
if [ "$dist" = "debian" ]; then export DEBIAN_FRONTEND=noninteractive; fi;
if ! command -v docker > /dev/null 2>&1; then $pm $check_pkgs && $pm $silent_inst $docker_pkg && systemctl enable --now docker; fi;
systemctl start docker; docker --version
`
	if _, errOut, dCode, err := client.RunSudoScript(ctx, dockerScript); err != nil || dCode != 0 {
		return fmt.Errorf("failed to install Docker (code %d): %s, %w", dCode, errOut, err)
	}
	return nil
}

func prepareHostAndContainers(ctx context.Context, client ssh.SSHClient) error {
	prepScript := `
mkdir -p /opt/amnezia/amnezia-awg /opt/amnezia/awg
if ! docker network ls | grep -q amnezia-dns-net; then
  docker network create --driver bridge --subnet=172.29.172.0/24 --opt com.docker.network.bridge.name=amn0 amnezia-dns-net || true
fi
`
	if _, _, _, err := client.RunSudoScript(ctx, prepScript); err != nil {
		return fmt.Errorf("failed to prepare host: %w", err)
	}

	for _, name := range AWGContainerNames {
		_, _, _, _ = client.RunSudoCommand(ctx, fmt.Sprintf("docker stop %s 2>/dev/null || true", name))
		_, _, _, _ = client.RunSudoCommand(ctx, fmt.Sprintf("docker rm -fv %s 2>/dev/null || true", name))
	}
	return nil
}

func buildAndRunAWGContainer(ctx context.Context, client ssh.SSHClient, port string) error {
	dockerfile := `FROM amneziavpn/amneziawg-go:latest
LABEL maintainer="AmneziaVPN"
RUN apk add --no-cache bash curl dumb-init iptables && apk --update upgrade --no-cache
RUN mkdir -p /opt/amnezia
RUN echo "#!/bin/bash" > /opt/amnezia/start.sh && echo "tail -f /dev/null" >> /opt/amnezia/start.sh && chmod a+x /opt/amnezia/start.sh
ENTRYPOINT [ "dumb-init", "/opt/amnezia/start.sh" ]
`
	if err := client.UploadSudoFile(ctx, "/opt/amnezia/amnezia-awg/Dockerfile", []byte(dockerfile), 0644); err != nil {
		return fmt.Errorf("failed to upload Dockerfile: %w", err)
	}

	if _, errOut, bCode, err := client.RunSudoCommand(ctx, "docker build --no-cache -t amnezia-awg /opt/amnezia/amnezia-awg"); err != nil || bCode != 0 {
		return fmt.Errorf("failed to build amnezia-awg image (code %d): %s, %w", bCode, errOut, err)
	}

	runCmd := fmt.Sprintf(`docker run -d \
--restart always \
--privileged \
--cap-add=NET_ADMIN \
--cap-add=SYS_MODULE \
-p %s:%s/udp \
-v /lib/modules:/lib/modules \
--sysctl="net.ipv4.conf.all.src_valid_mark=1" \
--name amnezia-awg \
amnezia-awg`, port, port)

	if _, errOut, rCode, err := client.RunSudoCommand(ctx, runCmd); err != nil || rCode != 0 {
		return fmt.Errorf("failed to run container (code %d): %s, %w", rCode, errOut, err)
	}

	_, _, _, _ = client.RunSudoCommand(ctx, "docker network connect amnezia-dns-net amnezia-awg 2>/dev/null || true")
	return nil
}

func initializeServerKeysAndConfig(ctx context.Context, client ssh.SSHClient, port string, awgParams *AWGParams) error {
	serverPrivKey, serverPubKey, err := GenerateWGKeypair()
	if err != nil {
		return fmt.Errorf("failed to generate server keypair: %w", err)
	}
	serverPSK, err := GeneratePSK()
	if err != nil {
		return fmt.Errorf("failed to generate server psk: %w", err)
	}

	keygenScript := fmt.Sprintf(`
mkdir -p /opt/amnezia/awg
echo "%s" > /opt/amnezia/awg/wireguard_server_private_key.key
echo "%s" > /opt/amnezia/awg/wireguard_server_public_key.key
echo "%s" > /opt/amnezia/awg/wireguard_psk.key
`, serverPrivKey, serverPubKey, serverPSK)
	_, _, _, _ = client.RunSudoCommand(ctx, fmt.Sprintf("docker exec -i amnezia-awg bash -c '%s'", keygenScript))

	serverConfig := RenderServerConfig(serverPrivKey, AWGDefaults["subnet_ip"], AWGDefaults["subnet_cidr"], port, awgParams.MTU, awgParams, nil)
	if err := client.UploadSudoFile(ctx, "/tmp/_amnz_awg0.conf", []byte(serverConfig), 0600); err != nil {
		return err
	}
	_, _, _, _ = client.RunSudoCommand(ctx, "docker cp /tmp/_amnz_awg0.conf amnezia-awg:/opt/amnezia/awg/awg0.conf")
	_, _, _, _ = client.RunSudoCommand(ctx, "rm -f /tmp/_amnz_awg0.conf")

	startScript := fmt.Sprintf(`#!/bin/bash
awg-quick down /opt/amnezia/awg/awg0.conf 2>/dev/null || true
if [ -f /opt/amnezia/awg/awg0.conf ]; then awg-quick up /opt/amnezia/awg/awg0.conf; fi
iptables -A INPUT -i awg0 -j ACCEPT
iptables -A FORWARD -i awg0 -j ACCEPT
iptables -A OUTPUT -o awg0 -j ACCEPT
iptables -A FORWARD -i awg0 -o eth0 -s %s/%s -j ACCEPT
iptables -A FORWARD -m state --state ESTABLISHED,RELATED -j ACCEPT
iptables -t nat -A POSTROUTING -s %s/%s -o eth0 -j MASQUERADE
tail -f /dev/null
`, AWGDefaults["subnet_ip"], AWGDefaults["subnet_cidr"], AWGDefaults["subnet_ip"], AWGDefaults["subnet_cidr"])

	if err := client.UploadSudoFile(ctx, "/tmp/_amnz_start.sh", []byte(startScript), 0755); err != nil {
		return err
	}
	_, _, _, _ = client.RunSudoCommand(ctx, "docker cp /tmp/_amnz_start.sh amnezia-awg:/opt/amnezia/start.sh")
	_, _, _, _ = client.RunSudoCommand(ctx, "docker exec amnezia-awg chmod +x /opt/amnezia/start.sh")
	_, _, _, _ = client.RunSudoCommand(ctx, "rm -f /tmp/_amnz_start.sh")
	_, _, _, _ = client.RunSudoCommand(ctx, "docker restart amnezia-awg")

	firewallScript := `
sysctl -w net.ipv4.ip_forward=1
iptables -C INPUT -p icmp --icmp-type echo-request -j DROP 2>/dev/null || iptables -A INPUT -p icmp --icmp-type echo-request -j DROP
`
	_, _, _, _ = client.RunSudoScript(ctx, firewallScript)
	return nil
}

// Install deploys Docker (if missing), builds the AWG container, configures parameters, and starts the service.
func (m *AWGManager) Install(ctx context.Context, server *models.Server, params map[string]any) error {
	client, err := m.getSSHClient(ctx, server)
	if err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	port := AWGDefaults["port"]
	if p, ok := params["port"]; ok && fmt.Sprint(p) != "" {
		port = fmt.Sprint(p)
	}

	profile := "standard"
	if p, ok := params["awg_profile"]; ok && fmt.Sprint(p) != "" {
		profile = fmt.Sprint(p)
	}

	awgParams, err := GenerateAWGParams(profile)
	if err != nil {
		return fmt.Errorf("failed to generate AWG params: %w", err)
	}
	awgParams.Port = port

	if (profile == "standard" || profile == "pro") && params != nil {
		cpsProto := "quic"
		if cp, ok := params["awg_cps_protocol"]; ok && fmt.Sprint(cp) != "" {
			cpsProto = fmt.Sprint(cp)
		}
		if d, err := cps.SelectMimicryDomain(ctx, client, cpsProto); err == nil && d != "" {
			if cpsPackets, err := cps.GenerateCPSPackets(profile, d); err == nil {
				awgParams.I1 = cpsPackets["i1"]
				awgParams.I2 = cpsPackets["i2"]
				awgParams.I3 = cpsPackets["i3"]
				awgParams.I4 = cpsPackets["i4"]
				awgParams.I5 = cpsPackets["i5"]
			}
		}
	}

	if err := ensureDockerInstalled(ctx, client); err != nil {
		return err
	}
	if err := prepareHostAndContainers(ctx, client); err != nil {
		return err
	}
	if err := buildAndRunAWGContainer(ctx, client, port); err != nil {
		return err
	}
	return initializeServerKeysAndConfig(ctx, client, port, awgParams)
}

// Uninstall stops and removes all AWG containers and clean up directories.
func (m *AWGManager) Uninstall(ctx context.Context, server *models.Server) error {
	client, err := m.getSSHClient(ctx, server)
	if err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	for _, name := range AWGContainerNames {
		_, _, _, _ = client.RunSudoCommand(ctx, fmt.Sprintf("docker stop %s 2>/dev/null || true", name))
		_, _, _, _ = client.RunSudoCommand(ctx, fmt.Sprintf("docker rm -fv %s 2>/dev/null || true", name))
		_, _, _, _ = client.RunSudoCommand(ctx, fmt.Sprintf("docker rmi %s 2>/dev/null || true", name))
	}
	_, _, _, _ = client.RunSudoCommand(ctx, "rm -rf /opt/amnezia/amnezia-awg /opt/amnezia/awg")
	return nil
}

func (m *AWGManager) getServerConfig(ctx context.Context, client ssh.SSHClient) (string, error) {
	out, errOut, code, err := client.RunSudoCommand(ctx, fmt.Sprintf("docker exec -i %s cat %s 2>/dev/null", m.containerName(), m.configPath()))
	if err != nil || code != 0 {
		return "", fmt.Errorf("failed to get server config (code %d): %s, %w", code, errOut, err)
	}
	return out, nil
}

func (m *AWGManager) saveServerConfig(ctx context.Context, client ssh.SSHClient, content string) error {
	tmpPath := "/tmp/_amnz_edit_config.conf"
	if err := client.UploadSudoFile(ctx, tmpPath, []byte(content), 0600); err != nil {
		return err
	}
	defer func() {
		_, _, _, _ = client.RunSudoCommand(ctx, fmt.Sprintf("rm -f %s", tmpPath))
	}()

	cpCmd := fmt.Sprintf("docker cp %s %s:%s", tmpPath, m.containerName(), m.configPath())
	if _, errOut, code, err := client.RunSudoCommand(ctx, cpCmd); err != nil || code != 0 {
		return fmt.Errorf("failed to copy config into container (code %d): %s, %w", code, errOut, err)
	}

	syncCmd := fmt.Sprintf("docker exec -i %s bash -c '%s syncconf %s <(%s-quick strip %s)'",
		m.containerName(), m.wgBinary(), m.interfaceName(), m.wgBinary(), m.configPath())
	_, _, _, _ = client.RunSudoCommand(ctx, syncCmd)
	return nil
}

func (m *AWGManager) getClientsTable(ctx context.Context, client ssh.SSHClient) ([]AWGClient, error) {
	out, _, code, _ := client.RunSudoCommand(ctx, fmt.Sprintf("docker exec -i %s cat %s 2>/dev/null", m.containerName(), m.clientsTablePath()))
	if code != 0 || strings.TrimSpace(out) == "" {
		return []AWGClient{}, nil
	}
	return ParseClientsTable(out)
}

func (m *AWGManager) saveClientsTable(ctx context.Context, client ssh.SSHClient, clients []AWGClient) error {
	jsonData, err := SerializeClientsTable(clients)
	if err != nil {
		return err
	}

	tmpPath := "/tmp/_amnz_clients.json"
	if err := client.UploadSudoFile(ctx, tmpPath, []byte(jsonData), 0600); err != nil {
		return err
	}
	defer func() {
		_, _, _, _ = client.RunSudoCommand(ctx, fmt.Sprintf("rm -f %s", tmpPath))
	}()

	cpCmd := fmt.Sprintf("docker cp %s %s:%s", tmpPath, m.containerName(), m.clientsTablePath())
	if _, errOut, code, err := client.RunSudoCommand(ctx, cpCmd); err != nil || code != 0 {
		return fmt.Errorf("failed to copy clientsTable into container (code %d): %s, %w", code, errOut, err)
	}
	return nil
}

// GetClients returns all registered clients from clientsTable enriched with live transfer data.
func (m *AWGManager) GetClients(ctx context.Context, server *models.Server) ([]map[string]any, error) {
	client, err := m.getSSHClient(ctx, server)
	if err != nil {
		return nil, err
	}

	clients, err := m.getClientsTable(ctx, client)
	if err != nil {
		return nil, err
	}

	// Live transfer stats via awg show all
	showOut, _, _, _ := client.RunSudoCommand(ctx, fmt.Sprintf("docker exec -i %s %s show all 2>/dev/null", m.containerName(), m.wgBinary()))
	showStats := parseWGShow(showOut)

	var result []map[string]any
	knownIDs := make(map[string]bool)

	for _, c := range clients {
		knownIDs[c.ClientID] = true
		ud := c.UserData

		if stat, ok := showStats[c.ClientID]; ok {
			ud.LatestHandshake = stat.LatestHandshake
			ud.DataReceived = stat.DataReceived
			ud.DataSent = stat.DataSent
			ud.DataReceivedBytes = stat.DataReceivedBytes
			ud.DataSentBytes = stat.DataSentBytes
			if stat.AllowedIPs != "" {
				ud.AllowedIPs = stat.AllowedIPs
			}
		}

		udMap := map[string]any{
			"clientName":        ud.ClientName,
			"creationDate":      ud.CreationDate,
			"clientPrivateKey":  ud.ClientPrivateKey,
			"clientIp":          ud.ClientIP,
			"psk":               ud.PSK,
			"enabled":           ud.Enabled,
			"awg_mimicry":       ud.AWGMimicry,
			"speed_limit_down":  ud.SpeedLimitDown,
			"speed_limit_up":    ud.SpeedLimitUp,
			"latestHandshake":   ud.LatestHandshake,
			"dataReceived":      ud.DataReceived,
			"dataSent":          ud.DataSent,
			"dataReceivedBytes": ud.DataReceivedBytes,
			"dataSentBytes":     ud.DataSentBytes,
			"allowedIps":        ud.AllowedIPs,
			"externalClient":    ud.ExternalClient,
			"rotated_at":        ud.RotatedAt,
		}

		result = append(result, map[string]any{
			"clientId": c.ClientID,
			"userData": udMap,
		})
	}

	// Pick up external peers in awg0.conf not in clientsTable
	if confText, err := m.getServerConfig(ctx, client); err == nil {
		_, peers, _ := ParseServerConfig(confText)
		for _, p := range peers {
			if !knownIDs[p.PublicKey] {
				stat := showStats[p.PublicKey]
				result = append(result, map[string]any{
					"clientId": p.PublicKey,
					"userData": map[string]any{
						"clientName":        fmt.Sprintf("External (%s)", p.AllowedIPs),
						"clientPrivateKey":  "",
						"externalClient":    true,
						"allowedIps":        p.AllowedIPs,
						"latestHandshake":   stat.LatestHandshake,
						"dataReceived":      stat.DataReceived,
						"dataSent":          stat.DataSent,
						"dataReceivedBytes": stat.DataReceivedBytes,
						"dataSentBytes":     stat.DataSentBytes,
					},
				})
			}
		}
	}

	return result, nil
}

type wgShowPeerStat struct {
	LatestHandshake   string
	DataReceived      string
	DataSent          string
	DataReceivedBytes int64
	DataSentBytes     int64
	AllowedIPs        string
}

func parseWGShow(out string) map[string]wgShowPeerStat {
	stats := make(map[string]wgShowPeerStat)
	var currentPeer string
	var currentStat wgShowPeerStat

	lines := strings.Split(out, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "peer:") {
			if currentPeer != "" {
				stats[currentPeer] = currentStat
			}
			currentPeer = strings.TrimSpace(strings.TrimPrefix(trimmed, "peer:"))
			currentStat = wgShowPeerStat{}
		} else if currentPeer != "" && strings.Contains(trimmed, ":") {
			parts := strings.SplitN(trimmed, ":", 2)
			k := strings.TrimSpace(parts[0])
			v := strings.TrimSpace(parts[1])
			switch k {
			case "latest handshake":
				currentStat.LatestHandshake = v
			case "transfer":
				tfParts := strings.Split(v, ",")
				if len(tfParts) == 2 {
					rx := strings.TrimSpace(strings.TrimSuffix(tfParts[0], "received"))
					tx := strings.TrimSpace(strings.TrimSuffix(tfParts[1], "sent"))
					currentStat.DataReceived = rx
					currentStat.DataSent = tx
					currentStat.DataReceivedBytes = parseSizeHuman(rx)
					currentStat.DataSentBytes = parseSizeHuman(tx)
				}
			case "allowed ips":
				currentStat.AllowedIPs = v
			}
		}
	}
	if currentPeer != "" {
		stats[currentPeer] = currentStat
	}
	return stats
}

func parseSizeHuman(s string) int64 {
	parts := strings.Fields(s)
	if len(parts) != 2 {
		return 0
	}
	val, err := strconv.ParseFloat(parts[0], 64)
	if err != nil {
		return 0
	}
	mult := int64(1)
	switch strings.ToLower(parts[1]) {
	case "kib", "kb":
		mult = 1024
	case "mib", "mb":
		mult = 1024 * 1024
	case "gib", "gb":
		mult = 1024 * 1024 * 1024
	case "tib", "tb":
		mult = 1024 * 1024 * 1024 * 1024
	}
	return int64(val * float64(mult))
}

// AddClient provisions a new client/peer in the AWG configuration.
func (m *AWGManager) AddClient(ctx context.Context, server *models.Server, clientParams map[string]any) (map[string]any, error) {
	client, err := m.getSSHClient(ctx, server)
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	clientName := "client"
	if n, ok := clientParams["name"]; ok && fmt.Sprint(n) != "" {
		clientName = fmt.Sprint(n)
	}

	clientPrivKey, clientPubKey, err := GenerateWGKeypair()
	if err != nil {
		return nil, fmt.Errorf("failed to generate client keypair: %w", err)
	}

	// Read server config
	confText, err := m.getServerConfig(ctx, client)
	if err != nil {
		return nil, err
	}

	usedIPs := GetUsedIPsFromConfig(confText)
	subnetAddr := AWGDefaults["subnet_address"]
	subnetCIDR, _ := strconv.Atoi(AWGDefaults["subnet_cidr"])
	gatewayIP := AWGDefaults["subnet_ip"]

	clientIP, err := GetNextIP(usedIPs, subnetAddr, subnetCIDR, gatewayIP)
	if err != nil {
		return nil, err
	}

	serverParams, _, _ := ParseServerConfig(confText)
	serverPubKeyOut, _, _, _ := client.RunSudoCommand(ctx, fmt.Sprintf("docker exec -i %s cat /opt/amnezia/awg/wireguard_server_public_key.key", m.containerName()))
	serverPubKey := strings.TrimSpace(serverPubKeyOut)
	pskOut, _, _, _ := client.RunSudoCommand(ctx, fmt.Sprintf("docker exec -i %s cat /opt/amnezia/awg/wireguard_psk.key", m.containerName()))
	psk := strings.TrimSpace(pskOut)

	// Append [Peer] to server config
	peerSection := fmt.Sprintf("\n[Peer]\nPublicKey = %s\nPresharedKey = %s\nAllowedIPs = %s/32\n", clientPubKey, psk, clientIP)
	newConfig := strings.TrimRight(confText, "\n") + "\n" + peerSection
	if err := m.saveServerConfig(ctx, client, newConfig); err != nil {
		return nil, err
	}

	// Parse speed limits if provided
	var speedDown, speedUp *int
	if v, ok := clientParams["awg_speed_limit_down"]; ok && v != nil {
		if val, err := strconv.Atoi(fmt.Sprint(v)); err == nil && val > 0 {
			speedDown = &val
		}
	}
	if v, ok := clientParams["awg_speed_limit_up"]; ok && v != nil {
		if val, err := strconv.Atoi(fmt.Sprint(v)); err == nil && val > 0 {
			speedUp = &val
		}
	}

	mimicry := "auto"
	if v, ok := clientParams["awg_mimicry"]; ok && fmt.Sprint(v) != "" {
		mimicry = fmt.Sprint(v)
	}

	// Save to clientsTable
	clients, _ := m.getClientsTable(ctx, client)
	newEntry := AWGClient{
		ClientID: clientPubKey,
		UserData: AWGClientUserData{
			ClientName:       clientName,
			ClientPrivateKey: clientPrivKey,
			ClientIP:         clientIP,
			PSK:              psk,
			Enabled:          true,
			AWGMimicry:       mimicry,
			SpeedLimitDown:   speedDown,
			SpeedLimitUp:     speedUp,
		},
	}
	clients = append(clients, newEntry)
	_ = m.saveClientsTable(ctx, client, clients)

	// Apply speed limit via TC
	if speedDown != nil || speedUp != nil {
		dVal, uVal := 0, 0
		if speedDown != nil {
			dVal = *speedDown
		}
		if speedUp != nil {
			uVal = *speedUp
		}
		_ = tc.ApplySpeedLimit(ctx, client, m.containerName(), m.interfaceName(), clientIP, dVal, uVal)
	}

	// Render client config
	parsedParams := AWGParamsFromMap(convertStringMapToAny(serverParams))
	if mimicry != "" {
		if mp, err := cps.GenerateMimicryPackets(ctx, mimicry, "", client); err == nil {
			parsedParams.I1 = mp["i1"]
			parsedParams.I2 = mp["i2"]
			parsedParams.I3 = mp["i3"]
			parsedParams.I4 = mp["i4"]
			parsedParams.I5 = mp["i5"]
		}
	}

	port := serverParams["port"]
	if port == "" {
		port = AWGDefaults["port"]
	}
	endpoint := fmt.Sprintf("%s:%s", server.Host, port)
	clientConfig := RenderClientConfig(clientPrivKey, clientIP, serverPubKey, psk, endpoint, AWGDefaults["dns1"], AWGDefaults["dns2"], parsedParams.MTU, parsedParams)
	connectionKit, _ := cps.GenerateConnectionKit(ctx, clientConfig, "", client)

	return map[string]any{
		"client_id":      clientPubKey,
		"client_name":    clientName,
		"client_ip":      clientIP,
		"config":         clientConfig,
		"connection_kit": connectionKit,
		"awg_mimicry":    mimicry,
	}, nil
}

func convertStringMapToAny(m map[string]string) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// RemoveClient removes a client from the server WireGuard configuration and clientsTable.
func (m *AWGManager) RemoveClient(ctx context.Context, server *models.Server, clientID string) error {
	client, err := m.getSSHClient(ctx, server)
	if err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// 1. Remove TC speed limit for peer IP
	clients, _ := m.getClientsTable(ctx, client)
	for _, c := range clients {
		if c.ClientID == clientID && c.UserData.ClientIP != "" {
			_ = tc.RemoveSpeedLimit(ctx, client, m.containerName(), m.interfaceName(), c.UserData.ClientIP)
			break
		}
	}

	// 2. Remove [Peer] section from config
	confText, err := m.getServerConfig(ctx, client)
	if err != nil {
		return err
	}

	sections := strings.Split(confText, "[")
	var newSections []string
	for _, sec := range sections {
		if strings.TrimSpace(sec) == "" {
			continue
		}
		if strings.Contains(sec, clientID) {
			continue
		}
		newSections = append(newSections, sec)
	}

	newConfig := "[" + strings.Join(newSections, "[")
	if err := m.saveServerConfig(ctx, client, newConfig); err != nil {
		return err
	}

	// 3. Update clientsTable
	var updatedClients []AWGClient
	for _, c := range clients {
		if c.ClientID != clientID {
			updatedClients = append(updatedClients, c)
		}
	}
	return m.saveClientsTable(ctx, client, updatedClients)
}

// GetClientConfig reconstructs the client config file for an existing client ID.
func (m *AWGManager) GetClientConfig(ctx context.Context, server *models.Server, clientID string) (string, error) {
	client, err := m.getSSHClient(ctx, server)
	if err != nil {
		return "", err
	}

	clients, err := m.getClientsTable(ctx, client)
	if err != nil {
		return "", err
	}

	var targetClient *AWGClient
	for _, c := range clients {
		if c.ClientID == clientID {
			targetClient = &c
			break
		}
	}
	if targetClient == nil {
		return "", fmt.Errorf("client %s not found in clients table", clientID)
	}

	ud := targetClient.UserData
	if ud.ClientPrivateKey == "" {
		return "", errors.New("client private key not stored; config cannot be reconstructed")
	}

	confText, err := m.getServerConfig(ctx, client)
	if err != nil {
		return "", err
	}

	serverParams, _, _ := ParseServerConfig(confText)
	serverPubKeyOut, _, _, _ := client.RunSudoCommand(ctx, fmt.Sprintf("docker exec -i %s cat /opt/amnezia/awg/wireguard_server_public_key.key", m.containerName()))
	serverPubKey := strings.TrimSpace(serverPubKeyOut)

	psk := ud.PSK
	if psk == "" {
		pskOut, _, _, _ := client.RunSudoCommand(ctx, fmt.Sprintf("docker exec -i %s cat /opt/amnezia/awg/wireguard_psk.key", m.containerName()))
		psk = strings.TrimSpace(pskOut)
	}

	parsedParams := AWGParamsFromMap(convertStringMapToAny(serverParams))
	if ud.AWGMimicry != "" {
		if mp, err := cps.GenerateMimicryPackets(ctx, ud.AWGMimicry, "", client); err == nil {
			parsedParams.I1 = mp["i1"]
			parsedParams.I2 = mp["i2"]
			parsedParams.I3 = mp["i3"]
			parsedParams.I4 = mp["i4"]
			parsedParams.I5 = mp["i5"]
		}
	}

	port := serverParams["port"]
	if port == "" {
		port = AWGDefaults["port"]
	}
	endpoint := fmt.Sprintf("%s:%s", server.Host, port)

	return RenderClientConfig(ud.ClientPrivateKey, ud.ClientIP, serverPubKey, psk, endpoint, AWGDefaults["dns1"], AWGDefaults["dns2"], parsedParams.MTU, parsedParams), nil
}

// ToggleClient enables or disables a client by adding or removing the [Peer] from the server config.
func (m *AWGManager) ToggleClient(ctx context.Context, server *models.Server, clientID string, enable bool) error {
	client, err := m.getSSHClient(ctx, server)
	if err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	clients, err := m.getClientsTable(ctx, client)
	if err != nil {
		return err
	}

	var target *AWGClient
	for i := range clients {
		if clients[i].ClientID == clientID {
			target = &clients[i]
			clients[i].UserData.Enabled = enable
			break
		}
	}
	if target == nil {
		return fmt.Errorf("client %s not found", clientID)
	}

	confText, err := m.getServerConfig(ctx, client)
	if err != nil {
		return err
	}

	var newConfig string
	if enable {
		psk := target.UserData.PSK
		peerSec := fmt.Sprintf("\n[Peer]\nPublicKey = %s\nPresharedKey = %s\nAllowedIPs = %s/32\n", clientID, psk, target.UserData.ClientIP)
		newConfig = strings.TrimRight(confText, "\n") + "\n" + peerSec
	} else {
		sections := strings.Split(confText, "[")
		var newSections []string
		for _, sec := range sections {
			if strings.TrimSpace(sec) == "" || strings.Contains(sec, clientID) {
				continue
			}
			newSections = append(newSections, sec)
		}
		newConfig = "[" + strings.Join(newSections, "[")
	}

	if err := m.saveServerConfig(ctx, client, newConfig); err != nil {
		return err
	}
	return m.saveClientsTable(ctx, client, clients)
}

// GetServerStatus returns whether the container is running and configuration details.
func (m *AWGManager) GetServerStatus(ctx context.Context, server *models.Server) (map[string]any, error) {
	client, err := m.getSSHClient(ctx, server)
	if err != nil {
		return nil, err
	}

	out, _, code, _ := client.RunSudoCommand(ctx, "docker ps --filter name=^amnezia-awg$ --format '{{.Status}}'")
	running := code == 0 && strings.Contains(out, "Up")

	outAll, _, codeAll, _ := client.RunSudoCommand(ctx, "docker ps -a --filter name=^amnezia-awg$ --format '{{.Names}}'")
	exists := codeAll == 0 && strings.Contains(outAll, "amnezia-awg")

	status := map[string]any{
		"protocol":          "awg",
		"container_exists":  exists,
		"container_running": running,
	}

	if running {
		if conf, err := m.getServerConfig(ctx, client); err == nil {
			params, peers, _ := ParseServerConfig(conf)
			status["port"] = params["port"]
			status["awg_params"] = params
			status["clients_count"] = len(peers)
		}
	}

	return status, nil
}

func parseParamString(params map[string]any, keys ...string) (string, bool) {
	for _, k := range keys {
		if v, ok := params[k]; ok && v != nil && fmt.Sprint(v) != "" {
			return fmt.Sprint(v), true
		}
	}
	return "", false
}

func parseBoolParam(val any) (bool, bool) {
	if val == nil {
		return false, false
	}
	switch v := val.(type) {
	case bool:
		return v, true
	case string:
		return strings.ToLower(v) == "true" || v == "1", true
	case int:
		return v != 0, true
	case int64:
		return v != 0, true
	case float64:
		return v != 0, true
	default:
		return false, false
	}
}

func parseSpeedLimit(params map[string]any, keys ...string) (*int, bool) {
	for _, k := range keys {
		if v, ok := params[k]; ok {
			if v == nil {
				return nil, true
			}
			if val, err := strconv.Atoi(fmt.Sprint(v)); err == nil && val > 0 {
				return &val, true
			}
			return nil, true
		}
	}
	return nil, false
}

// EditClient modifies client metadata, enabling/disabling, and bandwidth limits with TC sync.
func (m *AWGManager) EditClient(ctx context.Context, server *models.Server, clientID string, params map[string]any) error {
	client, err := m.getSSHClient(ctx, server)
	if err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	clients, err := m.getClientsTable(ctx, client)
	if err != nil {
		return err
	}

	var target *AWGClient
	for i := range clients {
		if clients[i].ClientID == clientID {
			target = &clients[i]
			break
		}
	}
	if target == nil {
		return fmt.Errorf("client %s not found in clients table", clientID)
	}

	if name, ok := parseParamString(params, "name", "clientName", "client_name"); ok {
		target.UserData.ClientName = name
	}
	if mimicry, ok := parseParamString(params, "awg_mimicry", "mimicry"); ok {
		target.UserData.AWGMimicry = mimicry
	}

	if newEnabled, ok := parseBoolParam(params["enabled"]); ok && newEnabled != target.UserData.Enabled {
		target.UserData.Enabled = newEnabled
		_ = m.updateServerConfigPeer(ctx, client, clientID, target.UserData.ClientIP, target.UserData.PSK, newEnabled)
	}

	down, downOk := parseSpeedLimit(params, "speed_limit_down", "awg_speed_limit_down", "speedDown")
	up, upOk := parseSpeedLimit(params, "speed_limit_up", "awg_speed_limit_up", "speedUp")
	if downOk || upOk {
		if downOk {
			target.UserData.SpeedLimitDown = down
		}
		if upOk {
			target.UserData.SpeedLimitUp = up
		}
		m.syncClientTC(ctx, client, target.UserData.ClientIP, target.UserData.SpeedLimitDown, target.UserData.SpeedLimitUp)
	}

	return m.saveClientsTable(ctx, client, clients)
}

func (m *AWGManager) updateServerConfigPeer(ctx context.Context, client ssh.SSHClient, clientID, clientIP, psk string, enable bool) error {
	confText, err := m.getServerConfig(ctx, client)
	if err != nil {
		return err
	}
	var newConfig string
	if enable {
		peerSec := fmt.Sprintf("\n[Peer]\nPublicKey = %s\nPresharedKey = %s\nAllowedIPs = %s/32\n", clientID, psk, clientIP)
		newConfig = strings.TrimRight(confText, "\n") + "\n" + peerSec
	} else {
		sections := strings.Split(confText, "[")
		var newSections []string
		for _, sec := range sections {
			if strings.TrimSpace(sec) == "" || strings.Contains(sec, clientID) {
				continue
			}
			newSections = append(newSections, sec)
		}
		newConfig = "[" + strings.Join(newSections, "[")
	}
	return m.saveServerConfig(ctx, client, newConfig)
}

func (m *AWGManager) syncClientTC(ctx context.Context, client ssh.SSHClient, clientIP string, curDown, curUp *int) {
	if (curDown != nil && *curDown > 0) || (curUp != nil && *curUp > 0) {
		dVal, uVal := 0, 0
		if curDown != nil {
			dVal = *curDown
		}
		if curUp != nil {
			uVal = *curUp
		}
		_ = tc.ApplySpeedLimit(ctx, client, m.containerName(), m.interfaceName(), clientIP, dVal, uVal)
	} else {
		_ = tc.RemoveSpeedLimit(ctx, client, m.containerName(), m.interfaceName(), clientIP)
	}
}

// RotateMimicry rotates a client's mimicry profile through the sequence:
// auto -> tls -> quic -> dns -> sip -> tls, regenerates I1-I5 packet headers, and updates clientsTable.
func (m *AWGManager) RotateMimicry(ctx context.Context, server *models.Server, clientID string) (string, error) {
	client, err := m.getSSHClient(ctx, server)
	if err != nil {
		return "", err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	clients, err := m.getClientsTable(ctx, client)
	if err != nil {
		return "", err
	}

	var target *AWGClient
	for i := range clients {
		if clients[i].ClientID == clientID {
			target = &clients[i]
			break
		}
	}
	if target == nil {
		return "", fmt.Errorf("client %s not found in clients table", clientID)
	}

	curr := strings.ToLower(strings.TrimSpace(target.UserData.AWGMimicry))
	if curr == "" {
		curr = "auto"
	}

	var nextProfile string
	switch curr {
	case "auto":
		nextProfile = "tls"
	case "tls":
		nextProfile = "quic"
	case "quic":
		nextProfile = "dns"
	case "dns":
		nextProfile = "sip"
	case "sip":
		nextProfile = "tls"
	default:
		nextProfile = "tls"
	}

	// Regenerate I1-I5 signature packets
	if mp, err := cps.GenerateMimicryPackets(ctx, nextProfile, "", client); err == nil {
		target.UserData.I1 = mp["i1"]
		target.UserData.I2 = mp["i2"]
		target.UserData.I3 = mp["i3"]
		target.UserData.I4 = mp["i4"]
		target.UserData.I5 = mp["i5"]
	}

	target.UserData.AWGMimicry = nextProfile
	target.UserData.RotatedAt = time.Now().UTC().Format(time.RFC3339)

	if err := m.saveClientsTable(ctx, client, clients); err != nil {
		return "", fmt.Errorf("failed to save clients table after mimicry rotation: %w", err)
	}

	return nextProfile, nil
}
