package awg

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"

	"github.com/devops-igor/amnezia-web-ui-go/internal/manager/awg/cps"
	"golang.org/x/crypto/curve25519"
)

// AWGDefaults holds standard default values for AmneziaWG protocol parameters.
var AWGDefaults = map[string]string{
	"port":                          "55424",
	"mtu":                           "1280",
	"subnet_address":                "10.8.1.0",
	"subnet_cidr":                   "24",
	"subnet_ip":                     "10.8.1.1",
	"dns1":                          "94.140.14.14",
	"dns2":                          "94.140.15.15",
	"junk_packet_count":             "3",
	"junk_packet_min_size":          "10",
	"junk_packet_max_size":          "30",
	"init_packet_junk_size":         "15",
	"response_packet_junk_size":     "18",
	"cookie_reply_packet_junk_size": "20",
	"transport_packet_junk_size":    "23",
	"init_packet_magic_header":      "1020325451",
	"response_packet_magic_header":  "3288052141",
	"transport_packet_magic_header": "2528465083",
	"underload_packet_magic_header": "1766607858",
}

// AWGParams encapsulates all parameters for server and client config generation.
//
//nolint:revive
type AWGParams struct {
	Port                       string `json:"port"`
	MTU                        string `json:"mtu"`
	SubnetAddress              string `json:"subnet_address"`
	SubnetCIDR                 string `json:"subnet_cidr"`
	SubnetIP                   string `json:"subnet_ip"`
	DNS1                       string `json:"dns1"`
	DNS2                       string `json:"dns2"`
	JunkPacketCount            string `json:"junk_packet_count"`
	JunkPacketMinSize          string `json:"junk_packet_min_size"`
	JunkPacketMaxSize          string `json:"junk_packet_max_size"`
	InitPacketJunkSize         string `json:"init_packet_junk_size"`
	ResponsePacketJunkSize     string `json:"response_packet_junk_size"`
	CookieReplyPacketJunkSize  string `json:"cookie_reply_packet_junk_size"`
	TransportPacketJunkSize    string `json:"transport_packet_junk_size"`
	InitPacketMagicHeader      string `json:"init_packet_magic_header"`
	ResponsePacketMagicHeader  string `json:"response_packet_magic_header"`
	UnderloadPacketMagicHeader string `json:"underload_packet_magic_header"`
	TransportPacketMagicHeader string `json:"transport_packet_magic_header"`
	I1                         string `json:"i1,omitempty"`
	I2                         string `json:"i2,omitempty"`
	I3                         string `json:"i3,omitempty"`
	I4                         string `json:"i4,omitempty"`
	I5                         string `json:"i5,omitempty"`
}

// ToMap converts AWGParams to a map of string key-values.
func (p *AWGParams) ToMap() map[string]string {
	m := map[string]string{
		"port":                          p.Port,
		"mtu":                           p.MTU,
		"subnet_address":                p.SubnetAddress,
		"subnet_cidr":                   p.SubnetCIDR,
		"subnet_ip":                     p.SubnetIP,
		"dns1":                          p.DNS1,
		"dns2":                          p.DNS2,
		"junk_packet_count":             p.JunkPacketCount,
		"junk_packet_min_size":          p.JunkPacketMinSize,
		"junk_packet_max_size":          p.JunkPacketMaxSize,
		"init_packet_junk_size":         p.InitPacketJunkSize,
		"response_packet_junk_size":     p.ResponsePacketJunkSize,
		"cookie_reply_packet_junk_size": p.CookieReplyPacketJunkSize,
		"transport_packet_junk_size":    p.TransportPacketJunkSize,
		"init_packet_magic_header":      p.InitPacketMagicHeader,
		"response_packet_magic_header":  p.ResponsePacketMagicHeader,
		"underload_packet_magic_header": p.UnderloadPacketMagicHeader,
		"transport_packet_magic_header": p.TransportPacketMagicHeader,
		"i1":                            p.I1,
		"i2":                            p.I2,
		"i3":                            p.I3,
		"i4":                            p.I4,
		"i5":                            p.I5,
	}
	return m
}

// AWGParamsFromMap populates AWGParams from a generic map.
//
//nolint:revive
func AWGParamsFromMap(m map[string]any) *AWGParams {
	p := &AWGParams{
		Port:                       AWGDefaults["port"],
		MTU:                        AWGDefaults["mtu"],
		SubnetAddress:              AWGDefaults["subnet_address"],
		SubnetCIDR:                 AWGDefaults["subnet_cidr"],
		SubnetIP:                   AWGDefaults["subnet_ip"],
		DNS1:                       AWGDefaults["dns1"],
		DNS2:                       AWGDefaults["dns2"],
		JunkPacketCount:            AWGDefaults["junk_packet_count"],
		JunkPacketMinSize:          AWGDefaults["junk_packet_min_size"],
		JunkPacketMaxSize:          AWGDefaults["junk_packet_max_size"],
		InitPacketJunkSize:         AWGDefaults["init_packet_junk_size"],
		ResponsePacketJunkSize:     AWGDefaults["response_packet_junk_size"],
		CookieReplyPacketJunkSize:  AWGDefaults["cookie_reply_packet_junk_size"],
		TransportPacketJunkSize:    AWGDefaults["transport_packet_junk_size"],
		InitPacketMagicHeader:      AWGDefaults["init_packet_magic_header"],
		ResponsePacketMagicHeader:  AWGDefaults["response_packet_magic_header"],
		UnderloadPacketMagicHeader: AWGDefaults["underload_packet_magic_header"],
		TransportPacketMagicHeader: AWGDefaults["transport_packet_magic_header"],
	}

	for k, v := range m {
		strVal := fmt.Sprint(v)
		switch strings.ToLower(k) {
		case "port", "listenport":
			p.Port = strVal
		case "mtu":
			p.MTU = strVal
		case "subnet_address":
			p.SubnetAddress = strVal
		case "subnet_cidr":
			p.SubnetCIDR = strVal
		case "subnet_ip":
			p.SubnetIP = strVal
		case "dns1":
			p.DNS1 = strVal
		case "dns2":
			p.DNS2 = strVal
		case "junk_packet_count", "jc":
			p.JunkPacketCount = strVal
		case "junk_packet_min_size", "jmin":
			p.JunkPacketMinSize = strVal
		case "junk_packet_max_size", "jmax":
			p.JunkPacketMaxSize = strVal
		case "init_packet_junk_size", "s1":
			p.InitPacketJunkSize = strVal
		case "response_packet_junk_size", "s2":
			p.ResponsePacketJunkSize = strVal
		case "cookie_reply_packet_junk_size", "s3":
			p.CookieReplyPacketJunkSize = strVal
		case "transport_packet_junk_size", "s4":
			p.TransportPacketJunkSize = strVal
		case "init_packet_magic_header", "h1":
			p.InitPacketMagicHeader = strVal
		case "response_packet_magic_header", "h2":
			p.ResponsePacketMagicHeader = strVal
		case "underload_packet_magic_header", "h3":
			p.UnderloadPacketMagicHeader = strVal
		case "transport_packet_magic_header", "h4":
			p.TransportPacketMagicHeader = strVal
		case "i1":
			p.I1 = strVal
		case "i2":
			p.I2 = strVal
		case "i3":
			p.I3 = strVal
		case "i4":
			p.I4 = strVal
		case "i5":
			p.I5 = strVal
		}
	}
	return p
}

// GenerateWGKeypair generates a Curve25519 keypair formatted as base64 strings.
func GenerateWGKeypair() (privateKeyBase64, publicKeyBase64 string, err error) {
	priv := make([]byte, 32)
	if _, err := rand.Read(priv); err != nil {
		return "", "", fmt.Errorf("failed to generate random private key: %w", err)
	}

	pub, err := curve25519.X25519(priv, curve25519.Basepoint)
	if err != nil {
		return "", "", fmt.Errorf("failed to derive public key: %w", err)
	}

	return base64.StdEncoding.EncodeToString(priv), base64.StdEncoding.EncodeToString(pub), nil
}

// GeneratePSK generates a 32-byte cryptographically secure preshared key as base64 string.
func GeneratePSK() (string, error) {
	psk := make([]byte, 32)
	if _, err := rand.Read(psk); err != nil {
		return "", fmt.Errorf("failed to generate random psk: %w", err)
	}
	return base64.StdEncoding.EncodeToString(psk), nil
}

func randIntBetween(min, max int) (int, error) {
	if min > max {
		min, max = max, min
	}
	diff := max - min + 1
	n, err := rand.Int(rand.Reader, big.NewInt(int64(diff)))
	if err != nil {
		return 0, err
	}
	return min + int(n.Int64()), nil
}

func randUint32Between(min, max uint32) (uint32, error) {
	if min > max {
		min, max = max, min
	}
	// #nosec G115
	diff := int64(max - min + 1)
	n, err := rand.Int(rand.Reader, big.NewInt(diff))
	if err != nil {
		return 0, err
	}
	// #nosec G115
	return min + uint32(n.Uint64()), nil
}

// GenerateQuadrantHeaders generates H1-H4 across non-overlapping quadrants of [5, 2^31 - 1] with min span >= 1000.
func GenerateQuadrantHeaders() (h1, h2, h3, h4 uint32, err error) {
	const maxVal uint32 = math.MaxInt32 // 2147483647
	const qSize uint32 = maxVal / 4     // 536870911

	headers := make([]uint32, 4)
	for i := 0; i < 4; i++ {
		lo := uint32(5 + uint32(i)*qSize)
		hi := uint32(i+1) * qSize
		if i < 3 {
			hi++
		}

		a, err := randUint32Between(lo, hi)
		if err != nil {
			return 0, 0, 0, 0, err
		}
		b, err := randUint32Between(lo, hi)
		if err != nil {
			return 0, 0, 0, 0, err
		}

		if a > b {
			a, b = b, a
		}
		if b-a < 1000 {
			if a+1000 <= hi {
				b = a + 1000
			} else {
				b = hi
			}
		}

		headers[i], err = randUint32Between(a, b)
		if err != nil {
			return 0, 0, 0, 0, err
		}
	}

	return headers[0], headers[1], headers[2], headers[3], nil
}

// GenerateAWGParams generates randomized AWG obfuscation parameters based on the given profile.
func GenerateAWGParams(profile string) (*AWGParams, error) {
	profile = strings.ToLower(strings.TrimSpace(profile))
	if profile == "" {
		profile = "standard"
	}

	var jc, jmin, jmax, s1, s2, s3, s4 int
	var err error

	switch profile {
	case "lite":
		jc, _ = randIntBetween(3, 5)
		jmin, _ = randIntBetween(5, 15)
		jmaxAddon, _ := randIntBetween(45, 55)
		jmax = jmin + jmaxAddon
		s1, _ = randIntBetween(97, 107)
		s2, _ = randIntBetween(17, 27)
		s3, _ = randIntBetween(16, 26)
		s4, _ = randIntBetween(4, 10)

	case "pro":
		jc, _ = randIntBetween(4, 16)
		jmin, _ = randIntBetween(50, 256)
		jmaxAddon, _ := randIntBetween(300, 1000)
		jmax = jmin + jmaxAddon
		s1, _ = randIntBetween(15, 150)
		s2, _ = randIntBetween(15, 150)
		s3, _ = randIntBetween(8, 64)
		s4, _ = randIntBetween(6, 31)

	default: // "standard"
		jc, _ = randIntBetween(5, 8)
		jmin, _ = randIntBetween(30, 80)
		jmaxAddon, _ := randIntBetween(100, 250)
		jmax = jmin + jmaxAddon
		s1, _ = randIntBetween(30, 80)
		s2, _ = randIntBetween(30, 80)
		s3, _ = randIntBetween(15, 32)
		s4, _ = randIntBetween(10, 20)
	}

	// Enforce |s1 - s2| >= 10 constraint
	for attempts := 0; attempts < 100; attempts++ {
		diff := s1 - s2
		if diff < 0 {
			diff = -diff
		}
		if diff >= 10 {
			break
		}
		switch profile {
		case "lite":
			s2, _ = randIntBetween(17, 27)
		case "pro":
			s2, _ = randIntBetween(15, 150)
		default:
			s2, _ = randIntBetween(30, 80)
		}
	}
	diff := s1 - s2
	if diff < 0 {
		diff = -diff
	}
	if diff < 10 {
		if s1+10 <= 150 {
			s2 = s1 + 10
		} else {
			s2 = s1 - 10
		}
	}

	h1, h2, h3, h4, err := GenerateQuadrantHeaders()
	if err != nil {
		return nil, err
	}

	mtu := "1280"
	if profile == "pro" {
		mtu = "1320"
	}

	cpsPackets, err := cps.GenerateCPSPackets(profile, "")
	if err != nil {
		return nil, err
	}

	params := &AWGParams{
		Port:                       AWGDefaults["port"],
		MTU:                        mtu,
		SubnetAddress:              AWGDefaults["subnet_address"],
		SubnetCIDR:                 AWGDefaults["subnet_cidr"],
		SubnetIP:                   AWGDefaults["subnet_ip"],
		DNS1:                       AWGDefaults["dns1"],
		DNS2:                       AWGDefaults["dns2"],
		JunkPacketCount:            strconv.Itoa(jc),
		JunkPacketMinSize:          strconv.Itoa(jmin),
		JunkPacketMaxSize:          strconv.Itoa(jmax),
		InitPacketJunkSize:         strconv.Itoa(s1),
		ResponsePacketJunkSize:     strconv.Itoa(s2),
		CookieReplyPacketJunkSize:  strconv.Itoa(s3),
		TransportPacketJunkSize:    strconv.Itoa(s4),
		InitPacketMagicHeader:      strconv.FormatUint(uint64(h1), 10),
		ResponsePacketMagicHeader:  strconv.FormatUint(uint64(h2), 10),
		UnderloadPacketMagicHeader: strconv.FormatUint(uint64(h3), 10),
		TransportPacketMagicHeader: strconv.FormatUint(uint64(h4), 10),
		I1:                         cpsPackets["i1"],
		I2:                         cpsPackets["i2"],
		I3:                         cpsPackets["i3"],
		I4:                         cpsPackets["i4"],
		I5:                         cpsPackets["i5"],
	}

	return params, nil
}

// ValidateAWGParams ensures all AWG parameters are numeric strings within safe ranges to prevent command injection.
func ValidateAWGParams(params map[string]string) error {
	numericBounds := map[string][2]int64{
		"junk_packet_count":             {1, 100},
		"junk_packet_min_size":          {1, 1000},
		"junk_packet_max_size":          {1, 1300},
		"init_packet_junk_size":         {1, 1000},
		"response_packet_junk_size":     {1, 1000},
		"cookie_reply_packet_junk_size": {1, 1000},
		"transport_packet_junk_size":    {1, 1000},
		"init_packet_magic_header":      {5, 4294967295},
		"response_packet_magic_header":  {5, 4294967295},
		"underload_packet_magic_header": {5, 4294967295},
		"transport_packet_magic_header": {5, 4294967295},
	}

	for k, bounds := range numericBounds {
		val, ok := params[k]
		if !ok || val == "" {
			continue
		}
		num, err := strconv.ParseInt(val, 10, 64)
		if err != nil {
			return fmt.Errorf("param %s must be a numeric integer, got: %s", k, val)
		}
		if num < bounds[0] || num > bounds[1] {
			return fmt.Errorf("param %s must be between %d and %d, got: %d", k, bounds[0], bounds[1], num)
		}
	}

	for _, k := range []string{"i1", "i2", "i3", "i4", "i5"} {
		val, ok := params[k]
		if !ok || val == "" {
			continue
		}
		if !strings.HasPrefix(val, "<") || !strings.HasSuffix(val, ">") {
			return fmt.Errorf("param %s must be in <b 0xHEX> or <r N><b 0xHEX> format, got: %s", k, val)
		}
	}

	if mtuStr, ok := params["mtu"]; ok && mtuStr != "" {
		mtu, err := strconv.Atoi(mtuStr)
		if err != nil || mtu < 1200 || mtu > 1500 {
			return fmt.Errorf("param mtu must be between 1200 and 1500, got: %s", mtuStr)
		}
	}

	return nil
}
