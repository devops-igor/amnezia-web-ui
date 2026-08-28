package cps

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"math/big"
	"strings"
)

// Default DNSDomains for mimicry probing.
var DNSDomains = []string{
	"google.com",
	"cloudflare.com",
	"one.one.one.one",
}

// GenDNS generates a DNS query payload with EDNS0 OPT-RR.
// The result is intended to be prefixed with a 2-byte transaction ID (e.g. via <r 2>).
func GenDNS(domain string) ([]byte, error) {
	if domain == "" {
		idx, err := rand.Int(rand.Reader, big.NewInt(int64(len(DNSDomains))))
		if err != nil {
			return nil, fmt.Errorf("failed to select random DNS domain: %w", err)
		}
		domain = DNSDomains[idx.Int64()]
	}

	flags := []byte{0x01, 0x00} // QR=0 Query, RD=1
	counts := []byte{0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01}

	var qn []byte
	labels := strings.Split(domain, ".")
	for _, lbl := range labels {
		if len(lbl) == 0 {
			continue
		}
		lblBytes := []byte(lbl)
		if len(lblBytes) > 63 {
			lblBytes = lblBytes[:63]
		}
		// #nosec G115
		qn = append(qn, byte(len(lblBytes)))
		qn = append(qn, lblBytes...)
	}
	qn = append(qn, 0x00)

	qtype := []byte{0x00, 0x01}  // A record
	qclass := []byte{0x00, 0x01} // IN

	udpSizes := []uint16{1232, 4096}
	sizeIdx, err := rand.Int(rand.Reader, big.NewInt(int64(len(udpSizes))))
	if err != nil {
		return nil, fmt.Errorf("failed to select udp size: %w", err)
	}
	udpSize := udpSizes[sizeIdx.Int64()]

	doBits := []uint16{0x0000, 0x8000}
	doIdx, err := rand.Int(rand.Reader, big.NewInt(int64(len(doBits))))
	if err != nil {
		return nil, fmt.Errorf("failed to select do bit: %w", err)
	}
	doBit := doBits[doIdx.Int64()]

	optRR := make([]byte, 11)
	optRR[0] = 0x00 // root name
	optRR[1] = 0x00 // type OPT (41)
	optRR[2] = 0x29
	binary.BigEndian.PutUint16(optRR[3:5], udpSize)
	optRR[5] = 0x00 // ext RCODE
	optRR[6] = 0x00 // version
	binary.BigEndian.PutUint16(optRR[7:9], doBit)
	optRR[9] = 0x00 // rdlen 0
	optRR[10] = 0x00

	var pkt []byte
	pkt = append(pkt, flags...)
	pkt = append(pkt, counts...)
	pkt = append(pkt, qn...)
	pkt = append(pkt, qtype...)
	pkt = append(pkt, qclass...)
	pkt = append(pkt, optRR...)

	return pkt, nil
}
