package awg

import (
	"encoding/base64"
	"strconv"
	"testing"
)

func TestGenerateWGKeypair(t *testing.T) {
	priv, pub, err := GenerateWGKeypair()
	if err != nil {
		t.Fatalf("GenerateWGKeypair failed: %v", err)
	}

	privBytes, err := base64.StdEncoding.DecodeString(priv)
	if err != nil || len(privBytes) != 32 {
		t.Errorf("invalid private key base64: %v", err)
	}

	pubBytes, err := base64.StdEncoding.DecodeString(pub)
	if err != nil || len(pubBytes) != 32 {
		t.Errorf("invalid public key base64: %v", err)
	}
}

func TestGeneratePSK(t *testing.T) {
	psk, err := GeneratePSK()
	if err != nil {
		t.Fatalf("GeneratePSK failed: %v", err)
	}

	pskBytes, err := base64.StdEncoding.DecodeString(psk)
	if err != nil || len(pskBytes) != 32 {
		t.Errorf("invalid PSK base64: %v", err)
	}
}

func TestGenerateQuadrantHeaders(t *testing.T) {
	for i := 0; i < 20; i++ {
		h1, h2, h3, h4, err := GenerateQuadrantHeaders()
		if err != nil {
			t.Fatalf("GenerateQuadrantHeaders failed: %v", err)
		}

		if h1 < 5 || h2 <= h1 || h3 <= h2 || h4 <= h3 {
			t.Errorf("quadrant headers not strictly increasing: %d, %d, %d, %d", h1, h2, h3, h4)
		}

		const qSize uint32 = 2147483647 / 4
		if h1 > qSize+1 {
			t.Errorf("h1 out of quadrant 1: %d", h1)
		}
		if h2 < qSize || h2 > 2*qSize+1 {
			t.Errorf("h2 out of quadrant 2: %d", h2)
		}
		if h3 < 2*qSize || h3 > 3*qSize+1 {
			t.Errorf("h3 out of quadrant 3: %d", h3)
		}
		if h4 < 3*qSize {
			t.Errorf("h4 out of quadrant 4: %d", h4)
		}
	}
}

func TestGenerateAWGParams(t *testing.T) {
	profiles := []string{"lite", "standard", "pro"}

	for _, profile := range profiles {
		params, err := GenerateAWGParams(profile)
		if err != nil {
			t.Fatalf("GenerateAWGParams(%s) failed: %v", profile, err)
		}

		s1, _ := strconv.Atoi(params.InitPacketJunkSize)
		s2, _ := strconv.Atoi(params.ResponsePacketJunkSize)
		diff := s1 - s2
		if diff < 0 {
			diff = -diff
		}
		if diff < 10 {
			t.Errorf("profile %s: |S1 - S2| < 10 (S1=%d, S2=%d)", profile, s1, s2)
		}

		if profile == "pro" && params.MTU != "1320" {
			t.Errorf("pro profile expected MTU 1320, got %s", params.MTU)
		}
		if (profile == "lite" || profile == "standard") && params.MTU != "1280" {
			t.Errorf("%s profile expected MTU 1280, got %s", profile, params.MTU)
		}

		if err := ValidateAWGParams(params.ToMap()); err != nil {
			t.Errorf("ValidateAWGParams failed on generated %s params: %v", profile, err)
		}
	}
}

func TestValidateAWGParams_Errors(t *testing.T) {
	invalidNumeric := map[string]string{
		"junk_packet_count": "invalid",
	}
	if err := ValidateAWGParams(invalidNumeric); err == nil {
		t.Errorf("expected error for non-numeric param")
	}

	outOfBounds := map[string]string{
		"junk_packet_count": "500",
	}
	if err := ValidateAWGParams(outOfBounds); err == nil {
		t.Errorf("expected error for out of bounds param")
	}

	invalidCPS := map[string]string{
		"i1": "bad_cps_format",
	}
	if err := ValidateAWGParams(invalidCPS); err == nil {
		t.Errorf("expected error for invalid CPS format")
	}

	invalidMTU := map[string]string{
		"mtu": "999",
	}
	if err := ValidateAWGParams(invalidMTU); err == nil {
		t.Errorf("expected error for invalid MTU")
	}
}
