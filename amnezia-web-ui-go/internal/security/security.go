package security

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
	"golang.org/x/crypto/hkdf"
	"golang.org/x/crypto/pbkdf2"
)

var (
	fernetSalt = []byte("amnezia-panel-credential-encryption")
	fernetInfo = []byte("fernet-credential-key")
	fernetRe   = regexp.MustCompile(`^g[A-Za-z0-9+/=_-]{20,}$`)

	// SensitiveProtocolFields defines fields that must never be stored in plain JSON or exposed in responses.
	SensitiveProtocolFields = []string{"reality_private_key"}
)

// ErrInvalidToken is returned when Fernet decryption or HMAC verification fails.
var ErrInvalidToken = errors.New("failed to decrypt credential: invalid token or corrupted data")

// ErrInvalidSession is returned when session cookie verification or decoding fails.
var ErrInvalidSession = errors.New("invalid or tampered session cookie")

// ErrIntegrityViolation is returned when file or content integrity verification fails.
var ErrIntegrityViolation = errors.New("file integrity verification failed: hash mismatch")

// DeriveFernetKeys derives a 16-byte HMAC signing key and 16-byte AES encryption key from secretKey via HKDF-SHA256.
func DeriveFernetKeys(secretKey string) (signingKey []byte, encryptionKey []byte, err error) {
	if secretKey == "" {
		return nil, nil, errors.New("secret key cannot be empty")
	}

	hkdfReader := hkdf.New(sha256.New, []byte(secretKey), fernetSalt, fernetInfo)
	rawKey := make([]byte, 32)
	if _, err := io.ReadFull(hkdfReader, rawKey); err != nil {
		return nil, nil, fmt.Errorf("failed to derive keys with HKDF: %w", err)
	}

	return rawKey[:16], rawKey[16:], nil
}

// EncryptCredential encrypts plaintext using Fernet-compatible AES-128-CBC + HMAC-SHA256 format.
func EncryptCredential(plaintext string, secretKey string) (string, error) {
	if plaintext == "" {
		return "", nil
	}

	signingKey, encryptionKey, err := DeriveFernetKeys(secretKey)
	if err != nil {
		return "", err
	}

	// 1 byte version (0x80)
	version := byte(0x80)

	// 8 bytes timestamp (big-endian uint64 seconds)
	timestampBytes := make([]byte, 8)
	nowUnix := time.Now().Unix()
	if nowUnix > 0 {
		binary.BigEndian.PutUint64(timestampBytes, uint64(nowUnix))
	}

	// 16 bytes IV
	iv := make([]byte, aes.BlockSize)
	if _, err := io.ReadFull(rand.Reader, iv); err != nil {
		return "", fmt.Errorf("failed to generate random IV: %w", err)
	}

	// PKCS#7 pad plaintext
	paddedData := pkcs7Pad([]byte(plaintext), aes.BlockSize)

	// AES-128-CBC encrypt
	block, err := aes.NewCipher(encryptionKey)
	if err != nil {
		return "", fmt.Errorf("failed to create AES cipher: %w", err)
	}

	ciphertext := make([]byte, len(paddedData))
	mode := cipher.NewCBCEncrypter(block, iv)
	mode.CryptBlocks(ciphertext, paddedData)

	// Construct message for HMAC: Version || Timestamp || IV || Ciphertext
	msg := make([]byte, 0, 1+8+aes.BlockSize+len(ciphertext))
	msg = append(msg, version)
	msg = append(msg, timestampBytes...)
	msg = append(msg, iv...)
	msg = append(msg, ciphertext...)

	// Compute HMAC-SHA256 over message
	mac := hmac.New(sha256.New, signingKey)
	mac.Write(msg)
	hmacSignature := mac.Sum(nil)

	// Full token: msg || HMAC
	fullToken := append(msg, hmacSignature...)

	return base64.URLEncoding.EncodeToString(fullToken), nil
}

// DecryptCredential decrypts a Fernet ciphertext back to plaintext.
func DecryptCredential(token string, secretKey string) (string, error) {
	if token == "" {
		return "", nil
	}

	signingKey, encryptionKey, err := DeriveFernetKeys(secretKey)
	if err != nil {
		return "", err
	}

	data, err := base64.URLEncoding.DecodeString(token)
	if err != nil {
		// Fallback without padding
		data, err = base64.RawURLEncoding.DecodeString(token)
		if err != nil {
			return "", ErrInvalidToken
		}
	}

	// Min length: 1 (version) + 8 (timestamp) + 16 (IV) + 16 (min 1 ciphertext block) + 32 (HMAC) = 73 bytes
	if len(data) < 73 {
		return "", ErrInvalidToken
	}

	if data[0] != 0x80 {
		return "", ErrInvalidToken
	}

	msgLen := len(data) - 32
	msg := data[:msgLen]
	expectedHMAC := data[msgLen:]

	// Verify HMAC in constant time
	mac := hmac.New(sha256.New, signingKey)
	mac.Write(msg)
	computedHMAC := mac.Sum(nil)

	if !hmac.Equal(computedHMAC, expectedHMAC) {
		return "", ErrInvalidToken
	}

	iv := msg[9:25]
	ciphertext := msg[25:]

	if len(ciphertext)%aes.BlockSize != 0 {
		return "", ErrInvalidToken
	}

	block, err := aes.NewCipher(encryptionKey)
	if err != nil {
		return "", fmt.Errorf("failed to create AES cipher: %w", err)
	}

	plaintext := make([]byte, len(ciphertext))
	mode := cipher.NewCBCDecrypter(block, iv)
	mode.CryptBlocks(plaintext, ciphertext)

	unpadded, err := pkcs7Unpad(plaintext, aes.BlockSize)
	if err != nil {
		return "", ErrInvalidToken
	}

	return string(unpadded), nil
}

// DecryptCredentialSafe decrypts a Fernet credential, returning empty string on failure.
func DecryptCredentialSafe(token string, secretKey string) string {
	val, err := DecryptCredential(token, secretKey)
	if err != nil {
		slog.Warn("Failed to decrypt credential safely", "err", err)
		return ""
	}
	return val
}

// LooksLikeFernetToken checks if the value matches the Fernet ciphertext pattern.
func LooksLikeFernetToken(value string) bool {
	if value == "" {
		return false
	}
	return fernetRe.MatchString(value)
}

// HashPassword hashes a plaintext password using bcrypt with cost 12.
// Implements SHA-256 pre-hash safeguard for passwords > 72 bytes to avoid truncation.
func HashPassword(password string) (string, error) {
	bytes := []byte(password)
	if len(bytes) > 72 {
		hash := sha256.Sum256(bytes)
		bytes = hash[:]
	}

	hashed, err := bcrypt.GenerateFromPassword(bytes, 12)
	if err != nil {
		return "", fmt.Errorf("failed to hash password: %w", err)
	}
	return string(hashed), nil
}

// CheckPasswordHash compares a plaintext password against a password hash (bcrypt or legacy PBKDF2).
func CheckPasswordHash(password, hash string) bool {
	if hash == "" {
		return false
	}

	// Legacy PBKDF2 hashes: "salt$hex" (contains '$' and does not start with "$2")
	// 100,000 iterations of PBKDF2-HMAC-SHA256 matching Python app/utils/helpers.py
	if strings.Contains(hash, "$") && !strings.HasPrefix(hash, "$2") {
		parts := strings.SplitN(hash, "$", 2)
		if len(parts) == 2 {
			salt := parts[0]
			expectedHex := strings.ToLower(strings.TrimSpace(parts[1]))
			derived := pbkdf2.Key([]byte(password), []byte(salt), 100000, 32, sha256.New)
			derivedHex := hex.EncodeToString(derived)
			if len(derivedHex) == len(expectedHex) && subtle.ConstantTimeCompare([]byte(derivedHex), []byte(expectedHex)) == 1 {
				return true
			}
		}
		return false
	}

	// Bcrypt hashes:
	// 1. Direct compare first (supports <=72 byte passwords and legacy >72 byte passwords truncated at 72 bytes)
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)); err == nil {
		return true
	}

	// 2. SHA-256 pre-hashed compare (supports Go-hashed >72 byte passwords)
	preHash := sha256.Sum256([]byte(password))
	return bcrypt.CompareHashAndPassword([]byte(hash), preHash[:]) == nil
}

// StripSensitiveProtocolFields cleans sensitive credentials from protocol definitions.
func StripSensitiveProtocolFields(protocols map[string]any) map[string]any {
	if protocols == nil {
		return nil
	}

	cleaned := make(map[string]any, len(protocols))
	for k, v := range protocols {
		if innerMap, ok := v.(map[string]any); ok {
			cleanInner := make(map[string]any, len(innerMap))
			for innerK, innerV := range innerMap {
				isSensitive := false
				for _, sensitive := range SensitiveProtocolFields {
					if innerK == sensitive {
						isSensitive = true
						break
					}
				}
				if !isSensitive {
					cleanInner[innerK] = innerV
				}
			}
			cleaned[k] = cleanInner
		} else {
			cleaned[k] = v
		}
	}
	return cleaned
}

// EncodeSession encodes a session map to a signed cookie value: base64(json) . base64(hmac-sha256).
func EncodeSession(data map[string]any, secretKey string) (string, error) {
	if secretKey == "" {
		return "", errors.New("secret key cannot be empty")
	}

	jsonBytes, err := json.Marshal(data)
	if err != nil {
		return "", fmt.Errorf("failed to marshal session data: %w", err)
	}

	payloadB64 := base64.RawURLEncoding.EncodeToString(jsonBytes)

	mac := hmac.New(sha256.New, []byte(secretKey))
	mac.Write([]byte(payloadB64))
	sig := mac.Sum(nil)
	sigB64 := base64.RawURLEncoding.EncodeToString(sig)

	return payloadB64 + "." + sigB64, nil
}

// DecodeSession decodes and verifies a signed cookie value using constant-time HMAC comparison.
func DecodeSession(cookieValue string, secretKey string) (map[string]any, error) {
	if cookieValue == "" || secretKey == "" {
		return nil, ErrInvalidSession
	}

	parts := strings.Split(cookieValue, ".")
	if len(parts) != 2 {
		return nil, ErrInvalidSession
	}

	payloadB64 := parts[0]
	sigB64 := parts[1]

	sig, err := base64.RawURLEncoding.DecodeString(sigB64)
	if err != nil {
		return nil, ErrInvalidSession
	}

	mac := hmac.New(sha256.New, []byte(secretKey))
	mac.Write([]byte(payloadB64))
	expectedSig := mac.Sum(nil)

	if !hmac.Equal(sig, expectedSig) {
		return nil, ErrInvalidSession
	}

	payloadBytes, err := base64.RawURLEncoding.DecodeString(payloadB64)
	if err != nil {
		return nil, ErrInvalidSession
	}

	var data map[string]any
	if err := json.Unmarshal(payloadBytes, &data); err != nil {
		return nil, ErrInvalidSession
	}

	return data, nil
}

// ComputeSHA256 computes the SHA-256 hex digest of a file, reading in 8KB chunks.
func ComputeSHA256(filePath string) (string, error) {
	cleanPath := filepath.Clean(filePath)
	// #nosec G304 -- Trusted file path integrity checking
	f, err := os.Open(cleanPath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	hasher := sha256.New()
	buf := make([]byte, 8192)
	for {
		n, err := f.Read(buf)
		if n > 0 {
			hasher.Write(buf[:n])
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return "", err
		}
	}

	return hex.EncodeToString(hasher.Sum(nil)), nil
}

// VerifyIntegrity verifies that a file's SHA-256 matches the expected 64-char hex hash using constant-time comparison.
func VerifyIntegrity(filePath string, expectedHash string) (bool, error) {
	actualHash, err := ComputeSHA256(filePath)
	if err != nil {
		return false, err
	}

	actualLower := strings.ToLower(actualHash)
	expectedLower := strings.ToLower(strings.TrimSpace(expectedHash))

	if len(actualLower) != len(expectedLower) {
		return false, nil
	}

	return subtle.ConstantTimeCompare([]byte(actualLower), []byte(expectedLower)) == 1, nil
}

// VerifyContentIntegrity verifies that in-memory content matches the expected SHA-256 hash using constant-time comparison.
func VerifyContentIntegrity(content []byte, expectedHash string) bool {
	sum := sha256.Sum256(content)
	actualHash := hex.EncodeToString(sum[:])

	actualLower := strings.ToLower(actualHash)
	expectedLower := strings.ToLower(strings.TrimSpace(expectedHash))

	if len(actualLower) != len(expectedLower) {
		return false
	}

	return subtle.ConstantTimeCompare([]byte(actualLower), []byte(expectedLower)) == 1
}

// LoadExpectedHash reads and validates a 64-character SHA-256 hex digest from a .sha256 file.
func LoadExpectedHash(hashFilePath string) (string, error) {
	cleanPath := filepath.Clean(hashFilePath)
	// #nosec G304 -- Reading expected hash file
	data, err := os.ReadFile(cleanPath)
	if err != nil {
		return "", err
	}

	hashVal := strings.TrimSpace(string(data))
	if len(hashVal) != 64 {
		return "", fmt.Errorf("%w: invalid hash length %d in %s", ErrIntegrityViolation, len(hashVal), hashFilePath)
	}

	for _, c := range hashVal {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return "", fmt.Errorf("%w: invalid non-hex character '%c' in %s", ErrIntegrityViolation, c, hashFilePath)
		}
	}

	return strings.ToLower(hashVal), nil
}

func pkcs7Pad(data []byte, blockSize int) []byte {
	padding := blockSize - (len(data) % blockSize)
	padByte := byte(padding & 0xFF)
	padtext := bytes.Repeat([]byte{padByte}, padding)
	return append(data, padtext...)
}

func pkcs7Unpad(data []byte, blockSize int) ([]byte, error) {
	length := len(data)
	if length == 0 || length%blockSize != 0 {
		return nil, errors.New("invalid padding: invalid data length")
	}

	padding := int(data[length-1])
	if padding == 0 || padding > blockSize || padding > length {
		return nil, errors.New("invalid padding: invalid padding size")
	}

	expectedByte := byte(padding & 0xFF)
	for i := length - padding; i < length; i++ {
		if data[i] != expectedByte {
			return nil, errors.New("invalid padding: byte mismatch")
		}
	}

	return data[:length-padding], nil
}
