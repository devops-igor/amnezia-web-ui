package health

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"net"
	"strconv"
	"time"

	"github.com/devops-igor/amnezia-web-ui-go/internal/manager/awg/cps"
)

// ProbeAWGEndpoint performs a pure-Go UDP Noise IK handshake probe and measures RTT latency.
func ProbeAWGEndpoint(ctx context.Context, endpoint string, serverPubKey string, clientPrivKey string, psk string, h1, h2 uint32, s1, s2 int, timeout time.Duration) (time.Duration, error) {
	if timeout <= 0 {
		timeout = 3 * time.Second
	}

	serverPubBytes, err := DecodeKey(serverPubKey)
	if err != nil {
		return 0, fmt.Errorf("invalid server public key: %w", err)
	}

	var clientPrivBytes []byte
	if clientPrivKey != "" {
		clientPrivBytes, err = DecodeKey(clientPrivKey)
		if err != nil {
			return 0, fmt.Errorf("invalid client private key: %w", err)
		}
	}

	var pskBytes []byte
	if psk != "" {
		pskBytes, err = DecodeKey(psk)
		if err != nil {
			return 0, fmt.Errorf("invalid preshared key: %w", err)
		}
	}

	initPacket, state, err := BuildAWGInitiationPacket(serverPubBytes, clientPrivBytes, pskBytes, h1, s1)
	if err != nil {
		return 0, fmt.Errorf("failed to build initiation packet: %w", err)
	}

	d := net.Dialer{Timeout: timeout}
	conn, err := d.DialContext(ctx, "udp", endpoint)
	if err != nil {
		return 0, fmt.Errorf("failed to dial UDP endpoint %s: %w", endpoint, err)
	}
	defer func() {
		_ = conn.Close()
	}()

	tStart := time.Now()
	if err := conn.SetDeadline(tStart.Add(timeout)); err != nil {
		return 0, err
	}

	if _, err := conn.Write(initPacket); err != nil {
		return 0, fmt.Errorf("failed to send handshake initiation: %w", err)
	}

	buf := make([]byte, 2048)
	n, err := conn.Read(buf)
	if err != nil {
		return 0, fmt.Errorf("failed to receive handshake response: %w", err)
	}

	rtt := time.Since(tStart)
	if !VerifyAWGResponsePacket(buf[:n], state, h2, s2) {
		return 0, errors.New("handshake response verification failed")
	}

	return rtt, nil
}

func extractPreambles(ctx context.Context, mimicryProfile string, awgParams map[string]any) [][]byte {
	var preambles [][]byte
	if mimicryProfile != "" {
		packets, err := cps.GenerateMimicryPackets(ctx, mimicryProfile, "", nil)
		if err == nil {
			for _, k := range []string{"i1", "i2", "i3", "i4", "i5"} {
				if blobStr, ok := packets[k]; ok && blobStr != "" {
					if raw, err := cps.ParseCPSBlob(blobStr); err == nil && len(raw) > 0 {
						preambles = append(preambles, raw)
					}
				}
			}
		}
	} else if awgParams != nil {
		for _, k := range []string{"i1", "i2", "i3", "i4", "i5"} {
			if v, ok := awgParams[k]; ok {
				if strVal, isStr := v.(string); isStr && strVal != "" {
					if raw, err := cps.ParseCPSBlob(strVal); err == nil && len(raw) > 0 {
						preambles = append(preambles, raw)
					}
				}
			}
		}
	}
	preambles = append(preambles, extractJunkPreambles(awgParams)...)
	return preambles
}

func extractJunkPreambles(awgParams map[string]any) [][]byte {
	if awgParams == nil {
		return nil
	}
	jcVal, ok := awgParams["junk_packet_count"]
	if !ok {
		return nil
	}
	jc, _ := strconv.Atoi(fmt.Sprint(jcVal))
	jmin, _ := strconv.Atoi(fmt.Sprint(awgParams["junk_packet_min_size"]))
	jmax, _ := strconv.Atoi(fmt.Sprint(awgParams["junk_packet_max_size"]))
	if jmin <= 0 {
		jmin = 10
	}
	if jmax < jmin {
		jmax = jmin + 20
	}
	var preambles [][]byte
	for i := 0; i < jc; i++ {
		diff := jmax - jmin + 1
		pLenBig, _ := rand.Int(rand.Reader, big.NewInt(int64(diff)))
		pLen := jmin + int(pLenBig.Int64())
		pData := make([]byte, pLen)
		_, _ = rand.Read(pData)
		preambles = append(preambles, pData)
	}
	return preambles
}

func extractAWGHeaderLimits(awgParams map[string]any) (h1, h2 uint32, s1, s2 int) {
	h1 = DefaultH1
	h2 = DefaultH2
	s1 = DefaultS1
	s2 = DefaultS2

	if awgParams == nil {
		return
	}
	if v, ok := awgParams["init_packet_magic_header"]; ok {
		if num, err := strconv.ParseUint(fmt.Sprint(v), 10, 32); err == nil {
			h1 = uint32(num)
		}
	}
	if v, ok := awgParams["response_packet_magic_header"]; ok {
		if num, err := strconv.ParseUint(fmt.Sprint(v), 10, 32); err == nil {
			h2 = uint32(num)
		}
	}
	if v, ok := awgParams["init_packet_junk_size"]; ok {
		if num, err := strconv.Atoi(fmt.Sprint(v)); err == nil {
			s1 = num
		}
	}
	if v, ok := awgParams["response_packet_junk_size"]; ok {
		if num, err := strconv.Atoi(fmt.Sprint(v)); err == nil {
			s2 = num
		}
	}
	return
}

// PerformAWGHandshake executes a complete AWG reachability probe including preambles and CPS blobs.
func PerformAWGHandshake(ctx context.Context, host string, port int, serverPubKey string, clientPrivKey string, psk string, awgParams map[string]any, mimicryProfile string, timeout time.Duration) (map[string]any, error) {
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	endpoint := net.JoinHostPort(host, strconv.Itoa(port))

	preambles := extractPreambles(ctx, mimicryProfile, awgParams)
	h1, h2, s1, s2 := extractAWGHeaderLimits(awgParams)

	// Dial socket
	d := net.Dialer{Timeout: timeout}
	conn, err := d.DialContext(ctx, "udp", endpoint)
	if err != nil {
		return map[string]any{
			"reachable":          false,
			"latency_ms":         0,
			"protocol":           "awg",
			"handshake_complete": false,
			"profile":            mimicryProfile,
			"last_checked":       time.Now().UTC().Format(time.RFC3339),
			"error":              err.Error(),
		}, nil
	}
	defer func() {
		_ = conn.Close()
	}()

	tStart := time.Now()
	if err := conn.SetDeadline(tStart.Add(timeout)); err != nil {
		return nil, err
	}

	// Send preambles
	for _, p := range preambles {
		_, _ = conn.Write(p)
	}

	// Send Handshake
	serverPubBytes, err := DecodeKey(serverPubKey)
	if err != nil {
		return nil, err
	}
	var clientPrivBytes, pskBytes []byte
	if clientPrivKey != "" {
		clientPrivBytes, _ = DecodeKey(clientPrivKey)
	}
	if psk != "" {
		pskBytes, _ = DecodeKey(psk)
	}

	initPacket, state, err := BuildAWGInitiationPacket(serverPubBytes, clientPrivBytes, pskBytes, h1, s1)
	if err != nil {
		return nil, err
	}

	if _, err := conn.Write(initPacket); err != nil {
		return map[string]any{
			"reachable":          false,
			"latency_ms":         0,
			"protocol":           "awg",
			"handshake_complete": false,
			"profile":            mimicryProfile,
			"last_checked":       time.Now().UTC().Format(time.RFC3339),
			"error":              err.Error(),
		}, nil
	}

	buf := make([]byte, 2048)
	n, err := conn.Read(buf)
	if err != nil {
		return map[string]any{
			"reachable":          false,
			"latency_ms":         0,
			"protocol":           "awg",
			"handshake_complete": false,
			"profile":            mimicryProfile,
			"last_checked":       time.Now().UTC().Format(time.RFC3339),
			"error":              "Handshake timeout (no response from server)",
		}, nil
	}

	rtt := time.Since(tStart)
	latencyMs := int(rtt.Milliseconds())
	if latencyMs <= 0 {
		latencyMs = 1
	}

	if !VerifyAWGResponsePacket(buf[:n], state, h2, s2) {
		return map[string]any{
			"reachable":          false,
			"latency_ms":         latencyMs,
			"protocol":           "awg",
			"handshake_complete": false,
			"profile":            mimicryProfile,
			"last_checked":       time.Now().UTC().Format(time.RFC3339),
			"error":              "Handshake response verification failed",
		}, nil
	}

	return map[string]any{
		"reachable":          true,
		"latency_ms":         latencyMs,
		"protocol":           "awg",
		"handshake_complete": true,
		"profile":            mimicryProfile,
		"last_checked":       time.Now().UTC().Format(time.RFC3339),
		"error":              "",
	}, nil
}

// RunAutoTrialProfiles tests reachability across all 4 AWG mimicry profiles.
func RunAutoTrialProfiles(ctx context.Context, host string, port int, serverPubKey string, clientPrivKey string, psk string, awgParams map[string]any, timeout time.Duration) (map[string]map[string]any, error) {
	profiles := []string{"tls", "quic", "dns", "sip"}
	results := make(map[string]map[string]any)

	for _, proto := range profiles {
		res, err := PerformAWGHandshake(ctx, host, port, serverPubKey, clientPrivKey, psk, awgParams, proto, timeout)
		if err != nil {
			results[proto] = map[string]any{
				"reachable":          false,
				"latency_ms":         0,
				"protocol":           "awg",
				"handshake_complete": false,
				"profile":            proto,
				"last_checked":       time.Now().UTC().Format(time.RFC3339),
				"error":              err.Error(),
			}
		} else {
			results[proto] = res
		}
	}

	return results, nil
}
