package ssh

import (
	"context"
	"path/filepath"
	"testing"

	gossh "golang.org/x/crypto/ssh"
)

func TestDetectDocker(t *testing.T) {
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

	// 1. Docker detected
	hasDocker, err := DetectDocker(ctx, client)
	if err != nil || !hasDocker {
		t.Fatalf("expected hasDocker=true, got %v, err=%v", hasDocker, err)
	}

	// 2. Docker missing handler
	server.SetCommandHandler("docker info", func(cmd string, stdin []byte) (stdout, stderr string, exitCode int) {
		return "", "command not found: docker", 127
	})
	server.SetCommandHandler("docker --version", func(cmd string, stdin []byte) (stdout, stderr string, exitCode int) {
		return "", "command not found: docker", 127
	})

	hasDocker, err = DetectDocker(ctx, client)
	if err != nil || hasDocker {
		t.Fatalf("expected hasDocker=false, got %v, err=%v", hasDocker, err)
	}

	// 3. Nil client
	if _, err := DetectDocker(ctx, nil); err == nil {
		t.Fatal("expected error on nil client")
	}
}

func TestDetectPackageManager(t *testing.T) {
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

	// 1. Default handler detects "apt"
	mgr, err := DetectPackageManager(ctx, client)
	if err != nil || mgr != "apt" {
		t.Fatalf("expected pkg mgr 'apt', got %q, err=%v", mgr, err)
	}

	// 2. Custom pacman handler
	server.SetCommandHandler("command -v apt-get", func(cmd string, stdin []byte) (string, string, int) {
		return "", "", 1
	})
	server.SetCommandHandler("command -v pacman", func(cmd string, stdin []byte) (string, string, int) {
		return "/usr/bin/pacman", "", 0
	})

	mgr, err = DetectPackageManager(ctx, client)
	if err != nil || mgr != "pacman" {
		t.Fatalf("expected pkg mgr 'pacman', got %q, err=%v", mgr, err)
	}

	// 3. Unknown package manager
	server.SetCommandHandler("command -v pacman", func(cmd string, stdin []byte) (string, string, int) {
		return "", "", 1
	})

	mgr, err = DetectPackageManager(ctx, client)
	if err != nil || mgr != "unknown" {
		t.Fatalf("expected 'unknown', got %q, err=%v", mgr, err)
	}

	// 4. Nil client
	if _, err := DetectPackageManager(ctx, nil); err == nil {
		t.Fatal("expected error on nil client")
	}
}

func TestEnsureDirectory(t *testing.T) {
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

	testDir := filepath.Join("/opt", "amnezia", "awg")

	// Non-sudo
	if err := EnsureDirectory(ctx, client, testDir, false); err != nil {
		t.Fatalf("EnsureDirectory non-sudo failed: %v", err)
	}

	// Sudo
	if err := EnsureDirectory(ctx, client, testDir, true); err != nil {
		t.Fatalf("EnsureDirectory sudo failed: %v", err)
	}

	// Errors: nil client & empty path
	if err := EnsureDirectory(ctx, nil, testDir, false); err == nil {
		t.Fatal("expected error on nil client")
	}
	if err := EnsureDirectory(ctx, client, "", false); err == nil {
		t.Fatal("expected error on empty path")
	}
}

func TestGetSystemInfo(t *testing.T) {
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

	info, err := GetSystemInfo(ctx, client)
	if err != nil {
		t.Fatalf("GetSystemInfo failed: %v", err)
	}

	if info["os_type"] != "Linux" || info["hostname"] != "test-host" || info["arch"] != "x86_64" {
		t.Fatalf("unexpected system info: %+v", info)
	}

	if info["os_name"] != "Ubuntu 24.04 LTS" {
		t.Fatalf("unexpected os_name: %+v", info)
	}

	if info["uptime_seconds"] != int64(54321) {
		t.Fatalf("unexpected uptime: %+v", info)
	}

	// Nil client check
	if _, err := GetSystemInfo(ctx, nil); err == nil {
		t.Fatal("expected error on nil client")
	}
}

func TestCheckCommandExists(t *testing.T) {
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

	server.SetCommandHandler("command -v 'curl'", func(cmd string, stdin []byte) (string, string, int) {
		return "/usr/bin/curl", "", 0
	})

	exists, err := CheckCommandExists(ctx, client, "curl")
	if err != nil || !exists {
		t.Fatalf("expected curl exists=true, got %v, err=%v", exists, err)
	}

	exists, err = CheckCommandExists(ctx, client, "nonexistent")
	if err != nil || exists {
		t.Fatalf("expected nonexistent exists=false, got %v, err=%v", exists, err)
	}

	// Empty command
	exists, err = CheckCommandExists(ctx, client, "")
	if err != nil || exists {
		t.Fatalf("expected empty command exists=false, got %v", exists)
	}
}

func TestDetectAppArmor(t *testing.T) {
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

	// 1. AppArmor enabled (exit code 0)
	server.SetCommandHandler("([ -f /sys/module/apparmor/parameters/enabled ]", func(cmd string, stdin []byte) (string, string, int) {
		return "", "", 0
	})

	enabled, err := DetectAppArmor(ctx, client)
	if err != nil || !enabled {
		t.Fatalf("expected AppArmor enabled=true, got %v, err=%v", enabled, err)
	}

	// 2. AppArmor disabled (exit code 1)
	server.SetCommandHandler("([ -f /sys/module/apparmor/parameters/enabled ]", func(cmd string, stdin []byte) (string, string, int) {
		return "", "", 1
	})

	enabled, err = DetectAppArmor(ctx, client)
	if err != nil || enabled {
		t.Fatalf("expected AppArmor enabled=false, got %v, err=%v", enabled, err)
	}

	// 3. Nil client
	if _, err := DetectAppArmor(ctx, nil); err == nil {
		t.Fatal("expected error on nil client")
	}
}
