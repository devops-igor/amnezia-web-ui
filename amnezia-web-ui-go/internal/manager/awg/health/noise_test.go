package health

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"net"
	"testing"
	"time"

	"golang.org/x/crypto/blake2s"
	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/curve25519"
)

func generateTestKeypair(t *testing.T) (priv, pub []byte) {
	priv = make([]byte, 32)
	if _, err := rand.Read(priv); err != nil {
		t.Fatalf("failed to generate random priv key: %v", err)
	}
	pub, err := curve25519.X25519(priv, curve25519.Basepoint)
	if err != nil {
		t.Fatalf("failed to generate pub key: %v", err)
	}
	return priv, pub
}

func TestKDFPrimitives(t *testing.T) {
	key := []byte("01234567890123456789012345678901")
	data := []byte("test-data-for-kdf")

	k1 := KDF1(key, data)
	if len(k1) != 32 {
		t.Errorf("KDF1 expected 32 bytes, got %d", len(k1))
	}

	t1, t2 := KDF2(key, data)
	if len(t1) != 32 || len(t2) != 32 {
		t.Errorf("KDF2 expected two 32-byte keys, got %d, %d", len(t1), len(t2))
	}

	t1_3, t2_3, t3_3 := KDF3(key, data)
	if len(t1_3) != 32 || len(t2_3) != 32 || len(t3_3) != 32 {
		t.Errorf("KDF3 expected three 32-byte keys, got %d, %d, %d", len(t1_3), len(t2_3), len(t3_3))
	}
}

func TestDecodeKey(t *testing.T) {
	raw := []byte("12345678901234567890123456789012")
	b64 := base64.StdEncoding.EncodeToString(raw)

	decRaw, err := DecodeKey(raw)
	if err != nil || string(decRaw) != string(raw) {
		t.Errorf("DecodeKey(raw) failed: %v", err)
	}

	decB64, err := DecodeKey(b64)
	if err != nil || string(decB64) != string(raw) {
		t.Errorf("DecodeKey(b64) failed: %v", err)
	}

	if _, err := DecodeKey("invalid-base64!!!"); err == nil {
		t.Errorf("expected error for invalid base64")
	}

	if _, err := DecodeKey([]byte("short")); err == nil {
		t.Errorf("expected error for short byte key")
	}
}

func TestAWGHandshakeRoundtrip(t *testing.T) {
	serverPriv, serverPub := generateTestKeypair(t)
	clientPriv, _ := generateTestKeypair(t)
	psk := make([]byte, 32)
	_, _ = rand.Read(psk)

	h1 := uint32(11111111)
	h2 := uint32(22222222)
	s1 := 20
	s2 := 30

	// 1. Client builds initiation packet
	initPacket, state, err := BuildAWGInitiationPacket(serverPub, clientPriv, psk, h1, s1)
	if err != nil {
		t.Fatalf("BuildAWGInitiationPacket failed: %v", err)
	}

	expectedLen := s1 + 148
	if len(initPacket) != expectedLen {
		t.Fatalf("expected packet length %d, got %d", expectedLen, len(initPacket))
	}

	// 2. Mock Server processes initiation packet and creates response
	// Skip S1 junk bytes
	msgBody := initPacket[s1 : s1+116]
	msgType := binary.LittleEndian.Uint32(msgBody[0:4])
	if msgType != h1 {
		t.Fatalf("server received invalid msgType: %d", msgType)
	}
	senderIdx := binary.LittleEndian.Uint32(msgBody[4:8])
	clientEPub := msgBody[8:40]
	encryptedStatic := msgBody[40:88]
	encryptedTimestamp := msgBody[88:116]

	// Server state initialization
	serverHSum := blake2s.Sum256(append(InitialHash[:], serverPub...))
	serverH := serverHSum[:]
	serverCK := InitialChainKey[:]

	serverHSum = blake2s.Sum256(append(serverH, clientEPub...))
	serverH = serverHSum[:]
	serverCK = KDF1(serverCK, clientEPub)

	serverSS1, _ := curve25519.X25519(serverPriv, clientEPub)
	var serverKey1 []byte
	serverCK, serverKey1 = KDF2(serverCK, serverSS1)

	serverAead1, err := chacha20poly1305.New(serverKey1)
	if err != nil {
		t.Fatalf("server aead1 failed: %v", err)
	}
	nonce0 := make([]byte, 12)
	clientStaticPub, err := serverAead1.Open(nil, nonce0, encryptedStatic, serverH)
	if err != nil {
		t.Fatalf("server failed to decrypt static pub key: %v", err)
	}
	serverHSum = blake2s.Sum256(append(serverH, encryptedStatic...))
	serverH = serverHSum[:]

	serverSS2, _ := curve25519.X25519(serverPriv, clientStaticPub)
	var serverKey2 []byte
	serverCK, serverKey2 = KDF2(serverCK, serverSS2)

	serverAead2, _ := chacha20poly1305.New(serverKey2)
	tai64n, err := serverAead2.Open(nil, nonce0, encryptedTimestamp, serverH)
	if err != nil || len(tai64n) != 12 {
		t.Fatalf("server failed to decrypt timestamp: %v", err)
	}
	serverHSum = blake2s.Sum256(append(serverH, encryptedTimestamp...))
	serverH = serverHSum[:]

	// Server constructs Response Packet
	serverEPriv, serverEPub := generateTestKeypair(t)
	serverHSum = blake2s.Sum256(append(serverH, serverEPub...))
	serverH = serverHSum[:]
	serverCK = KDF1(serverCK, serverEPub)

	serverSS3, _ := curve25519.X25519(serverEPriv, clientEPub)
	serverCK = KDF1(serverCK, serverSS3)

	serverSS4, _ := curve25519.X25519(serverEPriv, clientStaticPub)
	serverCK = KDF1(serverCK, serverSS4)

	var serverTau, serverKey3 []byte
	_, serverTau, serverKey3 = KDF3(serverCK, psk)
	serverHSum = blake2s.Sum256(append(serverH, serverTau...))
	serverH = serverHSum[:]

	serverAead3, _ := chacha20poly1305.New(serverKey3)
	encryptedEmpty := serverAead3.Seal(nil, nonce0, []byte{}, serverH)

	respMsgType := make([]byte, 4)
	binary.LittleEndian.PutUint32(respMsgType, h2)
	serverSenderIdx := make([]byte, 4)
	binary.LittleEndian.PutUint32(serverSenderIdx, 99999)
	respReceiverIdx := make([]byte, 4)
	binary.LittleEndian.PutUint32(respReceiverIdx, senderIdx)

	var respMsgBody []byte
	respMsgBody = append(respMsgBody, respMsgType...)
	respMsgBody = append(respMsgBody, serverSenderIdx...)
	respMsgBody = append(respMsgBody, respReceiverIdx...)
	respMsgBody = append(respMsgBody, serverEPub...)
	respMsgBody = append(respMsgBody, encryptedEmpty...)

	mac1KeySum := blake2s.Sum256(append(LabelMAC1, clientStaticPub...))
	hMac1, _ := blake2s.New128(mac1KeySum[:])
	hMac1.Write(respMsgBody)
	respMac1 := hMac1.Sum(nil)
	respMac2 := make([]byte, 16)

	var respPacket []byte
	if s2 > 0 {
		pad := make([]byte, s2)
		_, _ = rand.Read(pad)
		respPacket = append(respPacket, pad...)
	}
	respPacket = append(respPacket, respMsgBody...)
	respPacket = append(respPacket, respMac1...)
	respPacket = append(respPacket, respMac2...)

	// 3. Client verifies response packet
	valid := VerifyAWGResponsePacket(respPacket, state, h2, s2)
	if !valid {
		t.Fatalf("VerifyAWGResponsePacket failed to verify valid response packet")
	}

	// Corrupt response packet and verify failure
	corrupted := make([]byte, len(respPacket))
	copy(corrupted, respPacket)
	corrupted[s2+45] ^= 0xFF
	if VerifyAWGResponsePacket(corrupted, state, h2, s2) {
		t.Errorf("VerifyAWGResponsePacket should fail for corrupted packet")
	}

	// Test with nil state or too short packet
	if VerifyAWGResponsePacket(respPacket, nil, h2, s2) {
		t.Errorf("expected failure for nil state")
	}
	if VerifyAWGResponsePacket([]byte("short"), state, h2, s2) {
		t.Errorf("expected failure for short packet")
	}

	// Test BuildAWGInitiationPacket edge cases
	if _, _, err := BuildAWGInitiationPacket([]byte("short"), nil, nil, 0, 0); err == nil {
		t.Errorf("expected error for short server pub key")
	}
	if _, _, err := BuildAWGInitiationPacket(serverPub, []byte("short"), nil, 0, 0); err == nil {
		t.Errorf("expected error for short client priv key")
	}
	if _, _, err := BuildAWGInitiationPacket(serverPub, nil, []byte("short"), 0, 0); err == nil {
		t.Errorf("expected error for short psk")
	}

	// Test default parameters generation (nil clientPriv, nil psk, 0 h1, -1 s1)
	pktDef, stateDef, err := BuildAWGInitiationPacket(serverPub, nil, nil, 0, -1)
	if err != nil || pktDef == nil || stateDef == nil {
		t.Errorf("BuildAWGInitiationPacket with default params failed: %v", err)
	}
}

func TestMockUDPEndpointProbe(t *testing.T) {
	serverPriv, serverPub := generateTestKeypair(t)
	clientPriv, _ := generateTestKeypair(t)
	psk := make([]byte, 32)
	_, _ = rand.Read(psk)

	h1 := DefaultH1
	h2 := DefaultH2
	s1 := DefaultS1
	s2 := DefaultS2

	// Start a local UDP mock server
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen UDP: %v", err)
	}
	defer func() {
		_ = pc.Close()
	}()

	addr := pc.LocalAddr().String()

	// Server goroutine
	go func() {
		buf := make([]byte, 2048)
		n, clientAddr, err := pc.ReadFrom(buf)
		if err != nil {
			return
		}

		if n < s1+116 {
			return
		}

		msgBody := buf[s1 : s1+116]
		senderIdx := binary.LittleEndian.Uint32(msgBody[4:8])
		clientEPub := msgBody[8:40]
		encryptedStatic := msgBody[40:88]
		encryptedTimestamp := msgBody[88:116]

		serverHSum := blake2s.Sum256(append(InitialHash[:], serverPub...))
		serverH := serverHSum[:]
		serverCK := InitialChainKey[:]

		serverHSum = blake2s.Sum256(append(serverH, clientEPub...))
		serverH = serverHSum[:]
		serverCK = KDF1(serverCK, clientEPub)

		serverSS1, _ := curve25519.X25519(serverPriv, clientEPub)
		var serverKey1 []byte
		serverCK, serverKey1 = KDF2(serverCK, serverSS1)

		serverAead1, _ := chacha20poly1305.New(serverKey1)
		nonce0 := make([]byte, 12)
		clientStaticPub, _ := serverAead1.Open(nil, nonce0, encryptedStatic, serverH)
		serverHSum = blake2s.Sum256(append(serverH, encryptedStatic...))
		serverH = serverHSum[:]

		serverSS2, _ := curve25519.X25519(serverPriv, clientStaticPub)
		var serverKey2 []byte
		serverCK, serverKey2 = KDF2(serverCK, serverSS2)

		serverAead2, _ := chacha20poly1305.New(serverKey2)
		_, _ = serverAead2.Open(nil, nonce0, encryptedTimestamp, serverH)
		serverHSum = blake2s.Sum256(append(serverH, encryptedTimestamp...))
		serverH = serverHSum[:]

		serverEPriv, serverEPub := generateTestKeypair(t)
		serverHSum = blake2s.Sum256(append(serverH, serverEPub...))
		serverH = serverHSum[:]
		serverCK = KDF1(serverCK, serverEPub)

		serverSS3, _ := curve25519.X25519(serverEPriv, clientEPub)
		serverCK = KDF1(serverCK, serverSS3)

		serverSS4, _ := curve25519.X25519(serverEPriv, clientStaticPub)
		serverCK = KDF1(serverCK, serverSS4)

		var serverTau, serverKey3 []byte
		_, serverTau, serverKey3 = KDF3(serverCK, psk)
		serverHSum = blake2s.Sum256(append(serverH, serverTau...))
		serverH = serverHSum[:]

		serverAead3, _ := chacha20poly1305.New(serverKey3)
		encryptedEmpty := serverAead3.Seal(nil, nonce0, []byte{}, serverH)

		respMsgType := make([]byte, 4)
		binary.LittleEndian.PutUint32(respMsgType, h2)
		serverSenderIdx := make([]byte, 4)
		binary.LittleEndian.PutUint32(serverSenderIdx, 99999)
		respReceiverIdx := make([]byte, 4)
		binary.LittleEndian.PutUint32(respReceiverIdx, senderIdx)

		var respMsgBody []byte
		respMsgBody = append(respMsgBody, respMsgType...)
		respMsgBody = append(respMsgBody, serverSenderIdx...)
		respMsgBody = append(respMsgBody, respReceiverIdx...)
		respMsgBody = append(respMsgBody, serverEPub...)
		respMsgBody = append(respMsgBody, encryptedEmpty...)

		mac1KeySum := blake2s.Sum256(append(LabelMAC1, clientStaticPub...))
		hMac1, _ := blake2s.New128(mac1KeySum[:])
		hMac1.Write(respMsgBody)
		respMac1 := hMac1.Sum(nil)
		respMac2 := make([]byte, 16)

		var respPacket []byte
		if s2 > 0 {
			pad := make([]byte, s2)
			_, _ = rand.Read(pad)
			respPacket = append(respPacket, pad...)
		}
		respPacket = append(respPacket, respMsgBody...)
		respPacket = append(respPacket, respMac1...)
		respPacket = append(respPacket, respMac2...)

		_, _ = pc.WriteTo(respPacket, clientAddr)
	}()

	serverPubB64 := base64.StdEncoding.EncodeToString(serverPub)
	clientPrivB64 := base64.StdEncoding.EncodeToString(clientPriv)
	pskB64 := base64.StdEncoding.EncodeToString(psk)

	rtt, err := ProbeAWGEndpoint(context.Background(), addr, serverPubB64, clientPrivB64, pskB64, h1, h2, s1, s2, 2*time.Second)
	if err != nil {
		t.Fatalf("ProbeAWGEndpoint failed: %v", err)
	}

	if rtt <= 0 {
		t.Errorf("expected positive RTT, got: %v", rtt)
	}
}
