package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/devops-igor/amnezia-web-ui-go/internal/config"
	"github.com/devops-igor/amnezia-web-ui-go/internal/database"
	"github.com/devops-igor/amnezia-web-ui-go/internal/models"
	"github.com/devops-igor/amnezia-web-ui-go/internal/security"
)

func TestRunServerGracefulShutdown(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("DATA_DIR", tempDir)
	t.Setenv("DB_PATH", filepath.Join(tempDir, "panel_test.db"))
	t.Setenv("PORT", "59123")
	t.Setenv("SECRET_KEY", "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")

	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)
	go func() {
		errCh <- run(ctx)
	}()

	// Allow server to boot
	time.Sleep(100 * time.Millisecond)

	// Cancel context to trigger graceful shutdown
	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("run() returned error on graceful shutdown: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for run() to shutdown")
	}
}

func TestServerCredentialsEncryptionEndToEnd(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "panel_crypto_test.db")
	secretKey := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

	t.Setenv("DATA_DIR", tempDir)
	t.Setenv("DB_PATH", dbPath)
	t.Setenv("SECRET_KEY", secretKey)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load failed: %v", err)
	}

	db, err := database.New(cfg.DBPath, cfg.SecretKey)
	if err != nil {
		t.Fatalf("database.New failed: %v", err)
	}
	defer db.Close()

	ctx := context.Background()

	rawPlainPassword := "SuperSecretServerPassword!987"
	rawPlainKey := "-----BEGIN OPENSSH PRIVATE KEY-----\nb3BlbnNzaC1rZXktdjEAAAAA..."

	srv := &models.Server{
		Name:      "Production Germany",
		Host:      "203.0.113.10",
		SSHUser:   "root",
		SSHPort:   22,
		SSHPass:   rawPlainPassword,
		SSHKey:    rawPlainKey,
		Protocols: map[string]any{"awg": map[string]any{"port": 51820}},
	}

	serverID, err := db.CreateServer(ctx, srv)
	if err != nil || serverID <= 0 {
		t.Fatalf("db.CreateServer failed: %v", err)
	}

	// 1. Verify that raw SQLite row contains Fernet-encrypted ciphertext
	var storedPass, storedKey string
	err = db.QueryRowContext(ctx, "SELECT ssh_pass, ssh_key FROM servers WHERE id = ?", serverID).Scan(&storedPass, &storedKey)
	if err != nil {
		t.Fatalf("failed to query raw server row: %v", err)
	}

	if storedPass == rawPlainPassword {
		t.Errorf("CRIT-1 REGRESSION: ssh_pass stored in plaintext in SQLite: %s", storedPass)
	}
	if storedKey == rawPlainKey {
		t.Errorf("CRIT-1 REGRESSION: ssh_key stored in plaintext in SQLite: %s", storedKey)
	}
	if !security.LooksLikeFernetToken(storedPass) {
		t.Errorf("expected ssh_pass to be Fernet token, got %s", storedPass)
	}
	if !security.LooksLikeFernetToken(storedKey) {
		t.Errorf("expected ssh_key to be Fernet token, got %s", storedKey)
	}

	// 2. Verify that GetServer decrypts credentials transparently
	retrieved, err := db.GetServer(ctx, serverID)
	if err != nil {
		t.Fatalf("db.GetServer failed: %v", err)
	}
	if retrieved.SSHPass != rawPlainPassword {
		t.Errorf("expected decrypted SSHPass %q, got %q", rawPlainPassword, retrieved.SSHPass)
	}
	if retrieved.SSHKey != rawPlainKey {
		t.Errorf("expected decrypted SSHKey %q, got %q", rawPlainKey, retrieved.SSHKey)
	}

	// 3. Verify GetServerByID and GetAllServers return correct plaintext
	retrievedByID, err := db.GetServerByID(ctx, serverID)
	if err != nil {
		t.Fatalf("db.GetServerByID failed: %v", err)
	}
	if retrievedByID.SSHPass != rawPlainPassword {
		t.Errorf("expected decrypted SSHPass %q, got %q", rawPlainPassword, retrievedByID.SSHPass)
	}

	allServers, err := db.GetAllServers(ctx)
	if err != nil || len(allServers) != 1 {
		t.Fatalf("db.GetAllServers failed: %v", err)
	}
	if allServers[0].SSHPass != rawPlainPassword || allServers[0].SSHKey != rawPlainKey {
		t.Errorf("GetAllServers decrypted credentials mismatch: %+v", allServers[0])
	}
}

func TestRunLogLevels(t *testing.T) {
	logLevels := []string{"DEBUG", "WARN", "ERROR", "INFO"}

	for _, lvl := range logLevels {
		t.Run("LogLevel_"+lvl, func(t *testing.T) {
			tempDir := t.TempDir()
			t.Setenv("DATA_DIR", tempDir)
			t.Setenv("DB_PATH", filepath.Join(tempDir, "panel_log.db"))
			t.Setenv("PORT", "59199")
			t.Setenv("LOG_LEVEL", lvl)
			t.Setenv("SECRET_KEY", "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")

			ctx, cancel := context.WithCancel(context.Background())
			errCh := make(chan error, 1)
			go func() {
				errCh <- run(ctx)
			}()

			time.Sleep(50 * time.Millisecond)
			cancel()

			select {
			case err := <-errCh:
				if err != nil {
					t.Fatalf("run with log level %s failed: %v", lvl, err)
				}
			case <-time.After(3 * time.Second):
				t.Fatalf("timed out for log level %s", lvl)
			}
		})
	}
}
