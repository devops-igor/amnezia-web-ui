package ssh

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// DetectDocker verifies if the Docker daemon or CLI is available and operational on the remote host.
func DetectDocker(ctx context.Context, client SSHClient) (bool, error) {
	if client == nil {
		return false, ErrNotConnected
	}

	cmd := "docker info >/dev/null 2>&1 || docker --version >/dev/null 2>&1"
	_, _, code, err := client.RunCommand(ctx, cmd)
	if err != nil {
		return false, fmt.Errorf("failed to probe docker: %w", err)
	}

	return code == 0, nil
}

// DetectPackageManager determines the Linux package manager available on the remote system.
func DetectPackageManager(ctx context.Context, client SSHClient) (string, error) {
	if client == nil {
		return "", ErrNotConnected
	}

	managers := []struct {
		binary string
		name   string
	}{
		{"apt-get", "apt"},
		{"dnf", "dnf"},
		{"yum", "yum"},
		{"pacman", "pacman"},
		{"zypper", "zypper"},
		{"apk", "apk"},
	}

	for _, mgr := range managers {
		cmd := fmt.Sprintf("command -v %s >/dev/null 2>&1", mgr.binary)
		_, _, code, err := client.RunCommand(ctx, cmd)
		if err == nil && code == 0 {
			return mgr.name, nil
		}
	}

	return "unknown", nil
}

// EnsureDirectory creates a directory hierarchy on the remote host using mkdir -p.
func EnsureDirectory(ctx context.Context, client SSHClient, remotePath string, sudo bool) error {
	if client == nil {
		return ErrNotConnected
	}
	if strings.TrimSpace(remotePath) == "" {
		return ErrEmptyPath
	}

	cmd := fmt.Sprintf("mkdir -p %s", EscapeShellArg(remotePath))
	var code int
	var stderr string
	var err error

	if sudo {
		_, stderr, code, err = client.RunSudoCommand(ctx, cmd)
	} else {
		_, stderr, code, err = client.RunCommand(ctx, cmd)
	}

	if err != nil {
		return fmt.Errorf("failed to create remote directory %s: %w", remotePath, err)
	}
	if code != 0 {
		return fmt.Errorf("mkdir -p failed on %s (exit code %d): %s", remotePath, code, stderr)
	}

	return nil
}

// CheckCommandExists checks whether a CLI executable exists in PATH on the remote host.
func CheckCommandExists(ctx context.Context, client SSHClient, cmd string) (bool, error) {
	if client == nil {
		return false, ErrNotConnected
	}
	if strings.TrimSpace(cmd) == "" {
		return false, nil
	}

	checkCmd := fmt.Sprintf("command -v %s >/dev/null 2>&1", EscapeShellArg(cmd))
	_, _, code, err := client.RunCommand(ctx, checkCmd)
	if err != nil {
		return false, err
	}
	return code == 0, nil
}

// GetSystemInfo retrieves system architecture, kernel, hostname, and OS release information.
func GetSystemInfo(ctx context.Context, client SSHClient) (map[string]any, error) {
	if client == nil {
		return nil, ErrNotConnected
	}

	info := make(map[string]any)

	// Uname details: kernel-name, nodename, kernel-release, machine
	out, _, code, err := client.RunCommand(ctx, "uname -s && uname -n && uname -r && uname -m")
	if err == nil && code == 0 {
		lines := strings.Split(out, "\n")
		if len(lines) >= 1 {
			info["os_type"] = strings.TrimSpace(lines[0])
		}
		if len(lines) >= 2 {
			info["hostname"] = strings.TrimSpace(lines[1])
		}
		if len(lines) >= 3 {
			info["kernel"] = strings.TrimSpace(lines[2])
		}
		if len(lines) >= 4 {
			info["arch"] = strings.TrimSpace(lines[3])
		}
	}

	// OS release pretty name
	osOut, _, osCode, osErr := client.RunCommand(ctx, `cat /etc/os-release 2>/dev/null | grep -E '^PRETTY_NAME=' | head -1 | cut -d= -f2- | tr -d '"'`)
	if osErr == nil && osCode == 0 && strings.TrimSpace(osOut) != "" {
		info["os_name"] = strings.TrimSpace(osOut)
	}

	// Uptime in seconds
	uptimeOut, _, upCode, upErr := client.RunCommand(ctx, `cat /proc/uptime 2>/dev/null | awk '{print $1}'`)
	if upErr == nil && upCode == 0 && strings.TrimSpace(uptimeOut) != "" {
		if seconds, parseErr := strconv.ParseFloat(strings.TrimSpace(uptimeOut), 64); parseErr == nil {
			info["uptime_seconds"] = int64(seconds)
		}
	}

	return info, nil
}
