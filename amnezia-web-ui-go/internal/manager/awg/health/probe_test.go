package health

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"net"
	"strconv"
	"testing"
	"time"

	"golang.org/x/crypto/blake2s"
	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/curve25519"
)

func startMockAWGServer(t *testing.T, h1, h2 uint32, s1, s2 int) (net.PacketConn, []byte, string, int) {
	serverPriv, serverPub := generateTestKeypair(t)
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen UDP: %v", err)
	}

	host, portStr, _ := net.SplitHostPort(pc.LocalAddr().String())
	port, _ := strconv.Atoi(portStr)

	go func() {
		buf := make([]byte, 2048)
		for {
			n, clientAddr, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			if n < s1+116 {
				continue
			}

			msgBody := buf[s1 : s1+116]
			msgType := binary.LittleEndian.Uint32(msgBody[0:4])
			if msgType != h1 {
				continue
			}
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
			_, serverTau, serverKey3 = KDF3(serverCK, make([]byte, 32))
			serverHSum = blake2s.Sum256(append(serverH, serverTau...))
			serverH = serverHSum[:]

			serverAead3, _ := chacha20poly1305.New(serverKey3)
			encryptedEmpty := serverAead3.Seal(nil, nonce0, []byte{}, serverH)

			respMsgType := make([]byte, 4)
			binary.LittleEndian.PutUint32(respMsgType, h2)
			serverSenderIdx := make([]byte, 4)
			binary.LittleEndian.PutUint32(serverSenderIdx, 12345)
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
		}
	}()

	return pc, serverPub, host, port
}

func TestPerformAWGHandshake(t *testing.T) {
	ctx := context.Background()
	h1 := DefaultH1
	h2 := DefaultH2
	s1 := DefaultS1
	s2 := DefaultS2

	pc, serverPub, host, port := startMockAWGServer(t, h1, h2, s1, s2)
	defer func() {
		_ = pc.Close()
	}()

	serverPubB64 := base64.StdEncoding.EncodeToString(serverPub)
	params := map[string]any{
		"init_packet_magic_header":     strconv.FormatUint(uint64(h1), 10),
		"response_packet_magic_header": strconv.FormatUint(uint64(h2), 10),
		"init_packet_junk_size":        strconv.Itoa(s1),
		"response_packet_junk_size":    strconv.Itoa(s2),
		"junk_packet_count":            "2",
		"junk_packet_min_size":         "10",
		"junk_packet_max_size":         "20",
	}

	res, err := PerformAWGHandshake(ctx, host, port, serverPubB64, "", "", params, "quic", 2*time.Second)
	if err != nil {
		t.Fatalf("PerformAWGHandshake returned error: %v", err)
	}

	if reachable, ok := res["reachable"].(bool); !ok || !reachable {
		t.Errorf("expected reachable to be true, got %v (err=%v)", reachable, res["error"])
	}

	// Test with invalid endpoint
	resDead, _ := PerformAWGHandshake(ctx, "127.0.0.1", 1, serverPubB64, "", "", params, "", 200*time.Millisecond)
	if reachable, ok := resDead["reachable"].(bool); !ok || reachable {
		t.Errorf("expected unreachable for closed port, got: %v", resDead)
	}
}

func TestRunAutoTrialProfiles(t *testing.T) {
	ctx := context.Background()
	h1 := DefaultH1
	h2 := DefaultH2
	s1 := DefaultS1
	s2 := DefaultS2

	pc, serverPub, host, port := startMockAWGServer(t, h1, h2, s1, s2)
	defer func() {
		_ = pc.Close()
	}()

	serverPubB64 := base64.StdEncoding.EncodeToString(serverPub)
	params := map[string]any{
		"init_packet_magic_header":     strconv.FormatUint(uint64(h1), 10),
		"response_packet_magic_header": strconv.FormatUint(uint64(h2), 10),
		"init_packet_junk_size":        strconv.Itoa(s1),
		"response_packet_junk_size":    strconv.Itoa(s2),
	}

	results, err := RunAutoTrialProfiles(ctx, host, port, serverPubB64, "", "", params, 2*time.Second)
	if err != nil {
		t.Fatalf("RunAutoTrialProfiles failed: %v", err)
	}

	for _, proto := range []string{"tls", "quic", "dns", "sip"} {
		res, ok := results[proto]
		if !ok {
			t.Fatalf("missing profile result for %s", proto)
		}
		if reachable, ok := res["reachable"].(bool); !ok || !reachable {
			t.Errorf("profile %s expected reachable, got %v (err=%v)", proto, reachable, res["error"])
		}
	}

	// Test invalid key errors
	if _, err := ProbeAWGEndpoint(ctx, "127.0.0.1:55424", "invalid-key", "", "", 0, 0, 0, 0, 0); err == nil {
		t.Errorf("expected error for invalid server key")
	}
	if _, err := ProbeAWGEndpoint(ctx, "127.0.0.1:55424", serverPubB64, "invalid-key", "", 0, 0, 0, 0, 0); err == nil {
		t.Errorf("expected error for invalid client key")
	}
	if _, err := ProbeAWGEndpoint(ctx, "127.0.0.1:55424", serverPubB64, "", "invalid-key", 0, 0, 0, 0, 0); err == nil {
		t.Errorf("expected error for invalid psk")
	}
}
