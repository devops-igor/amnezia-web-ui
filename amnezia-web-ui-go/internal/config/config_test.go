package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfigDefaults(t *testing.T) {
	// Clean environment variables for test
	os.Unsetenv("SECRET_KEY")
	os.Unsetenv("PORT")
	os.Unsetenv("PANEL_PORT")
	os.Unsetenv("HOST")
	os.Unsetenv("TRUSTED_PROXIES")
	os.Unsetenv("LOG_LEVEL")
	os.Unsetenv("VPN_ENABLED")

	tmpDir := t.TempDir()
	os.Setenv("DATA_DIR", tmpDir)
	defer os.Unsetenv("DATA_DIR")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	if cfg.Port != 5000 {
		t.Errorf("expected default port 5000, got %d", cfg.Port)
	}
	if cfg.Host != "0.0.0.0" {
		t.Errorf("expected default host 0.0.0.0, got %s", cfg.Host)
	}
	if cfg.LogLevel != "INFO" {
		t.Errorf("expected default log level INFO, got %s", cfg.LogLevel)
	}
	if cfg.VPNEnabled {
		t.Errorf("expected VPNEnabled default false")
	}
	if cfg.VPNListenPort != 51820 {
		t.Errorf("expected VPNListenPort default 51820, got %d", cfg.VPNListenPort)
	}
	if cfg.VPNSubnet != "10.100.0.0/16" {
		t.Errorf("expected VPNSubnet default 10.100.0.0/16, got %s", cfg.VPNSubnet)
	}
	if len(cfg.SecretKey) != 64 {
		t.Errorf("expected generated secret key length 64, got %d", len(cfg.SecretKey))
	}
}

func TestConfigEnvOverrides(t *testing.T) {
	tmpDir := t.TempDir()
	os.Setenv("DATA_DIR", tmpDir)
	os.Setenv("SECRET_KEY", "env-secret-key-1234567890abcdef1234567890abcdef")
	os.Setenv("PORT", "8080")
	os.Setenv("HOST", "127.0.0.1")
	os.Setenv("LOG_LEVEL", "DEBUG")
	os.Setenv("TRUSTED_PROXIES", "10.0.0.0/8, 172.18.0.1, 192.168.1.1/24")
	os.Setenv("VPN_ENABLED", "true")
	os.Setenv("VPN_LISTEN_PORT", "55555")
	os.Setenv("VPN_SUBNET", "10.200.0.0/16")

	defer func() {
		os.Unsetenv("DATA_DIR")
		os.Unsetenv("SECRET_KEY")
		os.Unsetenv("PORT")
		os.Unsetenv("HOST")
		os.Unsetenv("LOG_LEVEL")
		os.Unsetenv("TRUSTED_PROXIES")
		os.Unsetenv("VPN_ENABLED")
		os.Unsetenv("VPN_LISTEN_PORT")
		os.Unsetenv("VPN_SUBNET")
	}()

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	if cfg.Port != 8080 {
		t.Errorf("expected port 8080, got %d", cfg.Port)
	}
	if cfg.Host != "127.0.0.1" {
		t.Errorf("expected host 127.0.0.1, got %s", cfg.Host)
	}
	if cfg.LogLevel != "DEBUG" {
		t.Errorf("expected log level DEBUG, got %s", cfg.LogLevel)
	}
	if !cfg.VPNEnabled {
		t.Errorf("expected VPNEnabled true")
	}
	if cfg.VPNListenPort != 55555 {
		t.Errorf("expected VPNListenPort 55555, got %d", cfg.VPNListenPort)
	}
	if cfg.VPNSubnet != "10.200.0.0/16" {
		t.Errorf("expected VPNSubnet 10.200.0.0/16, got %s", cfg.VPNSubnet)
	}
	if cfg.SecretKey != "env-secret-key-1234567890abcdef1234567890abcdef" {
		t.Errorf("expected secret key from env, got %s", cfg.SecretKey)
	}
	if len(cfg.TrustedProxies) != 3 {
		t.Errorf("expected 3 trusted proxies, got %d", len(cfg.TrustedProxies))
	}
	if len(cfg.TrustedCIDRs) != 2 {
		t.Errorf("expected 2 parsed CIDRs, got %d", len(cfg.TrustedCIDRs))
	}
	if len(cfg.TrustedIPs) != 1 {
		t.Errorf("expected 1 parsed IP, got %d", len(cfg.TrustedIPs))
	}
}

func TestConfigSecretKeyFilePersistence(t *testing.T) {
	tmpDir := t.TempDir()
	os.Unsetenv("SECRET_KEY")
	os.Setenv("DATA_DIR", tmpDir)
	defer os.Unsetenv("DATA_DIR")

	// First run: generates key and saves to .secret_key
	cfg1, err := LoadConfig()
	if err != nil {
		t.Fatalf("first LoadConfig failed: %v", err)
	}

	keyPath := filepath.Join(tmpDir, ".secret_key")
	if _, err := os.Stat(keyPath); err != nil {
		t.Fatalf("expected .secret_key file to exist: %v", err)
	}

	// Second run: reads saved key from file
	cfg2, err := LoadConfig()
	if err != nil {
		t.Fatalf("second LoadConfig failed: %v", err)
	}

	if cfg1.SecretKey != cfg2.SecretKey {
		t.Errorf("expected persistent secret key to match across loads: %s != %s", cfg1.SecretKey, cfg2.SecretKey)
	}
}

func TestTranslationsLoadingAndLookup(t *testing.T) {
	if err := LoadTranslations(); err != nil {
		t.Fatalf("LoadTranslations failed: %v", err)
	}

	// Test English translation
	enLogin := T("en", "login")
	if enLogin == "" || enLogin == "login" && T("en", "non_existent_key") == enLogin {
		// Verify English dictionary is loaded
		all := GetTranslations()
		if len(all["en"]) == 0 {
			t.Errorf("English translations empty")
		}
	}

	// Test Russian translation
	ruTranslations := GetTranslations()["ru"]
	if len(ruTranslations) == 0 {
		t.Errorf("Russian translations not loaded")
	}

	// Fallback to raw key for nonexistent key
	unknown := T("en", "completely_unknown_key_xyz_123")
	if unknown != "completely_unknown_key_xyz_123" {
		t.Errorf("expected raw key fallback for unknown key, got %q", unknown)
	}
}
