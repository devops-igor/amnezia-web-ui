package security

import (
	"strings"
	"testing"
)

func TestDeriveFernetKeys(t *testing.T) {
	secretKey := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	sigKey, encKey, err := DeriveFernetKeys(secretKey)
	if err != nil {
		t.Fatalf("DeriveFernetKeys failed: %v", err)
	}

	if len(sigKey) != 16 {
		t.Errorf("expected 16-byte signing key, got %d", len(sigKey))
	}
	if len(encKey) != 16 {
		t.Errorf("expected 16-byte encryption key, got %d", len(encKey))
	}

	// Deterministic
	sigKey2, encKey2, _ := DeriveFernetKeys(secretKey)
	if string(sigKey) != string(sigKey2) || string(encKey) != string(encKey2) {
		t.Errorf("expected deterministic key derivation")
	}

	// Empty secret key error
	_, _, err = DeriveFernetKeys("")
	if err == nil {
		t.Errorf("expected error with empty secret key")
	}
}

func TestFernetEncryptDecryptRoundTrip(t *testing.T) {
	secretKey := "my-very-secret-test-key-32-bytes"
	plaintext := "SuperSecretSSHPassword123!@#"

	token, err := EncryptCredential(plaintext, secretKey)
	if err != nil {
		t.Fatalf("EncryptCredential failed: %v", err)
	}

	if !strings.HasPrefix(token, "gAAAAA") {
		t.Errorf("expected Fernet token prefix gAAAAA, got: %s", token)
	}

	if !LooksLikeFernetToken(token) {
		t.Errorf("expected LooksLikeFernetToken to return true for %s", token)
	}

	decrypted, err := DecryptCredential(token, secretKey)
	if err != nil {
		t.Fatalf("DecryptCredential failed: %v", err)
	}

	if decrypted != plaintext {
		t.Errorf("decrypted text %q did not match plaintext %q", decrypted, plaintext)
	}
}

func TestFernetTamperedTokenAndWrongKey(t *testing.T) {
	secretKey := "secret-key-1"
	plaintext := "SecretData"

	token, err := EncryptCredential(plaintext, secretKey)
	if err != nil {
		t.Fatalf("EncryptCredential failed: %v", err)
	}

	// Decrypt with wrong key
	_, err = DecryptCredential(token, "different-secret-key-2")
	if err == nil {
		t.Errorf("expected decryption failure with wrong key")
	}

	// Safe decrypt with wrong key returns ""
	safe := DecryptCredentialSafe(token, "different-secret-key-2")
	if safe != "" {
		t.Errorf("expected safe decrypt with wrong key to return empty string, got: %s", safe)
	}

	// Decrypt with tampered token
	tampered := token[:len(token)-5] + "AAAAA"
	_, err = DecryptCredential(tampered, secretKey)
	if err == nil {
		t.Errorf("expected decryption failure with tampered HMAC")
	}
}

func TestFernetEmptyAndInvalidInputs(t *testing.T) {
	secretKey := "secret-key"
	enc, err := EncryptCredential("", secretKey)
	if err != nil || enc != "" {
		t.Errorf("expected empty encryption for empty plaintext")
	}

	dec, err := DecryptCredential("", secretKey)
	if err != nil || dec != "" {
		t.Errorf("expected empty decryption for empty token")
	}

	_, err = DecryptCredential("not-a-token", secretKey)
	if err == nil {
		t.Errorf("expected error for invalid token format")
	}

	if LooksLikeFernetToken("not-a-token") {
		t.Errorf("expected false for invalid token format")
	}
}

func TestBcryptPasswordHashing(t *testing.T) {
	password := "CorrectHorseBatteryStaple"
	hash, err := HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword failed: %v", err)
	}

	if !CheckPasswordHash(password, hash) {
		t.Errorf("CheckPasswordHash failed for correct password")
	}

	if CheckPasswordHash("WrongPassword", hash) {
		t.Errorf("CheckPasswordHash succeeded for wrong password")
	}

	// Long password (> 72 bytes)
	longPass := strings.Repeat("A", 120)
	longHash, err := HashPassword(longPass)
	if err != nil {
		t.Fatalf("HashPassword failed for long password: %v", err)
	}

	if !CheckPasswordHash(longPass, longHash) {
		t.Errorf("CheckPasswordHash failed for long password")
	}

	if CheckPasswordHash(longPass+"B", longHash) {
		t.Errorf("CheckPasswordHash succeeded for modified long password")
	}
}

func TestStripSensitiveProtocolFields(t *testing.T) {
	protocols := map[string]any{
		"xray": map[string]any{
			"port":                443,
			"reality_public_key":  "pubkey123",
			"reality_private_key": "sensitive-priv-key",
		},
		"awg": map[string]any{
			"port": 51820,
		},
		"non_map_field": "some_value",
	}

	cleaned := StripSensitiveProtocolFields(protocols)
	xrayMap, ok := cleaned["xray"].(map[string]any)
	if !ok {
		t.Fatalf("expected xray to be map[string]any")
	}

	if _, exists := xrayMap["reality_private_key"]; exists {
		t.Errorf("expected reality_private_key to be stripped")
	}
	if xrayMap["reality_public_key"] != "pubkey123" {
		t.Errorf("expected reality_public_key to be preserved")
	}
	if cleaned["non_map_field"] != "some_value" {
		t.Errorf("expected non_map_field to be preserved")
	}
}
