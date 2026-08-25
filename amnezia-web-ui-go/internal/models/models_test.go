package models

import (
	"testing"
)

func TestProtocolValidationAndNormalization(t *testing.T) {
	tests := []struct {
		input     string
		wantNorm  string
		wantValid bool
	}{
		{"awg", "awg", true},
		{"xray", "xray", true},
		{"telemt", "telemt", true},
		{"dns", "dns", true},
		{"awg2", "awg", true},
		{"awg_legacy", "awg", true},
		{"unknown_proto", "unknown_proto", false},
		{"", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			gotNorm := NormalizeProtocol(tt.input)
			if gotNorm != tt.wantNorm {
				t.Errorf("NormalizeProtocol(%q) = %q, want %q", tt.input, gotNorm, tt.wantNorm)
			}
			gotValid := IsValidProtocol(tt.input)
			if gotValid != tt.wantValid {
				t.Errorf("IsValidProtocol(%q) = %v, want %v", tt.input, gotValid, tt.wantValid)
			}
		})
	}
}

func TestEnums(t *testing.T) {
	if AWGProfileStandard != "standard" {
		t.Errorf("unexpected AWGProfileStandard: %s", AWGProfileStandard)
	}
	if RoleAdmin != "admin" || RoleUser != "user" {
		t.Errorf("unexpected role enums: %s, %s", RoleAdmin, RoleUser)
	}
	if LBLeastConnections != "least_conn" || LBWeighted != "weighted" || LBRoundRobin != "round_robin" {
		t.Errorf("unexpected LB enums")
	}
}
