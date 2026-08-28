package cps

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"math/big"
)

// Default TLSDomains for SNI generation.
var TLSDomains = []string{
	"www.google.com",
	"www.cloudflare.com",
	"www.microsoft.com",
	"www.apple.com",
	"aws.amazon.com",
	"www.wikipedia.org",
}

// GenTLS generates a realistic TLS 1.3 / 1.2 ClientHello packet byte layout.
func GenTLS(domain string) ([]byte, error) {
	if domain == "" {
		idx, err := rand.Int(rand.Reader, big.NewInt(int64(len(TLSDomains))))
		if err != nil {
			return nil, fmt.Errorf("failed to select random TLS domain: %w", err)
		}
		domain = TLSDomains[idx.Int64()]
	}

	domainBytes := []byte(domain)

	// 1. SNI extension (type 0x0000)
	// ServerName: NameType(1 byte, 0x00) + NameLength(2 bytes) + Name
	sniServerName := make([]byte, 1+2+len(domainBytes))
	sniServerName[0] = 0x00
	// #nosec G115
	binary.BigEndian.PutUint16(sniServerName[1:3], uint16(len(domainBytes)))
	copy(sniServerName[3:], domainBytes)

	// ServerNameList: Length(2 bytes) + ServerName
	sniList := make([]byte, 2+len(sniServerName))
	// #nosec G115
	binary.BigEndian.PutUint16(sniList[0:2], uint16(len(sniServerName)))
	copy(sniList[2:], sniServerName)

	// Extension: Type(2 bytes, 0x0000) + Length(2 bytes) + ServerNameList
	extSNI := make([]byte, 4+len(sniList))
	extSNI[0] = 0x00
	extSNI[1] = 0x00
	// #nosec G115
	binary.BigEndian.PutUint16(extSNI[2:4], uint16(len(sniList)))
	copy(extSNI[4:], sniList)

	// 2. Supported groups (type 0x000a): x25519 (0x001d), secp256r1 (0x0017), secp384r1 (0x0018)
	groups := []byte{0x00, 0x1d, 0x00, 0x17, 0x00, 0x18}
	extGroups := make([]byte, 4+2+len(groups))
	extGroups[0] = 0x00
	extGroups[1] = 0x0a
	// #nosec G115
	binary.BigEndian.PutUint16(extGroups[2:4], uint16(len(groups)+2))
	// #nosec G115
	binary.BigEndian.PutUint16(extGroups[4:6], uint16(len(groups)))
	copy(extGroups[6:], groups)

	// 3. EC Point Formats (type 0x000b): uncompressed (0x00)
	extECPoints := []byte{0x00, 0x0b, 0x00, 0x02, 0x01, 0x00}

	// 4. Signature Algorithms (type 0x000d)
	sigAlgs := []byte{
		0x04, 0x03, 0x08, 0x04, 0x04, 0x01, 0x05, 0x03,
		0x08, 0x05, 0x05, 0x01, 0x08, 0x06, 0x06, 0x01, 0x02, 0x01,
	}
	extSigAlgs := make([]byte, 4+2+len(sigAlgs))
	extSigAlgs[0] = 0x00
	extSigAlgs[1] = 0x0d
	// #nosec G115
	binary.BigEndian.PutUint16(extSigAlgs[2:4], uint16(len(sigAlgs)+2))
	// #nosec G115
	binary.BigEndian.PutUint16(extSigAlgs[4:6], uint16(len(sigAlgs)))
	copy(extSigAlgs[6:], sigAlgs)

	// 5. Supported Versions (type 0x002b): TLS 1.3 (0x0304), TLS 1.2 (0x0303)
	versions := []byte{0x03, 0x04, 0x03, 0x03}
	extVersions := make([]byte, 4+1+len(versions))
	extVersions[0] = 0x00
	extVersions[1] = 0x2b
	// #nosec G115
	binary.BigEndian.PutUint16(extVersions[2:4], uint16(len(versions)+1))
	// #nosec G115
	extVersions[4] = byte(len(versions))
	copy(extVersions[5:], versions)

	// 6. Key Share (type 0x0033): x25519 (0x001d) + 32-byte random public key
	keyPub := make([]byte, 32)
	if _, err := rand.Read(keyPub); err != nil {
		return nil, fmt.Errorf("failed to generate random key share: %w", err)
	}
	keyShareEntry := make([]byte, 2+2+32)
	keyShareEntry[0] = 0x00
	keyShareEntry[1] = 0x1d
	binary.BigEndian.PutUint16(keyShareEntry[2:4], 32)
	copy(keyShareEntry[4:], keyPub)

	keyShareData := make([]byte, 2+len(keyShareEntry))
	// #nosec G115
	binary.BigEndian.PutUint16(keyShareData[0:2], uint16(len(keyShareEntry)))
	copy(keyShareData[2:], keyShareEntry)

	extKeyShare := make([]byte, 4+len(keyShareData))
	extKeyShare[0] = 0x00
	extKeyShare[1] = 0x33
	// #nosec G115
	binary.BigEndian.PutUint16(extKeyShare[2:4], uint16(len(keyShareData)))
	copy(extKeyShare[4:], keyShareData)

	// 7. ALPN (type 0x0010): h2, http/1.1
	alpnList := []byte("\x02h2\x08http/1.1")
	alpnData := make([]byte, 2+len(alpnList))
	// #nosec G115
	binary.BigEndian.PutUint16(alpnData[0:2], uint16(len(alpnList)))
	copy(alpnData[2:], alpnList)

	extALPN := make([]byte, 4+len(alpnData))
	extALPN[0] = 0x00
	extALPN[1] = 0x10
	// #nosec G115
	binary.BigEndian.PutUint16(extALPN[2:4], uint16(len(alpnData)))
	copy(extALPN[4:], alpnData)

	// Combine all extensions
	var allExtensions []byte
	allExtensions = append(allExtensions, extSNI...)
	allExtensions = append(allExtensions, extGroups...)
	allExtensions = append(allExtensions, extECPoints...)
	allExtensions = append(allExtensions, extSigAlgs...)
	allExtensions = append(allExtensions, extVersions...)
	allExtensions = append(allExtensions, extKeyShare...)
	allExtensions = append(allExtensions, extALPN...)

	extensionsBlock := make([]byte, 2+len(allExtensions))
	// #nosec G115
	binary.BigEndian.PutUint16(extensionsBlock[0:2], uint16(len(allExtensions)))
	copy(extensionsBlock[2:], allExtensions)

	// Client Random & Session ID
	clientRandom := make([]byte, 32)
	if _, err := rand.Read(clientRandom); err != nil {
		return nil, fmt.Errorf("failed to generate client random: %w", err)
	}
	sessionID := make([]byte, 32)
	if _, err := rand.Read(sessionID); err != nil {
		return nil, fmt.Errorf("failed to generate session ID: %w", err)
	}
	sessionIDBlock := make([]byte, 1+len(sessionID))
	// #nosec G115
	sessionIDBlock[0] = byte(len(sessionID))
	copy(sessionIDBlock[1:], sessionID)

	// Cipher suites (16 suites = 32 bytes)
	cipherSuites := []byte{
		0x13, 0x01, 0x13, 0x02, 0x13, 0x03, // TLS 1.3
		0xc0, 0x2b, 0xc0, 0x2f, 0xc0, 0x2c, 0xc0, 0x30, // ECDHE-ECDSA/RSA AES-GCM
		0xcc, 0xa9, 0xcc, 0xa8, // CHACHA20-POLY1305
		0xc0, 0x13, 0xc0, 0x14, 0x00, 0x9c, 0x00, 0x9d, 0x00, 0x2f, 0x00, 0x35,
	}
	cipherBlock := make([]byte, 2+len(cipherSuites))
	// #nosec G115
	binary.BigEndian.PutUint16(cipherBlock[0:2], uint16(len(cipherSuites)))
	copy(cipherBlock[2:], cipherSuites)

	compressionMethods := []byte{0x01, 0x00} // 1 method: null

	// Handshake payload
	clientVersion := []byte{0x03, 0x03} // TLS 1.2 record version for max compatibility
	var handshakePayload []byte
	handshakePayload = append(handshakePayload, clientVersion...)
	handshakePayload = append(handshakePayload, clientRandom...)
	handshakePayload = append(handshakePayload, sessionIDBlock...)
	handshakePayload = append(handshakePayload, cipherBlock...)
	handshakePayload = append(handshakePayload, compressionMethods...)
	handshakePayload = append(handshakePayload, extensionsBlock...)

	// Handshake header: msg_type=1 (ClientHello) + 3-byte payload length
	handshakeLenBytes := make([]byte, 4)
	// #nosec G115
	binary.BigEndian.PutUint32(handshakeLenBytes, uint32(len(handshakePayload)))
	handshakeMsg := append([]byte{0x01}, handshakeLenBytes[1:]...)
	handshakeMsg = append(handshakeMsg, handshakePayload...)

	// TLS Record header: content_type=0x16 (Handshake), version=0x0301 (TLS 1.0), length=2 bytes
	recordHeader := make([]byte, 5)
	recordHeader[0] = 0x16
	recordHeader[1] = 0x03
	recordHeader[2] = 0x01
	// #nosec G115
	binary.BigEndian.PutUint16(recordHeader[3:5], uint16(len(handshakeMsg)))

	return append(recordHeader, handshakeMsg...), nil
}
