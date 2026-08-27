package security

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"regexp"
	"time"

	"golang.org/x/crypto/bcrypt"
	"golang.org/x/crypto/hkdf"
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

// DeriveFernetKeys derives 16-byte HMAC signing key and 16-byte AES encryption key from secretKey via HKDF-SHA256.
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

// CheckPasswordHash compares a plaintext password against a bcrypt hash.
func CheckPasswordHash(password, hash string) bool {
	bytes := []byte(password)
	if err := bcrypt.CompareHashAndPassword([]byte(hash), bytes); err == nil {
		return true
	}

	if len(bytes) > 72 {
		h := sha256.Sum256(bytes)
		return bcrypt.CompareHashAndPassword([]byte(hash), h[:]) == nil
	}

	return false
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
