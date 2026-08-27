package ssh

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"

	"github.com/pkg/sftp"
)

var (
	// ErrSFTPNotAvailable is returned when SFTP client cannot be opened or is nil.
	ErrSFTPNotAvailable = errors.New("sftp client is not available")
	// ErrEmptyPath is returned when remote path is empty.
	ErrEmptyPath = errors.New("remote path cannot be empty")
)

// NormalizeLineEndings replaces CRLF with LF in byte slices.
func NormalizeLineEndings(data []byte) []byte {
	return bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
}

// EnsureRemoteDir recursively creates remote directories over SFTP similar to mkdir -p.
func EnsureRemoteDir(client *sftp.Client, remoteDir string, mode os.FileMode) error {
	if client == nil {
		return ErrSFTPNotAvailable
	}
	cleanDir := path.Clean(remoteDir)
	if cleanDir == "." || cleanDir == "/" || cleanDir == "" {
		return nil
	}

	// Check if already exists
	fi, err := client.Stat(cleanDir)
	if err == nil {
		if fi.IsDir() {
			return nil
		}
		return fmt.Errorf("remote path %s exists but is not a directory", cleanDir)
	}

	// Recursively ensure parent
	parent := path.Dir(cleanDir)
	if parent != cleanDir && parent != "." && parent != "/" {
		if err := EnsureRemoteDir(client, parent, mode); err != nil {
			return err
		}
	}

	if err := client.Mkdir(cleanDir); err != nil {
		// Ignore error if directory was created concurrently
		if stat, statErr := client.Stat(cleanDir); statErr == nil && stat.IsDir() {
			return nil
		}
		return fmt.Errorf("failed to create remote directory %s: %w", cleanDir, err)
	}

	_ = client.Chmod(cleanDir, mode)
	return nil
}

// SFTPUpload writes content to remotePath, ensuring parent directory exists and setting file permissions.
func SFTPUpload(ctx context.Context, client *sftp.Client, remotePath string, content []byte, mode os.FileMode) error {
	if client == nil {
		return ErrSFTPNotAvailable
	}
	if strings.TrimSpace(remotePath) == "" {
		return ErrEmptyPath
	}

	if err := ctx.Err(); err != nil {
		return err
	}

	dir := path.Dir(remotePath)
	if err := EnsureRemoteDir(client, dir, 0755); err != nil {
		return fmt.Errorf("failed to ensure parent directory %s: %w", dir, err)
	}

	f, err := client.OpenFile(remotePath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
	if err != nil {
		return fmt.Errorf("failed to open remote file %s: %w", remotePath, err)
	}
	defer f.Close()

	if _, err := f.Write(content); err != nil {
		return fmt.Errorf("failed to write content to %s: %w", remotePath, err)
	}

	if err := client.Chmod(remotePath, mode); err != nil {
		return fmt.Errorf("failed to chmod %s: %w", remotePath, err)
	}

	return nil
}

// SFTPDownload reads the complete content of a remote file over SFTP.
func SFTPDownload(ctx context.Context, client *sftp.Client, remotePath string) ([]byte, error) {
	if client == nil {
		return nil, ErrSFTPNotAvailable
	}
	if strings.TrimSpace(remotePath) == "" {
		return nil, ErrEmptyPath
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	f, err := client.Open(remotePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open remote file %s: %w", remotePath, err)
	}
	defer f.Close()

	data, err := io.ReadAll(f)
	if err != nil {
		return nil, fmt.Errorf("failed to read remote file %s: %w", remotePath, err)
	}

	return data, nil
}

// SFTPFileExists checks if a remote file or directory exists over SFTP.
func SFTPFileExists(ctx context.Context, client *sftp.Client, remotePath string) (bool, error) {
	if client == nil {
		return false, ErrSFTPNotAvailable
	}
	if strings.TrimSpace(remotePath) == "" {
		return false, ErrEmptyPath
	}

	if err := ctx.Err(); err != nil {
		return false, err
	}

	_, err := client.Stat(remotePath)
	if err == nil {
		return true, nil
	}

	if os.IsNotExist(err) || errors.Is(err, os.ErrNotExist) || strings.Contains(strings.ToLower(err.Error()), "not exist") || strings.Contains(strings.ToLower(err.Error()), "file does not exist") {
		return false, nil
	}

	return false, err
}

// GenerateRandomTempPath creates a randomized temp file path under /tmp.
func GenerateRandomTempPath(prefix string) string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	if prefix == "" {
		prefix = "amnz"
	}
	return fmt.Sprintf("/tmp/_%s_%s", prefix, hex.EncodeToString(b))
}
