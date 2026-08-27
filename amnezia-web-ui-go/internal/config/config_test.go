package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfigDefaults(t *testing.T) {
	// Clear env vars
	os.Unsetenv("SECRET_KEY")
	os.Unsetenv("DATA_DIR")
	os.Unsetenv("DB_PATH")
	os.Unsetenv("PORT")
	os.Unsetenv("PANEL_PORT")
	os.Unsetenv("HOST")
	os.Unsetenv("TRUSTED_PROXIES")
	os.Unsetenv("VPN_ENABLED")

	tempDir := t.TempDir()
	t.Setenv("DATA_DIR", tempDir)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}

	if cfg.Host != "0.0.0.0" {
		t.Errorf("expected default Host 0.0.0.0, got %s", cfg.Host)
	}
	if cfg.Port != 5000 {
		t.Errorf("expected default Port 5000, got %d", cfg.Port)
	}
	if cfg.DataDir != tempDir {
		t.Errorf("expected DataDir %s, got %s", tempDir, cfg.DataDir)
	}
	if cfg.DBPath != filepath.Join(tempDir, "panel.db") {
		t.Errorf("expected DBPath %s, got %s", filepath.Join(tempDir, "panel.db"), cfg.DBPath)
	}
	if len(cfg.SecretKey) != 64 {
		t.Errorf("expected 64-char hex SecretKey, got length %d (%s)", len(cfg.SecretKey), cfg.SecretKey)
	}
	if cfg.VPNEnabled {
		t.Errorf("expected default VPNEnabled false, got true")
	}
}

func TestConfigEnvOverrides(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("DATA_DIR", tempDir)
	t.Setenv("SECRET_KEY", "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	t.Setenv("HOST", "127.0.0.1")
	t.Setenv("PORT", "8080")
	t.Setenv("TRUSTED_PROXIES", "10.0.0.1, 192.168.1.0/24")
	t.Setenv("VPN_ENABLED", "true")
	t.Setenv("VPN_LISTEN_PORT", "51821")
	t.Setenv("VPN_SUBNET", "10.200.0.0/16")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.Host != "127.0.0.1" {
		t.Errorf("expected Host 127.0.0.1, got %s", cfg.Host)
	}
	if cfg.Port != 8080 {
		t.Errorf("expected Port 8080, got %d", cfg.Port)
	}
	if cfg.SecretKey != "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef" {
		t.Errorf("expected custom SecretKey, got %s", cfg.SecretKey)
	}
	if len(cfg.TrustedProxies) != 2 || cfg.TrustedProxies[0] != "10.0.0.1" || cfg.TrustedProxies[1] != "192.168.1.0/24" {
		t.Errorf("unexpected TrustedProxies: %v", cfg.TrustedProxies)
	}
	if !cfg.VPNEnabled {
		t.Errorf("expected VPNEnabled true, got false")
	}
	if cfg.VPNListenPort != 51821 {
		t.Errorf("expected VPNListenPort 51821, got %d", cfg.VPNListenPort)
	}
	if cfg.VPNSubnet != "10.200.0.0/16" {
		t.Errorf("expected VPNSubnet 10.200.0.0/16, got %s", cfg.VPNSubnet)
	}
}

func TestConfigSecretKeyFilePersistence(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("DATA_DIR", tempDir)
	os.Unsetenv("SECRET_KEY")

	// First load generates key
	cfg1, err := Load()
	if err != nil {
		t.Fatalf("Load() 1 error: %v", err)
	}

	// Verify .secret_key exists
	keyPath := filepath.Join(tempDir, ".secret_key")
	data, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatalf("expected .secret_key file to exist: %v", err)
	}
	if string(data) != cfg1.SecretKey {
		t.Fatalf("file content %s did not match cfg.SecretKey %s", string(data), cfg1.SecretKey)
	}

	// Second load should read existing key
	cfg2, err := Load()
	if err != nil {
		t.Fatalf("Load() 2 error: %v", err)
	}
	if cfg2.SecretKey != cfg1.SecretKey {
		t.Errorf("expected persistent SecretKey %s, got %s", cfg1.SecretKey, cfg2.SecretKey)
	}
}
