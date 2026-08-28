package mtproxyl

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/devops-igor/amnezia-web-ui-go/internal/manager/ssh"
	"github.com/devops-igor/amnezia-web-ui-go/internal/models"
	gossh "golang.org/x/crypto/ssh"
)

func TestSecretsFileRoundtrip(t *testing.T) {
	sf := NewSecretsFile()
	raw := `# Comments line
tg_proxy|dd1234567890abcdef1234567890abcdef|2026-08-25T10:00:00|true|10|5|10737418240|2026-12-31|Admin User
alice|ee1234567890abcdef1234567890abcdef|2026-08-25T11:00:00|false|0|0|0|0|Test Alice
`

	entries, err := sf.Parse(raw)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}

	if entries[0].Label != "tg_proxy" || !entries[0].Enabled || entries[0].QuotaBytes != 10737418240 || entries[0].MaxConns != 10 {
		t.Errorf("entry 0 mismatch: %+v", entries[0])
	}
	if entries[1].Label != "alice" || entries[1].Enabled || entries[1].Expires != "0" {
		t.Errorf("entry 1 mismatch: %+v", entries[1])
	}

	serialized := sf.Serialize(entries)
	parsedAgain, err := sf.Parse(serialized)
	if err != nil {
		t.Fatalf("Parse serialized failed: %v", err)
	}

	if len(parsedAgain) != 2 || parsedAgain[0].Label != "tg_proxy" || parsedAgain[1].Label != "alice" {
		t.Errorf("roundtrip mismatch: %+v", parsedAgain)
	}
}

func TestParseTraffic(t *testing.T) {
	output := `
● tg_proxy: ↓ 1.96 ГБ  ↑ 96.64 ГБ  соед: 41
● user_small: ↓ 500 КБ  ↑ 2.50 МБ  соед: 2
● user_bytes: ↓ 100 Б  ↑ 200 Б  соед: 0
● user_tb: ↓ 1.50 ТБ  ↑ 2.00 ТБ  соед: 10
`

	stats, err := ParseTraffic(output)
	if err != nil {
		t.Fatalf("ParseTraffic failed: %v", err)
	}

	if len(stats) != 4 {
		t.Fatalf("expected 4 user stats, got %d", len(stats))
	}

	tgStat := stats["tg_proxy"]
	if tgStat.Connections != 41 {
		t.Errorf("tg_proxy connections expected 41, got %d", tgStat.Connections)
	}
	fTG := 1.96*1073741824.0 + 96.64*1073741824.0
	expectedTGBytes := int64(fTG)
	if tgStat.TotalBytes != expectedTGBytes {
		t.Errorf("tg_proxy total bytes expected %d, got %d", expectedTGBytes, tgStat.TotalBytes)
	}

	smallStat := stats["user_small"]
	if smallStat.Connections != 2 {
		t.Errorf("user_small connections expected 2, got %d", smallStat.Connections)
	}
	fSmall := 500.0*1024.0 + 2.50*1048576.0
	expectedSmall := int64(fSmall)
	if smallStat.TotalBytes != expectedSmall {
		t.Errorf("user_small total bytes expected %d, got %d", expectedSmall, smallStat.TotalBytes)
	}

	byteStat := stats["user_bytes"]
	if byteStat.TotalBytes != 300 {
		t.Errorf("user_bytes total expected 300, got %d", byteStat.TotalBytes)
	}
}

func TestParseConnections(t *testing.T) {
	output := `ПОЛЬЗОВАТЕЛЬ   СОЕД.   СКАЧАНО   ОТПРАВЛЕНО
─────────────────────────────────────────────
tg_proxy          6     1.68 МБ    61.83 МБ
alice            12     500 КБ     2.00 МБ
Всего            18
`

	conns, err := ParseConnections(output)
	if err != nil {
		t.Fatalf("ParseConnections failed: %v", err)
	}

	if conns["tg_proxy"] != 6 {
		t.Errorf("tg_proxy expected 6, got %d", conns["tg_proxy"])
	}
	if conns["alice"] != 12 {
		t.Errorf("alice expected 12, got %d", conns["alice"])
	}
	if _, ok := conns["Всего"]; ok {
		t.Errorf("Всего summary should not be in connections map")
	}
}

func TestDisableOverquotaUsers(t *testing.T) {
	secrets := []SecretEntry{
		{
			Label:      "user_over",
			Enabled:    true,
			QuotaBytes: 1000,
		},
		{
			Label:      "user_ok",
			Enabled:    true,
			QuotaBytes: 5000,
		},
		{
			Label:      "user_disabled_already",
			Enabled:    false,
			QuotaBytes: 500,
		},
	}

	traffic := map[string]TrafficStats{
		"user_over":             {TotalBytes: 2000},
		"user_ok":               {TotalBytes: 1000},
		"user_disabled_already": {TotalBytes: 2000},
	}

	mockSSH := &mockMTProxyLSSHClient{}
	disabled, err := DisableOverquotaUsers(context.Background(), mockSSH, DefaultCLIPath, secrets, traffic)
	if err != nil {
		t.Fatalf("DisableOverquotaUsers failed: %v", err)
	}

	if len(disabled) != 1 || disabled[0] != "user_over" {
		t.Errorf("expected only user_over disabled, got: %+v", disabled)
	}
}

type mockMTProxyLSSHClient struct {
	commandsRun []string
}

func (m *mockMTProxyLSSHClient) RunCommand(ctx context.Context, cmd string) (string, string, int, error) {
	m.commandsRun = append(m.commandsRun, cmd)
	if strings.Contains(cmd, "test -f") {
		return "found", "", 0, nil
	}
	if strings.Contains(cmd, "cat /opt/mtproxyl/secrets.conf") {
		return "tg_proxy|dd1234|2026-08-25|true|10|5|10737418240|2026-12-31|Notes\n", "", 0, nil
	}
	if strings.Contains(cmd, "traffic") {
		return "● tg_proxy: ↓ 1.96 ГБ  ↑ 96.64 ГБ  соед: 41\n", "", 0, nil
	}
	if strings.Contains(cmd, "connections") {
		return "ПОЛЬЗОВАТЕЛЬ СОЕД.\n─────────────────\ntg_proxy   6\n", "", 0, nil
	}
	if strings.Contains(cmd, "secret add") {
		return "Added secret. Link: tg://proxy?server=1.2.3.4&port=443&secret=dd1234\n", "", 0, nil
	}
	if strings.Contains(cmd, "secret link") {
		return "tg://proxy?server=1.2.3.4&port=443&secret=dd1234\n", "", 0, nil
	}
	if strings.Contains(cmd, "status --json") {
		return `{"status":"running","port":443,"domain":"cloudflare.com"}`, "", 0, nil
	}
	return "OK", "", 0, nil
}

func (m *mockMTProxyLSSHClient) RunSudoCommand(ctx context.Context, cmd string) (string, string, int, error) {
	return m.RunCommand(ctx, cmd)
}

func (m *mockMTProxyLSSHClient) RunScript(ctx context.Context, script string) (string, string, int, error) {
	return "OK", "", 0, nil
}

func (m *mockMTProxyLSSHClient) RunSudoScript(ctx context.Context, script string) (string, string, int, error) {
	return "OK", "", 0, nil
}

func (m *mockMTProxyLSSHClient) UploadFile(ctx context.Context, remotePath string, content []byte, mode os.FileMode) error {
	return nil
}

func (m *mockMTProxyLSSHClient) UploadSudoFile(ctx context.Context, remotePath string, content []byte, mode os.FileMode) error {
	return nil
}

func (m *mockMTProxyLSSHClient) DownloadFile(ctx context.Context, remotePath string) ([]byte, error) {
	return []byte("test"), nil
}

func (m *mockMTProxyLSSHClient) FileExists(ctx context.Context, remotePath string) (bool, error) {
	return true, nil
}

func (m *mockMTProxyLSSHClient) TestConnection(ctx context.Context) (string, error) {
	return "Linux", nil
}

func (m *mockMTProxyLSSHClient) Close() error {
	return nil
}

func (m *mockMTProxyLSSHClient) IsAlive() bool {
	return true
}

func (m *mockMTProxyLSSHClient) GetUnderlyingClient() *gossh.Client {
	return nil
}

func (m *mockMTProxyLSSHClient) GetHost() string {
	return "127.0.0.1"
}

func (m *mockMTProxyLSSHClient) GetPort() int {
	return 22
}

func (m *mockMTProxyLSSHClient) GetUser() string {
	return "root"
}

func (m *mockMTProxyLSSHClient) GetServerID() *int64 {
	return nil
}

func (m *mockMTProxyLSSHClient) GetLastActive() time.Time {
	return time.Now()
}

type mockMTProxyLSSHProvider struct {
	client *mockMTProxyLSSHClient
}

func (p *mockMTProxyLSSHProvider) Get(ctx context.Context, server *models.Server) (ssh.SSHClient, error) {
	if p.client == nil {
		p.client = &mockMTProxyLSSHClient{}
	}
	return p.client, nil
}

func TestMTProxyLManagerLifecycle(t *testing.T) {
	ctx := context.Background()
	provider := &mockMTProxyLSSHProvider{}
	mgr := NewMTProxyLManager(provider)

	server := &models.Server{
		ID:      1,
		Host:    "1.2.3.4",
		SSHPort: 22,
		SSHUser: "root",
	}

	if proto := mgr.Protocol(); proto != "telemt" {
		t.Errorf("expected protocol telemt, got %s", proto)
	}

	// 1. Install
	installParams := map[string]any{
		"port":       "443",
		"tls_domain": "cloudflare.com",
	}
	if err := mgr.Install(ctx, server, installParams); err != nil {
		t.Fatalf("Install failed: %v", err)
	}

	// 2. GetServerStatus
	status, err := mgr.GetServerStatus(ctx, server)
	if err != nil {
		t.Fatalf("GetServerStatus failed: %v", err)
	}
	if running, ok := status["container_running"].(bool); !ok || !running {
		t.Errorf("expected container_running true, got: %v", status)
	}

	// 3. GetClients
	clients, err := mgr.GetClients(ctx, server)
	if err != nil {
		t.Fatalf("GetClients failed: %v", err)
	}
	if len(clients) != 1 || clients[0]["clientId"] != "tg_proxy" {
		t.Errorf("unexpected clients: %+v", clients)
	}

	// 4. AddClient
	addParams := map[string]any{
		"name":           "bob",
		"telemt_quota":   1073741824,
		"telemt_max_ips": 3,
		"telemt_expiry":  "2026-12-31",
	}
	res, err := mgr.AddClient(ctx, server, addParams)
	if err != nil {
		t.Fatalf("AddClient failed: %v", err)
	}
	if res["client_id"] != "bob" || !strings.HasPrefix(res["config"].(string), "tg://proxy") {
		t.Errorf("unexpected AddClient result: %+v", res)
	}

	// 5. GetClientConfig
	conf, err := mgr.GetClientConfig(ctx, server, "bob")
	if err != nil || !strings.HasPrefix(conf, "tg://proxy") {
		t.Errorf("GetClientConfig failed: %v, conf: %s", err, conf)
	}

	// 6. ToggleClient
	if err := mgr.ToggleClient(ctx, server, "bob", false); err != nil {
		t.Fatalf("ToggleClient(disable) failed: %v", err)
	}
	if err := mgr.ToggleClient(ctx, server, "bob", true); err != nil {
		t.Fatalf("ToggleClient(enable) failed: %v", err)
	}

	// 7. EditClient
	editParams := map[string]any{
		"telemt_quota": 2048,
	}
	if err := mgr.EditClient(ctx, server, "bob", editParams); err != nil {
		t.Fatalf("EditClient failed: %v", err)
	}

	// 8. RemoveClient
	if err := mgr.RemoveClient(ctx, server, "bob"); err != nil {
		t.Fatalf("RemoveClient failed: %v", err)
	}

	// 9. Uninstall
	if err := mgr.Uninstall(ctx, server); err != nil {
		t.Fatalf("Uninstall failed: %v", err)
	}

	// 10. Nil server / pool errors
	nilMgr := NewMTProxyLManager(nil)
	if err := nilMgr.Install(ctx, server, nil); err == nil {
		t.Errorf("expected error for nil ssh pool")
	}
	if err := mgr.Install(ctx, nil, nil); err == nil {
		t.Errorf("expected error for nil server")
	}
}

type bwConflictSSHClient struct {
	mockMTProxyLSSHClient
}

func (b *bwConflictSSHClient) RunCommand(ctx context.Context, cmd string) (string, string, int, error) {
	if strings.Contains(cmd, "test -f") {
		return "not_found", "", 0, nil
	}
	if strings.Contains(cmd, "bunkerweb") {
		return "bunkerweb", "", 0, nil
	}
	if strings.Contains(cmd, "status --json") {
		return "invalid-json", "", 0, nil
	}
	return b.mockMTProxyLSSHClient.RunCommand(ctx, cmd)
}

type bwConflictSSHProvider struct {
	client *bwConflictSSHClient
}

func (p *bwConflictSSHProvider) Get(ctx context.Context, server *models.Server) (ssh.SSHClient, error) {
	return p.client, nil
}

func TestMTProxyL_BunkerWebAndInstall(t *testing.T) {
	ctx := context.Background()
	server := &models.Server{ID: 1, Host: "1.2.3.4"}
	provider := &bwConflictSSHProvider{client: &bwConflictSSHClient{}}
	mgr := NewMTProxyLManager(provider)

	// Test install with missing binary and port 443 bunkerweb conflict
	if err := mgr.Install(ctx, server, map[string]any{"port": "443", "tls_domain": "bing.com"}); err != nil {
		t.Fatalf("Install with bunkerweb conflict failed: %v", err)
	}

	// Test GetServerStatus with invalid json
	status, err := mgr.GetServerStatus(ctx, server)
	if err != nil {
		t.Fatalf("GetServerStatus failed: %v", err)
	}
	if running, _ := status["container_running"].(bool); running {
		t.Errorf("expected running false on invalid status json")
	}
}
