package ssh

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"

	gossh "golang.org/x/crypto/ssh"
)

type memoryHostKeyStore struct {
	mu   sync.Mutex
	data map[int64]string
	err  error
}

func newMemoryHostKeyStore() *memoryHostKeyStore {
	return &memoryHostKeyStore{
		data: make(map[int64]string),
	}
}

func (m *memoryHostKeyStore) GetKnownHostFingerprint(ctx context.Context, serverID int64) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return "", m.err
	}
	return m.data[serverID], nil
}

func (m *memoryHostKeyStore) SaveKnownHostFingerprint(ctx context.Context, serverID int64, fingerprint string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return m.err
	}
	m.data[serverID] = fingerprint
	return nil
}

func generateDummyPublicKey(t *testing.T) gossh.PublicKey {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate rsa key: %v", err)
	}
	pub, err := gossh.NewPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatalf("failed to create ssh public key: %v", err)
	}
	return pub
}

func TestFingerprintSHA256(t *testing.T) {
	pub := generateDummyPublicKey(t)
	fp := FingerprintSHA256(pub)
	if len(fp) < 10 || fp[:7] != "SHA256:" {
		t.Fatalf("expected fingerprint to start with SHA256:, got %s", fp)
	}

	if FingerprintSHA256(nil) != "" {
		t.Fatalf("expected empty fingerprint for nil key")
	}
}

func TestCompareFingerprints(t *testing.T) {
	tests := []struct {
		name    string
		stored  string
		actual  string
		matched bool
	}{
		{"exact match", "SHA256:abc123xyz", "SHA256:abc123xyz", true},
		{"prefix match", "abc123xyz", "SHA256:abc123xyz", true},
		{"reverse prefix match", "SHA256:abc123xyz", "abc123xyz", true},
		{"hex case insensitive", "ab:cd:ef:01", "AB:CD:EF:01", true},
		{"mismatch", "SHA256:abc123xyz", "SHA256:different", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CompareFingerprints(tt.stored, tt.actual)
			if got != tt.matched {
				t.Fatalf("expected match=%v, got %v", tt.matched, got)
			}
		})
	}
}

func TestHostKeyCallback_FirstSeenAutoTrust(t *testing.T) {
	ctx := context.Background()
	store := newMemoryHostKeyStore()
	serverID := int64(100)
	pub := generateDummyPublicKey(t)
	expectedFP := FingerprintSHA256(pub)

	var captured string
	callback := NewHostKeyCallback(ctx, HostKeyCallbackOptions{
		ServerID:               &serverID,
		Store:                  store,
		RequireConfirm:         false,
		CapturedFingerprintOut: &captured,
	})

	dummyAddr, _ := net.ResolveTCPAddr("tcp", "1.2.3.4:22")
	err := callback("1.2.3.4", dummyAddr, pub)
	if err != nil {
		t.Fatalf("unexpected error on first seen: %v", err)
	}

	if captured != expectedFP {
		t.Fatalf("expected captured fingerprint %s, got %s", expectedFP, captured)
	}

	storedFP, _ := store.GetKnownHostFingerprint(ctx, serverID)
	if storedFP != expectedFP {
		t.Fatalf("expected stored fingerprint %s, got %s", expectedFP, storedFP)
	}
}

func TestHostKeyCallback_FirstSeenRequireConfirm(t *testing.T) {
	ctx := context.Background()
	store := newMemoryHostKeyStore()
	serverID := int64(101)
	pub := generateDummyPublicKey(t)
	expectedFP := FingerprintSHA256(pub)

	callback := NewHostKeyCallback(ctx, HostKeyCallbackOptions{
		ServerID:       &serverID,
		Store:          store,
		RequireConfirm: true,
	})

	dummyAddr, _ := net.ResolveTCPAddr("tcp", "1.2.3.4:22")
	err := callback("1.2.3.4", dummyAddr, pub)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	var reqErr *ErrFingerprintRequired
	if !errors.As(err, &reqErr) {
		t.Fatalf("expected ErrFingerprintRequired, got %T (%v)", err, err)
	}

	if reqErr.Fingerprint != expectedFP {
		t.Fatalf("expected fingerprint %s, got %s", expectedFP, reqErr.Fingerprint)
	}

	if !strings.Contains(reqErr.Error(), expectedFP) {
		t.Fatalf("expected error message to contain %s, got %s", expectedFP, reqErr.Error())
	}
	if !reqErr.Is(&ErrFingerprintRequired{}) {
		t.Fatal("expected Is to return true for ErrFingerprintRequired")
	}
}

func TestHostKeyCallback_MatchAndMismatch(t *testing.T) {
	ctx := context.Background()
	store := newMemoryHostKeyStore()
	serverID := int64(102)

	pub1 := generateDummyPublicKey(t)
	fp1 := FingerprintSHA256(pub1)
	_ = store.SaveKnownHostFingerprint(ctx, serverID, fp1)

	callback := NewHostKeyCallback(ctx, HostKeyCallbackOptions{
		ServerID: &serverID,
		Store:    store,
	})

	dummyAddr, _ := net.ResolveTCPAddr("tcp", "1.2.3.4:22")

	// 1. Same key -> succeeds
	if err := callback("1.2.3.4", dummyAddr, pub1); err != nil {
		t.Fatalf("expected verification success, got: %v", err)
	}

	// 2. Different key -> fails with ErrHostKeyMismatch
	pub2 := generateDummyPublicKey(t)
	err := callback("1.2.3.4", dummyAddr, pub2)
	if err == nil {
		t.Fatal("expected mismatch error, got nil")
	}

	var misErr *ErrHostKeyMismatch
	if !errors.As(err, &misErr) {
		t.Fatalf("expected ErrHostKeyMismatch, got %T (%v)", err, err)
	}
	if misErr.Expected != fp1 {
		t.Fatalf("expected %s, got %s", fp1, misErr.Expected)
	}
	if !strings.Contains(misErr.Error(), "host key mismatch") {
		t.Fatalf("expected error message to contain 'host key mismatch', got %s", misErr.Error())
	}
	if !misErr.Is(&ErrHostKeyMismatch{}) {
		t.Fatal("expected Is to return true for ErrHostKeyMismatch")
	}
}

func TestHostKeyCallback_FallbackAndErrors(t *testing.T) {
	ctx := context.Background()
	pub := generateDummyPublicKey(t)
	dummyAddr, _ := net.ResolveTCPAddr("tcp", "1.2.3.4:22")

	// 1. Fallback callback
	calledFallback := false
	callback := NewHostKeyCallback(ctx, HostKeyCallbackOptions{
		Fallback: func(hostname string, remote net.Addr, key gossh.PublicKey) error {
			calledFallback = true
			return nil
		},
	})
	if err := callback("1.2.3.4", dummyAddr, pub); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !calledFallback {
		t.Fatal("expected fallback to be called")
	}

	// 2. Store get error
	store := newMemoryHostKeyStore()
	store.err = errors.New("db query error")
	serverID := int64(103)
	errCallback := NewHostKeyCallback(ctx, HostKeyCallbackOptions{
		ServerID: &serverID,
		Store:    store,
	})
	if err := errCallback("1.2.3.4", dummyAddr, pub); err == nil {
		t.Fatal("expected store error, got nil")
	}

	// 3. Store save error on first-seen
	store2 := newMemoryHostKeyStore()
	serverID2 := int64(104)
	saveErrCallback := NewHostKeyCallback(ctx, HostKeyCallbackOptions{
		ServerID: &serverID2,
		Store:    store2,
	})
	store2.err = errors.New("db save error")
	if err := saveErrCallback("1.2.3.4", dummyAddr, pub); err == nil {
		t.Fatal("expected store save error, got nil")
	}
}
