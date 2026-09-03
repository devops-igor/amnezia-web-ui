package database_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/devops-igor/amnezia-web-ui-go/internal/database"
)

func TestCheckDirWritable_Success(t *testing.T) {
	tempDir := t.TempDir()
	targetDir := filepath.Join(tempDir, "subdir_writable")

	if err := database.CheckDirWritable(targetDir); err != nil {
		t.Fatalf("expected CheckDirWritable to succeed on new subdir, got: %v", err)
	}

	// Verify probe file was cleaned up
	entries, err := os.ReadDir(targetDir)
	if err != nil {
		t.Fatalf("failed to read target directory: %v", err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".perm_probe") {
			t.Errorf("temporary probe file was not cleaned up: %s", entry.Name())
		}
	}
}

func TestCheckDirWritable_ReadOnlyDir(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("skipping directory permission test when executing as root (UID 0)")
	}

	tempDir := t.TempDir()
	roDir := filepath.Join(tempDir, "readonly_dir")
	if err := os.MkdirAll(roDir, 0755); err != nil {
		t.Fatalf("failed to create directory: %v", err)
	}
	// Make directory read-only
	if err := os.Chmod(roDir, 0555); err != nil {
		t.Fatalf("failed to chmod directory: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(roDir, 0755)
	})

	err := database.CheckDirWritable(roDir)
	if err == nil {
		t.Fatalf("expected CheckDirWritable to fail on read-only directory, got nil")
	}

	expectedSubstring := fmt.Sprintf("data directory %q is not writable by current user (UID %d, GID %d)", roDir, os.Getuid(), os.Getgid())
	if !strings.Contains(err.Error(), expectedSubstring) {
		t.Errorf("expected error to contain %q, got %q", expectedSubstring, err.Error())
	}

	remediationGuide := fmt.Sprintf("sudo chown -R 1000:1000 %q", roDir)
	if !strings.Contains(err.Error(), remediationGuide) {
		t.Errorf("expected error to contain remediation instruction %q, got %q", remediationGuide, err.Error())
	}
}

func TestCheckDirWritable_UnwritableParentDir(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("skipping directory permission test when executing as root (UID 0)")
	}

	tempDir := t.TempDir()
	roParent := filepath.Join(tempDir, "ro_parent")
	if err := os.MkdirAll(roParent, 0755); err != nil {
		t.Fatalf("failed to create parent dir: %v", err)
	}
	if err := os.Chmod(roParent, 0555); err != nil {
		t.Fatalf("failed to chmod parent dir: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(roParent, 0755)
	})

	targetDir := filepath.Join(roParent, "child_dir")
	err := database.CheckDirWritable(targetDir)
	if err == nil {
		t.Fatalf("expected CheckDirWritable to fail when parent is not writable, got nil")
	}

	expectedSubstring := fmt.Sprintf("data directory %q is not writable by current user", targetDir)
	if !strings.Contains(err.Error(), expectedSubstring) {
		t.Errorf("expected error to contain %q, got %q", expectedSubstring, err.Error())
	}
}

func TestCheckPreflight_Success(t *testing.T) {
	tempDir := t.TempDir()
	dataDir := filepath.Join(tempDir, "data")
	dbPath := filepath.Join(tempDir, "db", "panel.db")

	if err := database.CheckPreflight(dataDir, dbPath); err != nil {
		t.Fatalf("expected CheckPreflight to succeed, got: %v", err)
	}
}

func TestCheckPreflight_ReadOnlyDataDir(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("skipping directory permission test when executing as root (UID 0)")
	}

	tempDir := t.TempDir()
	roDataDir := filepath.Join(tempDir, "ro_data")
	if err := os.MkdirAll(roDataDir, 0755); err != nil {
		t.Fatalf("failed to create data dir: %v", err)
	}
	if err := os.Chmod(roDataDir, 0555); err != nil {
		t.Fatalf("failed to chmod data dir: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(roDataDir, 0755)
	})

	dbPath := filepath.Join(roDataDir, "panel.db")
	err := database.CheckPreflight(roDataDir, dbPath)
	if err == nil {
		t.Fatalf("expected CheckPreflight to fail on read-only data dir, got nil")
	}

	if !strings.Contains(err.Error(), "is not writable by current user") {
		t.Errorf("expected error to contain writability warning, got: %v", err)
	}
}

func TestDatabaseOpen_PreflightFailsOnReadOnlyDir(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("skipping directory permission test when executing as root (UID 0)")
	}

	tempDir := t.TempDir()
	roDir := filepath.Join(tempDir, "ro_db_dir")
	if err := os.MkdirAll(roDir, 0755); err != nil {
		t.Fatalf("failed to create db dir: %v", err)
	}
	if err := os.Chmod(roDir, 0555); err != nil {
		t.Fatalf("failed to chmod db dir: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(roDir, 0755)
	})

	dbPath := filepath.Join(roDir, "panel.db")
	db, err := database.Open(dbPath, "test-secret-key-1234567890123456")
	if err == nil {
		_ = db.Close()
		t.Fatalf("expected database.Open to fail on unwritable directory, got nil")
	}

	if !strings.Contains(err.Error(), "is not writable by current user") {
		t.Errorf("expected error to contain preflight message, got: %v", err)
	}
}
