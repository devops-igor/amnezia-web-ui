package dns

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

// DefaultDNS1 and DefaultDNS2 are default DNS-over-TLS upstream servers (AdGuard DNS).
const (
	DefaultDNS1 = "94.140.14.14"
	DefaultDNS2 = "94.140.15.15"
)

// RenderForwardRecords renders Unbound forward-records.conf for DNS-over-TLS upstreams.
func RenderForwardRecords(dns1, dns2 string) string {
	if dns1 == "" {
		dns1 = DefaultDNS1
	}
	if dns2 == "" {
		dns2 = DefaultDNS2
	}

	return fmt.Sprintf(`forward-zone:
   name: "."
   forward-tls-upstream: yes
   forward-addr: %s@853
   forward-addr: %s@853
`, dns1, dns2)
}

// RenderDockerfile renders the Dockerfile for mvance/unbound based AmneziaDNS container.
func RenderDockerfile() string {
	return `FROM mvance/unbound:latest
LABEL maintainer="AmneziaVPN"
COPY forward-records.conf /opt/unbound/etc/unbound/forward-records.conf
`
}

// ProbeDNSQuery sends a UDP DNS query for the given domain and measures RTT.
func ProbeDNSQuery(ctx context.Context, host string, port int, domain string, timeout time.Duration) (time.Duration, error) {
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	if port <= 0 {
		port = 53
	}
	if domain == "" {
		domain = "google.com"
	}

	endpoint := net.JoinHostPort(host, strconv.Itoa(port))

	// Build DNS Query Packet
	txid := make([]byte, 2)
	if _, err := rand.Read(txid); err != nil {
		return 0, fmt.Errorf("failed to generate txid: %w", err)
	}

	flags := []byte{0x01, 0x00} // Standard query, RD=1
	counts := []byte{0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}

	var qn []byte
	for _, lbl := range strings.Split(domain, ".") {
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

	qtype := []byte{0x00, 0x01}  // Type A
	qclass := []byte{0x00, 0x01} // Class IN

	var query []byte
	query = append(query, txid...)
	query = append(query, flags...)
	query = append(query, counts...)
	query = append(query, qn...)
	query = append(query, qtype...)
	query = append(query, qclass...)

	d := net.Dialer{Timeout: timeout}
	conn, err := d.DialContext(ctx, "udp", endpoint)
	if err != nil {
		return 0, fmt.Errorf("failed to dial DNS endpoint %s: %w", endpoint, err)
	}
	defer func() {
		_ = conn.Close()
	}()

	tStart := time.Now()
	if err := conn.SetDeadline(tStart.Add(timeout)); err != nil {
		return 0, err
	}

	if _, err := conn.Write(query); err != nil {
		return 0, fmt.Errorf("failed to send DNS query: %w", err)
	}

	buf := make([]byte, 1024)
	n, err := conn.Read(buf)
	if err != nil {
		return 0, fmt.Errorf("failed to receive DNS response: %w", err)
	}

	rtt := time.Since(tStart)
	if n < 12 {
		return 0, errors.New("DNS response too short")
	}

	// Verify Transaction ID
	if buf[0] != txid[0] || buf[1] != txid[1] {
		return 0, errors.New("DNS transaction ID mismatch in response")
	}

	// Verify QR bit is set (bit 7 of byte 2)
	if buf[2]&0x80 == 0 {
		return 0, errors.New("DNS response flag QR bit not set")
	}

	return rtt, nil
}
