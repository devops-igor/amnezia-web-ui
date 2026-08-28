package tc

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	gossh "golang.org/x/crypto/ssh"
)

type mockSSHClient struct {
	commandsRun []string
	scriptsRun  []string
}

func (m *mockSSHClient) RunCommand(ctx context.Context, cmd string) (string, string, int, error) {
	m.commandsRun = append(m.commandsRun, cmd)
	if strings.Contains(cmd, "filter show") {
		return "filter parent 1: protocol ip pref 1 u32 fh 800::800 order 2048 key ht 800 bkt 0 flowid 1:145", "", 0, nil
	}
	return "OK", "", 0, nil
}

func (m *mockSSHClient) RunSudoCommand(ctx context.Context, cmd string) (string, string, int, error) {
	m.commandsRun = append(m.commandsRun, "sudo: "+cmd)
	if strings.Contains(cmd, "filter show") {
		return "filter parent 1: protocol ip pref 1 u32 fh 800::800 order 2048 key ht 800 bkt 0 flowid 1:145", "", 0, nil
	}
	return "OK", "", 0, nil
}

func (m *mockSSHClient) RunScript(ctx context.Context, script string) (string, string, int, error) {
	m.scriptsRun = append(m.scriptsRun, script)
	return "OK", "", 0, nil
}

func (m *mockSSHClient) RunSudoScript(ctx context.Context, script string) (string, string, int, error) {
	m.scriptsRun = append(m.scriptsRun, "sudo: "+script)
	return "OK", "", 0, nil
}

func (m *mockSSHClient) UploadFile(ctx context.Context, remotePath string, content []byte, mode os.FileMode) error {
	return nil
}

func (m *mockSSHClient) UploadSudoFile(ctx context.Context, remotePath string, content []byte, mode os.FileMode) error {
	return nil
}

func (m *mockSSHClient) DownloadFile(ctx context.Context, remotePath string) ([]byte, error) {
	return []byte("test"), nil
}

func (m *mockSSHClient) FileExists(ctx context.Context, remotePath string) (bool, error) {
	return true, nil
}

func (m *mockSSHClient) TestConnection(ctx context.Context) (string, error) {
	return "Linux", nil
}

func (m *mockSSHClient) Close() error {
	return nil
}

func (m *mockSSHClient) IsAlive() bool {
	return true
}

func (m *mockSSHClient) GetUnderlyingClient() *gossh.Client {
	return nil
}

func (m *mockSSHClient) GetHost() string {
	return "127.0.0.1"
}

func (m *mockSSHClient) GetPort() int {
	return 22
}

func (m *mockSSHClient) GetUser() string {
	return "root"
}

func (m *mockSSHClient) GetServerID() *int64 {
	return nil
}

func (m *mockSSHClient) GetLastActive() time.Time {
	return time.Now()
}

func TestPeerToClassID(t *testing.T) {
	tests := []struct {
		ip      string
		want    int
		wantErr bool
	}{
		{"10.8.1.45", 145, false},
		{"10.8.1.1", 101, false},
		{"10.8.1.253", 353, false},
		{"10.8.1.0", 0, true},
		{"10.8.1.254", 0, true},
		{"invalid.ip", 0, true},
		{"10.8.1", 0, true},
	}

	for _, tt := range tests {
		got, err := PeerToClassID(tt.ip)
		if (err != nil) != tt.wantErr {
			t.Errorf("PeerToClassID(%q) error = %v, wantErr %v", tt.ip, err, tt.wantErr)
			continue
		}
		if !tt.wantErr && got != tt.want {
			t.Errorf("PeerToClassID(%q) = %d, want %d", tt.ip, got, tt.want)
		}
	}
}

func TestBuildBatchTCScript(t *testing.T) {
	clients := []map[string]any{
		{
			"clientIp": "10.8.1.5",
			"userData": map[string]any{
				"speed_limit_down": 20,
				"speed_limit_up":   10,
			},
		},
		{
			"userData": map[string]any{
				"clientIp":         "10.8.1.6",
				"speed_limit_down": 50,
			},
		},
		{
			"clientIp": "10.8.1.7",
		},
	}

	downLimit := 100
	upLimit := 50
	infra, client := BuildBatchTCScript("amnezia-awg", clients, &downLimit, &upLimit)

	if !strings.Contains(infra, "100mbit") || !strings.Contains(infra, "50mbit") {
		t.Errorf("infra script missing global limits")
	}
	if !strings.Contains(client, "10.8.1.5") || !strings.Contains(client, "classid 1:105") {
		t.Errorf("client script missing peer 10.8.1.5 rules")
	}
	if !strings.Contains(client, "10.8.1.6") || !strings.Contains(client, "classid 1:106") {
		t.Errorf("client script missing peer 10.8.1.6 rules")
	}
	if strings.Contains(client, "10.8.1.7") {
		t.Errorf("client script should not contain unlimited peer 10.8.1.7")
	}
}

func TestTCOperationsWithMockSSH(t *testing.T) {
	ctx := context.Background()
	mockSSH := &mockSSHClient{}

	if err := SetupIFB(ctx, mockSSH, "amnezia-awg"); err != nil {
		t.Fatalf("SetupIFB failed: %v", err)
	}

	limit := 100
	if err := SetupQdisc(ctx, mockSSH, "amnezia-awg", "awg0", &limit); err != nil {
		t.Fatalf("SetupQdisc failed: %v", err)
	}

	if err := ApplySpeedLimit(ctx, mockSSH, "amnezia-awg", "awg0", "10.8.1.45", 20, 10); err != nil {
		t.Fatalf("ApplySpeedLimit failed: %v", err)
	}

	if err := RemoveSpeedLimit(ctx, mockSSH, "amnezia-awg", "awg0", "10.8.1.45"); err != nil {
		t.Fatalf("RemoveSpeedLimit failed: %v", err)
	}

	dLimit := 500
	uLimit := 200
	if err := SetGlobalLimit(ctx, mockSSH, "amnezia-awg", &dLimit, &uLimit); err != nil {
		t.Fatalf("SetGlobalLimit failed: %v", err)
	}

	if err := ReapplyAllLimits(ctx, mockSSH, "amnezia-awg", "awg0", nil, &dLimit, &uLimit); err != nil {
		t.Fatalf("ReapplyAllLimits failed: %v", err)
	}

	if err := TeardownIFB(ctx, mockSSH, "amnezia-awg"); err != nil {
		t.Fatalf("TeardownIFB failed: %v", err)
	}

	// Test with default container and interface
	if err := SetupIFB(ctx, mockSSH, ""); err != nil {
		t.Fatalf("SetupIFB with empty container failed: %v", err)
	}
	if err := SetupQdisc(ctx, mockSSH, "", "", nil); err != nil {
		t.Fatalf("SetupQdisc with empty container/iface failed: %v", err)
	}
	if err := ApplySpeedLimit(ctx, mockSSH, "", "", "10.8.1.10", 15, 15); err != nil {
		t.Fatalf("ApplySpeedLimit with empty container/iface failed: %v", err)
	}
	if err := RemoveSpeedLimit(ctx, mockSSH, "", "", "10.8.1.10"); err != nil {
		t.Fatalf("RemoveSpeedLimit with empty container/iface failed: %v", err)
	}
	if err := SetGlobalLimit(ctx, mockSSH, "", nil, nil); err != nil {
		t.Fatalf("SetGlobalLimit with nil limits failed: %v", err)
	}
	if err := TeardownIFB(ctx, mockSSH, ""); err != nil {
		t.Fatalf("TeardownIFB with empty container failed: %v", err)
	}

	// Test invalid IP
	if err := ApplySpeedLimit(ctx, mockSSH, "", "", "invalid.ip", 10, 10); err == nil {
		t.Errorf("expected error for invalid IP")
	}
	if err := RemoveSpeedLimit(ctx, mockSSH, "", "", "invalid.ip"); err == nil {
		t.Errorf("expected error for invalid IP")
	}
}

type errorSSHClient struct {
	mockSSHClient
}

func (e *errorSSHClient) RunSudoCommand(ctx context.Context, cmd string) (string, string, int, error) {
	return "", "Simulated command failure", 1, nil
}
func (e *errorSSHClient) RunSudoScript(ctx context.Context, script string) (string, string, int, error) {
	return "", "Simulated script failure", 1, nil
}

func TestTCOperations_Errors(t *testing.T) {
	ctx := context.Background()
	errClient := &errorSSHClient{}

	if err := SetupIFB(ctx, errClient, "amnezia-awg"); err == nil {
		t.Errorf("expected error from SetupIFB")
	}

	limit := 50
	if err := SetupQdisc(ctx, errClient, "amnezia-awg", "awg0", &limit); err == nil {
		t.Errorf("expected error from SetupQdisc")
	}

	if err := ApplySpeedLimit(ctx, errClient, "amnezia-awg", "awg0", "10.8.1.5", 10, 10); err == nil {
		t.Errorf("expected error from ApplySpeedLimit")
	}

	if err := ReapplyAllLimits(ctx, errClient, "amnezia-awg", "awg0", nil, &limit, &limit); err == nil {
		t.Errorf("expected error from ReapplyAllLimits")
	}
}
