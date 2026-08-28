package awg

import (
	"strings"
	"testing"
)

func TestRenderServerConfig(t *testing.T) {
	params := &AWGParams{
		JunkPacketCount:           "4",
		JunkPacketMinSize:         "30",
		JunkPacketMaxSize:         "80",
		InitPacketJunkSize:        "40",
		ResponsePacketJunkSize:    "60",
		InitPacketMagicHeader:     "12345",
		ResponsePacketMagicHeader: "67890",
		I1:                        "<b 0xdeadbeef>", // Should NOT be in server config!
	}

	peers := []AWGPeer{
		{
			PublicKey:    "pubkey1",
			PresharedKey: "psk1",
			AllowedIPs:   "10.8.1.2/32",
		},
	}

	conf := RenderServerConfig("serverPrivKey", "10.8.1.1", "24", "55424", "1280", params, peers)

	if !strings.Contains(conf, "[Interface]") || !strings.Contains(conf, "[Peer]") {
		t.Errorf("missing sections in server config")
	}
	if !strings.Contains(conf, "PrivateKey = serverPrivKey") {
		t.Errorf("missing private key")
	}
	if !strings.Contains(conf, "PublicKey = pubkey1") {
		t.Errorf("missing peer public key")
	}
	if strings.Contains(conf, "I1") || strings.Contains(conf, "deadbeef") {
		t.Errorf("server config must NEVER contain I1-I5 signatures")
	}
}

func TestRenderClientConfig(t *testing.T) {
	params := &AWGParams{
		JunkPacketCount:           "4",
		JunkPacketMinSize:         "30",
		JunkPacketMaxSize:         "80",
		InitPacketJunkSize:        "40",
		ResponsePacketJunkSize:    "60",
		InitPacketMagicHeader:     "12345",
		ResponsePacketMagicHeader: "67890",
		I1:                        "<b 0xdeadbeef>",
	}

	conf := RenderClientConfig("clientPrivKey", "10.8.1.2", "serverPubKey", "psk1", "1.2.3.4:55424", "94.140.14.14", "94.140.15.15", "1280", params)

	if !strings.Contains(conf, "Address = 10.8.1.2/32") {
		t.Errorf("missing client Address")
	}
	if !strings.Contains(conf, "I1 = <b 0xdeadbeef>") {
		t.Errorf("missing I1 in client config")
	}
	if !strings.Contains(conf, "Endpoint = 1.2.3.4:55424") {
		t.Errorf("missing Endpoint in client config")
	}
}

func TestParseServerConfig(t *testing.T) {
	confText := `
[Interface]
PrivateKey = sPriv
Address = 10.8.1.1/24
ListenPort = 55424
MTU = 1280
Jc = 4
Jmin = 30
Jmax = 80
S1 = 40
S2 = 60
H1 = 12345
H2 = 67890

[Peer]
PublicKey = pKey1
PresharedKey = psk1
AllowedIPs = 10.8.1.2/32

[Peer]
PublicKey = pKey2
AllowedIPs = 10.8.1.3/32
`

	params, peers, err := ParseServerConfig(confText)
	if err != nil {
		t.Fatalf("ParseServerConfig failed: %v", err)
	}

	if params["port"] != "55424" || params["junk_packet_count"] != "4" || params["init_packet_magic_header"] != "12345" {
		t.Errorf("unexpected parsed params: %+v", params)
	}

	if len(peers) != 2 {
		t.Fatalf("expected 2 peers, got %d", len(peers))
	}
	if peers[0].PublicKey != "pKey1" || peers[0].PresharedKey != "psk1" {
		t.Errorf("unexpected peer 1: %+v", peers[0])
	}
	if peers[1].PublicKey != "pKey2" || peers[1].PresharedKey != "" {
		t.Errorf("unexpected peer 2: %+v", peers[1])
	}
}

func TestGetNextIP(t *testing.T) {
	usedIPs := []string{"10.8.1.2", "10.8.1.3"}
	nextIP, err := GetNextIP(usedIPs, "10.8.1.0", 24, "10.8.1.1")
	if err != nil {
		t.Fatalf("GetNextIP failed: %v", err)
	}
	if nextIP != "10.8.1.4" {
		t.Errorf("expected 10.8.1.4, got %s", nextIP)
	}

	// Test subnet exhaustion on /30 (usable: .1 gateway, .2 client)
	usedAll := []string{"10.8.1.2"}
	_, err = GetNextIP(usedAll, "10.8.1.0", 30, "10.8.1.1")
	if err == nil {
		t.Errorf("expected error on subnet exhaustion")
	}
}

func TestClientsTableSerialization(t *testing.T) {
	clients := []AWGClient{
		{
			ClientID: "pubkey1",
			UserData: AWGClientUserData{
				ClientName:       "User1",
				ClientPrivateKey: "privkey1",
				ClientIP:         "10.8.1.2",
				Enabled:          true,
			},
		},
	}

	jsonStr, err := SerializeClientsTable(clients)
	if err != nil {
		t.Fatalf("SerializeClientsTable failed: %v", err)
	}

	parsed, err := ParseClientsTable(jsonStr)
	if err != nil {
		t.Fatalf("ParseClientsTable failed: %v", err)
	}
	if len(parsed) != 1 || parsed[0].ClientID != "pubkey1" || parsed[0].UserData.ClientName != "User1" {
		t.Errorf("parsed clients mismatch: %+v", parsed)
	}

	// Test legacy dict format
	legacyJSON := `{"client1": {"clientName": "LegacyUser"}}`
	parsedLegacy, err := ParseClientsTable(legacyJSON)
	if err != nil || len(parsedLegacy) != 1 || parsedLegacy[0].UserData.ClientName != "LegacyUser" {
		t.Errorf("failed to parse legacy JSON format: %v, %+v", err, parsedLegacy)
	}
}
