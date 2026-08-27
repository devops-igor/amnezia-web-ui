package config

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// AppVersion represents the current version of the web panel.
const AppVersion = "1.0.0"

// Config contains runtime configuration parameters for the application.
type Config struct {
	AppVersion     string
	Host           string
	Port           int
	DataDir        string
	DBPath         string
	SecretKey      string
	TrustedProxies []string
	LogLevel       string
	VPNEnabled     bool
	VPNListenPort  int
	VPNSubnet      string
}

// Load initializes configuration from environment variables and local secrets.
func Load() (*Config, error) {
	dataDir := os.Getenv("DATA_DIR")
	if dataDir == "" {
		dataDir = "."
	}

	dbPath := os.Getenv("DB_PATH")
	if dbPath == "" {
		dbPath = filepath.Join(dataDir, "panel.db")
	}

	host := os.Getenv("HOST")
	if host == "" {
		host = "0.0.0.0"
	}

	port := 5000
	portStr := os.Getenv("PORT")
	if portStr == "" {
		portStr = os.Getenv("PANEL_PORT")
	}
	if portStr != "" {
		if p, err := strconv.Atoi(portStr); err == nil && p > 0 && p <= 65535 {
			port = p
		}
	}

	trustedProxiesStr := os.Getenv("TRUSTED_PROXIES")
	var trustedProxies []string
	if trustedProxiesStr != "" {
		for _, proxy := range strings.Split(trustedProxiesStr, ",") {
			trimmed := strings.TrimSpace(proxy)
			if trimmed != "" {
				trustedProxies = append(trustedProxies, trimmed)
			}
		}
	}

	logLevel := os.Getenv("LOG_LEVEL")
	if logLevel == "" {
		logLevel = "INFO"
	}

	vpnEnabled := strings.EqualFold(os.Getenv("VPN_ENABLED"), "true") || os.Getenv("VPN_ENABLED") == "1"

	vpnListenPort := 51820
	if vpnPortStr := os.Getenv("VPN_LISTEN_PORT"); vpnPortStr != "" {
		if p, err := strconv.Atoi(vpnPortStr); err == nil && p > 0 && p <= 65535 {
			vpnListenPort = p
		}
	}

	vpnSubnet := os.Getenv("VPN_SUBNET")
	if vpnSubnet == "" {
		vpnSubnet = "10.100.0.0/16"
	}

	secretKey, err := resolveSecretKey(dataDir)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve secret key: %w", err)
	}

	return &Config{
		AppVersion:     AppVersion,
		Host:           host,
		Port:           port,
		DataDir:        dataDir,
		DBPath:         dbPath,
		SecretKey:      secretKey,
		TrustedProxies: trustedProxies,
		LogLevel:       logLevel,
		VPNEnabled:     vpnEnabled,
		VPNListenPort:  vpnListenPort,
		VPNSubnet:      vpnSubnet,
	}, nil
}

// resolveSecretKey resolves or generates the application SECRET_KEY per specification:
// 1. SECRET_KEY env variable
// 2. <DATA_DIR>/.secret_key file
// 3. Generate 32 crypto random bytes -> 64-char hex string, save with 0600 permissions.
func resolveSecretKey(dataDir string) (string, error) {
	if envKey := strings.TrimSpace(os.Getenv("SECRET_KEY")); envKey != "" {
		slog.Info("Using SECRET_KEY from environment variable")
		return envKey, nil
	}

	cleanDataDir := filepath.Clean(dataDir)
	cleanKeyPath := filepath.Clean(filepath.Join(cleanDataDir, ".secret_key"))

	// #nosec G304 G703 -- Reading secret key from configured data directory is intended
	if data, err := os.ReadFile(cleanKeyPath); err == nil {
		key := strings.TrimSpace(string(data))
		if key != "" {
			slog.Info("Loaded SECRET_KEY from persistent storage")
			return key, nil
		}
	}

	// Generate 32 bytes (64 hex characters)
	randomBytes := make([]byte, 32)
	if _, err := rand.Read(randomBytes); err != nil {
		return "", fmt.Errorf("failed to generate random bytes: %w", err)
	}
	newKey := hex.EncodeToString(randomBytes)

	// Ensure directory exists
	// #nosec G703
	if err := os.MkdirAll(cleanDataDir, 0750); err != nil {
		return "", fmt.Errorf("failed to create data dir %s: %w", cleanDataDir, err)
	}

	// #nosec G703
	if err := os.WriteFile(cleanKeyPath, []byte(newKey), 0600); err != nil {
		slog.Warn("Failed to persist generated SECRET_KEY to file", "err", err)
	} else {
		slog.Warn("Generated new SECRET_KEY on first boot. Set SECRET_KEY in production to avoid persistence issues.")
	}

	return newKey, nil
}
