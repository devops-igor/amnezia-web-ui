package mtproxyl

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
)

// SecretEntry represents a single user secret record in /opt/mtproxyl/secrets.conf.
// Format: LABEL|SECRET|CREATED_TS|ENABLED|MAX_CONNS|MAX_IPS|QUOTA_BYTES|EXPIRES|NOTES
type SecretEntry struct {
	Label      string
	Secret     string
	CreatedTS  string
	Enabled    bool
	MaxConns   int
	MaxIPs     int
	QuotaBytes int64
	Expires    string
	Notes      string
}

// SecretsFile manages thread-safe parsing and serialization of secrets.conf.
type SecretsFile struct {
	mu sync.RWMutex
}

// NewSecretsFile creates a new SecretsFile helper.
func NewSecretsFile() *SecretsFile {
	return &SecretsFile{}
}

// Parse parses the raw text of /opt/mtproxyl/secrets.conf into SecretEntry items.
func (sf *SecretsFile) Parse(content string) ([]SecretEntry, error) {
	sf.mu.RLock()
	defer sf.mu.RUnlock()

	var entries []SecretEntry
	lines := strings.Split(content, "\n")

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		parts := strings.Split(trimmed, "|")
		if len(parts) < 9 {
			continue
		}

		maxConns, _ := strconv.Atoi(parts[4])
		maxIPs, _ := strconv.Atoi(parts[5])
		quotaBytes, _ := strconv.ParseInt(parts[6], 10, 64)

		entries = append(entries, SecretEntry{
			Label:      parts[0],
			Secret:     parts[1],
			CreatedTS:  parts[2],
			Enabled:    strings.EqualFold(parts[3], "true"),
			MaxConns:   maxConns,
			MaxIPs:     maxIPs,
			QuotaBytes: quotaBytes,
			Expires:    parts[7],
			Notes:      parts[8],
		})
	}

	return entries, nil
}

// Serialize converts a slice of SecretEntry records into secrets.conf formatted text.
func (sf *SecretsFile) Serialize(entries []SecretEntry) string {
	sf.mu.RLock()
	defer sf.mu.RUnlock()

	var sb strings.Builder
	sb.WriteString("# LABEL|SECRET|CREATED_TS|ENABLED|MAX_CONNS|MAX_IPS|QUOTA_BYTES|EXPIRES|NOTES\n")

	for _, e := range entries {
		enabledStr := "false"
		if e.Enabled {
			enabledStr = "true"
		}

		expires := e.Expires
		if expires == "" {
			expires = "0"
		}

		sb.WriteString(fmt.Sprintf("%s|%s|%s|%s|%d|%d|%d|%s|%s\n",
			e.Label,
			e.Secret,
			e.CreatedTS,
			enabledStr,
			e.MaxConns,
			e.MaxIPs,
			e.QuotaBytes,
			expires,
			e.Notes,
		))
	}

	return sb.String()
}
