package ssh

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/devops-igor/amnezia-web-ui-go/internal/models"
	gossh "golang.org/x/crypto/ssh"
)

func TestClient_PasswordAuthAndCommands(t *testing.T) {
	server := NewMockSSHServer(t, "root", "secretPass")
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cfg := Config{
		Host:            server.Host(),
		Port:            server.Port(),
		User:            "root",
		Password:        "secretPass",
		HostKeyCallback: gossh.InsecureIgnoreHostKey(),
		Timeout:         3 * time.Second,
	}

	client, err := Dial(ctx, cfg)
	if err != nil {
		t.Fatalf("failed to dial mock server: %v", err)
	}
	defer client.Close()

	if !client.IsAlive() {
		t.Fatal("expected client to be alive")
	}

	if client.GetHost() != server.Host() || client.GetPort() != server.Port() || client.GetUser() != "root" {
		t.Fatalf("unexpected client getters: %s:%d user=%s", client.GetHost(), client.GetPort(), client.GetUser())
	}

	if client.GetUnderlyingClient() == nil {
		t.Fatal("expected non-nil underlying client")
	}

	if client.GetLastActive().IsZero() {
		t.Fatal("expected non-zero last active time")
	}

	// 1. Successful command
	stdout, stderr, code, err := client.RunCommand(ctx, "echo hello world")
	if err != nil || code != 0 {
		t.Fatalf("RunCommand failed (code %d, err %v): %s", code, err, stderr)
	}
	if stdout != "hello world" {
		t.Fatalf("expected stdout %q, got %q", "hello world", stdout)
	}

	// 2. Non-zero exit code
	_, stderr, code, err = client.RunCommand(ctx, "exit 42")
	if err != nil {
		t.Fatalf("unexpected transport error: %v", err)
	}
	if code != 42 {
		t.Fatalf("expected exit code 42, got %d", code)
	}
	if stderr != "custom error" {
		t.Fatalf("expected stderr 'custom error', got %q", stderr)
	}

	// 3. TestConnection
	info, err := client.TestConnection(ctx)
	if err != nil || !strings.Contains(info, "Linux") {
		t.Fatalf("TestConnection failed: %v, info: %s", err, info)
	}

	// 4. TestConnection error case
	server.SetCommandHandler("uname -sr && cat /etc/os-release 2>/dev/null | head -2", func(cmd string, stdin []byte) (string, string, int) {
		return "", "kernel probe failed", 1
	})
	if _, err := client.TestConnection(ctx); err == nil {
		t.Fatal("expected error on failed TestConnection")
	}
}

func TestClient_PublicKeyAuth(t *testing.T) {
	server := NewMockSSHServer(t, "admin", "")
	defer server.Close()

	edKeyPEM, err := GenerateTestEd25519Key()
	if err != nil {
		t.Fatalf("failed to generate ed25519 key: %v", err)
	}

	signer, err := ParsePrivateKey(edKeyPEM, "")
	if err != nil {
		t.Fatalf("failed to parse ed25519 key: %v", err)
	}
	server.SetAuthorizedKey(signer.PublicKey())

	ctx := context.Background()
	cfg := Config{
		Host:            server.Host(),
		Port:            server.Port(),
		User:            "admin",
		PrivateKey:      edKeyPEM,
		HostKeyCallback: gossh.InsecureIgnoreHostKey(),
	}

	client, err := Dial(ctx, cfg)
	if err != nil {
		t.Fatalf("failed to dial with public key: %v", err)
	}
	defer client.Close()

	stdout, _, code, err := client.RunCommand(ctx, "echo auth-success")
	if err != nil || code != 0 || stdout != "auth-success" {
		t.Fatalf("command failed: code=%d, err=%v, out=%s", code, err, stdout)
	}
}

func TestClient_SudoCommands(t *testing.T) {
	server := NewMockSSHServer(t, "debian", "userPass")
	defer server.Close()

	ctx := context.Background()

	// 1. Non-root user with password
	cfg := Config{
		Host:            server.Host(),
		Port:            server.Port(),
		User:            "debian",
		Password:        "userPass",
		HostKeyCallback: gossh.InsecureIgnoreHostKey(),
	}

	client, err := Dial(ctx, cfg)
	if err != nil {
		t.Fatalf("failed to dial non-root user: %v", err)
	}
	defer client.Close()

	stdout, stderr, code, err := client.RunSudoCommand(ctx, "sudo echo sudo-works")
	if err != nil || code != 0 {
		t.Fatalf("RunSudoCommand failed: code=%d, err=%v, stderr=%s", code, err, stderr)
	}
	if stdout != "success" && stdout != "sudo executed" {
		t.Fatalf("unexpected sudo stdout: %s", stdout)
	}

	// 2. Root user executes sudo directly
	rootServer := NewMockSSHServer(t, "root", "rootPass")
	defer rootServer.Close()

	rootCfg := Config{
		Host:            rootServer.Host(),
		Port:            rootServer.Port(),
		User:            "root",
		Password:        "rootPass",
		HostKeyCallback: gossh.InsecureIgnoreHostKey(),
	}

	rootClient, err := Dial(ctx, rootCfg)
	if err != nil {
		t.Fatalf("failed to dial root user: %v", err)
	}
	defer rootClient.Close()

	stdout, stderr, code, err = rootClient.RunSudoCommand(ctx, "echo root-sudo")
	if err != nil || code != 0 || stdout != "root-sudo" {
		t.Fatalf("root RunSudoCommand failed: code=%d, err=%v, out=%s, stderr=%s", code, err, stdout, stderr)
	}
}

func TestClient_SFTPAndUploadDownload(t *testing.T) {
	server := NewMockSSHServer(t, "root", "pass")
	defer server.Close()

	ctx := context.Background()
	cfg := Config{
		Host:            server.Host(),
		Port:            server.Port(),
		User:            "root",
		Password:        "pass",
		HostKeyCallback: gossh.InsecureIgnoreHostKey(),
	}

	client, err := Dial(ctx, cfg)
	if err != nil {
		t.Fatalf("failed to dial: %v", err)
	}
	defer client.Close()

	testFile := filepath.Join(server.BaseDir(), "etc", "test", "config.txt")
	testContent := []byte("setting1 = value1\nsetting2 = value2\n")

	// 1. FileExists before upload -> false
	exists, err := client.FileExists(ctx, testFile)
	if err != nil || exists {
		t.Fatalf("expected file to not exist, got exists=%v, err=%v", exists, err)
	}

	// 2. UploadFile
	if err := client.UploadFile(ctx, testFile, testContent, 0644); err != nil {
		t.Fatalf("UploadFile failed: %v", err)
	}

	// 3. FileExists after upload -> true
	exists, err = client.FileExists(ctx, testFile)
	if err != nil || !exists {
		t.Fatalf("expected file to exist, got exists=%v, err=%v", exists, err)
	}

	// 4. DownloadFile
	downloaded, err := client.DownloadFile(ctx, testFile)
	if err != nil {
		t.Fatalf("DownloadFile failed: %v", err)
	}
	if string(downloaded) != string(testContent) {
		t.Fatalf("content mismatch: expected %q, got %q", string(testContent), string(downloaded))
	}

	// 5. UploadSudoFile as root
	sudoFile := filepath.Join(server.BaseDir(), "etc", "amnezia", "wg0.conf")
	sudoContent := []byte("[Interface]\nPrivateKey = secret\n")
	if err := client.UploadSudoFile(ctx, sudoFile, sudoContent, 0600); err != nil {
		t.Fatalf("UploadSudoFile failed: %v", err)
	}

	sudoDownloaded, err := client.DownloadFile(ctx, sudoFile)
	if err != nil || string(sudoDownloaded) != string(sudoContent) {
		t.Fatalf("downloaded sudo file mismatch: %v, content=%s", err, string(sudoDownloaded))
	}

	// 6. UploadSudoFile as non-root user
	nonRootServer := NewMockSSHServer(t, "ubuntu", "ubuntuPass")
	defer nonRootServer.Close()

	nonRootCfg := Config{
		Host:            nonRootServer.Host(),
		Port:            nonRootServer.Port(),
		User:            "ubuntu",
		Password:        "ubuntuPass",
		HostKeyCallback: gossh.InsecureIgnoreHostKey(),
	}

	nonRootClient, err := Dial(ctx, nonRootCfg)
	if err != nil {
		t.Fatalf("failed to dial non-root: %v", err)
	}
	defer nonRootClient.Close()

	nonRootTargetFile := filepath.Join(nonRootServer.BaseDir(), "etc", "amnezia", "config.json")
	if err := nonRootClient.UploadSudoFile(ctx, nonRootTargetFile, []byte(`{"inbounds":[]}`), 0644); err != nil {
		t.Fatalf("UploadSudoFile non-root failed: %v", err)
	}
}

func TestClient_Scripts(t *testing.T) {
	server := NewMockSSHServer(t, "root", "pass")
	defer server.Close()

	ctx := context.Background()
	cfg := Config{
		Host:            server.Host(),
		Port:            server.Port(),
		User:            "root",
		Password:        "pass",
		HostKeyCallback: gossh.InsecureIgnoreHostKey(),
	}

	client, err := Dial(ctx, cfg)
	if err != nil {
		t.Fatalf("failed to dial: %v", err)
	}
	defer client.Close()

	script := `
echo "line 1"
echo "line 2"
`
	// RunScript
	stdout, stderr, code, err := client.RunScript(ctx, script)
	if err != nil || code != 0 {
		t.Fatalf("RunScript failed: code=%d, err=%v, stderr=%s, out=%s", code, err, stderr, stdout)
	}

	// RunSudoScript as root
	stdout, stderr, code, err = client.RunSudoScript(ctx, script)
	if err != nil || code != 0 {
		t.Fatalf("RunSudoScript failed: code=%d, err=%v, stderr=%s, out=%s", code, err, stderr, stdout)
	}

	// RunSudoScript as non-root
	nonRootServer := NewMockSSHServer(t, "ubuntu", "ubuntuPass")
	defer nonRootServer.Close()

	nonRootCfg := Config{
		Host:            nonRootServer.Host(),
		Port:            nonRootServer.Port(),
		User:            "ubuntu",
		Password:        "ubuntuPass",
		HostKeyCallback: gossh.InsecureIgnoreHostKey(),
	}

	nonRootClient, err := Dial(ctx, nonRootCfg)
	if err != nil {
		t.Fatalf("failed to dial non-root: %v", err)
	}
	defer nonRootClient.Close()

	_, _, code, err = nonRootClient.RunSudoScript(ctx, "echo non-root-script")
	if err != nil || code != 0 {
		t.Fatalf("non-root RunSudoScript failed: code=%d, err=%v", code, err)
	}
}

func TestClient_TimeoutHandling(t *testing.T) {
	server := NewMockSSHServer(t, "root", "pass")
	defer server.Close()
	server.SetHangCommands(true)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	cfg := Config{
		Host:            server.Host(),
		Port:            server.Port(),
		User:            "root",
		Password:        "pass",
		HostKeyCallback: gossh.InsecureIgnoreHostKey(),
	}

	client, err := Dial(context.Background(), cfg)
	if err != nil {
		t.Fatalf("failed to dial: %v", err)
	}
	defer client.Close()

	_, _, code, err := client.RunCommand(ctx, "hang")
	if code != -1 {
		t.Fatalf("expected exitCode -1 on timeout, got %d", code)
	}
	if err == nil {
		t.Fatal("expected error on timeout, got nil")
	}
}

func TestDialFromServer(t *testing.T) {
	server := NewMockSSHServer(t, "root", "pass")
	defer server.Close()

	ctx := context.Background()
	store := newMemoryHostKeyStore()

	sModel := &models.Server{
		ID:      200,
		Name:    "Test Remote",
		Host:    server.Host(),
		SSHPort: server.Port(),
		SSHUser: "root",
		SSHPass: "pass",
	}

	// Dial with nil server
	_, err := DialFromServer(ctx, nil, store)
	if err == nil {
		t.Fatal("expected error on nil server")
	}

	// Dial valid server
	client, err := DialFromServer(ctx, sModel, store)
	if err != nil {
		t.Fatalf("DialFromServer failed: %v", err)
	}
	defer client.Close()

	if client.GetServerID() == nil || *client.GetServerID() != 200 {
		t.Fatalf("expected server ID 200, got %v", client.GetServerID())
	}

	// Verification check: fingerprint should be stored in store
	fp, err := store.GetKnownHostFingerprint(ctx, 200)
	if err != nil || fp == "" {
		t.Fatalf("expected stored fingerprint, got %q, err=%v", fp, err)
	}
}

func TestClient_DialErrors(t *testing.T) {
	ctx := context.Background()

	// Missing host
	_, err := Dial(ctx, Config{User: "root"})
	if err == nil {
		t.Fatal("expected error on missing host")
	}

	// Missing auth credentials
	_, err = Dial(ctx, Config{Host: "127.0.0.1", Port: 22})
	if err == nil {
		t.Fatal("expected error on missing credentials")
	}

	// Connection refused (invalid port)
	_, err = Dial(ctx, Config{
		Host:            "127.0.0.1",
		Port:            54321,
		Password:        "pass",
		Timeout:         100 * time.Millisecond,
		HostKeyCallback: gossh.InsecureIgnoreHostKey(),
	})
	if err == nil {
		t.Fatal("expected error on connection refused")
	}
}

func TestClient_CloseNotConnected(t *testing.T) {
	client := NewClient(Config{})
	if err := client.Close(); err != nil {
		t.Fatalf("expected nil error on closing unconnected client, got %v", err)
	}
	if client.IsAlive() {
		t.Fatal("expected unconnected client to not be alive")
	}

	ctx := context.Background()
	if _, _, code, err := client.RunCommand(ctx, "ls"); err != ErrNotConnected || code != -1 {
		t.Fatalf("expected ErrNotConnected, got code=%d, err=%v", code, err)
	}
	if _, _, code, err := client.RunSudoCommand(ctx, "ls"); err != ErrNotConnected || code != -1 {
		t.Fatalf("expected ErrNotConnected, got code=%d, err=%v", code, err)
	}
	if _, _, code, err := client.RunScript(ctx, "ls"); err != ErrNotConnected || code != -1 {
		t.Fatalf("expected ErrNotConnected, got code=%d, err=%v", code, err)
	}
	if err := client.UploadFile(ctx, "/tmp/a", []byte("a"), 0644); err != ErrNotConnected {
		t.Fatalf("expected ErrNotConnected, got %v", err)
	}
}

func TestClient_ConcurrentRunCommands(t *testing.T) {
	server := NewMockSSHServer(t, "root", "secretPass")
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cfg := Config{
		Host:            server.Host(),
		Port:            server.Port(),
		User:            "root",
		Password:        "secretPass",
		HostKeyCallback: gossh.InsecureIgnoreHostKey(),
		Timeout:         3 * time.Second,
	}

	client, err := Dial(ctx, cfg)
	if err != nil {
		t.Fatalf("failed to dial mock server: %v", err)
	}
	defer client.Close()

	const concurrency = 20
	var wg sync.WaitGroup
	errCh := make(chan error, concurrency)

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			cmd := fmt.Sprintf("echo worker-%d", idx)
			out, stderr, code, err := client.RunCommand(ctx, cmd)
			if err != nil {
				errCh <- fmt.Errorf("worker %d err: %w (stderr: %s)", idx, err, stderr)
				return
			}
			if code != 0 {
				errCh <- fmt.Errorf("worker %d exit code: %d", idx, code)
				return
			}
			expected := fmt.Sprintf("worker-%d", idx)
			if out != expected {
				errCh <- fmt.Errorf("worker %d expected %q, got %q", idx, expected, out)
				return
			}
		}(i)
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Errorf("concurrent RunCommand failed: %v", err)
	}
}

func TestClient_UploadSudoFile_CleanupOnMoveFailure(t *testing.T) {
	server := NewMockSSHServer(t, "ubuntu", "ubuntuPass")
	defer server.Close()

	ctx := context.Background()
	cfg := Config{
		Host:            server.Host(),
		Port:            server.Port(),
		User:            "ubuntu",
		Password:        "ubuntuPass",
		HostKeyCallback: gossh.InsecureIgnoreHostKey(),
	}

	client, err := Dial(ctx, cfg)
	if err != nil {
		t.Fatalf("failed to dial: %v", err)
	}
	defer client.Close()

	// Simulate failure on mv
	server.SetCommandHandler("sudo -S -p '' -- /bin/bash -c 'mv", func(cmd string, stdin []byte) (string, string, int) {
		return "", "permission denied / disk full", 1
	})

	var rmCalled bool
	var rmMu sync.Mutex
	server.SetCommandHandler("sudo -S -p '' -- /bin/bash -c 'rm -f", func(cmd string, stdin []byte) (string, string, int) {
		rmMu.Lock()
		rmCalled = true
		rmMu.Unlock()
		return "", "", 0
	})

	targetFile := filepath.Join(server.BaseDir(), "etc", "protected", "file.conf")
	err = client.UploadSudoFile(ctx, targetFile, []byte("sensitive content"), 0600)
	if err == nil {
		t.Fatal("expected error on failed mv, got nil")
	}

	rmMu.Lock()
	wasRmCalled := rmCalled
	rmMu.Unlock()

	if !wasRmCalled {
		t.Fatal("expected temporary file cleanup via rm -f after mv failure")
	}
}

func TestClient_StaleSFTPRefresh(t *testing.T) {
	server := NewMockSSHServer(t, "root", "pass")
	defer server.Close()

	ctx := context.Background()
	cfg := Config{
		Host:            server.Host(),
		Port:            server.Port(),
		User:            "root",
		Password:        "pass",
		HostKeyCallback: gossh.InsecureIgnoreHostKey(),
	}

	client, err := Dial(ctx, cfg)
	if err != nil {
		t.Fatalf("failed to dial: %v", err)
	}
	defer client.Close()

	testPath := filepath.Join(server.BaseDir(), "etc", "sftp-refresh.txt")

	// 1. Initial SFTP upload
	if err := client.UploadFile(ctx, testPath, []byte("initial"), 0644); err != nil {
		t.Fatalf("initial upload failed: %v", err)
	}

	// 2. Force close internal sftp client to make it stale
	client.mu.Lock()
	if client.sftpClient != nil {
		_ = client.sftpClient.Close()
	}
	client.mu.Unlock()

	// 3. Subsequent SFTP upload should detect stale client, refresh it, and succeed
	if err := client.UploadFile(ctx, testPath, []byte("refreshed"), 0644); err != nil {
		t.Fatalf("upload after stale sftp client failed: %v", err)
	}

	// 4. Verify downloaded content
	content, err := client.DownloadFile(ctx, testPath)
	if err != nil {
		t.Fatalf("download failed: %v", err)
	}
	if string(content) != "refreshed" {
		t.Fatalf("expected 'refreshed', got %q", string(content))
	}
}
