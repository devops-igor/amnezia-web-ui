package ssh

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"net"
	"strings"

	gossh "golang.org/x/crypto/ssh"
)

// HostKeyStore defines the persistence interface for known host fingerprints.
type HostKeyStore interface {
	GetKnownHostFingerprint(ctx context.Context, serverID int64) (string, error)
	SaveKnownHostFingerprint(ctx context.Context, serverID int64, fingerprint string) error
}

// ErrHostKeyMismatch is returned when a presented host key differs from the stored known_hosts fingerprint.
type ErrHostKeyMismatch struct {
	ServerID int64
	Host     string
	Expected string
	Actual   string
}

func (e *ErrHostKeyMismatch) Error() string {
	return fmt.Sprintf("host key mismatch for %s (server ID %d): expected %s, got %s", e.Host, e.ServerID, e.Expected, e.Actual)
}

func (e *ErrHostKeyMismatch) Is(target error) bool {
	var t *ErrHostKeyMismatch
	return errors.As(target, &t)
}

// ErrFingerprintRequired is returned on first-seen connection when interactive fingerprint confirmation is enabled.
type ErrFingerprintRequired struct {
	ServerID    int64
	Host        string
	Fingerprint string
}

func (e *ErrFingerprintRequired) Error() string {
	return fmt.Sprintf("host key fingerprint confirmation required for %s (server ID %d): %s", e.Host, e.ServerID, e.Fingerprint)
}

func (e *ErrFingerprintRequired) Is(target error) bool {
	var t *ErrFingerprintRequired
	return errors.As(target, &t)
}

// FingerprintSHA256 computes the OpenSSH SHA-256 fingerprint formatted as "SHA256:<base64>".
func FingerprintSHA256(key gossh.PublicKey) string {
	if key == nil {
		return ""
	}
	return gossh.FingerprintSHA256(key)
}

// FingerprintLegacyMD5 computes the legacy OpenSSH MD5 fingerprint formatted as "aa:bb:cc:...".
func FingerprintLegacyMD5(key gossh.PublicKey) string {
	if key == nil {
		return ""
	}
	return gossh.FingerprintLegacyMD5(key)
}

func normalizeMD5(fp string) string {
	s := strings.TrimSpace(fp)
	s = strings.TrimPrefix(s, "MD5:")
	s = strings.TrimPrefix(s, "md5:")
	s = strings.ReplaceAll(s, ":", "")
	return strings.ToLower(s)
}

// CompareFingerprints performs a timing-safe, format-tolerant comparison of two fingerprints.
// It supports OpenSSH SHA-256 (e.g. "SHA256:<base64>" or "<base64>") and legacy MD5
// (e.g. "MD5:aa:bb:...", "aa:bb:...", or raw 32-char hex "aabb...").
func CompareFingerprints(stored, actual string) bool {
	s := strings.TrimSpace(stored)
	a := strings.TrimSpace(actual)

	if len(s) == 0 || len(a) == 0 {
		return false
	}

	// 1. Direct constant-time compare
	if len(s) == len(a) && subtle.ConstantTimeCompare([]byte(s), []byte(a)) == 1 {
		return true
	}

	// 2. SHA-256 prefix normalization
	sTrim := strings.TrimPrefix(s, "SHA256:")
	aTrim := strings.TrimPrefix(a, "SHA256:")
	if len(sTrim) == len(aTrim) && subtle.ConstantTimeCompare([]byte(sTrim), []byte(aTrim)) == 1 {
		return true
	}

	// 3. MD5 normalization (strips MD5: prefix, colons, lowercases)
	sMD5 := normalizeMD5(s)
	aMD5 := normalizeMD5(a)
	if len(sMD5) == 32 && len(aMD5) == 32 && subtle.ConstantTimeCompare([]byte(sMD5), []byte(aMD5)) == 1 {
		return true
	}

	// 4. Case-insensitive constant-time compare
	sLower := strings.ToLower(s)
	aLower := strings.ToLower(a)
	if len(sLower) == len(aLower) && subtle.ConstantTimeCompare([]byte(sLower), []byte(aLower)) == 1 {
		return true
	}

	return false
}

// HostKeyCallbackOptions configures host key verification behavior.
type HostKeyCallbackOptions struct {
	ServerID               *int64
	Store                  HostKeyStore
	RequireConfirm         bool
	CapturedFingerprintOut *string
	Fallback               gossh.HostKeyCallback
}

// NewHostKeyCallback constructs an OpenSSH-compliant host key verification callback.
func NewHostKeyCallback(ctx context.Context, opts HostKeyCallbackOptions) gossh.HostKeyCallback {
	return func(hostname string, remote net.Addr, key gossh.PublicKey) error {
		actualFP := FingerprintSHA256(key)
		if opts.CapturedFingerprintOut != nil {
			*opts.CapturedFingerprintOut = actualFP
		}

		if opts.ServerID == nil || opts.Store == nil {
			if opts.Fallback != nil {
				return opts.Fallback(hostname, remote, key)
			}
			// If no store/serverID is configured and no fallback specified, default to trust
			return nil
		}

		serverID := *opts.ServerID
		knownFP, err := opts.Store.GetKnownHostFingerprint(ctx, serverID)
		if err != nil {
			return fmt.Errorf("failed to query known_hosts for server %d: %w", serverID, err)
		}

		// First-seen connection
		if strings.TrimSpace(knownFP) == "" {
			if opts.RequireConfirm {
				return &ErrFingerprintRequired{
					ServerID:    serverID,
					Host:        hostname,
					Fingerprint: actualFP,
				}
			}
			// Auto-trust first-seen: persist to DB
			if err := opts.Store.SaveKnownHostFingerprint(ctx, serverID, actualFP); err != nil {
				return fmt.Errorf("failed to save first-seen host fingerprint for server %d: %w", serverID, err)
			}
			return nil
		}

		// Known host verification: check SHA-256 match
		if CompareFingerprints(knownFP, actualFP) {
			return nil
		}

		// Check legacy MD5 match
		actualMD5 := FingerprintLegacyMD5(key)
		if actualMD5 != "" && CompareFingerprints(knownFP, actualMD5) {
			// Opportunistically upgrade stored fingerprint to SHA-256
			_ = opts.Store.SaveKnownHostFingerprint(ctx, serverID, actualFP)
			return nil
		}

		return &ErrHostKeyMismatch{
			ServerID: serverID,
			Host:     hostname,
			Expected: knownFP,
			Actual:   actualFP,
		}
	}
}
