"""
Comprehensive tests for Pydantic request model validation.

Covers all 25 request models in app.py with Field() constraints and
field_validator decorators. Tests follow the VERIFICATION_PLAN.md spec.
"""

import pytest
from pydantic import ValidationError

from schemas import (
    VALID_PROTOCOLS,
    LoginRequest,
    AddServerRequest,
    InstallProtocolRequest,
    ProtocolRequest,
    AddConnectionRequest,
    EditConnectionRequest,
    ConnectionActionRequest,
    ToggleConnectionRequest,
    AddUserRequest,
    ServerConfigSaveRequest,
    AppearanceSettings,
    SyncSettings,
    CaptchaSettings,
    SSLSettings,
    ConnectionLimits,
    ProtocolPaths,
    UpdateUserRequest,
    SaveSettingsRequest,
    ToggleUserRequest,
    AddUserConnectionRequest,
    ChangePasswordRequest,
    ShareSetupRequest,
    ShareAuthRequest,
    MyAddConnectionRequest,
)

# ======================== VALID_PROTOCOLS ========================


class TestValidProtocols:
    """Test that VALID_PROTOCOLS constant is correct."""

    def test_valid_protocols_set(self):
        assert VALID_PROTOCOLS == {"awg", "awg2", "awg_legacy", "xray", "telemt", "dns"}


# ======================== LoginRequest ========================


class TestLoginRequest:
    """Tests for LoginRequest model."""

    def test_valid_login(self):
        req = LoginRequest(username="admin", password="secret")
        assert req.username == "admin"
        assert req.password == "secret"

    def test_optional_captcha(self):
        req = LoginRequest(username="admin", password="secret", captcha="abc123")
        assert req.captcha == "abc123"

    def test_empty_username_rejected(self):
        with pytest.raises(ValidationError):
            LoginRequest(username="", password="secret")

    def test_empty_password_rejected(self):
        with pytest.raises(ValidationError):
            LoginRequest(username="admin", password="")

    def test_too_long_username(self):
        with pytest.raises(ValidationError):
            LoginRequest(username="a" * 256, password="secret")

    def test_too_long_password(self):
        with pytest.raises(ValidationError):
            LoginRequest(username="admin", password="a" * 4097)


# ======================== AddServerRequest ========================


class TestAddServerRequest:
    """Tests for AddServerRequest model."""

    def test_valid_server_ip(self):
        req = AddServerRequest(host="1.2.3.4", ssh_port=22)
        assert req.host == "1.2.3.4"

    def test_valid_server_hostname(self):
        req = AddServerRequest(host="example.com")
        assert req.host == "example.com"

    def test_invalid_host_script(self):
        with pytest.raises(ValidationError):
            AddServerRequest(host="<script>")

    def test_invalid_host_empty_after_validation(self):
        """Empty host string is allowed (default)."""
        req = AddServerRequest(host="")
        assert req.host == ""

    def test_port_zero_rejected(self):
        with pytest.raises(ValidationError):
            AddServerRequest(host="1.2.3.4", ssh_port=0)

    def test_port_too_high(self):
        with pytest.raises(ValidationError):
            AddServerRequest(host="1.2.3.4", ssh_port=65536)

    def test_port_negative(self):
        with pytest.raises(ValidationError):
            AddServerRequest(host="1.2.3.4", ssh_port=-1)

    def test_valid_port(self):
        req = AddServerRequest(host="1.2.3.4", ssh_port=22)
        assert req.ssh_port == 22

    def test_empty_username_accepted(self):
        """AddServerRequest.username allows empty string (default for SSH user)."""
        req = AddServerRequest(username="", host="1.2.3.4")
        assert req.username == ""

    def test_too_long_username(self):
        with pytest.raises(ValidationError):
            AddServerRequest(username="a" * 256, host="1.2.3.4")

    def test_defaults(self):
        req = AddServerRequest()
        assert req.host == ""
        assert req.ssh_port == 22
        assert req.username == ""
        assert req.password == ""
        assert req.private_key == ""
        assert req.name == ""

    def test_too_long_name(self):
        with pytest.raises(ValidationError):
            AddServerRequest(name="a" * 256)


# ======================== InstallProtocolRequest ========================


class TestInstallProtocolRequest:
    """Tests for InstallProtocolRequest model — preserves existing tls_domain validator."""

    def test_valid_protocol(self):
        req = InstallProtocolRequest(protocol="awg")
        assert req.protocol == "awg"

    def test_all_valid_protocols(self):
        for proto in VALID_PROTOCOLS:
            req = InstallProtocolRequest(protocol=proto)
            assert req.protocol == proto

    def test_invalid_protocol(self):
        with pytest.raises(ValidationError):
            InstallProtocolRequest(protocol="evil")

    def test_empty_protocol_rejected(self):
        with pytest.raises(ValidationError):
            InstallProtocolRequest(protocol="")

    def test_tls_domain_valid(self):
        req = InstallProtocolRequest(tls_domain="example.com")
        assert req.tls_domain == "example.com"

    def test_tls_domain_none(self):
        req = InstallProtocolRequest()
        assert req.tls_domain is None

    def test_tls_domain_empty(self):
        req = InstallProtocolRequest(tls_domain="")
        assert req.tls_domain == ""

    def test_tls_domain_invalid(self):
        """Existing tls_domain validator from batch 1F must still work."""
        with pytest.raises(ValidationError):
            InstallProtocolRequest(tls_domain="<script>")

    def test_tls_domain_valid_complex(self):
        req = InstallProtocolRequest(tls_domain="sub.domain-example.com")
        assert req.tls_domain == "sub.domain-example.com"

    def test_max_connections_range(self):
        req = InstallProtocolRequest(max_connections=100)
        assert req.max_connections == 100

    def test_max_connections_zero_rejected(self):
        with pytest.raises(ValidationError):
            InstallProtocolRequest(max_connections=0)


# ======================== ProtocolRequest ========================


class TestProtocolRequest:
    """Tests for ProtocolRequest model."""

    def test_valid_protocol(self):
        req = ProtocolRequest(protocol="awg")
        assert req.protocol == "awg"

    def test_all_valid_protocols(self):
        for proto in VALID_PROTOCOLS:
            req = ProtocolRequest(protocol=proto)
            assert req.protocol == proto

    def test_invalid_protocol(self):
        with pytest.raises(ValidationError):
            ProtocolRequest(protocol="<script>")

    def test_sql_injection_protocol(self):
        with pytest.raises(ValidationError):
            ProtocolRequest(protocol="'; DROP TABLE")

    def test_empty_protocol_rejected(self):
        with pytest.raises(ValidationError):
            ProtocolRequest(protocol="")


# ======================== AddConnectionRequest ========================


class TestAddConnectionRequest:
    """Tests for AddConnectionRequest model."""

    def test_valid_connection(self):
        req = AddConnectionRequest(protocol="awg", name="Test")
        assert req.protocol == "awg"
        assert req.name == "Test"

    def test_invalid_protocol(self):
        with pytest.raises(ValidationError):
            AddConnectionRequest(protocol="<script>")

    def test_too_long_name(self):
        with pytest.raises(ValidationError):
            AddConnectionRequest(name="a" * 256)

    def test_defaults(self):
        req = AddConnectionRequest()
        assert req.protocol == "awg"
        assert req.name == "Connection"


# ======================== EditConnectionRequest ========================


class TestEditConnectionRequest:
    """Tests for EditConnectionRequest model."""

    def test_valid_edit(self):
        req = EditConnectionRequest(protocol="telemt", client_id="abc")
        assert req.protocol == "telemt"
        assert req.client_id == "abc"

    def test_invalid_protocol(self):
        with pytest.raises(ValidationError):
            EditConnectionRequest(protocol="<script>")

    def test_defaults(self):
        req = EditConnectionRequest()
        assert req.protocol == "telemt"
        assert req.client_id == ""


# ======================== ConnectionActionRequest ========================


class TestConnectionActionRequest:
    """Tests for ConnectionActionRequest model."""

    def test_valid_action(self):
        req = ConnectionActionRequest(protocol="awg", client_id="abc")
        assert req.protocol == "awg"

    def test_invalid_protocol(self):
        with pytest.raises(ValidationError):
            ConnectionActionRequest(protocol="'; DROP TABLE")


# ======================== ToggleConnectionRequest ========================


class TestToggleConnectionRequest:
    """Tests for ToggleConnectionRequest model."""

    def test_valid_toggle(self):
        req = ToggleConnectionRequest(protocol="awg", client_id="abc")
        assert req.protocol == "awg"
        assert req.enable is True

    def test_invalid_protocol(self):
        with pytest.raises(ValidationError):
            ToggleConnectionRequest(protocol="'; DROP TABLE")


# ======================== AddUserRequest ========================


class TestAddUserRequest:
    """Tests for AddUserRequest model — the most validation-heavy model."""

    def test_valid_user(self):
        req = AddUserRequest(username="testuser", password="Pass1234", role="admin")
        assert req.username == "testuser"
        assert req.password == "Pass1234"
        assert req.role == "admin"

    def test_username_lowercased(self):
        """Username is normalized to lowercase."""
        req = AddUserRequest(username="TestUser", password="Pass1234", role="user")
        assert req.username == "testuser"

    def test_empty_username_rejected(self):
        with pytest.raises(ValidationError):
            AddUserRequest(username="", password="Pass1234")

    def test_too_short_username_rejected(self):
        with pytest.raises(ValidationError):
            AddUserRequest(username="ab", password="Pass1234")

    def test_too_long_username(self):
        with pytest.raises(ValidationError):
            AddUserRequest(username="a" * 256, password="Pass1234")

    def test_username_special_chars_rejected(self):
        with pytest.raises(ValidationError):
            AddUserRequest(username="user<script>", password="Pass1234")

    def test_username_spaces_rejected(self):
        with pytest.raises(ValidationError):
            AddUserRequest(username="test user", password="Pass1234")

    def test_username_hyphens_underscores_accepted(self):
        req = AddUserRequest(username="test_user-1", password="Pass1234")
        assert req.username == "test_user-1"

    def test_empty_password_rejected(self):
        with pytest.raises(ValidationError):
            AddUserRequest(username="testuser", password="")

    def test_short_password_rejected(self):
        """Password must be at least 8 chars."""
        with pytest.raises(ValidationError):
            AddUserRequest(username="testuser", password="Pass12")

    def test_password_no_uppercase(self):
        with pytest.raises(ValidationError):
            AddUserRequest(username="testuser", password="pass1234")

    def test_password_no_lowercase(self):
        with pytest.raises(ValidationError):
            AddUserRequest(username="testuser", password="PASS1234")

    def test_password_no_digit(self):
        with pytest.raises(ValidationError):
            AddUserRequest(username="testuser", password="Passwordsss")

    def test_invalid_role(self):
        with pytest.raises(ValidationError):
            AddUserRequest(username="testuser", password="Pass1234", role="superadmin")

    def test_role_admin(self):
        req = AddUserRequest(username="testuser", password="Pass1234", role="admin")
        assert req.role == "admin"

    def test_role_user(self):
        req = AddUserRequest(username="testuser", password="Pass1234", role="user")
        assert req.role == "user"

    def test_optional_fields(self):
        req = AddUserRequest(
            username="testuser",
            password="Pass1234",
            telegramId="12345",
            email="test@test.com",
            description="A test user",
            protocol="awg",
        )
        assert req.telegramId == "12345"
        assert req.email == "test@test.com"
        assert req.protocol == "awg"

    def test_invalid_protocol_in_optional(self):
        with pytest.raises(ValidationError):
            AddUserRequest(
                username="testuser",
                password="Pass1234",
                protocol="evil",
            )


# ======================== ServerConfigSaveRequest ========================


class TestServerConfigSaveRequest:
    """Tests for ServerConfigSaveRequest model."""

    def test_valid_config(self):
        req = ServerConfigSaveRequest(protocol="awg", config="some config data")
        assert req.protocol == "awg"

    def test_empty_protocol_rejected(self):
        with pytest.raises(ValidationError):
            ServerConfigSaveRequest(protocol="", config="data")

    def test_invalid_protocol(self):
        with pytest.raises(ValidationError):
            ServerConfigSaveRequest(protocol="evil", config="data")

    def test_xss_in_config_accepted_but_bounded(self):
        """Config data is accepted (not rendered as HTML on server), but length bounded."""
        config_val = "<script>alert(1)</script>"
        req = ServerConfigSaveRequest(protocol="awg", config=config_val)
        assert req.config == config_val

    def test_too_long_config(self):
        with pytest.raises(ValidationError):
            ServerConfigSaveRequest(protocol="awg", config="a" * 65537)


# ======================== AppearanceSettings ========================


class TestAppearanceSettings:
    """Tests for AppearanceSettings model."""

    def test_defaults(self):
        req = AppearanceSettings()
        assert req.title == "Amnezia"
        assert req.logo == "\U0001f6e1"
        assert req.subtitle == "Web Panel"

    def test_valid_custom(self):
        req = AppearanceSettings(title="My Panel", logo="\u2605", subtitle="Test")
        assert req.title == "My Panel"

    def test_empty_title_rejected(self):
        with pytest.raises(ValidationError):
            AppearanceSettings(title="")

    def test_too_long_title(self):
        with pytest.raises(ValidationError):
            AppearanceSettings(title="a" * 101)


# ======================== SyncSettings ========================


class TestSyncSettings:
    """Tests for SyncSettings model."""

    def test_defaults(self):
        req = SyncSettings()
        assert req.remnawave_url == ""
        assert req.remnawave_protocol == "awg"

    def test_valid_url(self):
        req = SyncSettings(remnawave_url="https://api.example.com/v1")
        assert req.remnawave_url == "https://api.example.com/v1"

    def test_invalid_url(self):
        with pytest.raises(ValidationError):
            SyncSettings(remnawave_url="not-a-url")

    def test_invalid_protocol(self):
        with pytest.raises(ValidationError):
            SyncSettings(remnawave_protocol="evil")

    def test_empty_url_accepted(self):
        req = SyncSettings(remnawave_url="")
        assert req.remnawave_url == ""


# ======================== CaptchaSettings ========================


class TestCaptchaSettings:
    """Tests for CaptchaSettings — just a bool, minimal."""

    def test_defaults(self):
        req = CaptchaSettings()
        assert req.enabled is False

    def test_enabled(self):
        req = CaptchaSettings(enabled=True)
        assert req.enabled is True


# ======================== SSLSettings ========================


class TestSSLSettings:
    """Tests for SSLSettings model."""

    def test_defaults(self):
        req = SSLSettings()
        assert req.domain == ""
        assert req.panel_port == 5000

    def test_valid_domain(self):
        req = SSLSettings(domain="example.com")
        assert req.domain == "example.com"

    def test_invalid_domain_script(self):
        with pytest.raises(ValidationError):
            SSLSettings(domain="<script>")

    def test_path_traversal_rejected(self):
        with pytest.raises(ValidationError):
            SSLSettings(cert_path="/etc/../etc/passwd")

    def test_key_path_traversal_rejected(self):
        with pytest.raises(ValidationError):
            SSLSettings(key_path="/../secret")

    def test_path_without_traversal_accepted(self):
        req = SSLSettings(cert_path="/etc/ssl/certs/cert.pem")
        assert req.cert_path == "/etc/ssl/certs/cert.pem"

    def test_port_range_valid(self):
        req = SSLSettings(panel_port=443)
        assert req.panel_port == 443

    def test_port_zero_rejected(self):
        with pytest.raises(ValidationError):
            SSLSettings(panel_port=0)

    def test_port_too_high(self):
        with pytest.raises(ValidationError):
            SSLSettings(panel_port=65536)


# ======================== ConnectionLimits ========================


class TestConnectionLimits:
    """Tests for ConnectionLimits model."""

    def test_defaults(self):
        req = ConnectionLimits()
        assert req.max_connections_per_user == 10
        assert req.connection_rate_limit_count == 5
        assert req.connection_rate_limit_window == 60

    def test_valid_limits(self):
        req = ConnectionLimits(
            max_connections_per_user=100,
            connection_rate_limit_count=10,
            connection_rate_limit_window=3600,
        )
        assert req.max_connections_per_user == 100

    def test_zero_connections_rejected(self):
        with pytest.raises(ValidationError):
            ConnectionLimits(max_connections_per_user=0)

    def test_too_high_connections_rejected(self):
        with pytest.raises(ValidationError):
            ConnectionLimits(max_connections_per_user=1001)

    def test_zero_window_rejected(self):
        with pytest.raises(ValidationError):
            ConnectionLimits(connection_rate_limit_window=0)

    def test_window_too_high(self):
        with pytest.raises(ValidationError):
            ConnectionLimits(connection_rate_limit_window=86401)


# ======================== ProtocolPaths ========================


class TestProtocolPaths:
    """Tests for ProtocolPaths model."""

    def test_defaults(self):
        req = ProtocolPaths()
        assert req.telemt_config_dir == "/opt/amnezia/telemt"

    def test_valid_path(self):
        req = ProtocolPaths(telemt_config_dir="/etc/telemt")
        assert req.telemt_config_dir == "/etc/telemt"

    def test_path_traversal_rejected(self):
        with pytest.raises(ValidationError):
            ProtocolPaths(telemt_config_dir="/opt/../etc/passwd")

    def test_empty_path_rejected(self):
        with pytest.raises(ValidationError):
            ProtocolPaths(telemt_config_dir="")


# ======================== UpdateUserRequest ========================


class TestUpdateUserRequest:
    """Tests for UpdateUserRequest model."""

    def test_empty_update(self):
        req = UpdateUserRequest()
        assert req.telegramId is None
        assert req.password is None

    def test_valid_update(self):
        req = UpdateUserRequest(email="test@test.com", description="Updated")
        assert req.email == "test@test.com"

    def test_password_complexity(self):
        """Password must meet complexity requirements if provided."""
        req = UpdateUserRequest(password="NewPass123")
        assert req.password == "NewPass123"

    def test_weak_password_rejected(self):
        with pytest.raises(ValidationError):
            UpdateUserRequest(password="abc")

    def test_password_no_uppercase(self):
        with pytest.raises(ValidationError):
            UpdateUserRequest(password="newpass123")

    def test_password_none_accepted(self):
        req = UpdateUserRequest(password=None)
        assert req.password is None


# ======================== SaveSettingsRequest ========================


class TestSaveSettingsRequest:
    """Tests for SaveSettingsRequest — composed model, no direct changes."""

    def test_defaults(self):
        req = SaveSettingsRequest(
            appearance=AppearanceSettings(),
            sync=SyncSettings(),
            captcha=CaptchaSettings(),
            telegram={},
            ssl=SSLSettings(),
        )
        assert req.appearance.title == "Amnezia"

    def test_with_invalid_sub_model(self):
        with pytest.raises(ValidationError):
            SaveSettingsRequest(
                appearance=AppearanceSettings(title=""),
                sync=SyncSettings(),
                captcha=CaptchaSettings(),
                telegram={},
                ssl=SSLSettings(),
            )


# ======================== ToggleUserRequest ========================


class TestToggleUserRequest:
    """Tests for ToggleUserRequest — just a bool, minimal."""

    def test_toggle(self):
        req = ToggleUserRequest(enabled=True)
        assert req.enabled is True

    def test_toggle_false(self):
        req = ToggleUserRequest(enabled=False)
        assert req.enabled is False


# ======================== AddUserConnectionRequest ========================


class TestAddUserConnectionRequest:
    """Tests for AddUserConnectionRequest model."""

    def test_valid_connection(self):
        req = AddUserConnectionRequest(server_id=1, protocol="awg")
        assert req.server_id == 1
        assert req.protocol == "awg"

    def test_invalid_protocol(self):
        with pytest.raises(ValidationError):
            AddUserConnectionRequest(server_id=1, protocol="<script>")

    def test_invalid_server_id_zero(self):
        with pytest.raises(ValidationError):
            AddUserConnectionRequest(server_id=0)

    def test_invalid_server_id_negative(self):
        with pytest.raises(ValidationError):
            AddUserConnectionRequest(server_id=-1)


# ======================== ChangePasswordRequest ========================


class TestChangePasswordRequest:
    """Tests for ChangePasswordRequest model."""

    def test_valid_change(self):
        req = ChangePasswordRequest(
            current_password="oldpassword",
            new_password="NewPass123",
            confirm_password="NewPass123",
        )
        assert req.new_password == "NewPass123"

    def test_weak_new_password(self):
        with pytest.raises(ValidationError):
            ChangePasswordRequest(
                current_password="old",
                new_password="abc",
                confirm_password="abc",
            )

    def test_password_no_uppercase(self):
        with pytest.raises(ValidationError):
            ChangePasswordRequest(
                current_password="old",
                new_password="pass1234",
                confirm_password="pass1234",
            )

    def test_password_no_lowercase(self):
        with pytest.raises(ValidationError):
            ChangePasswordRequest(
                current_password="old",
                new_password="PASS1234",
                confirm_password="PASS1234",
            )

    def test_password_no_digit(self):
        with pytest.raises(ValidationError):
            ChangePasswordRequest(
                current_password="old",
                new_password="Passwordsss",
                confirm_password="Passwordsss",
            )

    def test_password_with_null_byte(self):
        with pytest.raises(ValidationError):
            ChangePasswordRequest(
                current_password="old",
                new_password="Pass1234\x00",
                confirm_password="Pass1234\x00",
            )

    def test_empty_current_password(self):
        with pytest.raises(ValidationError):
            ChangePasswordRequest(
                current_password="",
                new_password="NewPass123",
                confirm_password="NewPass123",
            )


# ======================== ShareSetupRequest ========================


class TestShareSetupRequest:
    """Tests for ShareSetupRequest model."""

    def test_valid_setup(self):
        req = ShareSetupRequest(enabled=True, password="secretpassword")
        assert req.enabled is True
        assert req.password == "secretpassword"

    def test_no_password(self):
        req = ShareSetupRequest(enabled=True)
        assert req.password is None

    def test_too_long_password(self):
        with pytest.raises(ValidationError):
            ShareSetupRequest(enabled=True, password="a" * 4097)


# ======================== ShareAuthRequest ========================


class TestShareAuthRequest:
    """Tests for ShareAuthRequest model."""

    def test_valid_auth(self):
        req = ShareAuthRequest(password="secretpassword")
        assert req.password == "secretpassword"

    def test_empty_password_rejected(self):
        with pytest.raises(ValidationError):
            ShareAuthRequest(password="")

    def test_too_long_password(self):
        with pytest.raises(ValidationError):
            ShareAuthRequest(password="a" * 4097)


# ======================== MyAddConnectionRequest ========================


class TestMyAddConnectionRequest:
    """Tests for MyAddConnectionRequest model."""

    def test_valid_connection(self):
        req = MyAddConnectionRequest(server_id=1, protocol="awg")
        assert req.server_id == 1
        assert req.protocol == "awg"

    def test_invalid_protocol(self):
        with pytest.raises(ValidationError):
            MyAddConnectionRequest(server_id=1, protocol="<script>")

    def test_invalid_server_id(self):
        with pytest.raises(ValidationError):
            MyAddConnectionRequest(server_id=0)

    def test_defaults(self):
        req = MyAddConnectionRequest(server_id=1)
        assert req.name == "Connection"
        assert req.protocol == "awg"


# ======================== Cross-cutting Security Tests ========================


class TestCrossCuttingSecurity:
    """Cross-cutting security validation tests."""

    def test_unicode_in_username_rejected(self):
        """Non-ASCII chars in username should be rejected."""
        with pytest.raises(ValidationError):
            AddUserRequest(username="\u7528\u6237", password="Pass1234")

    def test_sql_injection_in_username(self):
        """SQL injection attempt in username should be rejected."""
        with pytest.raises(ValidationError):
            AddUserRequest(username="'; DROP TABLE users;--", password="Pass1234")

    def test_xss_payload_in_name(self):
        """XSS payload in connection name should be rejected (max_length)."""
        # Names allow most chars, but the max_length bounds them
        with pytest.raises(ValidationError):
            AddConnectionRequest(name="<img src=x onerror=alert(1)>" * 50)

    def test_extremely_long_string_dos(self):
        """Extremely long strings (DoS vector) should be rejected."""
        with pytest.raises(ValidationError):
            LoginRequest(username="a" * 10000, password="a")

    def test_null_byte_in_username(self):
        """Null bytes should be rejected by the alphanumeric pattern."""
        with pytest.raises(ValidationError):
            AddUserRequest(username="admin\x00", password="Pass1234")

    def test_null_byte_in_connection_name(self):
        """Null bytes in connection name are bounded by max_length."""
        # Even if it fits in max_length, it's suspicious; Field handles length
        req = AddConnectionRequest(name="test\x00conn")
        # name field doesn't have a pattern validator, so null bytes pass
        # but max_length still bounds the string
        assert req.name == "test\x00conn"

    def test_protocol_allowlist_enforced(self):
        """All protocol fields must be in VALID_PROTOCOLS."""
        for proto in ["awg", "awg2", "awg_legacy", "xray", "telemt", "dns"]:
            req = ProtocolRequest(protocol=proto)
            assert req.protocol == proto

    def test_protocol_rejects_random_string(self):
        with pytest.raises(ValidationError):
            ProtocolRequest(protocol="random_protocol")
