package cps

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"
)

// Default SIPDomains for mimicry probing.
var SIPDomains = []string{
	"sip.zadarma.com",
	"sip.iptel.org",
	"sip.linphone.org",
}

// SIPPool for fallback random domain choices.
var SIPPool = []string{
	"sipgate.de",
	"sip.ovh.net",
	"sip.voipfone.co.uk",
	"sip.linphone.org",
	"sip.zadarma.com",
	"sip.dus.net",
	"sip.easybell.de",
	"sip.1und1.de",
	"sip.voys.nl",
	"sip.antisip.com",
	"sip.iptel.org",
	"sip.voipgate.com",
}

// SIPUAPool for User-Agent headers.
var SIPUAPool = []string{
	"Linphone/5.2.5 (belle-sip/5.2.0)",
	"Zoiper rv2.10.20.4",
	"MicroSIP/3.21.4",
	"Bria 6.5.1",
	"PortSIP UA 16.4",
}

func randPrivateIP() (string, error) {
	kindBig, err := rand.Int(rand.Reader, big.NewInt(3))
	if err != nil {
		return "", err
	}
	switch kindBig.Int64() {
	case 0:
		b2, _ := rand.Int(rand.Reader, big.NewInt(254))
		b3, _ := rand.Int(rand.Reader, big.NewInt(256))
		b4, _ := rand.Int(rand.Reader, big.NewInt(253))
		return fmt.Sprintf("10.%d.%d.%d", b2.Int64()+1, b3.Int64(), b4.Int64()+2), nil
	case 1:
		b2, _ := rand.Int(rand.Reader, big.NewInt(16))
		b3, _ := rand.Int(rand.Reader, big.NewInt(256))
		b4, _ := rand.Int(rand.Reader, big.NewInt(253))
		return fmt.Sprintf("172.%d.%d.%d", b2.Int64()+16, b3.Int64(), b4.Int64()+2), nil
	default:
		b3, _ := rand.Int(rand.Reader, big.NewInt(256))
		b4, _ := rand.Int(rand.Reader, big.NewInt(253))
		return fmt.Sprintf("192.168.%d.%d", b3.Int64(), b4.Int64()+2), nil
	}
}

// GenSIP generates a realistic SIP REGISTER packet.
func GenSIP(domain string) ([]byte, error) {
	host := domain
	if host == "" {
		idx, err := rand.Int(rand.Reader, big.NewInt(int64(len(SIPPool))))
		if err != nil {
			return nil, fmt.Errorf("failed to select random SIP host: %w", err)
		}
		host = SIPPool[idx.Int64()]
	}

	userPrefixes := []string{"alice", "bob", "100", "200", "sip", "user", "client"}
	uIdx, err := rand.Int(rand.Reader, big.NewInt(int64(len(userPrefixes))))
	if err != nil {
		return nil, fmt.Errorf("failed to select user prefix: %w", err)
	}
	userNum, err := rand.Int(rand.Reader, big.NewInt(9990))
	if err != nil {
		return nil, fmt.Errorf("failed to select user num: %w", err)
	}
	user := fmt.Sprintf("%s%d", userPrefixes[uIdx.Int64()], userNum.Int64()+10)

	lip, err := randPrivateIP()
	if err != nil {
		return nil, fmt.Errorf("failed to generate private ip: %w", err)
	}

	ports := []string{"5060", "5062", "5080", "5160"}
	pIdx, err := rand.Int(rand.Reader, big.NewInt(int64(len(ports))))
	if err != nil {
		return nil, fmt.Errorf("failed to select port: %w", err)
	}
	lport := ports[pIdx.Int64()]

	branchBytes := make([]byte, 7)
	if _, err := rand.Read(branchBytes); err != nil {
		return nil, fmt.Errorf("failed to generate branch: %w", err)
	}
	branch := "z9hG4bK" + hex.EncodeToString(branchBytes)

	tagBytes := make([]byte, 4)
	if _, err := rand.Read(tagBytes); err != nil {
		return nil, fmt.Errorf("failed to generate tag: %w", err)
	}
	tag := hex.EncodeToString(tagBytes)

	callidBytes := make([]byte, 8)
	if _, err := rand.Read(callidBytes); err != nil {
		return nil, fmt.Errorf("failed to generate callid: %w", err)
	}
	callid := fmt.Sprintf("%s@%s", hex.EncodeToString(callidBytes), host)

	cseqNum, err := rand.Int(rand.Reader, big.NewInt(50))
	if err != nil {
		return nil, fmt.Errorf("failed to generate cseq: %w", err)
	}
	cseq := int(cseqNum.Int64()) + 1

	transports := []string{"UDP", "UDP", "UDP", "UDP", "TCP"}
	tIdx, err := rand.Int(rand.Reader, big.NewInt(int64(len(transports))))
	if err != nil {
		return nil, fmt.Errorf("failed to select transport: %w", err)
	}
	transport := transports[tIdx.Int64()]

	uaIdx, err := rand.Int(rand.Reader, big.NewInt(int64(len(SIPUAPool))))
	if err != nil {
		return nil, fmt.Errorf("failed to select UA: %w", err)
	}
	userAgent := SIPUAPool[uaIdx.Int64()]

	expiresOptions := []string{"300", "600", "1800", "3600"}
	eIdx, err := rand.Int(rand.Reader, big.NewInt(int64(len(expiresOptions))))
	if err != nil {
		return nil, fmt.Errorf("failed to select expires: %w", err)
	}
	expires := expiresOptions[eIdx.Int64()]

	lines := []string{
		fmt.Sprintf("REGISTER sip:%s SIP/2.0", host),
		fmt.Sprintf("Via: SIP/2.0/%s %s:%s;branch=%s;rport", transport, lip, lport, branch),
		"Max-Forwards: 70",
		fmt.Sprintf("From: <sip:%s@%s>;tag=%s", user, host, tag),
		fmt.Sprintf("To: <sip:%s@%s>", user, host),
		fmt.Sprintf("Call-ID: %s", callid),
		fmt.Sprintf("CSeq: %d REGISTER", cseq),
		fmt.Sprintf("Contact: <sip:%s@%s:%s;transport=%s>", user, lip, lport, strings.ToLower(transport)),
		fmt.Sprintf("User-Agent: %s", userAgent),
		"Allow: INVITE, ACK, CANCEL, BYE, REFER, OPTIONS, NOTIFY, SUBSCRIBE, PRACK, MESSAGE, INFO, UPDATE",
		"Supported: replaces, outbound, gruu, path",
		fmt.Sprintf("Expires: %s", expires),
		"Content-Length: 0",
		"",
		"",
	}

	return []byte(strings.Join(lines, "\r\n")), nil
}
