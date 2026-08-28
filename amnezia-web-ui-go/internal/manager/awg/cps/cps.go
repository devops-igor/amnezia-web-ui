package cps

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math/big"
	"strconv"
	"strings"

	"github.com/devops-igor/amnezia-web-ui-go/internal/manager/ssh"
)

// ProtocolPorts maps mimicry protocol names to reachability probe ports.
var ProtocolPorts = map[string]int{
	"quic": 443,
	"dns":  53,
	"sip":  5060,
	"tls":  443,
}

// FallbackDomains maps mimicry protocol names to fallback domains.
var FallbackDomains = map[string]string{
	"quic": "google.com",
	"dns":  "one.one.one.one",
	"sip":  "sip.linphone.org",
	"tls":  "www.google.com",
}

// ToCPS formats raw bytes into AWG binary blob tag format: <b 0xHEXSTRING>.
func ToCPS(raw []byte) string {
	return fmt.Sprintf("<b 0x%x>", raw)
}

// ParseCPSBlob parses AWG binary tag string format '<b 0xHEX>' or '<r N><b 0xHEX>' into raw bytes.
func ParseCPSBlob(tagStr string) ([]byte, error) {
	s := strings.TrimSpace(tagStr)
	if s == "" {
		return nil, nil
	}

	rCount := 0
	if strings.HasPrefix(s, "<r ") {
		rEnd := strings.Index(s, ">")
		if rEnd != -1 {
			nStr := strings.TrimSpace(s[3:rEnd])
			if n, err := strconv.Atoi(nStr); err == nil && n >= 0 {
				rCount = n
			}
			s = strings.TrimSpace(s[rEnd+1:])
		}
	}

	if strings.HasPrefix(s, "<b 0x") && strings.HasSuffix(s, ">") {
		hexData := strings.TrimSpace(s[5 : len(s)-1])
		raw, err := hex.DecodeString(hexData)
		if err != nil {
			return nil, fmt.Errorf("invalid hex in CPS blob: %w", err)
		}

		if rCount > 0 && len(raw) >= rCount {
			rnd := make([]byte, rCount)
			if _, err := rand.Read(rnd); err != nil {
				return nil, fmt.Errorf("failed to generate random prefix: %w", err)
			}
			copy(raw[:rCount], rnd)
		}
		return raw, nil
	}

	return nil, fmt.Errorf("unrecognized CPS tag format: %s", tagStr)
}

// GenerateCPSPackets generates I1-I5 CPS packet signatures for the specified profile.
func GenerateCPSPackets(profile string, domain string) (map[string]string, error) {
	switch strings.ToLower(profile) {
	case "lite":
		dnsDomain := domain
		if dnsDomain == "" {
			dnsDomain = "icloud.com"
		}
		dnsPayload, err := GenDNS(dnsDomain)
		if err != nil {
			return nil, err
		}
		txid := make([]byte, 2)
		if _, err := rand.Read(txid); err != nil {
			return nil, err
		}
		i1Raw := append(txid, dnsPayload...)
		return map[string]string{
			"i1": fmt.Sprintf("<r 2><b 0x%x>", i1Raw),
			"i2": "",
			"i3": "",
			"i4": "",
			"i5": "",
		}, nil

	case "pro":
		i1, err := GenQUICInitial(domain)
		if err != nil {
			return nil, err
		}
		i2, err := GenQUICShort()
		if err != nil {
			return nil, err
		}
		i3, err := GenQUICShort()
		if err != nil {
			return nil, err
		}
		i4, err := GenQUICShort()
		if err != nil {
			return nil, err
		}
		i5, err := GenQUICShort()
		if err != nil {
			return nil, err
		}
		return map[string]string{
			"i1": ToCPS(i1),
			"i2": ToCPS(i2),
			"i3": ToCPS(i3),
			"i4": ToCPS(i4),
			"i5": ToCPS(i5),
		}, nil

	default: // "standard"
		i1, err := GenQUICInitial(domain)
		if err != nil {
			return nil, err
		}
		return map[string]string{
			"i1": ToCPS(i1),
			"i2": "",
			"i3": "",
			"i4": "",
			"i5": "",
		}, nil
	}
}

// GenerateMimicryPackets generates I1-I5 signature packets for a specific mimicry profile.
func GenerateMimicryPackets(ctx context.Context, mimicry string, domain string, sshClient ssh.SSHClient) (map[string]string, error) {
	m := strings.ToLower(strings.TrimSpace(mimicry))
	if m == "" || m == "auto" {
		if sshClient != nil {
			d, err := SelectMimicryDomain(ctx, sshClient, "tls")
			if err == nil && d != "" {
				domain = d
			}
		}
		m = "tls"
	}

	switch m {
	case "tls":
		tlsPkt, err := GenTLS(domain)
		if err != nil {
			return nil, err
		}
		return map[string]string{
			"i1": ToCPS(tlsPkt),
			"i2": "",
			"i3": "",
			"i4": "",
			"i5": "",
		}, nil

	case "dns":
		dnsDomain := domain
		if dnsDomain == "" {
			dnsDomain = "one.one.one.one"
		}
		dnsPayload, err := GenDNS(dnsDomain)
		if err != nil {
			return nil, err
		}
		txid := make([]byte, 2)
		if _, err := rand.Read(txid); err != nil {
			return nil, err
		}
		i1Raw := append(txid, dnsPayload...)
		return map[string]string{
			"i1": fmt.Sprintf("<r 2><b 0x%x>", i1Raw),
			"i2": "",
			"i3": "",
			"i4": "",
			"i5": "",
		}, nil

	case "sip":
		sipPkt, err := GenSIP(domain)
		if err != nil {
			return nil, err
		}
		return map[string]string{
			"i1": ToCPS(sipPkt),
			"i2": "",
			"i3": "",
			"i4": "",
			"i5": "",
		}, nil

	case "quic":
		quicPkt, err := GenQUICInitial(domain)
		if err != nil {
			return nil, err
		}
		return map[string]string{
			"i1": ToCPS(quicPkt),
			"i2": "",
			"i3": "",
			"i4": "",
			"i5": "",
		}, nil

	default:
		quicPkt, err := GenQUICInitial(domain)
		if err != nil {
			return nil, err
		}
		return map[string]string{
			"i1": ToCPS(quicPkt),
			"i2": "",
			"i3": "",
			"i4": "",
			"i5": "",
		}, nil
	}
}

// SelectMimicryDomain probes candidate domains from the server via SSH and returns the first reachable one.
func SelectMimicryDomain(ctx context.Context, sshClient ssh.SSHClient, protocol string) (string, error) {
	if sshClient == nil {
		return FallbackDomains[protocol], nil
	}

	var domainPool []string
	switch protocol {
	case "quic":
		domainPool = QUICDomains
	case "dns":
		domainPool = DNSDomains
	case "sip":
		domainPool = SIPDomains
	case "tls":
		domainPool = TLSDomains
	default:
		domainPool = QUICDomains
	}

	port, ok := ProtocolPorts[protocol]
	if !ok {
		port = 443
	}
	fallback := FallbackDomains[protocol]
	if fallback == "" {
		fallback = "google.com"
	}

	// Shuffle candidates
	candidates := make([]string, len(domainPool))
	copy(candidates, domainPool)
	for i := len(candidates) - 1; i > 0; i-- {
		jBig, err := rand.Int(rand.Reader, big.NewInt(int64(i+1)))
		if err == nil {
			j := int(jBig.Int64())
			candidates[i], candidates[j] = candidates[j], candidates[i]
		}
	}

	// Try up to 5 random domains
	limit := 5
	if len(candidates) < limit {
		limit = len(candidates)
	}

	for _, domain := range candidates[:limit] {
		cmd := fmt.Sprintf("timeout 2 bash -c 'echo > /dev/tcp/%s/%d' 2>/dev/null && echo OK || echo FAIL", domain, port)
		stdout, _, _, err := sshClient.RunCommand(ctx, cmd)
		if err == nil && strings.Contains(stdout, "OK") {
			return domain, nil
		}
	}

	return fallback, nil
}

// GenerateConnectionKit generates a multi-config Connection Kit (TLS, QUIC, DNS, SIP) from a base AWG config.
func GenerateConnectionKit(ctx context.Context, baseConfig string, domain string, sshClient ssh.SSHClient) (map[string]string, error) {
	profiles := []string{"tls", "quic", "dns", "sip"}
	kit := make(map[string]string)

	for _, proto := range profiles {
		packets, err := GenerateMimicryPackets(ctx, proto, domain, sshClient)
		if err != nil {
			return nil, fmt.Errorf("failed to generate %s mimicry packets: %w", proto, err)
		}

		lines := strings.Split(baseConfig, "\n")
		var newLines []string
		inInterface := false
		iKeysAdded := false

		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if strings.EqualFold(trimmed, "[Interface]") {
				inInterface = true
				newLines = append(newLines, line)
				continue
			} else if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
				if inInterface && !iKeysAdded {
					for _, k := range []string{"i1", "i2", "i3", "i4", "i5"} {
						if val, exists := packets[k]; exists && val != "" {
							newLines = append(newLines, fmt.Sprintf("%s = %s", strings.ToUpper(k), val))
						}
					}
					iKeysAdded = true
				}
				inInterface = false
				newLines = append(newLines, line)
				continue
			}

			// Strip existing I1-I5 lines
			if inInterface {
				isIKey := false
				for _, num := range []string{"1", "2", "3", "4", "5"} {
					if strings.HasPrefix(strings.ToUpper(trimmed), "I"+num) {
						isIKey = true
						break
					}
				}
				if isIKey {
					continue
				}
			}

			newLines = append(newLines, line)
		}

		if inInterface && !iKeysAdded {
			for _, k := range []string{"i1", "i2", "i3", "i4", "i5"} {
				if val, exists := packets[k]; exists && val != "" {
					newLines = append(newLines, fmt.Sprintf("%s = %s", strings.ToUpper(k), val))
				}
			}
		}

		kit[proto] = strings.Join(newLines, "\n")
	}

	return kit, nil
}
