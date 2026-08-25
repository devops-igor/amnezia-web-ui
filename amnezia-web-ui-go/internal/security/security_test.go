package security

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDeriveFernetKeys(t *testing.T) {
	secretKey := "test-secret-key-12345"
	sigKey, encKey, err := DeriveFernetKeys(secretKey)
	if err != nil {
		t.Fatalf("DeriveFernetKeys failed: %v", err)
	}

	if len(sigKey) != 16 {
		t.Errorf("expected 16 bytes signing key, got %d", len(sigKey))
	}
	if len(encKey) != 16 {
		t.Errorf("expected 16 bytes encryption key, got %d", len(encKey))
	}

	// Empty key should fail
	if _, _, err := DeriveFernetKeys(""); err == nil {
		t.Errorf("expected error with empty secret key")
	}
}

func TestFernetEncryptDecryptRoundTrip(t *testing.T) {
	secretKey := "my-very-secure-secret-key-67890"
	testCases := []string{
		"Hello, World!",
		"Super Secret Password",
		"---BEGIN RSA PRIVATE KEY---\nMIIEowIBAAKCAQEA0...\n---END RSA PRIVATE KEY---",
		"1",
		strings.Repeat("A", 1000),
		"unicode-test: 🛡️ ❤️ 🚀 Привет мир! 123",
	}

	for _, tc := range testCases {
		encrypted, err := EncryptCredential(tc, secretKey)
		if err != nil {
			t.Fatalf("encryption failed for %q: %v", tc, err)
		}

		if !LooksLikeFernetToken(encrypted) {
			t.Errorf("expected token to match Fernet pattern: %s", encrypted)
		}

		decrypted, err := DecryptCredential(encrypted, secretKey)
		if err != nil {
			t.Fatalf("decryption failed for %q: %v", tc, err)
		}

		if decrypted != tc {
			t.Errorf("expected decrypted %q, got %q", tc, decrypted)
		}
	}
}

func TestFernetPythonTestVector(t *testing.T) {
	// Vector generated via Python cryptography.fernet.Fernet with identical HKDF parameters
	secretKey := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	pythonToken := "gAAAAABqjhh3x8QuseXT_8PD13JkCyAviTSi8spHchi5SVWd6hNDi6B48TedazDfd_MdDaApz0td6_dfTUwlW1euLX4kHKTBRMT-SERDg5OAh2pzWSBgsuGaC8YKbkCMdytrhd0jdsm2"
	expectedPlaintext := "Hello World! Sensitive Password 123456"

	decrypted, err := DecryptCredential(pythonToken, secretKey)
	if err != nil {
		t.Fatalf("failed to decrypt Python Fernet test vector: %v", err)
	}

	if decrypted != expectedPlaintext {
		t.Errorf("expected %q, got %q", expectedPlaintext, decrypted)
	}
}

func TestFernetTamperedTokenAndWrongKey(t *testing.T) {
	secretKey1 := "key-number-one-12345"
	secretKey2 := "key-number-two-67890"

	encrypted, err := EncryptCredential("sensitive information", secretKey1)
	if err != nil {
		t.Fatalf("failed to encrypt: %v", err)
	}

	// Decrypt with wrong key must fail
	if _, err := DecryptCredential(encrypted, secretKey2); err == nil {
		t.Errorf("expected decryption error with wrong key")
	}

	// Decrypt with safe fallback returns empty string
	safeResult := DecryptCredentialSafe(encrypted, secretKey2)
	if safeResult != "" {
		t.Errorf("expected empty string on safe decrypt failure, got %q", safeResult)
	}

	// Tampered token
	tampered := encrypted[:len(encrypted)-5] + "AAAAA"
	if _, err := DecryptCredential(tampered, secretKey1); err == nil {
		t.Errorf("expected decryption error with tampered HMAC")
	}
}

func TestFernetEmptyAndInvalidInputs(t *testing.T) {
	secretKey := "valid-secret-key"

	// Empty string input
	res, err := EncryptCredential("", secretKey)
	if err != nil || res != "" {
		t.Errorf("expected empty string result for empty plaintext")
	}

	res, err = DecryptCredential("", secretKey)
	if err != nil || res != "" {
		t.Errorf("expected empty string result for empty token")
	}

	// Invalid short token
	if _, err := DecryptCredential("gAAAAABshort", secretKey); err == nil {
		t.Errorf("expected error on short token")
	}

	if LooksLikeFernetToken("") {
		t.Errorf("empty token should not match Fernet pattern")
	}
}

func TestBcryptPasswordHashing(t *testing.T) {
	password := "CorrectHorseBatteryStaple123!"
	hashed, err := HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword failed: %v", err)
	}

	if !CheckPasswordHash(password, hashed) {
		t.Errorf("CheckPasswordHash failed to verify correct password")
	}

	if CheckPasswordHash("WrongPassword!", hashed) {
		t.Errorf("CheckPasswordHash verified incorrect password")
	}

	// Test 72+ bytes safeguard
	longPassword := strings.Repeat("LongSecurePasswordWithManyChars1234567890!", 4)
	longHashed, err := HashPassword(longPassword)
	if err != nil {
		t.Fatalf("HashPassword failed for >72 byte password: %v", err)
	}

	if !CheckPasswordHash(longPassword, longHashed) {
		t.Errorf("CheckPasswordHash failed for long password >72 bytes")
	}

	if CheckPasswordHash(longPassword+"extra", longHashed) {
		t.Errorf("CheckPasswordHash verified modified long password")
	}
}

func TestBcryptPythonCrossVerification(t *testing.T) {
	// Python-generated hashes
	pyHash := "$2b$12$u1MSQrJFIcgW/9euUst0POT./x1w8hmB.dX6t.ZP9ct/XvoTSPu6O"
	pyPassword := "AdminPassword123!"

	if !CheckPasswordHash(pyPassword, pyHash) {
		t.Errorf("failed to verify Python bcrypt hash")
	}

	pyLongHash := "$2b$12$ylY2whlbnH4s6rxD.gFlm./j3NgIzqJdBxwvo7Y40Fg.eK9KWVIYu"
	pyLongPassword := strings.Repeat("VeryLongPasswordExceeding72Bytes_", 4)

	if !CheckPasswordHash(pyLongPassword, pyLongHash) {
		t.Errorf("failed to verify Python long bcrypt hash with SHA-256 safeguard")
	}
}

func TestSessionCookies(t *testing.T) {
	secretKey := "session-signing-secret-key-12345"

	sessionData := map[string]any{
		"user_id":  "550e8400-e29b-41d4-a716-446655440000",
		"username": "admin",
		"role":     "admin",
	}

	cookie, err := EncodeSession(sessionData, secretKey)
	if err != nil {
		t.Fatalf("EncodeSession failed: %v", err)
	}

	decoded, err := DecodeSession(cookie, secretKey)
	if err != nil {
		t.Fatalf("DecodeSession failed: %v", err)
	}

	if decoded["user_id"] != sessionData["user_id"] || decoded["username"] != sessionData["username"] {
		t.Errorf("decoded session mismatch: got %v", decoded)
	}

	// Tampered cookie
	tamperedCookie := cookie + "tampered"
	if _, err := DecodeSession(tamperedCookie, secretKey); err == nil {
		t.Errorf("expected error decoding tampered session cookie")
	}

	// Wrong key
	if _, err := DecodeSession(cookie, "wrong-key"); err == nil {
		t.Errorf("expected error decoding session cookie with wrong secret key")
	}
}

func TestSHA256Integrity(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.txt")
	testContent := []byte("Testing SHA256 integrity verification in pure Go")

	if err := os.WriteFile(testFile, testContent, 0600); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	hash, err := ComputeSHA256(testFile)
	if err != nil {
		t.Fatalf("ComputeSHA256 failed: %v", err)
	}

	if len(hash) != 64 {
		t.Errorf("expected 64 character hex hash, got %d", len(hash))
	}

	// Verify content integrity
	if !VerifyContentIntegrity(testContent, hash) {
		t.Errorf("VerifyContentIntegrity failed with matching hash")
	}

	if VerifyContentIntegrity([]byte("modified content"), hash) {
		t.Errorf("VerifyContentIntegrity succeeded with modified content")
	}

	// Verify file integrity
	ok, err := VerifyIntegrity(testFile, hash)
	if err != nil || !ok {
		t.Errorf("VerifyIntegrity failed: ok=%v, err=%v", ok, err)
	}

	// Load expected hash
	hashFile := filepath.Join(tmpDir, "test.txt.sha256")
	if err := os.WriteFile(hashFile, []byte(hash+"\n"), 0600); err != nil {
		t.Fatalf("failed to write hash file: %v", err)
	}

	loadedHash, err := LoadExpectedHash(hashFile)
	if err != nil {
		t.Fatalf("LoadExpectedHash failed: %v", err)
	}

	if loadedHash != hash {
		t.Errorf("expected %q, got %q", hash, loadedHash)
	}
}

func TestStripSensitiveProtocolFields(t *testing.T) {
	input := map[string]any{
		"awg": map[string]any{
			"port": 51820,
		},
		"xray": map[string]any{
			"port":                443,
			"reality_private_key": "SUPER_SECRET_KEY",
			"reality_public_key":  "PUBLIC_KEY",
		},
	}

	cleaned := StripSensitiveProtocolFields(input)
	xrayMap, ok := cleaned["xray"].(map[string]any)
	if !ok {
		t.Fatalf("expected xray to be map[string]any")
	}

	if _, exists := xrayMap["reality_private_key"]; exists {
		t.Errorf("reality_private_key was not stripped")
	}

	if xrayMap["reality_public_key"] != "PUBLIC_KEY" {
		t.Errorf("reality_public_key was unexpectedly removed or modified")
	}

	if nilCleaned := StripSensitiveProtocolFields(nil); nilCleaned != nil {
		t.Errorf("expected nil for nil input")
	}
}

func TestPKCS7PaddingEdgeCases(t *testing.T) {
	blockSize := 16

	// Data equal to block size -> extra block added
	data16 := []byte("1234567890123456")
	padded16 := pkcs7Pad(data16, blockSize)
	if len(padded16) != 32 {
		t.Errorf("expected 32 bytes for 16-byte block, got %d", len(padded16))
	}
	unpadded16, err := pkcs7Unpad(padded16, blockSize)
	if err != nil || string(unpadded16) != string(data16) {
		t.Errorf("unpad 16-byte block failed: %v", err)
	}

	// Invalid padding sizes
	if _, err := pkcs7Unpad([]byte{}, blockSize); err == nil {
		t.Errorf("expected error for empty data")
	}
	if _, err := pkcs7Unpad([]byte("123"), blockSize); err == nil {
		t.Errorf("expected error for non-block-aligned data")
	}

	// Corrupted padding bytes
	badPadding := make([]byte, 16)
	badPadding[15] = 0 // padding cannot be 0
	if _, err := pkcs7Unpad(badPadding, blockSize); err == nil {
		t.Errorf("expected error for 0 padding byte")
	}

	badPadding2 := make([]byte, 16)
	badPadding2[14] = 99 // preceding byte is not 2
	badPadding2[15] = 2  // indicates 2 bytes of padding
	if _, err := pkcs7Unpad(badPadding2, blockSize); err == nil {
		t.Errorf("expected error for mismatched padding bytes")
	}
}

func TestLoadExpectedHashErrors(t *testing.T) {
	tmpDir := t.TempDir()

	// Non-existent file
	if _, err := LoadExpectedHash(filepath.Join(tmpDir, "missing.sha256")); err == nil {
		t.Errorf("expected error loading missing hash file")
	}

	// Invalid length (short)
	shortFile := filepath.Join(tmpDir, "short.sha256")
	_ = os.WriteFile(shortFile, []byte("abc123\n"), 0600)
	if _, err := LoadExpectedHash(shortFile); err == nil {
		t.Errorf("expected error loading short hash file")
	}

	// Invalid non-hex chars
	badCharFile := filepath.Join(tmpDir, "badchar.sha256")
	_ = os.WriteFile(badCharFile, []byte(strings.Repeat("0", 63)+"Z\n"), 0600)
	if _, err := LoadExpectedHash(badCharFile); err == nil {
		t.Errorf("expected error loading non-hex hash file")
	}
}

func TestSessionCookieEdgeCases(t *testing.T) {
	secretKey := "test-secret"

	// Empty cookie
	if _, err := DecodeSession("", secretKey); err == nil {
		t.Errorf("expected error decoding empty cookie")
	}

	// Empty secretKey
	if _, err := DecodeSession("a.b", ""); err == nil {
		t.Errorf("expected error decoding with empty secretKey")
	}
	if _, err := EncodeSession(map[string]any{"a": 1}, ""); err == nil {
		t.Errorf("expected error encoding with empty secretKey")
	}

	// Malformed cookie (not 2 parts)
	if _, err := DecodeSession("singlepart", secretKey); err == nil {
		t.Errorf("expected error decoding malformed single-part cookie")
	}

	// Invalid base64 in signature or payload
	if _, err := DecodeSession("invalid_payload.invalid_sig", secretKey); err == nil {
		t.Errorf("expected error decoding invalid base64 cookie")
	}
}

func TestComputeSHA256Error(t *testing.T) {
	if _, err := ComputeSHA256("/non/existent/path/file.bin"); err == nil {
		t.Errorf("expected error computing SHA256 for non-existent file")
	}
	if ok, _ := VerifyIntegrity("/non/existent/path/file.bin", strings.Repeat("0", 64)); ok {
		t.Errorf("expected VerifyIntegrity to return false for non-existent file")
	}
}
