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

// CompareFingerprints performs a timing-safe, format-tolerant comparison of two fingerprints.
func CompareFingerprints(stored, actual string) bool {
	s := strings.TrimSpace(stored)
	a := strings.TrimSpace(actual)

	if subtle.ConstantTimeCompare([]byte(s), []byte(a)) == 1 {
		return true
	}

	// Normalize potential SHA256: prefix differences
	sTrim := strings.TrimPrefix(s, "SHA256:")
	aTrim := strings.TrimPrefix(a, "SHA256:")
	if subtle.ConstantTimeCompare([]byte(sTrim), []byte(aTrim)) == 1 {
		return true
	}

	// Case-insensitive check for legacy hex fingerprints (MD5 / raw sha256 hex)
	if strings.EqualFold(s, a) {
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

		// Known host verification
		if !CompareFingerprints(knownFP, actualFP) {
			return &ErrHostKeyMismatch{
				ServerID: serverID,
				Host:     hostname,
				Expected: knownFP,
				Actual:   actualFP,
			}
		}

		return nil
	}
}
