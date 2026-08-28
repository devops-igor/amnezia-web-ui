package health

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"math/big"
	"time"

	"golang.org/x/crypto/blake2s"
	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/curve25519"
)

var (
	InitialChainKey = blake2s.Sum256([]byte("Noise_IKpsk2_25519_ChaChaPoly_BLAKE2s"))
	InitialHash     = blake2s.Sum256(append(InitialChainKey[:], []byte("WireGuard v1 zx2c4 Jason@zx2c4.com")...))
	LabelMAC1       = []byte("mac1----")
)

const (
	DefaultH1 = uint32(1020325451)
	DefaultH2 = uint32(3288052141)
	DefaultS1 = 15
	DefaultS2 = 18
)

// NoiseClientState maintains state across Noise protocol handshake messages.
type NoiseClientState struct {
	H           []byte
	CK          []byte
	ClientEPriv []byte
	ClientPriv  []byte
	ServerPub   []byte
	PSK         []byte
	SenderIndex uint32
	MAC1Key     []byte
}

// HMACBlake2s computes keyed BLAKE2s-256 MAC for Noise KDF functions.
func HMACBlake2s(key, data []byte) []byte {
	h, err := blake2s.New256(key)
	if err != nil {
		return nil
	}
	h.Write(data)
	return h.Sum(nil)
}

// KDF1 derives a new chaining key from key and input data.
func KDF1(key, data []byte) []byte {
	prk := HMACBlake2s(key, data)
	return HMACBlake2s(prk, []byte{0x01})
}

// KDF2 derives a new chaining key and an encryption key.
func KDF2(key, data []byte) (t1, t2 []byte) {
	prk := HMACBlake2s(key, data)
	t1 = HMACBlake2s(prk, []byte{0x01})
	t2 = HMACBlake2s(prk, append(t1, 0x02))
	return t1, t2
}

// KDF3 derives a new chaining key, a tau hash, and an encryption key.
func KDF3(key, data []byte) (t1, t2, t3 []byte) {
	prk := HMACBlake2s(key, data)
	t1 = HMACBlake2s(prk, []byte{0x01})
	t2 = HMACBlake2s(prk, append(t1, 0x02))
	t3 = HMACBlake2s(prk, append(t2, 0x03))
	return t1, t2, t3
}

// DecodeKey decodes base64 string or returns 32 raw key bytes.
func DecodeKey(keyVal any) ([]byte, error) {
	switch v := keyVal.(type) {
	case []byte:
		if len(v) == 32 {
			return v, nil
		}
		return nil, fmt.Errorf("byte key must be 32 bytes, got %d", len(v))
	case string:
		decoded, err := base64.StdEncoding.DecodeString(v)
		if err != nil {
			return nil, fmt.Errorf("failed to decode base64 key: %w", err)
		}
		if len(decoded) != 32 {
			return nil, fmt.Errorf("decoded base64 key must be 32 bytes, got %d", len(decoded))
		}
		return decoded, nil
	default:
		return nil, errors.New("unsupported key type")
	}
}

// BuildAWGInitiationPacket creates an AmneziaWG Handshake Initiation packet and state.
func BuildAWGInitiationPacket(serverPubKey, clientPrivKey, psk []byte, h1 uint32, s1 int) ([]byte, *NoiseClientState, error) {
	if len(serverPubKey) != 32 {
		return nil, nil, errors.New("server public key must be 32 bytes")
	}

	if clientPrivKey == nil {
		clientPrivKey = make([]byte, 32)
		if _, err := rand.Read(clientPrivKey); err != nil {
			return nil, nil, fmt.Errorf("failed to generate random client private key: %w", err)
		}
	} else if len(clientPrivKey) != 32 {
		return nil, nil, errors.New("client private key must be 32 bytes")
	}

	clientPubKey, err := curve25519.X25519(clientPrivKey, curve25519.Basepoint)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to compute client public key: %w", err)
	}

	if psk == nil {
		psk = make([]byte, 32)
	} else if len(psk) != 32 {
		return nil, nil, errors.New("psk must be 32 bytes")
	}

	if h1 == 0 {
		h1 = DefaultH1
	}
	if s1 < 0 {
		s1 = DefaultS1
	}

	// 1. Initialize Noise hash & chain key
	hSum := blake2s.Sum256(append(InitialHash[:], serverPubKey...))
	h := hSum[:]
	ck := InitialChainKey[:]

	// 2. Generate client ephemeral keypair
	clientEPriv := make([]byte, 32)
	if _, err := rand.Read(clientEPriv); err != nil {
		return nil, nil, fmt.Errorf("failed to generate ephemeral private key: %w", err)
	}
	clientEPub, err := curve25519.X25519(clientEPriv, curve25519.Basepoint)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to compute ephemeral public key: %w", err)
	}

	// 3. Mix ephemeral public key into hash & chain key
	hSum = blake2s.Sum256(append(h, clientEPub...))
	h = hSum[:]
	ck = KDF1(ck, clientEPub)

	// 4. First DH exchange: ss1 = DH(client_e_priv, serverPubKey)
	ss1, err := curve25519.X25519(clientEPriv, serverPubKey)
	if err != nil {
		return nil, nil, fmt.Errorf("first DH exchange failed: %w", err)
	}
	var key1 []byte
	ck, key1 = KDF2(ck, ss1)

	// 5. Encrypt client static public key with key1
	aead1, err := chacha20poly1305.New(key1)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create aead1: %w", err)
	}
	nonce0 := make([]byte, 12)
	encryptedStatic := aead1.Seal(nil, nonce0, clientPubKey, h)
	hSum = blake2s.Sum256(append(h, encryptedStatic...))
	h = hSum[:]

	// 6. Second DH exchange: ss2 = DH(clientPrivKey, serverPubKey)
	ss2, err := curve25519.X25519(clientPrivKey, serverPubKey)
	if err != nil {
		return nil, nil, fmt.Errorf("second DH exchange failed: %w", err)
	}
	var key2 []byte
	ck, key2 = KDF2(ck, ss2)

	// 7. Generate and encrypt TAI64N timestamp
	now := time.Now()
	unixSec := now.Unix()
	unixNsec := now.Nanosecond()
	// #nosec G115
	taiSec := uint64(0x400000000000000A + unixSec)
	// #nosec G115
	taiNsec := uint32(unixNsec & ^0xFFFFFF)

	tai64n := make([]byte, 12)
	binary.BigEndian.PutUint64(tai64n[0:8], taiSec)
	binary.BigEndian.PutUint32(tai64n[8:12], taiNsec)

	aead2, err := chacha20poly1305.New(key2)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create aead2: %w", err)
	}
	encryptedTimestamp := aead2.Seal(nil, nonce0, tai64n, h)
	hSum = blake2s.Sum256(append(h, encryptedTimestamp...))
	h = hSum[:]

	// 8. Assemble 116-byte message body
	idxBig, err := rand.Int(rand.Reader, big.NewInt(0xFFFFFFFF))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to generate sender index: %w", err)
	}
	// #nosec G115
	senderIdx := uint32(idxBig.Int64())

	msgTypeBytes := make([]byte, 4)
	binary.LittleEndian.PutUint32(msgTypeBytes, h1)

	senderIdxBytes := make([]byte, 4)
	binary.LittleEndian.PutUint32(senderIdxBytes, senderIdx)

	var msgBody []byte
	msgBody = append(msgBody, msgTypeBytes...)
	msgBody = append(msgBody, senderIdxBytes...)
	msgBody = append(msgBody, clientEPub...)
	msgBody = append(msgBody, encryptedStatic...)
	msgBody = append(msgBody, encryptedTimestamp...)

	// 9. Compute MAC1 (16 bytes) and MAC2 (16 zero bytes)
	mac1KeySum := blake2s.Sum256(append(LabelMAC1, serverPubKey...))
	mac1Key := mac1KeySum[:]
	hMac1, err := blake2s.New128(mac1Key)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create mac1 hasher: %w", err)
	}
	hMac1.Write(msgBody)
	mac1 := hMac1.Sum(nil)
	mac2 := make([]byte, 16)

	// 10. Frame wire packet with S1 random junk bytes at the front
	var packet []byte
	if s1 > 0 {
		padding := make([]byte, s1)
		if _, err := rand.Read(padding); err != nil {
			return nil, nil, fmt.Errorf("failed to generate S1 padding: %w", err)
		}
		packet = append(packet, padding...)
	}
	packet = append(packet, msgBody...)
	packet = append(packet, mac1...)
	packet = append(packet, mac2...)

	state := &NoiseClientState{
		H:           h,
		CK:          ck,
		ClientEPriv: clientEPriv,
		ClientPriv:  clientPrivKey,
		ServerPub:   serverPubKey,
		PSK:         psk,
		SenderIndex: senderIdx,
		MAC1Key:     mac1Key,
	}

	return packet, state, nil
}

// VerifyAWGResponsePacket verifies and authenticates an AmneziaWG Handshake Response packet.
func VerifyAWGResponsePacket(respPacket []byte, state *NoiseClientState, h2 uint32, s2 int) bool {
	if state == nil {
		return false
	}
	if h2 == 0 {
		h2 = DefaultH2
	}
	if s2 < 0 {
		s2 = DefaultS2
	}

	expectedMinLen := s2 + 92
	if len(respPacket) < expectedMinLen {
		return false
	}

	payload := respPacket[s2:]
	msgType := binary.LittleEndian.Uint32(payload[0:4])
	if msgType != h2 && msgType != 2 {
		return false
	}

	receiverIdx := binary.LittleEndian.Uint32(payload[8:12])
	if receiverIdx != state.SenderIndex {
		return false
	}

	serverEPub := payload[12:44]
	encryptedEmpty := payload[44:60]

	// Complete Noise handshake verification
	hSum := blake2s.Sum256(append(state.H, serverEPub...))
	h := hSum[:]
	ck := KDF1(state.CK, serverEPub)

	ss3, err := curve25519.X25519(state.ClientEPriv, serverEPub)
	if err != nil {
		return false
	}
	ck = KDF1(ck, ss3)

	ss4, err := curve25519.X25519(state.ClientPriv, serverEPub)
	if err != nil {
		return false
	}
	ck = KDF1(ck, ss4)

	var tau, key3 []byte
	_, tau, key3 = KDF3(ck, state.PSK)
	hSum = blake2s.Sum256(append(h, tau...))
	h = hSum[:]

	aead3, err := chacha20poly1305.New(key3)
	if err != nil {
		return false
	}
	nonce0 := make([]byte, 12)
	decrypted, err := aead3.Open(nil, nonce0, encryptedEmpty, h)
	if err != nil {
		return false
	}

	return len(decrypted) == 0
}
