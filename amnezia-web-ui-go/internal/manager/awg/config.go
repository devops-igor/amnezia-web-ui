package awg

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strings"
)

// AWGPeer represents a WireGuard peer entry in the server configuration.
//
//nolint:revive
type AWGPeer struct {
	PublicKey    string `json:"public_key"`
	PresharedKey string `json:"preshared_key"`
	AllowedIPs   string `json:"allowed_ips"`
}

// AWGClientUserData represents metadata stored per client in clients.json / clientsTable.
//
//nolint:revive
type AWGClientUserData struct {
	ClientName        string  `json:"clientName"`
	CreationDate      string  `json:"creationDate,omitempty"`
	ClientPrivateKey  string  `json:"clientPrivateKey,omitempty"`
	ClientIP          string  `json:"clientIp,omitempty"`
	PSK               string  `json:"psk,omitempty"`
	Enabled           bool    `json:"enabled"`
	AWGMimicry        string  `json:"awg_mimicry,omitempty"`
	SpeedLimitDown    *int    `json:"speed_limit_down,omitempty"`
	SpeedLimitUp      *int    `json:"speed_limit_up,omitempty"`
	LatestHandshake   string  `json:"latestHandshake,omitempty"`
	DataReceived      string  `json:"dataReceived,omitempty"`
	DataSent          string  `json:"dataSent,omitempty"`
	DataReceivedBytes int64   `json:"dataReceivedBytes,omitempty"`
	DataSentBytes     int64   `json:"dataSentBytes,omitempty"`
	AllowedIPs        string  `json:"allowedIps,omitempty"`
	ExternalClient    bool    `json:"externalClient,omitempty"`
	TrialProfile      string  `json:"trial_profile,omitempty"`
	TrialFor          string  `json:"trial_for,omitempty"`
	TrialUserID       *string `json:"trial_user_id,omitempty"`
	MainClientID      *string `json:"main_client_id,omitempty"`
	TrialCreatedAt    string  `json:"trial_created_at,omitempty"`
	ExpiresAt         string  `json:"expires_at,omitempty"`
}

// AWGClient represents a client entry in the clients table.
//
//nolint:revive
type AWGClient struct {
	ClientID string            `json:"clientId"`
	UserData AWGClientUserData `json:"userData"`
}

// RenderServerConfig builds the WireGuard / AmneziaWG server configuration text.
// NOTE: I1-I5 parameters are CLIENT-only and are strictly excluded from the server configuration.
func RenderServerConfig(serverPrivKey string, subnetIP, subnetCIDR string, port string, mtu string, params *AWGParams, peers []AWGPeer) string {
	if mtu == "" {
		mtu = "1280"
	}
	if params == nil {
		params = &AWGParams{}
	}

	lines := []string{
		"[Interface]",
		fmt.Sprintf("PrivateKey = %s", serverPrivKey),
		fmt.Sprintf("Address = %s/%s", subnetIP, subnetCIDR),
		fmt.Sprintf("MTU = %s", mtu),
		fmt.Sprintf("ListenPort = %s", port),
	}

	mapping := []struct {
		val string
		key string
	}{
		{params.JunkPacketCount, "Jc"},
		{params.JunkPacketMinSize, "Jmin"},
		{params.JunkPacketMaxSize, "Jmax"},
		{params.InitPacketJunkSize, "S1"},
		{params.ResponsePacketJunkSize, "S2"},
		{params.CookieReplyPacketJunkSize, "S3"},
		{params.TransportPacketJunkSize, "S4"},
		{params.InitPacketMagicHeader, "H1"},
		{params.ResponsePacketMagicHeader, "H2"},
		{params.UnderloadPacketMagicHeader, "H3"},
		{params.TransportPacketMagicHeader, "H4"},
	}

	for _, item := range mapping {
		if item.val != "" {
			lines = append(lines, fmt.Sprintf("%s = %s", item.key, item.val))
		}
	}

	for _, peer := range peers {
		lines = append(lines, "", "[Peer]", fmt.Sprintf("PublicKey = %s", peer.PublicKey))
		if peer.PresharedKey != "" {
			lines = append(lines, fmt.Sprintf("PresharedKey = %s", peer.PresharedKey))
		}
		lines = append(lines, fmt.Sprintf("AllowedIPs = %s", peer.AllowedIPs))
	}

	return strings.Join(lines, "\n") + "\n"
}

// RenderClientConfig builds the WireGuard / AmneziaWG client configuration file text.
func RenderClientConfig(clientPrivKey string, clientIP string, serverPubKey string, psk string, endpoint string, dns1, dns2 string, mtu string, params *AWGParams) string {
	if mtu == "" {
		mtu = "1280"
	}
	if dns1 == "" {
		dns1 = "94.140.14.14"
	}
	if dns2 == "" {
		dns2 = "94.140.15.15"
	}
	if params == nil {
		params = &AWGParams{}
	}

	lines := []string{
		"[Interface]",
		fmt.Sprintf("Address = %s/32", clientIP),
		fmt.Sprintf("DNS = %s, %s", dns1, dns2),
		fmt.Sprintf("PrivateKey = %s", clientPrivKey),
		fmt.Sprintf("MTU = %s", mtu),
	}

	mapping := []struct {
		val string
		key string
	}{
		{params.JunkPacketCount, "Jc"},
		{params.JunkPacketMinSize, "Jmin"},
		{params.JunkPacketMaxSize, "Jmax"},
		{params.InitPacketJunkSize, "S1"},
		{params.ResponsePacketJunkSize, "S2"},
		{params.CookieReplyPacketJunkSize, "S3"},
		{params.TransportPacketJunkSize, "S4"},
		{params.InitPacketMagicHeader, "H1"},
		{params.ResponsePacketMagicHeader, "H2"},
		{params.UnderloadPacketMagicHeader, "H3"},
		{params.TransportPacketMagicHeader, "H4"},
		{params.I1, "I1"},
		{params.I2, "I2"},
		{params.I3, "I3"},
		{params.I4, "I4"},
		{params.I5, "I5"},
	}

	for _, item := range mapping {
		if item.val != "" {
			lines = append(lines, fmt.Sprintf("%s = %s", item.key, item.val))
		}
	}

	lines = append(lines,
		"",
		"[Peer]",
		fmt.Sprintf("PublicKey = %s", serverPubKey),
	)
	if psk != "" {
		lines = append(lines, fmt.Sprintf("PresharedKey = %s", psk))
	}
	lines = append(lines,
		"AllowedIPs = 0.0.0.0/0, ::/0",
		fmt.Sprintf("Endpoint = %s", endpoint),
		"PersistentKeepalive = 25",
	)

	return strings.Join(lines, "\n") + "\n"
}

// ParseServerConfig extracts parameters and peers from a server WireGuard config file.
func ParseServerConfig(configText string) (map[string]string, []AWGPeer, error) {
	params := make(map[string]string)
	var peers []AWGPeer

	paramMap := map[string]string{
		"listenport": "port",
		"mtu":        "mtu",
		"jc":         "junk_packet_count",
		"jmin":       "junk_packet_min_size",
		"jmax":       "junk_packet_max_size",
		"s1":         "init_packet_junk_size",
		"s2":         "response_packet_junk_size",
		"s3":         "cookie_reply_packet_junk_size",
		"s4":         "transport_packet_junk_size",
		"h1":         "init_packet_magic_header",
		"h2":         "response_packet_magic_header",
		"h3":         "underload_packet_magic_header",
		"h4":         "transport_packet_magic_header",
	}

	var currentPeer *AWGPeer
	inInterface := false

	lines := strings.Split(configText, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") || trimmed == "" {
			continue
		}

		if strings.EqualFold(trimmed, "[Interface]") {
			if currentPeer != nil {
				peers = append(peers, *currentPeer)
				currentPeer = nil
			}
			inInterface = true
			continue
		}

		if strings.EqualFold(trimmed, "[Peer]") {
			if currentPeer != nil {
				peers = append(peers, *currentPeer)
			}
			currentPeer = &AWGPeer{}
			inInterface = false
			continue
		}

		parts := strings.SplitN(trimmed, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])

		if inInterface {
			if mappedKey, ok := paramMap[strings.ToLower(key)]; ok {
				params[mappedKey] = val
			} else {
				params[key] = val
			}
		} else if currentPeer != nil {
			switch strings.ToLower(key) {
			case "publickey":
				currentPeer.PublicKey = val
			case "presharedkey":
				currentPeer.PresharedKey = val
			case "allowedips":
				currentPeer.AllowedIPs = val
			}
		}
	}

	if currentPeer != nil {
		peers = append(peers, *currentPeer)
	}

	return params, peers, nil
}

// GetUsedIPsFromConfig extracts assigned IPv4 addresses from the server configuration.
func GetUsedIPsFromConfig(configText string) []string {
	var ips []string
	ipRegex := regexp.MustCompile(`(\d+\.\d+\.\d+\.\d+)`)

	lines := strings.Split(configText, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "AllowedIPs") || strings.HasPrefix(trimmed, "Address") {
			if match := ipRegex.FindString(trimmed); match != "" {
				ips = append(ips, match)
			}
		}
	}
	return ips
}

// GetNextIP calculates the next sequential available IP in the subnet.
func GetNextIP(usedIPs []string, subnetAddr string, subnetCIDR int, gatewayIP string) (string, error) {
	cidrStr := fmt.Sprintf("%s/%d", subnetAddr, subnetCIDR)
	ip, ipNet, err := net.ParseCIDR(cidrStr)
	if err != nil {
		return "", fmt.Errorf("invalid subnet CIDR %s: %w", cidrStr, err)
	}

	usedSet := make(map[string]bool)
	for _, u := range usedIPs {
		usedSet[u] = true
	}
	if gatewayIP != "" {
		usedSet[gatewayIP] = true
	}

	// Calculate start and end IP within subnet
	current := make(net.IP, len(ip.To4()))
	copy(current, ip.To4())

	incIP := func(cur net.IP) {
		for j := len(cur) - 1; j >= 0; j-- {
			cur[j]++
			if cur[j] > 0 {
				break
			}
		}
	}

	// Skip network IP (e.g. .0)
	incIP(current)

	for ipNet.Contains(current) {
		// Check broadcast address
		isBroadcast := true
		for j := 0; j < len(current); j++ {
			if current[j] != (ipNet.IP[j] | ^ipNet.Mask[j]) {
				isBroadcast = false
				break
			}
		}
		if isBroadcast {
			break
		}

		ipStr := current.String()
		if !usedSet[ipStr] {
			return ipStr, nil
		}

		incIP(current)
	}

	return "", fmt.Errorf("subnet %s is exhausted; no more IPs available", cidrStr)
}

// ParseClientsTable parses JSON text into a list of AWGClient entries.
func ParseClientsTable(jsonData string) ([]AWGClient, error) {
	if strings.TrimSpace(jsonData) == "" {
		return []AWGClient{}, nil
	}

	var clients []AWGClient
	if err := json.Unmarshal([]byte(jsonData), &clients); err == nil {
		return clients, nil
	}

	// Fallback: try parsing legacy map[string]info format
	var legacyMap map[string]map[string]any
	if err := json.Unmarshal([]byte(jsonData), &legacyMap); err == nil {
		for clientID, info := range legacyMap {
			name := "Unknown"
			if n, ok := info["clientName"].(string); ok {
				name = n
			}
			clients = append(clients, AWGClient{
				ClientID: clientID,
				UserData: AWGClientUserData{
					ClientName: name,
					Enabled:    true,
				},
			})
		}
		return clients, nil
	}

	return nil, errors.New("failed to parse clients table JSON")
}

// SerializeClientsTable encodes clients table entries into formatted JSON.
func SerializeClientsTable(clients []AWGClient) (string, error) {
	b, err := json.MarshalIndent(clients, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to serialize clients table: %w", err)
	}
	return string(b), nil
}
