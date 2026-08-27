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
		{"xray", "xray", false},
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
	if AWGProfileLite != "lite" || AWGProfileStandard != "standard" || AWGProfilePro != "pro" {
		t.Errorf("unexpected AWGProfile enums")
	}
	if AWGMimicryAuto != "auto" || AWGMimicryTLS != "tls" || AWGMimicryDNS != "dns" || AWGMimicrySIP != "sip" || AWGMimicryQUIC != "quic" {
		t.Errorf("unexpected AWGMimicry enums")
	}
	if RoleAdmin != "admin" || RoleSupport != "support" || RoleUser != "user" {
		t.Errorf("unexpected role enums: %s, %s, %s", RoleAdmin, RoleSupport, RoleUser)
	}
	if ResetStrategyNever != "never" || ResetStrategyMonthly != "monthly" || ResetStrategyDaily != "daily" {
		t.Errorf("unexpected ResetStrategy enums")
	}
	if ReachabilityOnline != "online" || ReachabilityOffline != "offline" || ReachabilityUnknown != "unknown" {
		t.Errorf("unexpected ReachabilityStatus enums")
	}
	if LBLeastConnections != "least_conn" || LBWeighted != "weighted" || LBRoundRobin != "round_robin" {
		t.Errorf("unexpected LB enums")
	}
}

func TestUserRoleAndPrivileges(t *testing.T) {
	if !RoleAdmin.IsAdminOrSupport() {
		t.Errorf("expected RoleAdmin.IsAdminOrSupport() to be true")
	}
	if !RoleSupport.IsAdminOrSupport() {
		t.Errorf("expected RoleSupport.IsAdminOrSupport() to be true")
	}
	if RoleUser.IsAdminOrSupport() {
		t.Errorf("expected RoleUser.IsAdminOrSupport() to be false")
	}
	if UserRole("guest").IsAdminOrSupport() {
		t.Errorf("expected UserRole(guest).IsAdminOrSupport() to be false")
	}

	if !ValidateRole(RoleAdmin) || !ValidateRole(RoleSupport) || !ValidateRole(RoleUser) {
		t.Errorf("ValidateRole failed for valid roles")
	}
	if ValidateRole(UserRole("invalid_role")) {
		t.Errorf("ValidateRole succeeded for invalid role")
	}
	if !IsValidRole("admin") || !IsValidRole("support") || !IsValidRole("user") {
		t.Errorf("IsValidRole failed for valid role strings")
	}
	if IsValidRole("root") || IsValidRole("") {
		t.Errorf("IsValidRole succeeded for invalid role strings")
	}

	var nilUser *User
	if nilUser.IsAdmin() {
		t.Errorf("nil user should not be admin")
	}

	adminUser := &User{Role: RoleAdmin}
	if !adminUser.IsAdmin() {
		t.Errorf("admin user should be admin")
	}

	supportUser := &User{Role: RoleSupport}
	if !supportUser.IsAdmin() {
		t.Errorf("support user should be admin")
	}

	regularUser := &User{Role: RoleUser}
	if regularUser.IsAdmin() {
		t.Errorf("regular user should not be admin")
	}
}

func TestLoginRequestValidation(t *testing.T) {
	tests := []struct {
		name    string
		req     LoginRequest
		wantErr bool
	}{
		{"valid login", LoginRequest{Username: "admin", Password: "Password123!"}, false},
		{"empty username", LoginRequest{Username: "", Password: "Password123!"}, true},
		{"whitespace username", LoginRequest{Username: "   ", Password: "Password123!"}, true},
		{"empty password", LoginRequest{Username: "admin", Password: ""}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.req.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("LoginRequest.Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestSetupRequestValidation(t *testing.T) {
	tests := []struct {
		name    string
		req     SetupRequest
		wantErr bool
	}{
		{"valid setup", SetupRequest{Username: "admin_user", Password: "SecurePassword1!", ConfirmPassword: "SecurePassword1!"}, false},
		{"short username", SetupRequest{Username: "ad", Password: "SecurePassword1!", ConfirmPassword: "SecurePassword1!"}, true},
		{"invalid username chars", SetupRequest{Username: "admin@user", Password: "SecurePassword1!", ConfirmPassword: "SecurePassword1!"}, true},
		{"short password", SetupRequest{Username: "admin_user", Password: "Ab1", ConfirmPassword: "Ab1"}, true},
		{"password mismatch", SetupRequest{Username: "admin_user", Password: "SecurePassword1!", ConfirmPassword: "SecurePassword2!"}, true},
		{"missing uppercase", SetupRequest{Username: "admin_user", Password: "password123!", ConfirmPassword: "password123!"}, true},
		{"missing lowercase", SetupRequest{Username: "admin_user", Password: "PASSWORD123!", ConfirmPassword: "PASSWORD123!"}, true},
		{"missing digit", SetupRequest{Username: "admin_user", Password: "PasswordSecure!", ConfirmPassword: "PasswordSecure!"}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.req.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("SetupRequest.Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestChangePasswordRequestValidation(t *testing.T) {
	tests := []struct {
		name    string
		req     ChangePasswordRequest
		wantErr bool
	}{
		{"valid change", ChangePasswordRequest{CurrentPassword: "OldPassword1!", NewPassword: "NewSecurePassword2!", ConfirmPassword: "NewSecurePassword2!"}, false},
		{"null byte in password", ChangePasswordRequest{CurrentPassword: "OldPassword1!", NewPassword: "New\x00Password2!", ConfirmPassword: "New\x00Password2!"}, true},
		{"short password", ChangePasswordRequest{CurrentPassword: "OldPassword1!", NewPassword: "Short1!", ConfirmPassword: "Short1!"}, true},
		{"password mismatch", ChangePasswordRequest{CurrentPassword: "OldPassword1!", NewPassword: "NewSecurePassword2!", ConfirmPassword: "DifferentPassword2!"}, true},
		{"no digit in new password", ChangePasswordRequest{CurrentPassword: "OldPassword1!", NewPassword: "NewSecurePassword!", ConfirmPassword: "NewSecurePassword!"}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.req.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("ChangePasswordRequest.Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestAddServerRequestValidation(t *testing.T) {
	reqDefaultPort := AddServerRequest{Host: "192.0.2.1", SSHPort: 0}
	if err := reqDefaultPort.Validate(); err != nil || reqDefaultPort.SSHPort != 22 {
		t.Errorf("expected SSHPort to default to 22, got %d, err: %v", reqDefaultPort.SSHPort, err)
	}

	reqInvalidPort := AddServerRequest{Host: "192.0.2.1", SSHPort: 70000}
	if err := reqInvalidPort.Validate(); err == nil {
		t.Errorf("expected error for invalid SSHPort 70000")
	}

	reqValidHost := AddServerRequest{Host: "vpn.example.com", SSHPort: 2222}
	if err := reqValidHost.Validate(); err != nil {
		t.Errorf("unexpected error for valid host: %v", err)
	}

	reqInvalidHost := AddServerRequest{Host: "invalid host with spaces!", SSHPort: 22}
	if err := reqInvalidHost.Validate(); err == nil {
		t.Errorf("expected error for invalid host")
	}
}

func TestInstallProtocolRequestValidation(t *testing.T) {
	domain := "tls.example.com"
	req := InstallProtocolRequest{
		Protocol:  "awg2",
		Port:      "51820",
		TLSDomain: &domain,
	}
	if err := req.Validate(); err != nil || req.Protocol != "awg" {
		t.Errorf("expected protocol normalized to awg, got %s, err: %v", req.Protocol, err)
	}

	invalidProto := InstallProtocolRequest{Protocol: "unknown", Port: "443"}
	if err := invalidProto.Validate(); err == nil {
		t.Errorf("expected error for unknown protocol")
	}

	invalidPort := InstallProtocolRequest{Protocol: "awg", Port: "invalid_port"}
	if err := invalidPort.Validate(); err == nil {
		t.Errorf("expected error for non-integer port")
	}

	invalidDomainStr := "bad domain with spaces!"
	invalidDomain := InstallProtocolRequest{Protocol: "awg", Port: "443", TLSDomain: &invalidDomainStr}
	if err := invalidDomain.Validate(); err == nil {
		t.Errorf("expected error for invalid TLS domain")
	}
}

func TestAddUserRequestValidation(t *testing.T) {
	validAWG := "awg"
	req := AddUserRequest{
		Username: "Test_User-1",
		Password: "Password123!",
		Role:     RoleSupport,
		Protocol: &validAWG,
	}

	if err := req.Validate(); err != nil {
		t.Fatalf("unexpected error validating AddUserRequest: %v", err)
	}
	if req.Username != "test_user-1" {
		t.Errorf("expected username normalized to lowercase, got %s", req.Username)
	}

	// Invalid role
	reqInvalidRole := AddUserRequest{
		Username: "valid_user",
		Password: "Password123!",
		Role:     UserRole("superuser"),
	}
	if err := reqInvalidRole.Validate(); err == nil {
		t.Errorf("expected error for invalid user role")
	}

	// Invalid username characters
	reqInvalidUsername := AddUserRequest{
		Username: "user@domain.com",
		Password: "Password123!",
		Role:     RoleUser,
	}
	if err := reqInvalidUsername.Validate(); err == nil {
		t.Errorf("expected error for invalid username characters")
	}

	// Invalid protocol
	invalidProto := "unknown_proto"
	reqInvalidProto := AddUserRequest{
		Username: "user_valid",
		Password: "Password123!",
		Role:     RoleUser,
		Protocol: &invalidProto,
	}
	if err := reqInvalidProto.Validate(); err == nil {
		t.Errorf("expected error for unknown protocol in AddUserRequest")
	}
}

func TestValidationHelpers(t *testing.T) {
	if err := ValidateHost("127.0.0.1"); err != nil {
		t.Errorf("ValidateHost failed for 127.0.0.1: %v", err)
	}
	if err := ValidateHost("::1"); err != nil {
		t.Errorf("ValidateHost failed for IPv6 ::1: %v", err)
	}
	if err := ValidateHost("example.com"); err != nil {
		t.Errorf("ValidateHost failed for example.com: %v", err)
	}
	if err := ValidateHost("invalid host $$"); err == nil {
		t.Errorf("ValidateHost succeeded for invalid host")
	}

	if err := ValidateTLSDomain("example.com"); err != nil {
		t.Errorf("ValidateTLSDomain failed for example.com: %v", err)
	}
	if err := ValidateTLSDomain("sub.domain-1.example.org"); err != nil {
		t.Errorf("ValidateTLSDomain failed for sub.domain-1.example.org: %v", err)
	}
	if err := ValidateTLSDomain("bad domain!"); err == nil {
		t.Errorf("ValidateTLSDomain succeeded for invalid domain")
	}
}

func TestSessionDataMethods(t *testing.T) {
	var nilSession *SessionData
	if nilSession.IsAuthenticated() {
		t.Errorf("nil session should not be authenticated")
	}
	if nilSession.IsAdmin() {
		t.Errorf("nil session should not be admin")
	}
	if nilSession.IsAdminOrSupport() {
		t.Errorf("nil session should not be admin or support")
	}
	if nilSession.ToMap() != nil {
		t.Errorf("nil session ToMap should return nil")
	}
	if SessionDataFromMap(nil) != nil {
		t.Errorf("SessionDataFromMap(nil) should return nil")
	}

	userSession := &SessionData{
		UserID:                 "user-1",
		Username:               "john",
		Role:                   RoleUser,
		PasswordChangeRequired: true,
		CaptchaAnswer:          "ABCD",
		ShareAuthenticated:     map[string]bool{"token1": true},
		Extra:                  map[string]any{"custom": "val"},
	}

	if !userSession.IsAuthenticated() {
		t.Errorf("userSession should be authenticated")
	}
	if userSession.IsAdmin() {
		t.Errorf("userSession should not be admin")
	}
	if userSession.IsAdminOrSupport() {
		t.Errorf("userSession should not be admin or support")
	}

	m := userSession.ToMap()
	if m["user_id"] != "user-1" || m["role"] != "user" || m["captcha_answer"] != "ABCD" {
		t.Errorf("ToMap serialized incorrectly: %+v", m)
	}

	parsed := SessionDataFromMap(m)
	if parsed.UserID != "user-1" || parsed.Role != RoleUser || !parsed.PasswordChangeRequired || parsed.CaptchaAnswer != "ABCD" {
		t.Errorf("SessionDataFromMap parsed incorrectly: %+v", parsed)
	}
	if !parsed.ShareAuthenticated["token1"] {
		t.Errorf("ShareAuthenticated not restored: %+v", parsed.ShareAuthenticated)
	}

	adminSession := &SessionData{
		UserID: "admin-1",
		Role:   RoleAdmin,
	}
	if !adminSession.IsAdmin() || !adminSession.IsAdminOrSupport() {
		t.Errorf("adminSession should be admin and admin_or_support")
	}

	supportSession := &SessionData{
		UserID: "support-1",
		Role:   RoleSupport,
	}
	if supportSession.IsAdmin() || !supportSession.IsAdminOrSupport() {
		t.Errorf("supportSession should be admin_or_support but not admin")
	}
}

func TestAdditionalRequestModelsValidation(t *testing.T) {
	// ProtocolRequest
	prValid := ProtocolRequest{Protocol: "awg"}
	if err := prValid.Validate(); err != nil {
		t.Errorf("ProtocolRequest.Validate failed: %v", err)
	}
	prInvalid := ProtocolRequest{Protocol: "invalid_proto"}
	if err := prInvalid.Validate(); err == nil {
		t.Errorf("ProtocolRequest.Validate should fail for invalid proto")
	}

	// ServerConfigSaveRequest
	scValid := ServerConfigSaveRequest{Protocol: "awg", Config: "[Interface]\nPrivateKey = ..."}
	if err := scValid.Validate(); err != nil {
		t.Errorf("ServerConfigSaveRequest.Validate failed: %v", err)
	}
	scInvalidProto := ServerConfigSaveRequest{Protocol: "unknown", Config: "valid"}
	if err := scInvalidProto.Validate(); err == nil {
		t.Errorf("ServerConfigSaveRequest.Validate should fail for invalid proto")
	}
	scEmptyConfig := ServerConfigSaveRequest{Protocol: "awg", Config: ""}
	if err := scEmptyConfig.Validate(); err == nil {
		t.Errorf("ServerConfigSaveRequest.Validate should fail for empty config")
	}

	// AddConnectionRequest
	acValid := AddConnectionRequest{Protocol: "awg", Name: "my-conn"}
	if err := acValid.Validate(); err != nil {
		t.Errorf("AddConnectionRequest.Validate failed: %v", err)
	}
	acInvalidName := AddConnectionRequest{Protocol: "awg", Name: "   "}
	if err := acInvalidName.Validate(); err == nil {
		t.Errorf("AddConnectionRequest.Validate should fail for empty name")
	}

	// MyAddConnectionRequest
	myAcValid := MyAddConnectionRequest{ServerID: 1, Protocol: "awg", Name: "conn-1"}
	if err := myAcValid.Validate(); err != nil {
		t.Errorf("MyAddConnectionRequest.Validate failed: %v", err)
	}
	myAcInvalidID := MyAddConnectionRequest{ServerID: 0, Protocol: "awg", Name: "conn-1"}
	if err := myAcInvalidID.Validate(); err == nil {
		t.Errorf("MyAddConnectionRequest.Validate should fail for server_id <= 0")
	}

	// AddUserConnectionRequest
	aucValid := AddUserConnectionRequest{ServerID: 1, Protocol: "awg", Name: "conn-1"}
	if err := aucValid.Validate(); err != nil {
		t.Errorf("AddUserConnectionRequest.Validate failed: %v", err)
	}
	aucInvalidID := AddUserConnectionRequest{ServerID: -1, Protocol: "awg", Name: "conn-1"}
	if err := aucInvalidID.Validate(); err == nil {
		t.Errorf("AddUserConnectionRequest.Validate should fail for server_id <= 0")
	}

	// RenameConnectionRequest
	rcValid := RenameConnectionRequest{Name: "new-name"}
	if err := rcValid.Validate(); err != nil {
		t.Errorf("RenameConnectionRequest.Validate failed: %v", err)
	}
	rcNullByte := RenameConnectionRequest{Name: "new\x00name"}
	if err := rcNullByte.Validate(); err == nil {
		t.Errorf("RenameConnectionRequest.Validate should fail for null bytes")
	}

	// UpdateUserRequest
	validPass := "NewPassword123!"
	uuValid := UpdateUserRequest{Password: &validPass}
	if err := uuValid.Validate(); err != nil {
		t.Errorf("UpdateUserRequest.Validate failed: %v", err)
	}
	shortPass := "short"
	uuShort := UpdateUserRequest{Password: &shortPass}
	if err := uuShort.Validate(); err == nil {
		t.Errorf("UpdateUserRequest.Validate should fail for short password")
	}

	// ConnectionActionRequest
	caValid := ConnectionActionRequest{ClientID: "c1", Protocol: "awg"}
	if err := caValid.Validate(); err != nil {
		t.Errorf("ConnectionActionRequest.Validate failed: %v", err)
	}
	caEmptyClient := ConnectionActionRequest{ClientID: "", Protocol: "awg"}
	if err := caEmptyClient.Validate(); err == nil {
		t.Errorf("ConnectionActionRequest.Validate should fail for empty client_id")
	}
}
