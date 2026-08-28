package cps

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
	"time"

	gossh "golang.org/x/crypto/ssh"
)

func TestGenTLS(t *testing.T) {
	pkt, err := GenTLS("example.com")
	if err != nil {
		t.Fatalf("GenTLS failed: %v", err)
	}

	if len(pkt) < 100 {
		t.Fatalf("GenTLS returned packet too short: %d bytes", len(pkt))
	}

	// Check TLS Record Header: Handshake (0x16), Version (0x0301)
	if pkt[0] != 0x16 || pkt[1] != 0x03 || pkt[2] != 0x01 {
		t.Errorf("invalid TLS record header: %x %x %x", pkt[0], pkt[1], pkt[2])
	}

	// Check Handshake Type: ClientHello (0x01)
	if pkt[5] != 0x01 {
		t.Errorf("invalid handshake type: %x", pkt[5])
	}

	// Check that domain is present in SNI
	if !bytes.Contains(pkt, []byte("example.com")) {
		t.Errorf("domain example.com not found in TLS ClientHello")
	}

	// Test default domain selection
	pktDef, err := GenTLS("")
	if err != nil {
		t.Fatalf("GenTLS with default domain failed: %v", err)
	}
	if len(pktDef) < 100 {
		t.Fatalf("GenTLS default domain returned packet too short")
	}
}

func TestGenQUICInitial(t *testing.T) {
	pkt, err := GenQUICInitial("example.com")
	if err != nil {
		t.Fatalf("GenQUICInitial failed: %v", err)
	}

	if len(pkt) != 216 {
		t.Fatalf("GenQUICInitial expected 216 bytes, got %d", len(pkt))
	}

	// First byte must have high bits set (0xC0 or 0xC3)
	if pkt[0] != 0xC0 && pkt[0] != 0xC3 {
		t.Errorf("unexpected first byte in QUIC initial: 0x%02X", pkt[0])
	}

	// QUIC v1 Version: 0x00000001 at bytes 1..4
	if !bytes.Equal(pkt[1:5], []byte{0x00, 0x00, 0x00, 0x01}) {
		t.Errorf("unexpected QUIC version: %x", pkt[1:5])
	}

	// DCID len and SCID len must be 8
	if pkt[5] != 8 || pkt[14] != 8 {
		t.Errorf("unexpected CID lengths: DCID len=%d, SCID len=%d", pkt[5], pkt[14])
	}
}

func TestGenQUICShort(t *testing.T) {
	pkt, err := GenQUICShort()
	if err != nil {
		t.Fatalf("GenQUICShort failed: %v", err)
	}

	if len(pkt) < 49 || len(pkt) > 105 {
		t.Fatalf("GenQUICShort expected 49-105 bytes, got %d", len(pkt))
	}

	// Short header first byte has fixed 0x40 bit set
	if pkt[0]&0x40 == 0 {
		t.Errorf("short header fixed bit not set: 0x%02X", pkt[0])
	}
}

func TestGenDNS(t *testing.T) {
	pkt, err := GenDNS("google.com")
	if err != nil {
		t.Fatalf("GenDNS failed: %v", err)
	}

	if len(pkt) < 20 {
		t.Fatalf("GenDNS returned packet too short: %d", len(pkt))
	}

	// Flags: 0x0100
	if pkt[0] != 0x01 || pkt[1] != 0x00 {
		t.Errorf("invalid DNS flags: %x %x", pkt[0], pkt[1])
	}

	// Check domain labels: \x06google\x03com\x00
	expectedQName := []byte("\x06google\x03com\x00")
	if !bytes.Contains(pkt, expectedQName) {
		t.Errorf("DNS QNAME not formatted correctly")
	}

	// Test default domain
	pktDef, err := GenDNS("")
	if err != nil {
		t.Fatalf("GenDNS default domain failed: %v", err)
	}
	if len(pktDef) < 20 {
		t.Fatalf("GenDNS default domain returned packet too short")
	}
}

func TestGenSIP(t *testing.T) {
	pkt, err := GenSIP("sip.example.com")
	if err != nil {
		t.Fatalf("GenSIP failed: %v", err)
	}

	str := string(pkt)
	if !strings.HasPrefix(str, "REGISTER sip:sip.example.com SIP/2.0\r\n") {
		t.Errorf("invalid SIP request line: %s", str[:40])
	}

	if !strings.Contains(str, "User-Agent:") || !strings.Contains(str, "Content-Length: 0\r\n\r\n") {
		t.Errorf("missing standard SIP headers")
	}

	// Test default domain
	pktDef, err := GenSIP("")
	if err != nil {
		t.Fatalf("GenSIP default domain failed: %v", err)
	}
	if !strings.Contains(string(pktDef), "REGISTER sip:") {
		t.Errorf("missing SIP register in default domain generation")
	}
}

func TestCPSBlobConversion(t *testing.T) {
	raw := []byte{0xde, 0xad, 0xbe, 0xef}
	tag := ToCPS(raw)
	if tag != "<b 0xdeadbeef>" {
		t.Errorf("unexpected ToCPS output: %s", tag)
	}

	parsed, err := ParseCPSBlob(tag)
	if err != nil {
		t.Fatalf("ParseCPSBlob failed: %v", err)
	}
	if !bytes.Equal(parsed, raw) {
		t.Errorf("parsed bytes mismatch: expected %x, got %x", raw, parsed)
	}

	// Test empty tag
	empty, err := ParseCPSBlob("")
	if err != nil || empty != nil {
		t.Errorf("expected nil for empty tag, got %v, err: %v", empty, err)
	}

	// Test <r N> random prefix tag
	rTag := "<r 2><b 0x11223344>"
	rParsed, err := ParseCPSBlob(rTag)
	if err != nil {
		t.Fatalf("ParseCPSBlob with <r 2> failed: %v", err)
	}
	if len(rParsed) != 4 {
		t.Fatalf("expected 4 bytes, got %d", len(rParsed))
	}
	// Bytes 2 and 3 should match 0x33, 0x44
	if rParsed[2] != 0x33 || rParsed[3] != 0x44 {
		t.Errorf("non-random suffix changed: %x", rParsed)
	}

	// Invalid tags
	if _, err := ParseCPSBlob("invalid"); err == nil {
		t.Errorf("expected error for invalid tag format")
	}
	if _, err := ParseCPSBlob("<b 0xzzzz>"); err == nil {
		t.Errorf("expected error for invalid hex in tag")
	}
}

func TestGenerateCPSPackets(t *testing.T) {
	// Lite
	litePackets, err := GenerateCPSPackets("lite", "example.com")
	if err != nil {
		t.Fatalf("GenerateCPSPackets(lite) failed: %v", err)
	}
	if !strings.HasPrefix(litePackets["i1"], "<r 2><b 0x") || litePackets["i2"] != "" {
		t.Errorf("unexpected lite packets: %+v", litePackets)
	}

	// Standard
	stdPackets, err := GenerateCPSPackets("standard", "example.com")
	if err != nil {
		t.Fatalf("GenerateCPSPackets(standard) failed: %v", err)
	}
	if !strings.HasPrefix(stdPackets["i1"], "<b 0x") || stdPackets["i2"] != "" {
		t.Errorf("unexpected standard packets: %+v", stdPackets)
	}

	// Pro
	proPackets, err := GenerateCPSPackets("pro", "example.com")
	if err != nil {
		t.Fatalf("GenerateCPSPackets(pro) failed: %v", err)
	}
	for _, k := range []string{"i1", "i2", "i3", "i4", "i5"} {
		if !strings.HasPrefix(proPackets[k], "<b 0x") {
			t.Errorf("expected pro packet for %s, got: %s", k, proPackets[k])
		}
	}
}

func TestGenerateMimicryPackets(t *testing.T) {
	ctx := context.Background()
	profiles := []string{"tls", "dns", "sip", "quic", "auto"}

	for _, proto := range profiles {
		packets, err := GenerateMimicryPackets(ctx, proto, "example.com", nil)
		if err != nil {
			t.Fatalf("GenerateMimicryPackets(%s) failed: %v", proto, err)
		}
		if packets["i1"] == "" {
			t.Errorf("expected non-empty i1 for profile %s", proto)
		}
	}
}

func TestGenerateConnectionKit(t *testing.T) {
	ctx := context.Background()
	baseConfig := `[Interface]
Address = 10.8.1.2/32
PrivateKey = aaaaaaaa
DNS = 94.140.14.14, 94.140.15.15
I1 = <b 0xold>

[Peer]
PublicKey = bbbbbbbb
Endpoint = 1.2.3.4:55424
`

	kit, err := GenerateConnectionKit(ctx, baseConfig, "example.com", nil)
	if err != nil {
		t.Fatalf("GenerateConnectionKit failed: %v", err)
	}

	for _, proto := range []string{"tls", "quic", "dns", "sip"} {
		conf, ok := kit[proto]
		if !ok {
			t.Fatalf("missing %s in connection kit", proto)
		}
		if !strings.Contains(conf, "[Interface]") || !strings.Contains(conf, "[Peer]") {
			t.Errorf("missing Interface/Peer sections in %s config", proto)
		}
		if !strings.Contains(conf, "I1 = <") {
			t.Errorf("missing I1 parameter in %s config:\n%s", proto, conf)
		}
		if strings.Contains(conf, "I1 = <b 0xold>") {
			t.Errorf("old I1 was not stripped in %s config", proto)
		}
	}
}

type mockCPSSSHClient struct {
	fail bool
}

func (m *mockCPSSSHClient) RunCommand(ctx context.Context, cmd string) (string, string, int, error) {
	if m.fail {
		return "FAIL", "", 1, nil
	}
	return "OK", "", 0, nil
}
func (m *mockCPSSSHClient) RunSudoCommand(ctx context.Context, cmd string) (string, string, int, error) {
	return m.RunCommand(ctx, cmd)
}
func (m *mockCPSSSHClient) RunScript(ctx context.Context, script string) (string, string, int, error) {
	return "OK", "", 0, nil
}
func (m *mockCPSSSHClient) RunSudoScript(ctx context.Context, script string) (string, string, int, error) {
	return "OK", "", 0, nil
}
func (m *mockCPSSSHClient) UploadFile(ctx context.Context, remotePath string, content []byte, mode os.FileMode) error {
	return nil
}
func (m *mockCPSSSHClient) UploadSudoFile(ctx context.Context, remotePath string, content []byte, mode os.FileMode) error {
	return nil
}
func (m *mockCPSSSHClient) DownloadFile(ctx context.Context, remotePath string) ([]byte, error) {
	return []byte("test"), nil
}
func (m *mockCPSSSHClient) FileExists(ctx context.Context, remotePath string) (bool, error) {
	return true, nil
}
func (m *mockCPSSSHClient) TestConnection(ctx context.Context) (string, error) {
	return "Linux", nil
}
func (m *mockCPSSSHClient) Close() error {
	return nil
}
func (m *mockCPSSSHClient) IsAlive() bool {
	return true
}
func (m *mockCPSSSHClient) GetUnderlyingClient() *gossh.Client {
	return nil
}
func (m *mockCPSSSHClient) GetHost() string {
	return "127.0.0.1"
}
func (m *mockCPSSSHClient) GetPort() int {
	return 22
}
func (m *mockCPSSSHClient) GetUser() string {
	return "root"
}
func (m *mockCPSSSHClient) GetServerID() *int64 {
	return nil
}
func (m *mockCPSSSHClient) GetLastActive() time.Time {
	return time.Now()
}

func TestSelectMimicryDomain(t *testing.T) {
	ctx := context.Background()

	// Nil client -> fallback
	d, err := SelectMimicryDomain(ctx, nil, "tls")
	if err != nil || d != "www.google.com" {
		t.Errorf("expected www.google.com, got %s (err: %v)", d, err)
	}

	// Active client returning OK
	mockSuccess := &mockCPSSSHClient{fail: false}
	for _, proto := range []string{"tls", "quic", "dns", "sip", "unknown"} {
		d, err := SelectMimicryDomain(ctx, mockSuccess, proto)
		if err != nil || d == "" {
			t.Errorf("SelectMimicryDomain(%s) failed: %v", proto, err)
		}
	}

	// Active client returning FAIL -> fallback
	mockFail := &mockCPSSSHClient{fail: true}
	dFail, err := SelectMimicryDomain(ctx, mockFail, "sip")
	if err != nil || dFail != "sip.linphone.org" {
		t.Errorf("expected sip.linphone.org fallback, got %s", dFail)
	}

	// GenerateMimicryPackets with active SSH client auto profile
	mp, err := GenerateMimicryPackets(ctx, "auto", "", mockSuccess)
	if err != nil || mp["i1"] == "" {
		t.Errorf("GenerateMimicryPackets(auto) failed: %v", err)
	}
}
