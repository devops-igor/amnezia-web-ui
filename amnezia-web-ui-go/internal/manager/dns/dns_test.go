package dns

import (
	"context"
	"errors"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/devops-igor/amnezia-web-ui-go/internal/manager/ssh"
	"github.com/devops-igor/amnezia-web-ui-go/internal/models"
	gossh "golang.org/x/crypto/ssh"
)

func TestRenderForwardRecords(t *testing.T) {
	conf := RenderForwardRecords("1.1.1.1", "1.0.0.1")
	if !strings.Contains(conf, "forward-tls-upstream: yes") {
		t.Errorf("missing forward-tls-upstream")
	}
	if !strings.Contains(conf, "forward-addr: 1.1.1.1@853") || !strings.Contains(conf, "forward-addr: 1.0.0.1@853") {
		t.Errorf("missing forward-addr in conf: %s", conf)
	}

	confDef := RenderForwardRecords("", "")
	if !strings.Contains(confDef, DefaultDNS1) || !strings.Contains(confDef, DefaultDNS2) {
		t.Errorf("default DNS servers not used: %s", confDef)
	}
}

func TestRenderDockerfile(t *testing.T) {
	df := RenderDockerfile()
	if !strings.Contains(df, "FROM mvance/unbound:latest") || !strings.Contains(df, "COPY forward-records.conf") {
		t.Errorf("unexpected Dockerfile: %s", df)
	}
}

func TestProbeDNSQuery(t *testing.T) {
	// Start mock UDP DNS server
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen UDP: %v", err)
	}
	defer func() {
		_ = pc.Close()
	}()

	host, portStr, _ := net.SplitHostPort(pc.LocalAddr().String())
	port := 0
	if addr, ok := pc.LocalAddr().(*net.UDPAddr); ok {
		port = addr.Port
	}
	_ = portStr

	go func() {
		buf := make([]byte, 1024)
		for {
			n, clientAddr, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			if n < 12 {
				continue
			}

			// Echo request back with QR bit set (0x80)
			resp := make([]byte, n)
			copy(resp, buf[:n])
			resp[2] |= 0x80 // QR = Response
			_, _ = pc.WriteTo(resp, clientAddr)
		}
	}()

	rtt, err := ProbeDNSQuery(context.Background(), host, port, "example.com", 2*time.Second)
	if err != nil {
		t.Fatalf("ProbeDNSQuery failed: %v", err)
	}

	if rtt <= 0 {
		t.Errorf("expected positive RTT, got: %v", rtt)
	}
}

type mockDNSSSHClient struct {
	commandsRun []string
}

func (m *mockDNSSSHClient) RunCommand(ctx context.Context, cmd string) (string, string, int, error) {
	m.commandsRun = append(m.commandsRun, cmd)
	if strings.Contains(cmd, "docker --version") {
		return "Docker version 24.0.5", "", 0, nil
	}
	return "OK", "", 0, nil
}

func (m *mockDNSSSHClient) RunSudoCommand(ctx context.Context, cmd string) (string, string, int, error) {
	m.commandsRun = append(m.commandsRun, "sudo: "+cmd)
	if strings.Contains(cmd, "docker ps --filter name=^amnezia-dns$") {
		return "Up 5 hours", "", 0, nil
	}
	if strings.Contains(cmd, "docker ps -a --filter name=^amnezia-dns$") {
		return "amnezia-dns", "", 0, nil
	}
	return "OK", "", 0, nil
}

func (m *mockDNSSSHClient) RunScript(ctx context.Context, script string) (string, string, int, error) {
	return "OK", "", 0, nil
}

func (m *mockDNSSSHClient) RunSudoScript(ctx context.Context, script string) (string, string, int, error) {
	return "OK", "", 0, nil
}

func (m *mockDNSSSHClient) UploadFile(ctx context.Context, remotePath string, content []byte, mode os.FileMode) error {
	return nil
}

func (m *mockDNSSSHClient) UploadSudoFile(ctx context.Context, remotePath string, content []byte, mode os.FileMode) error {
	return nil
}

func (m *mockDNSSSHClient) DownloadFile(ctx context.Context, remotePath string) ([]byte, error) {
	return []byte("test"), nil
}

func (m *mockDNSSSHClient) FileExists(ctx context.Context, remotePath string) (bool, error) {
	return true, nil
}

func (m *mockDNSSSHClient) TestConnection(ctx context.Context) (string, error) {
	return "Linux", nil
}

func (m *mockDNSSSHClient) Close() error {
	return nil
}

func (m *mockDNSSSHClient) IsAlive() bool {
	return true
}

func (m *mockDNSSSHClient) GetUnderlyingClient() *gossh.Client {
	return nil
}

func (m *mockDNSSSHClient) GetHost() string {
	return "127.0.0.1"
}

func (m *mockDNSSSHClient) GetPort() int {
	return 22
}

func (m *mockDNSSSHClient) GetUser() string {
	return "root"
}

func (m *mockDNSSSHClient) GetServerID() *int64 {
	return nil
}

func (m *mockDNSSSHClient) GetLastActive() time.Time {
	return time.Now()
}

type mockDNSSSHProvider struct {
	client *mockDNSSSHClient
}

func (p *mockDNSSSHProvider) Get(ctx context.Context, server *models.Server) (ssh.SSHClient, error) {
	if p.client == nil {
		p.client = &mockDNSSSHClient{}
	}
	return p.client, nil
}

func TestDNSManagerLifecycle(t *testing.T) {
	ctx := context.Background()
	provider := &mockDNSSSHProvider{}
	mgr := NewDNSManager(provider)

	server := &models.Server{
		ID:      1,
		Host:    "1.2.3.4",
		SSHPort: 22,
		SSHUser: "root",
	}

	if proto := mgr.Protocol(); proto != "dns" {
		t.Errorf("expected protocol dns, got %s", proto)
	}

	// 1. Install
	installParams := map[string]any{
		"dns1": "1.1.1.1",
		"dns2": "1.0.0.1",
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
	if err != nil || len(clients) != 0 {
		t.Errorf("GetClients for DNS should return empty list, got: %+v", clients)
	}

	// 4. AddClient
	res, err := mgr.AddClient(ctx, server, nil)
	if err != nil || res["dns_ip"] != DNSStaticIP {
		t.Errorf("AddClient unexpected result: %+v", res)
	}

	// 5. GetClientConfig
	conf, err := mgr.GetClientConfig(ctx, server, "")
	if err != nil || conf != DNSStaticIP {
		t.Errorf("GetClientConfig unexpected result: %s", conf)
	}

	// 6. RemoveClient
	if err := mgr.RemoveClient(ctx, server, "client1"); err != nil {
		t.Errorf("RemoveClient failed: %v", err)
	}

	// 7. Uninstall
	if err := mgr.Uninstall(ctx, server); err != nil {
		t.Fatalf("Uninstall failed: %v", err)
	}

	// 8. Test nil server / nil pool errors
	nilMgr := NewDNSManager(nil)
	if err := nilMgr.Install(ctx, server, nil); err == nil {
		t.Errorf("expected error for nil ssh pool")
	}
	if err := mgr.Install(ctx, nil, nil); err == nil {
		t.Errorf("expected error for nil server")
	}

	// 9. ProbeDNSQuery timeout / closed port
	if _, err := ProbeDNSQuery(ctx, "127.0.0.1", 1, "test.com", 100*time.Millisecond); err == nil {
		t.Errorf("expected error for closed DNS port")
	}
}

type errorDNSSSHClient struct {
	mockDNSSSHClient
	noDocker   bool
	failBuild  bool
	failRun    bool
	failMkdir  bool
	failUpload bool
}

func (e *errorDNSSSHClient) RunCommand(ctx context.Context, cmd string) (string, string, int, error) {
	if e.noDocker && strings.Contains(cmd, "docker --version") {
		return "not found", "", 1, nil
	}
	return e.mockDNSSSHClient.RunCommand(ctx, cmd)
}

func (e *errorDNSSSHClient) RunSudoCommand(ctx context.Context, cmd string) (string, string, int, error) {
	if e.failMkdir && strings.Contains(cmd, "mkdir") {
		return "", "mkdir error", 1, nil
	}
	if e.failBuild && strings.Contains(cmd, "docker build") {
		return "", "build error", 1, nil
	}
	if e.failRun && strings.Contains(cmd, "docker run") {
		return "", "run error", 1, nil
	}
	return e.mockDNSSSHClient.RunSudoCommand(ctx, cmd)
}

func (e *errorDNSSSHClient) UploadSudoFile(ctx context.Context, remotePath string, content []byte, mode os.FileMode) error {
	if e.failUpload {
		return errors.New("upload failed")
	}
	return nil
}

type errorDNSSSHProvider struct {
	client *errorDNSSSHClient
}

func (p *errorDNSSSHProvider) Get(ctx context.Context, server *models.Server) (ssh.SSHClient, error) {
	return p.client, nil
}

func TestDNSManager_Errors(t *testing.T) {
	ctx := context.Background()
	server := &models.Server{ID: 1, Host: "1.2.3.4"}

	// No docker
	p1 := &errorDNSSSHProvider{client: &errorDNSSSHClient{noDocker: true}}
	m1 := NewDNSManager(p1)
	if err := m1.Install(ctx, server, nil); err == nil {
		t.Errorf("expected error when docker missing")
	}

	// Fail mkdir
	p2 := &errorDNSSSHProvider{client: &errorDNSSSHClient{failMkdir: true}}
	m2 := NewDNSManager(p2)
	if err := m2.Install(ctx, server, nil); err == nil {
		t.Errorf("expected error when mkdir fails")
	}

	// Fail upload
	p3 := &errorDNSSSHProvider{client: &errorDNSSSHClient{failUpload: true}}
	m3 := NewDNSManager(p3)
	if err := m3.Install(ctx, server, nil); err == nil {
		t.Errorf("expected error when upload fails")
	}

	// Fail build
	p4 := &errorDNSSSHProvider{client: &errorDNSSSHClient{failBuild: true}}
	m4 := NewDNSManager(p4)
	if err := m4.Install(ctx, server, nil); err == nil {
		t.Errorf("expected error when build fails")
	}

	// Fail run
	p5 := &errorDNSSSHProvider{client: &errorDNSSSHClient{failRun: true}}
	m5 := NewDNSManager(p5)
	if err := m5.Install(ctx, server, nil); err == nil {
		t.Errorf("expected error when run fails")
	}
}

type customDNSSSHClient struct {
	mockDNSSSHClient
	psaOut  string
	psaCode int
	psaErr  error
	psOut   string
	psCode  int
	psErr   error
}

func (c *customDNSSSHClient) RunSudoCommand(ctx context.Context, cmd string) (string, string, int, error) {
	if strings.Contains(cmd, "docker ps -a") {
		return c.psaOut, "", c.psaCode, c.psaErr
	}
	if strings.Contains(cmd, "docker ps") {
		return c.psOut, "", c.psCode, c.psErr
	}
	return c.mockDNSSSHClient.RunSudoCommand(ctx, cmd)
}

type customDNSSSHProvider struct {
	client *customDNSSSHClient
}

func (p *customDNSSSHProvider) Get(ctx context.Context, server *models.Server) (ssh.SSHClient, error) {
	return p.client, nil
}

func TestDNSManager_GetServerStatus_ErrorsAndAbsence(t *testing.T) {
	ctx := context.Background()
	server := &models.Server{ID: 1, Host: "1.2.3.4"}

	// 1. Docker daemon failure on docker ps -a -> returns explicit error
	pErr := &customDNSSSHProvider{client: &customDNSSSHClient{psaCode: 1, psaErr: errors.New("daemon down")}}
	mErr := NewDNSManager(pErr)
	if _, err := mErr.GetServerStatus(ctx, server); err == nil {
		t.Errorf("expected error when docker ps -a fails")
	}

	// 2. Container absent -> container_exists: false, err: nil
	pAbs := &customDNSSSHProvider{client: &customDNSSSHClient{psaOut: "other-container\n", psaCode: 0}}
	mAbs := NewDNSManager(pAbs)
	stAbs, err := mAbs.GetServerStatus(ctx, server)
	if err != nil {
		t.Fatalf("unexpected error on absent container: %v", err)
	}
	if exists, _ := stAbs["container_exists"].(bool); exists {
		t.Errorf("expected container_exists: false")
	}
	if running, _ := stAbs["container_running"].(bool); running {
		t.Errorf("expected container_running: false")
	}

	// 3. Container present and running -> container_exists: true, container_running: true
	pRun := &customDNSSSHProvider{client: &customDNSSSHClient{
		psaOut: "amnezia-dns\n", psaCode: 0,
		psOut: "Up 2 hours", psCode: 0,
	}}
	mRun := NewDNSManager(pRun)
	stRun, err := mRun.GetServerStatus(ctx, server)
	if err != nil {
		t.Fatalf("unexpected error on running container: %v", err)
	}
	if exists, _ := stRun["container_exists"].(bool); !exists {
		t.Errorf("expected container_exists: true")
	}
	if running, _ := stRun["container_running"].(bool); !running {
		t.Errorf("expected container_running: true")
	}

	// 4. Container present but stopped -> container_exists: true, container_running: false
	pStop := &customDNSSSHProvider{client: &customDNSSSHClient{
		psaOut: "amnezia-dns\n", psaCode: 0,
		psOut: "Exited (0) 10 minutes ago", psCode: 0,
	}}
	mStop := NewDNSManager(pStop)
	stStop, err := mStop.GetServerStatus(ctx, server)
	if err != nil {
		t.Fatalf("unexpected error on stopped container: %v", err)
	}
	if exists, _ := stStop["container_exists"].(bool); !exists {
		t.Errorf("expected container_exists: true")
	}
	if running, _ := stStop["container_running"].(bool); running {
		t.Errorf("expected container_running: false")
	}

	// 5. Container present, but docker ps status check fails -> returns explicit error
	pRunErr := &customDNSSSHProvider{client: &customDNSSSHClient{
		psaOut: "amnezia-dns\n", psaCode: 0,
		psCode: 1, psErr: errors.New("status check error"),
	}}
	mRunErr := NewDNSManager(pRunErr)
	if _, err := mRunErr.GetServerStatus(ctx, server); err == nil {
		t.Errorf("expected error when docker ps status check fails")
	}
}
