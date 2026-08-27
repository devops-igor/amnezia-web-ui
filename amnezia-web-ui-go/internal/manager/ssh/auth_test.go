package ssh

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"testing"
)

func TestBuildAuthMethods_PasswordOnly(t *testing.T) {
	methods, err := BuildAuthMethods("secret123", "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(methods) != 2 {
		t.Fatalf("expected 2 auth methods (password + keyboard-interactive), got %d", len(methods))
	}
}

func TestBuildAuthMethods_NoAuth(t *testing.T) {
	_, err := BuildAuthMethods("", "", "")
	if !errors.Is(err, ErrNoAuthMethod) {
		t.Fatalf("expected ErrNoAuthMethod, got %v", err)
	}
}

func TestBuildAuthMethods_RSAKey(t *testing.T) {
	keyPEM, err := GenerateTestRSAKey(2048)
	if err != nil {
		t.Fatalf("failed to generate RSA key: %v", err)
	}

	methods, err := BuildAuthMethods("", keyPEM, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(methods) != 1 {
		t.Fatalf("expected 1 auth method, got %d", len(methods))
	}
}

func TestBuildAuthMethods_Ed25519Key(t *testing.T) {
	keyPEM, err := GenerateTestEd25519Key()
	if err != nil {
		t.Fatalf("failed to generate Ed25519 key: %v", err)
	}

	methods, err := BuildAuthMethods("", keyPEM, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(methods) != 1 {
		t.Fatalf("expected 1 auth method, got %d", len(methods))
	}
}

func TestBuildAuthMethods_ECDSAKey(t *testing.T) {
	keyPEM, err := GenerateTestECDSAKey()
	if err != nil {
		t.Fatalf("failed to generate ECDSA key: %v", err)
	}

	methods, err := BuildAuthMethods("", keyPEM, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(methods) != 1 {
		t.Fatalf("expected 1 auth method, got %d", len(methods))
	}
}

func TestBuildAuthMethods_BothKeyAndPassword(t *testing.T) {
	keyPEM, err := GenerateTestEd25519Key()
	if err != nil {
		t.Fatalf("failed to generate key: %v", err)
	}

	methods, err := BuildAuthMethods("pass123", keyPEM, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(methods) != 3 { // publickey, password, keyboard-interactive
		t.Fatalf("expected 3 auth methods, got %d", len(methods))
	}
}

func TestParsePrivateKey_Encrypted(t *testing.T) {
	// Generate an RSA private key and encrypt it with PKCS#1 DES/AES
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate key: %v", err)
	}
	der := x509.MarshalPKCS1PrivateKey(key)

	// Encrypt PEM block
	passphrase := "mySecretPassphrase"
	// Using x509.EncryptPEMBlock (legacy format test)
	block, err := x509.EncryptPEMBlock(rand.Reader, "RSA PRIVATE KEY", der, []byte(passphrase), x509.PEMCipherAES256) //nolint:staticcheck
	if err != nil {
		t.Fatalf("failed to encrypt PEM block: %v", err)
	}
	encPEM := pem.EncodeToMemory(block)

	// 1. Missing passphrase
	_, err = ParsePrivateKey(string(encPEM), "")
	if !errors.Is(err, ErrPassphraseRequired) {
		t.Fatalf("expected ErrPassphraseRequired, got %v", err)
	}

	// 2. Wrong passphrase
	_, err = ParsePrivateKey(string(encPEM), "wrongpass")
	if !errors.Is(err, ErrInvalidPassphrase) {
		t.Fatalf("expected ErrInvalidPassphrase, got %v", err)
	}

	// 3. Correct passphrase
	signer, err := ParsePrivateKey(string(encPEM), passphrase)
	if err != nil {
		t.Fatalf("failed to parse encrypted private key: %v", err)
	}
	if signer == nil {
		t.Fatal("expected non-nil signer")
	}
}

func TestParsePrivateKey_Errors(t *testing.T) {
	// Empty key
	_, err := ParsePrivateKey("", "")
	if !errors.Is(err, ErrEmptyPrivateKey) {
		t.Fatalf("expected ErrEmptyPrivateKey, got %v", err)
	}

	// Corrupted / invalid key
	_, err = ParsePrivateKey("not a valid pem string", "")
	if !errors.Is(err, ErrInvalidPrivateKey) {
		t.Fatalf("expected ErrInvalidPrivateKey, got %v", err)
	}
}
