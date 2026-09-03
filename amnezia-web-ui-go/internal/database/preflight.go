package database

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

// ActionableWritabilityError formats the human-actionable remediation error message
// recommending directory chown when directory permissions prevent write access.
func ActionableWritabilityError(dir string) string {
	return fmt.Sprintf("data directory %q is not writable by current user (UID %d, GID %d); if upgrading from legacy Python deployment, please run: sudo chown -R 1000:1000 %q", dir, os.Getuid(), os.Getgid(), dir)
}

// CheckDirWritable verifies that dir exists (or can be created) and that a temporary probe
// file can be created, written, and removed by the current process user.
// If unwritable or permission denied, it logs an actionable error via slog.Error and returns an error.
func CheckDirWritable(dir string) error {
	if dir == "" {
		dir = "."
	}

	cleanDir := filepath.Clean(dir)

	// Attempt to create the directory if it does not already exist
	if err := os.MkdirAll(cleanDir, 0750); err != nil {
		actionableMsg := ActionableWritabilityError(cleanDir)
		slog.Error(actionableMsg, "path", cleanDir, "uid", os.Getuid(), "gid", os.Getgid(), "err", err)
		return fmt.Errorf("%s: %w", actionableMsg, err)
	}

	// Attempt creating, writing, and removing a temporary probe file
	probeFile := filepath.Join(cleanDir, fmt.Sprintf(".perm_probe_%d", time.Now().UnixNano()))
	// #nosec G304 -- Trusted data directory writability probe file
	f, err := os.OpenFile(probeFile, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		actionableMsg := ActionableWritabilityError(cleanDir)
		slog.Error(actionableMsg, "path", cleanDir, "uid", os.Getuid(), "gid", os.Getgid(), "err", err)
		return fmt.Errorf("%s: %w", actionableMsg, err)
	}

	if _, err := f.Write([]byte("probe")); err != nil {
		_ = f.Close()
		_ = os.Remove(probeFile)
		actionableMsg := ActionableWritabilityError(cleanDir)
		slog.Error(actionableMsg, "path", cleanDir, "uid", os.Getuid(), "gid", os.Getgid(), "err", err)
		return fmt.Errorf("%s: %w", actionableMsg, err)
	}

	if err := f.Close(); err != nil {
		_ = os.Remove(probeFile)
		actionableMsg := ActionableWritabilityError(cleanDir)
		slog.Error(actionableMsg, "path", cleanDir, "uid", os.Getuid(), "gid", os.Getgid(), "err", err)
		return fmt.Errorf("%s: %w", actionableMsg, err)
	}

	if err := os.Remove(probeFile); err != nil {
		actionableMsg := ActionableWritabilityError(cleanDir)
		slog.Error(actionableMsg, "path", cleanDir, "uid", os.Getuid(), "gid", os.Getgid(), "err", err)
		return fmt.Errorf("%s: %w", actionableMsg, err)
	}

	return nil
}

// CheckPreflight performs startup writability preflight checks for both the data directory
// and the database file's parent directory before initializing SQLite.
func CheckPreflight(dataDir, dbPath string) error {
	dirs := []string{dataDir}
	if dbPath != "" && dbPath != ":memory:" {
		dbDir := filepath.Dir(dbPath)
		if filepath.Clean(dbDir) != filepath.Clean(dataDir) {
			dirs = append(dirs, dbDir)
		}
	}

	for _, d := range dirs {
		if d == "" {
			d = "."
		}
		if err := CheckDirWritable(d); err != nil {
			return err
		}
	}

	return nil
}
