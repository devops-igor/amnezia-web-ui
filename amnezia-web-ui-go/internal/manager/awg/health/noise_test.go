package health

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
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

func TestNoiseIKPythonGoldenVectors(t *testing.T) {
	// Golden test vectors captured from Python reference implementation
	// (app/managers/awg_health.py with fixed keys, PSK, and timestamp)
	sPrivHex := "0101010101010101010101010101010101010101010101010101010101010101"
	sPubHex := "a4e09292b651c278b9772c569f5fa9bb13d906b46ab68c9df9dc2b4409f8a209"
	cPrivHex := "0202020202020202020202020202020202020202020202020202020202020202"
	cPubHex := "ce8d3ad1ccb633ec7b70c17814a5c76ecd029685050d344745ba05870e587d59"
	cEPrivHex := "0303030303030303030303030303030303030303030303030303030303030303"
	cEPubHex := "5dfedd3b6bd47f6fa28ee15d969d5bb0ea53774d488bdaf9df1c6e0124b3ef22"
	sEPubHex := "ac01b2209e86354fb853237b5de0f4fab13c7fcbf433a61c019369617fecf10b"
	pskHex := "0505050505050505050505050505050505050505050505050505050505050505"

	goldenInitHex := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaa4beed03c4e61bc005dfedd3b6bd47f6fa28ee15d969d5bb0ea53774d488bdaf9df1c6e0124b3ef221f1f5b7b3fdae8d1855d0c746a81d11e1b6bf4e46a142c120afa1bb28112d1a2053183af575274f778f3250872f547f306e9ee1ed0d58854344a16a4fffd3520f045865b5093a8c092b33cfe919e8dac3e1630faf20fbb4b406eb1a000000000000000000000000000000000"
	goldenRespHex := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbadb1fbc3b17f39054e61bc00ac01b2209e86354fb853237b5de0f4fab13c7fcbf433a61c019369617fecf10bdc0bbda656ef152174c75dd5f737f8b47a44068ca4776e6bc60bc59d0c49ea1900000000000000000000000000000000"

	goldenInitH := "e10412408eaa413229b675b8ae6d6ae4bfe825c5e72eaae416bfde4dc89f0565"
	goldenInitCK := "6737aec9e9513fe7b32f464ee0626d0301a88b13abc57ec8a39ed822fe8fcfae"
	goldenRespH := "cd46e07a37f3ef55f83b1fd3bae906f47ef708e76e16f86b2b70b3e56af09d32"
	goldenRespCK := "1f0f8f0d6a3b73f8740d5537021c8ea1b034a72b0b608adfdae43fd7925ffb22"
	goldenKey3 := "4af4e0b2aaffb2f71942ecc23083dc5e00eb835879f4fae18fce2d36a358c710"
	goldenEncEmpty := "dc0bbda656ef152174c75dd5f737f8b4"

	sPriv, _ := hex.DecodeString(sPrivHex)
	sPub, _ := hex.DecodeString(sPubHex)
	cPriv, _ := hex.DecodeString(cPrivHex)
	cPub, _ := hex.DecodeString(cPubHex)
	cEPriv, _ := hex.DecodeString(cEPrivHex)
	cEPub, _ := hex.DecodeString(cEPubHex)
	sEPub, _ := hex.DecodeString(sEPubHex)
	psk, _ := hex.DecodeString(pskHex)

	goldenInitPacket, _ := hex.DecodeString(goldenInitHex)
	goldenRespPacket, _ := hex.DecodeString(goldenRespHex)

	h1 := uint32(1020325451)
	h2 := uint32(3288052141)
	s1 := 15
	s2 := 18
	senderIdx := uint32(12345678)

	// 1. Verify Public Keys derived from Private Keys match expected vectors
	computedSPub, err := curve25519.X25519(sPriv, curve25519.Basepoint)
	if err != nil || !bytes.Equal(computedSPub, sPub) {
		t.Fatalf("Server public key mismatch: got %x, want %x", computedSPub, sPub)
	}
	computedCPub, err := curve25519.X25519(cPriv, curve25519.Basepoint)
	if err != nil || !bytes.Equal(computedCPub, cPub) {
		t.Fatalf("Client public key mismatch: got %x, want %x", computedCPub, cPub)
	}
	computedCEPub, err := curve25519.X25519(cEPriv, curve25519.Basepoint)
	if err != nil || !bytes.Equal(computedCEPub, cEPub) {
		t.Fatalf("Client ephemeral public key mismatch: got %x, want %x", computedCEPub, cEPub)
	}

	// 2. Client-side initiation state reconstruction
	hSum := blake2s.Sum256(append(InitialHash[:], sPub...))
	h := hSum[:]
	ck := InitialChainKey[:]

	hSum = blake2s.Sum256(append(h, cEPub...))
	h = hSum[:]
	ck = KDF1(ck, cEPub)

	ss1, _ := curve25519.X25519(cEPriv, sPub)
	var key1 []byte
	ck, key1 = KDF2(ck, ss1)

	nonce0 := make([]byte, 12)
	aead1, _ := chacha20poly1305.New(key1)
	encryptedStatic := aead1.Seal(nil, nonce0, cPub, h)
	hSum = blake2s.Sum256(append(h, encryptedStatic...))
	h = hSum[:]

	ss2, _ := curve25519.X25519(cPriv, sPub)
	var key2 []byte
	ck, key2 = KDF2(ck, ss2)

	tai64n := make([]byte, 12)
	binary.BigEndian.PutUint64(tai64n[0:8], 0x400000006000000A)
	binary.BigEndian.PutUint32(tai64n[8:12], 0x12340000)

	aead2, _ := chacha20poly1305.New(key2)
	encryptedTimestamp := aead2.Seal(nil, nonce0, tai64n, h)
	hSum = blake2s.Sum256(append(h, encryptedTimestamp...))
	h = hSum[:]

	// Verify Hash and Chaining Key after initiation match Python golden values
	if hex.EncodeToString(h) != goldenInitH {
		t.Errorf("Initiation H mismatch: got %s, want %s", hex.EncodeToString(h), goldenInitH)
	}
	if hex.EncodeToString(ck) != goldenInitCK {
		t.Errorf("Initiation CK mismatch: got %s, want %s", hex.EncodeToString(ck), goldenInitCK)
	}

	// 3. Assemble and compare initiation packet
	msgTypeBytes := make([]byte, 4)
	binary.LittleEndian.PutUint32(msgTypeBytes, h1)
	senderIdxBytes := make([]byte, 4)
	binary.LittleEndian.PutUint32(senderIdxBytes, senderIdx)

	var msgBody []byte
	msgBody = append(msgBody, msgTypeBytes...)
	msgBody = append(msgBody, senderIdxBytes...)
	msgBody = append(msgBody, cEPub...)
	msgBody = append(msgBody, encryptedStatic...)
	msgBody = append(msgBody, encryptedTimestamp...)

	mac1KeySum := blake2s.Sum256(append(LabelMAC1, sPub...))
	mac1Hasher, _ := blake2s.New128(mac1KeySum[:])
	mac1Hasher.Write(msgBody)
	mac1 := mac1Hasher.Sum(nil)
	mac2 := make([]byte, 16)

	s1Padding := bytes.Repeat([]byte{0xAA}, s1)
	var assembledInitPacket []byte
	assembledInitPacket = append(assembledInitPacket, s1Padding...)
	assembledInitPacket = append(assembledInitPacket, msgBody...)
	assembledInitPacket = append(assembledInitPacket, mac1...)
	assembledInitPacket = append(assembledInitPacket, mac2...)

	if !bytes.Equal(assembledInitPacket, goldenInitPacket) {
		t.Fatalf("Assembled initiation packet does not match Python golden vector:\ngot:  %x\nwant: %x", assembledInitPacket, goldenInitPacket)
	}

	// 4. Server-side processing of the golden initiation packet
	srvMsgBody := goldenInitPacket[s1 : s1+116]
	srvMsgType := binary.LittleEndian.Uint32(srvMsgBody[0:4])
	if srvMsgType != h1 {
		t.Fatalf("Server msgType mismatch: %d != %d", srvMsgType, h1)
	}
	srvSenderIdx := binary.LittleEndian.Uint32(srvMsgBody[4:8])
	if srvSenderIdx != senderIdx {
		t.Fatalf("Server senderIdx mismatch: %d != %d", srvSenderIdx, senderIdx)
	}
	srvCEPub := srvMsgBody[8:40]
	srvEncStatic := srvMsgBody[40:88]
	srvEncTs := srvMsgBody[88:116]

	srvHSum := blake2s.Sum256(append(InitialHash[:], sPub...))
	srvH := srvHSum[:]
	srvCK := InitialChainKey[:]

	srvHSum = blake2s.Sum256(append(srvH, srvCEPub...))
	srvH = srvHSum[:]
	srvCK = KDF1(srvCK, srvCEPub)

	srvSS1, _ := curve25519.X25519(sPriv, srvCEPub)
	var srvKey1 []byte
	srvCK, srvKey1 = KDF2(srvCK, srvSS1)

	srvAead1, _ := chacha20poly1305.New(srvKey1)
	decStaticPub, err := srvAead1.Open(nil, nonce0, srvEncStatic, srvH)
	if err != nil || !bytes.Equal(decStaticPub, cPub) {
		t.Fatalf("Server failed to decrypt client static public key: %v, got %x, want %x", err, decStaticPub, cPub)
	}
	srvHSum = blake2s.Sum256(append(srvH, srvEncStatic...))
	srvH = srvHSum[:]

	srvSS2, _ := curve25519.X25519(sPriv, decStaticPub)
	var srvKey2 []byte
	_, srvKey2 = KDF2(srvCK, srvSS2)

	srvAead2, _ := chacha20poly1305.New(srvKey2)
	decTs, err := srvAead2.Open(nil, nonce0, srvEncTs, srvH)
	if err != nil || !bytes.Equal(decTs, tai64n) {
		t.Fatalf("Server failed to decrypt TAI64N timestamp: %v, got %x, want %x", err, decTs, tai64n)
	}

	// 5. Response packet intermediate key verification
	respHSum := blake2s.Sum256(append(h, sEPub...))
	respH := respHSum[:]
	respCK := KDF1(ck, sEPub)

	clientSS3, _ := curve25519.X25519(cEPriv, sEPub)
	respCK = KDF1(respCK, clientSS3)

	clientSS4, _ := curve25519.X25519(cPriv, sEPub)
	respCK = KDF1(respCK, clientSS4)

	var tau, key3 []byte
	respCK, tau, key3 = KDF3(respCK, psk)
	respHSum = blake2s.Sum256(append(respH, tau...))
	respH = respHSum[:]

	if hex.EncodeToString(respH) != goldenRespH {
		t.Errorf("Response H mismatch: got %s, want %s", hex.EncodeToString(respH), goldenRespH)
	}
	if hex.EncodeToString(respCK) != goldenRespCK {
		t.Errorf("Response CK mismatch: got %s, want %s", hex.EncodeToString(respCK), goldenRespCK)
	}
	if hex.EncodeToString(key3) != goldenKey3 {
		t.Errorf("Response Key3 mismatch: got %s, want %s", hex.EncodeToString(key3), goldenKey3)
	}

	aead3, _ := chacha20poly1305.New(key3)
	computedEncEmpty := aead3.Seal(nil, nonce0, []byte{}, respH)
	if hex.EncodeToString(computedEncEmpty) != goldenEncEmpty {
		t.Errorf("Encrypted empty payload mismatch: got %s, want %s", hex.EncodeToString(computedEncEmpty), goldenEncEmpty)
	}

	// 6. Verify full response packet with VerifyAWGResponsePacket
	clientState := &NoiseClientState{
		H:           h,
		CK:          ck,
		ClientEPriv: cEPriv,
		ClientPriv:  cPriv,
		ServerPub:   sPub,
		PSK:         psk,
		SenderIndex: senderIdx,
		MAC1Key:     mac1KeySum[:],
	}

	valid := VerifyAWGResponsePacket(goldenRespPacket, clientState, h2, s2)
	if !valid {
		t.Fatalf("VerifyAWGResponsePacket failed on Python golden response packet")
	}

	// Negative test: verify failure on corrupted golden packet
	corruptedResp := make([]byte, len(goldenRespPacket))
	copy(corruptedResp, goldenRespPacket)
	corruptedResp[s2+44] ^= 0x01
	if VerifyAWGResponsePacket(corruptedResp, clientState, h2, s2) {
		t.Errorf("VerifyAWGResponsePacket should fail on corrupted golden packet")
	}
}
