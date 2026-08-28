package mtproxyl

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/devops-igor/amnezia-web-ui-go/internal/manager/ssh"
)

// SizeMultipliers maps Russian size units to byte multipliers.
var SizeMultipliers = map[string]int64{
	"Б":  1,
	"КБ": 1024,
	"МБ": 1024 * 1024,
	"ГБ": 1024 * 1024 * 1024,
	"ТБ": 1024 * 1024 * 1024 * 1024,
}

// TrafficStats stores bandwidth metrics per user.
type TrafficStats struct {
	TotalBytes  int64
	Connections int
}

var (
	connRegex = regexp.MustCompile(`соед[.:]\s*(\d+)`)
)

// ParseTraffic parses `mtproxyl traffic` output with Russian unit multipliers.
func ParseTraffic(output string) (map[string]TrafficStats, error) {
	traffic := make(map[string]TrafficStats)
	lines := strings.Split(output, "\n")

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "●") {
			continue
		}

		colonIdx := strings.Index(trimmed, ":")
		if colonIdx == -1 {
			continue
		}

		label := strings.TrimSpace(trimmed[len("●"):colonIdx])
		if label == "" {
			continue
		}

		conns := 0
		if match := connRegex.FindStringSubmatch(trimmed); len(match) > 1 {
			conns, _ = strconv.Atoi(match[1])
		}

		// Calculate total bytes across all unit matches in the line
		var totalBytes int64
		for unit, mult := range SizeMultipliers {
			re := regexp.MustCompile(`([\d.]+)\s*` + regexp.QuoteMeta(unit))
			matches := re.FindAllStringSubmatch(trimmed, -1)
			for _, m := range matches {
				if len(m) > 1 {
					if val, err := strconv.ParseFloat(m[1], 64); err == nil {
						totalBytes += int64(val * float64(mult))
					}
				}
			}
		}

		traffic[label] = TrafficStats{
			TotalBytes:  totalBytes,
			Connections: conns,
		}
	}

	return traffic, nil
}

// ParseConnections parses `mtproxyl connections` output for active connection counts.
func ParseConnections(output string) (map[string]int, error) {
	connections := make(map[string]int)
	lines := strings.Split(output, "\n")
	inData := false

	rowRegex := regexp.MustCompile(`^([a-zA-Z0-9_-]+)\s+(\d+)`)

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.Contains(trimmed, "─────") {
			inData = true
			continue
		}
		if !inData || trimmed == "" || strings.HasPrefix(trimmed, "Всего") {
			continue
		}

		if match := rowRegex.FindStringSubmatch(trimmed); len(match) > 2 {
			label := match[1]
			count, _ := strconv.Atoi(match[2])
			connections[label] = count
		}
	}

	return connections, nil
}

// DisableOverquotaUsers checks active clients against their quotas and disables those exceeding limits.
func DisableOverquotaUsers(ctx context.Context, sshClient ssh.SSHClient, cliPath string, secrets []SecretEntry, traffic map[string]TrafficStats) ([]string, error) {
	if cliPath == "" {
		cliPath = "/usr/local/bin/mtproxyl"
	}

	var disabled []string
	for _, sec := range secrets {
		if !sec.Enabled || sec.QuotaBytes <= 0 {
			continue
		}

		stat, ok := traffic[sec.Label]
		if ok && stat.TotalBytes >= sec.QuotaBytes {
			cmd := fmt.Sprintf("%s secret disable %s", cliPath, sec.Label)
			if _, _, code, err := sshClient.RunCommand(ctx, cmd); err == nil && code == 0 {
				disabled = append(disabled, sec.Label)
			}
		}
	}

	return disabled, nil
}
