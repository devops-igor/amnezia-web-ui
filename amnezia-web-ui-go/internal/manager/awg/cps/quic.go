package cps

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"math/big"
)

// Default QUICDomains for mimicry probing.
var QUICDomains = []string{
	"google.com",
	"youtube.com",
	"cdn.jsdelivr.net",
	"unpkg.com",
	"icloud.com",
	"fastly.net",
	"github.com",
}

// GenQUICInitial generates a realistic compact QUIC Initial packet (216 bytes).
func GenQUICInitial(domain string) ([]byte, error) {
	_ = domain // Reserved for SNI embedding if needed
	const targetLen = 216

	firstBytes := []byte{0xC0, 0xC0, 0xC0, 0xC3}
	idx, err := rand.Int(rand.Reader, big.NewInt(int64(len(firstBytes))))
	if err != nil {
		return nil, fmt.Errorf("failed to select first byte: %w", err)
	}
	fb := firstBytes[idx.Int64()]

	pnLen := int(fb&0x03) + 1
	dcid := make([]byte, 8)
	if _, err := rand.Read(dcid); err != nil {
		return nil, fmt.Errorf("failed to generate dcid: %w", err)
	}
	scid := make([]byte, 8)
	if _, err := rand.Read(scid); err != nil {
		return nil, fmt.Errorf("failed to generate scid: %w", err)
	}

	encSize := targetLen - 26 - pnLen
	if encSize < 1 {
		encSize = 1
	}
	plenVal := uint16(pnLen + encSize)
	plVarint := make([]byte, 2)
	binary.BigEndian.PutUint16(plVarint, 0x4000|plenVal)

	pn := make([]byte, pnLen)
	if _, err := rand.Read(pn); err != nil {
		return nil, fmt.Errorf("failed to generate pn: %w", err)
	}
	payload := make([]byte, encSize)
	if _, err := rand.Read(payload); err != nil {
		return nil, fmt.Errorf("failed to generate payload: %w", err)
	}

	pkt := make([]byte, 0, targetLen)
	pkt = append(pkt, fb)
	pkt = append(pkt, 0x00, 0x00, 0x00, 0x01) // Version QUIC v1
	pkt = append(pkt, 8)
	pkt = append(pkt, dcid...)
	pkt = append(pkt, 8)
	pkt = append(pkt, scid...)
	pkt = append(pkt, 0x00) // Token length 0
	pkt = append(pkt, plVarint...)
	pkt = append(pkt, pn...)
	pkt = append(pkt, payload...)

	if len(pkt) < targetLen {
		pad := make([]byte, targetLen-len(pkt))
		if _, err := rand.Read(pad); err != nil {
			return nil, fmt.Errorf("failed to generate pad: %w", err)
		}
		pkt = append(pkt, pad...)
	} else if len(pkt) > targetLen {
		pkt = pkt[:targetLen]
	}

	return pkt, nil
}

// GenQUICShort generates a realistic QUIC Short Header (1-RTT) packet (50-100 bytes).
func GenQUICShort() ([]byte, error) {
	pnLenBig, err := rand.Int(rand.Reader, big.NewInt(4))
	if err != nil {
		return nil, fmt.Errorf("failed to generate pnLen: %w", err)
	}
	pnLen := int(pnLenBig.Int64()) + 1 // 1..4

	spinBig, err := rand.Int(rand.Reader, big.NewInt(2))
	if err != nil {
		return nil, fmt.Errorf("failed to generate spin: %w", err)
	}
	// #nosec G115
	spin := byte(spinBig.Int64() << 5)

	keyBig, err := rand.Int(rand.Reader, big.NewInt(2))
	if err != nil {
		return nil, fmt.Errorf("failed to generate key: %w", err)
	}
	// #nosec G115
	key := byte(keyBig.Int64() << 2)
	// #nosec G115
	fb := 0x40 | spin | key | byte(pnLen-1)

	dcid := make([]byte, 8)
	if _, err := rand.Read(dcid); err != nil {
		return nil, fmt.Errorf("failed to generate dcid: %w", err)
	}
	pn := make([]byte, pnLen)
	if _, err := rand.Read(pn); err != nil {
		return nil, fmt.Errorf("failed to generate pn: %w", err)
	}

	dataLenBig, err := rand.Int(rand.Reader, big.NewInt(51)) // 0..50
	if err != nil {
		return nil, fmt.Errorf("failed to generate data len: %w", err)
	}
	dataLen := 40 + int(dataLenBig.Int64()) // 40..90
	data := make([]byte, dataLen)
	if _, err := rand.Read(data); err != nil {
		return nil, fmt.Errorf("failed to generate random data: %w", err)
	}

	pkt := make([]byte, 0, 1+len(dcid)+len(pn)+len(data))
	pkt = append(pkt, fb)
	pkt = append(pkt, dcid...)
	pkt = append(pkt, pn...)
	pkt = append(pkt, data...)

	return pkt, nil
}
