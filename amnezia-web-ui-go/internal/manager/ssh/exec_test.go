package ssh

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
)

func TestEscapeShellArg(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"", "''"},
		{"hello", "'hello'"},
		{"hello world", "'hello world'"},
		{"it's me", `'it'\''s me'`},
		{"$VAR and `calc`", `'$VAR and ` + "`calc`'"},
		{"'quoted'", `''\''quoted'\'''`},
	}

	for _, tt := range tests {
		got := EscapeShellArg(tt.input)
		if got != tt.expected {
			t.Errorf("EscapeShellArg(%q) = %q; expected %q", tt.input, got, tt.expected)
		}
	}
}

func TestCleanSudoCommand(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"sudo apt update", "apt update"},
		{"   sudo   docker ps", "docker ps"},
		{"sudo sudo systemctl restart", "systemctl restart"},
		{"echo sudo", "echo sudo"},
		{"ls -la", "ls -la"},
	}

	for _, tt := range tests {
		got := CleanSudoCommand(tt.input)
		if got != tt.expected {
			t.Errorf("CleanSudoCommand(%q) = %q; expected %q", tt.input, got, tt.expected)
		}
	}
}

func TestFormatSudoCommand(t *testing.T) {
	// 1. Root user
	cmd, stdin := FormatSudoCommand("sudo docker ps", "pass", true)
	if cmd != "docker ps" || stdin != "" {
		t.Fatalf("expected direct command for root, got cmd=%q, stdin=%q", cmd, stdin)
	}

	// 2. Non-root with password
	cmd, stdin = FormatSudoCommand("apt-get update", "my'pass", false)
	if !strings.Contains(cmd, "sudo -S -p '' -- /bin/bash -c") || !strings.Contains(cmd, "'apt-get update'") {
		t.Fatalf("unexpected formatted cmd: %s", cmd)
	}
	if stdin != "my'pass\n" {
		t.Fatalf("expected stdin password, got %q", stdin)
	}

	// 3. Non-root without password
	cmd, stdin = FormatSudoCommand("apt-get update", "", false)
	if !strings.Contains(cmd, "sudo -n -p '' -- /bin/bash -c") {
		t.Fatalf("unexpected formatted cmd: %s", cmd)
	}
	if stdin != "" {
		t.Fatalf("expected empty stdin, got %q", stdin)
	}
}

func TestFormatSudoCommandLine(t *testing.T) {
	// 1. Root
	line := FormatSudoCommandLine("docker ps", "pass", true)
	if line != "docker ps" {
		t.Fatalf("expected raw command for root, got %s", line)
	}

	// 2. Non-root with pass
	line = FormatSudoCommandLine("docker ps", "p@ss'word", false)
	if !strings.HasPrefix(line, "echo 'p@ss'\\''word' | sudo -S -p '' -- /bin/bash -c 'docker ps'") {
		t.Fatalf("unexpected command line: %s", line)
	}

	// 3. Non-root without pass
	line = FormatSudoCommandLine("docker ps", "", false)
	if line != "sudo -n -p '' -- /bin/bash -c 'docker ps'" {
		t.Fatalf("unexpected command line: %s", line)
	}
}

func TestSafeBuffer(t *testing.T) {
	var buf SafeBuffer
	var wg sync.WaitGroup

	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = buf.Write([]byte("data\n"))
		}()
	}

	wg.Wait()
	str := buf.String()
	count := strings.Count(str, "data\n")
	if count != 50 {
		t.Fatalf("expected 50 writes, got %d", count)
	}
}

func TestRunSession_NilClient(t *testing.T) {
	_, _, code, err := RunSession(context.Background(), nil, "echo 1", nil)
	if !errors.Is(err, ErrNotConnected) {
		t.Fatalf("expected ErrNotConnected, got %v", err)
	}
	if code != -1 {
		t.Fatalf("expected exitCode -1, got %d", code)
	}
}

func TestRunSession_CancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, _, code, err := RunSession(ctx, nil, "echo 1", nil)
	if code != -1 {
		t.Fatalf("expected code -1, got %d", code)
	}
	if err == nil {
		t.Fatal("expected error on canceled context, got nil")
	}
}
