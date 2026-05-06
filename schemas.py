"""Pydantic models for the Amnezia Web Panel API.

All request/response models are centralized here for:
- Clean API documentation (OpenAPI schema generation)
- Model reuse across routers
- Isolated unit testing
- No circular imports
"""

from pydantic import BaseModel, Field, field_validator
from typing import Optional
import re

# ===== Shared Constants =====

VALID_PROTOCOLS = {"awg", "awg2", "awg_legacy", "xray", "telemt", "dns"}

# ===== Auth =====


class LoginRequest(BaseModel):
    username: str = Field(min_length=1, max_length=255)
    password: str = Field(min_length=1, max_length=4096)
    captcha: Optional[str] = Field(default=None, max_length=4096)


# ===== Servers =====


class AddServerRequest(BaseModel):
    host: str = Field(default="", min_length=0, max_length=255)
    ssh_port: int = Field(default=22, ge=1, le=65535)
    username: str = Field(default="", min_length=0, max_length=255)
    password: str = Field(default="", min_length=0, max_length=4096)
    private_key: str = Field(default="", min_length=0, max_length=16384)
    name: str = Field(default="", min_length=0, max_length=255)

    @field_validator("host")
    @classmethod
    def validate_host(cls, v: str) -> str:
        """Validate host: must be a valid IPv4 or hostname if non-empty."""
        if not v:
            return v
        # IPv4 pattern
        ipv4_pattern = re.compile(
            r"^(?:(?:25[0-5]|2[0-4]\d|[01]?\d?\d)\.){3}" r"(?:25[0-5]|2[0-4]\d|[01]?\d?\d)$"
        )
        # Hostname pattern: alphanumeric, dots, hyphens; labels 1-63 chars
        hostname_pattern = re.compile(
            r"^[a-zA-Z0-9]([a-zA-Z0-9.-]{0,253}[a-zA-Z0-9])?$|^[a-zA-Z0-9]$"
        )
        if ipv4_pattern.match(v):
            return v
        if hostname_pattern.match(v):
            return v
        raise ValueError(
            "host must be a valid IPv4 address or hostname " "(alphanumeric, dots, hyphens only)"
        )


class InstallProtocolRequest(BaseModel):
    protocol: str = Field(default="awg", min_length=1, max_length=50)
    port: str = Field(default="55424", min_length=1, max_length=10)
    tls_emulation: Optional[bool] = None
    tls_domain: Optional[str] = Field(default=None, max_length=128)
    max_connections: Optional[int] = Field(default=None, ge=1, le=100000)

    @field_validator("protocol")
    @classmethod
    def validate_protocol(cls, v: str) -> str:
        """Validate protocol against allowlist."""
        if v not in VALID_PROTOCOLS:
            raise ValueError(f"protocol must be one of: {', '.join(sorted(VALID_PROTOCOLS))}")
        return v

    @field_validator("tls_domain")
    @classmethod
    def validate_tls_domain(cls, v: Optional[str]) -> Optional[str]:
        """Validate tls_domain to prevent regex/config injection.

        Only allow alphanumeric chars, dots, hyphens, and underscores.
        Must not contain newlines, shell metacharacters, or regex specials.
        """
        if v is None or v == "":
            return v
        pattern = re.compile(r"^[a-zA-Z0-9]([a-zA-Z0-9._-]{0,126}[a-zA-Z0-9])?$|^[a-zA-Z0-9]$")
        if not pattern.match(v):
            raise ValueError(
                "tls_domain must be 1-128 chars, alphanumeric/dots/hyphens/underscores only, "
                "starting and ending with alphanumeric. No newlines, shell metacharacters, "
                "or regex specials allowed."
            )
        return v


class ProtocolRequest(BaseModel):
    protocol: str = Field(default="awg", min_length=1, max_length=50)

    @field_validator("protocol")
    @classmethod
    def validate_protocol(cls, v: str) -> str:
        """Validate protocol against allowlist."""
        if v not in VALID_PROTOCOLS:
            raise ValueError(f"protocol must be one of: {', '.join(sorted(VALID_PROTOCOLS))}")
        return v


class ServerConfigSaveRequest(BaseModel):
    protocol: str = Field(min_length=1, max_length=50)
    config: str = Field(min_length=1, max_length=65536)

    @field_validator("protocol")
    @classmethod
    def validate_protocol(cls, v: str) -> str:
        """Validate protocol against allowlist."""
        if v not in VALID_PROTOCOLS:
            raise ValueError(f"protocol must be one of: {', '.join(sorted(VALID_PROTOCOLS))}")
        return v


class ConfirmFingerprintRequest(BaseModel):
    """Request body for confirming SSH host key fingerprint after initial connection test.

    All fields needed to create the server are re-sent by the frontend so that
    the add-server endpoint never persists anything before the admin confirms
    the host key.
    """

    host: str = Field(default="", min_length=0, max_length=255)
    ssh_port: int = Field(default=22, ge=1, le=65535)
    username: str = Field(default="", min_length=0, max_length=255)
    password: str = Field(default="", min_length=0, max_length=4096)
    private_key: str = Field(default="", min_length=0, max_length=16384)
    name: str = Field(default="", min_length=0, max_length=255)
    server_info: str = Field(default="", min_length=0, max_length=16384)
    fingerprint: str = Field(min_length=1, max_length=256)

    @field_validator("host")
    @classmethod
    def validate_host(cls, v: str) -> str:
        """Validate host: must be a valid IPv4 or hostname if non-empty."""
        if not v:
            return v
        ipv4_pattern = re.compile(
            r"^(?:(?:25[0-5]|2[0-4]\d|[01]?\d?\d)\.){3}(?:25[0-5]|2[0-4]\d|[01]?\d?\d)$"
        )
        hostname_pattern = re.compile(
            r"^[a-zA-Z0-9]([a-zA-Z0-9.-]{0,253}[a-zA-Z0-9])?$|^[a-zA-Z0-9]$"
        )
        if ipv4_pattern.match(v):
            return v
        if hostname_pattern.match(v):
            return v
        raise ValueError(
            "host must be a valid IPv4 address or hostname (alphanumeric, dots, hyphens only)"
        )


# ===== Connections =====


class AddConnectionRequest(BaseModel):
    protocol: str = Field(default="awg", min_length=1, max_length=50)
    name: str = Field(default="Connection", min_length=1, max_length=255)
    user_id: Optional[str] = Field(default=None, max_length=255)
    telemt_quota: Optional[str] = Field(default=None, max_length=50)
    telemt_max_ips: Optional[int] = Field(default=None, ge=1, le=1000000)
    telemt_expiry: Optional[str] = Field(default=None, max_length=50)

    @field_validator("protocol")
    @classmethod
    def validate_protocol(cls, v: str) -> str:
        """Validate protocol against allowlist."""
        if v not in VALID_PROTOCOLS:
            raise ValueError(f"protocol must be one of: {', '.join(sorted(VALID_PROTOCOLS))}")
        return v


class EditConnectionRequest(BaseModel):
    protocol: str = Field(default="telemt", min_length=1, max_length=50)
    client_id: str = Field(default="", min_length=0, max_length=255)
    telemt_quota: Optional[str] = Field(default=None, max_length=50)
    telemt_max_ips: Optional[int] = Field(default=None, ge=1, le=1000000)
    telemt_expiry: Optional[str] = Field(default=None, max_length=50)

    @field_validator("protocol")
    @classmethod
    def validate_protocol(cls, v: str) -> str:
        """Validate protocol against allowlist."""
        if v not in VALID_PROTOCOLS:
            raise ValueError(f"protocol must be one of: {', '.join(sorted(VALID_PROTOCOLS))}")
        return v


class ConnectionActionRequest(BaseModel):
    protocol: str = Field(default="awg", min_length=1, max_length=50)
    client_id: str = Field(default="", min_length=0, max_length=255)

    @field_validator("protocol")
    @classmethod
    def validate_protocol(cls, v: str) -> str:
        """Validate protocol against allowlist."""
        if v not in VALID_PROTOCOLS:
            raise ValueError(f"protocol must be one of: {', '.join(sorted(VALID_PROTOCOLS))}")
        return v


class ToggleConnectionRequest(BaseModel):
    protocol: str = Field(default="awg", min_length=1, max_length=50)
    client_id: str = Field(default="", min_length=0, max_length=255)
    enable: bool = True

    @field_validator("protocol")
    @classmethod
    def validate_protocol(cls, v: str) -> str:
        """Validate protocol against allowlist."""
        if v not in VALID_PROTOCOLS:
            raise ValueError(f"protocol must be one of: {', '.join(sorted(VALID_PROTOCOLS))}")
        return v


class AddUserConnectionRequest(BaseModel):
    server_id: int = Field(ge=1)
    protocol: str = Field(default="awg", min_length=1, max_length=50)
    name: str = Field(default="VPN Connection", min_length=1, max_length=255)
    client_id: Optional[str] = Field(default=None, max_length=255)
    telemt_quota: Optional[str] = Field(default=None, max_length=50)
    telemt_max_ips: Optional[int] = Field(default=None, ge=1, le=1000000)
    telemt_expiry: Optional[str] = Field(default=None, max_length=50)

    @field_validator("protocol")
    @classmethod
    def validate_protocol(cls, v: str) -> str:
        """Validate protocol against allowlist."""
        if v not in VALID_PROTOCOLS:
            raise ValueError(f"protocol must be one of: {', '.join(sorted(VALID_PROTOCOLS))}")
        return v


class MyAddConnectionRequest(BaseModel):
    server_id: int = Field(ge=1)
    protocol: str = Field(default="awg", min_length=1, max_length=50)
    name: str = Field(default="Connection", min_length=1, max_length=255)
    telemt_quota: Optional[str] = Field(default=None, max_length=50)
    telemt_max_ips: Optional[int] = Field(default=None, ge=1, le=1000000)
    telemt_expiry: Optional[str] = Field(default=None, max_length=50)

    @field_validator("protocol")
    @classmethod
    def validate_protocol(cls, v: str) -> str:
        """Validate protocol against allowlist."""
        if v not in VALID_PROTOCOLS:
            raise ValueError(f"protocol must be one of: {', '.join(sorted(VALID_PROTOCOLS))}")
        return v


# ===== Users =====


class AddUserRequest(BaseModel):
    username: str = Field(min_length=3, max_length=255)
    password: str = Field(min_length=8, max_length=4096)
    role: str = Field(default="user", min_length=1, max_length=50)
    telegramId: Optional[str] = Field(default=None, max_length=255)
    email: Optional[str] = Field(default=None, max_length=255)
    description: Optional[str] = Field(default=None, max_length=1000)
    traffic_limit: Optional[float] = Field(default=0, ge=0)
    traffic_reset_strategy: Optional[str] = Field(default="never", max_length=50)
    server_id: Optional[int] = Field(default=None, ge=1)
    protocol: Optional[str] = Field(default=None, max_length=50)
    connection_name: Optional[str] = Field(default=None, max_length=255)
    expiration_date: Optional[str] = Field(default=None, max_length=50)

    @field_validator("username")
    @classmethod
    def validate_username(cls, v: str) -> str:
        """Validate username: alphanumeric, hyphens, underscores. Normalize to lowercase."""
        v = v.lower()
        if not re.match(r"^[a-z0-9_-]+$", v):
            raise ValueError(
                "username must contain only lowercase letters, digits, " "hyphens, and underscores"
            )
        return v

    @field_validator("password")
    @classmethod
    def validate_password(cls, v: str) -> str:
        """Validate password: at least 8 chars, 1 uppercase, 1 lowercase, 1 digit."""
        if not re.search(r"[A-Z]", v):
            raise ValueError("password must contain at least one uppercase letter")
        if not re.search(r"[a-z]", v):
            raise ValueError("password must contain at least one lowercase letter")
        if not re.search(r"\d", v):
            raise ValueError("password must contain at least one digit")
        return v

    @field_validator("role")
    @classmethod
    def validate_role(cls, v: str) -> str:
        """Validate role: must be 'admin' or 'user'."""
        if v not in ("admin", "user"):
            raise ValueError("role must be 'admin' or 'user'")
        return v

    @field_validator("protocol")
    @classmethod
    def validate_protocol(cls, v: Optional[str]) -> Optional[str]:
        """Validate protocol against allowlist, if provided."""
        if v is not None and v not in VALID_PROTOCOLS:
            raise ValueError(f"protocol must be one of: {', '.join(sorted(VALID_PROTOCOLS))}")
        return v


class UpdateUserRequest(BaseModel):
    telegramId: Optional[str] = Field(default=None, max_length=255)
    email: Optional[str] = Field(default=None, max_length=255)
    description: Optional[str] = Field(default=None, max_length=1000)
    traffic_limit: Optional[float] = Field(default=0, ge=0)
    traffic_reset_strategy: Optional[str] = Field(default=None, max_length=50)
    expiration_date: Optional[str] = Field(default=None, max_length=50)
    password: Optional[str] = Field(default=None, min_length=8, max_length=4096)

    @field_validator("password")
    @classmethod
    def validate_password(cls, v: Optional[str]) -> Optional[str]:
        """Validate password if provided: 8+ chars, 1 uppercase, 1 lowercase, 1 digit."""
        if v is None:
            return v
        if not re.search(r"[A-Z]", v):
            raise ValueError("password must contain at least one uppercase letter")
        if not re.search(r"[a-z]", v):
            raise ValueError("password must contain at least one lowercase letter")
        if not re.search(r"\d", v):
            raise ValueError("password must contain at least one digit")
        return v


class ToggleUserRequest(BaseModel):
    enabled: bool


class ChangePasswordRequest(BaseModel):
    current_password: str = Field(min_length=1, max_length=4096)
    new_password: str = Field(min_length=8, max_length=4096)
    confirm_password: str = Field(min_length=1, max_length=4096)

    @field_validator("new_password")
    @classmethod
    def validate_new_password(cls, v: str) -> str:
        """Validate new password: 8+ chars, 1 uppercase, 1 lowercase, 1 digit, no null bytes."""
        if "\x00" in v:
            raise ValueError("password must not contain null bytes")
        if not re.search(r"[A-Z]", v):
            raise ValueError("password must contain at least one uppercase letter")
        if not re.search(r"[a-z]", v):
            raise ValueError("password must contain at least one lowercase letter")
        if not re.search(r"\d", v):
            raise ValueError("password must contain at least one digit")
        return v


class SetupRequest(BaseModel):
    """Request model for the first-run setup wizard — creates the initial admin user."""

    username: str = Field(min_length=3, max_length=32, pattern=r"^[a-zA-Z0-9_]+$")
    password: str = Field(min_length=8, max_length=4096)
    confirm_password: str = Field(min_length=1, max_length=4096)


# ===== Settings =====


class AppearanceSettings(BaseModel):
    title: str = Field(default="Amnezia", min_length=1, max_length=100)
    logo: str = Field(default="🛡", min_length=1, max_length=100)
    subtitle: str = Field(default="Web Panel", min_length=1, max_length=200)
    language: str = Field(default="en", min_length=1, max_length=10)

    @field_validator("language")
    @classmethod
    def validate_language(cls, v: str) -> str:
        """Validate language against available translations."""
        if v:  # Skip if empty (default)
            from config import TRANSLATIONS

            v = v.strip().lower()
            if v not in TRANSLATIONS:
                raise ValueError(
                    f"language must be one of: {', '.join(sorted(TRANSLATIONS.keys()))}"
                )
        return v


class SyncSettings(BaseModel):
    remnawave_url: str = Field(default="", max_length=2048)
    remnawave_api_key: str = Field(default="", max_length=512)
    remnawave_sync: bool = False
    remnawave_sync_users: bool = False
    remnawave_create_conns: bool = False
    remnawave_server_id: int = Field(default=0, ge=0)
    remnawave_protocol: str = Field(default="awg", min_length=1, max_length=50)

    @field_validator("remnawave_url")
    @classmethod
    def validate_remnawave_url(cls, v: str) -> str:
        """Validate remnawave_url: must be valid URL format if non-empty."""
        if not v:
            return v
        if not re.match(r"^https?://[^\s<>\"']+$", v):
            raise ValueError("remnawave_url must be a valid HTTP(S) URL")
        return v

    @field_validator("remnawave_protocol")
    @classmethod
    def validate_protocol(cls, v: str) -> str:
        """Validate protocol against allowlist."""
        if v not in VALID_PROTOCOLS:
            raise ValueError(f"protocol must be one of: {', '.join(sorted(VALID_PROTOCOLS))}")
        return v


class CaptchaSettings(BaseModel):
    enabled: bool = False


class SSLSettings(BaseModel):
    enabled: bool = False
    domain: str = Field(default="", max_length=255)
    cert_path: str = Field(default="", max_length=4096)
    key_path: str = Field(default="", max_length=4096)
    cert_text: str = Field(default="", max_length=65536)
    key_text: str = Field(default="", max_length=65536)
    panel_port: int = Field(default=5000, ge=1, le=65535)

    @field_validator("domain")
    @classmethod
    def validate_domain(cls, v: str) -> str:
        """Validate domain: alphanumeric, dots, hyphens if non-empty."""
        if not v:
            return v
        pattern = re.compile(r"^[a-zA-Z0-9]([a-zA-Z0-9.-]{0,253}[a-zA-Z0-9])?$|^[a-zA-Z0-9]$")
        if not pattern.match(v):
            raise ValueError("domain must contain only letters, digits, dots, and hyphens")
        return v

    @field_validator("cert_path", "key_path")
    @classmethod
    def validate_path_no_traversal(cls, v: str) -> str:
        """Validate paths: no directory traversal."""
        if not v:
            return v
        if ".." in v:
            raise ValueError("path must not contain directory traversal (..)")
        return v


class ConnectionLimits(BaseModel):
    max_connections_per_user: int = Field(default=10, ge=1, le=1000)
    connection_rate_limit_count: int = Field(default=5, ge=1, le=1000)
    connection_rate_limit_window: int = Field(default=60, ge=1, le=86400)


class ProtocolPaths(BaseModel):
    telemt_config_dir: str = Field(default="/opt/amnezia/telemt", min_length=1, max_length=4096)

    @field_validator("telemt_config_dir")
    @classmethod
    def validate_path_no_traversal(cls, v: str) -> str:
        """Validate path: no directory traversal."""
        if ".." in v:
            raise ValueError("path must not contain directory traversal (..)")
        return v


class SaveSettingsRequest(BaseModel):
    appearance: AppearanceSettings
    sync: SyncSettings
    captcha: CaptchaSettings
    telegram: dict = Field(default_factory=dict)
    ssl: SSLSettings
    limits: ConnectionLimits = ConnectionLimits()
    protocol_paths: ProtocolPaths = ProtocolPaths()


# ===== Sharing =====


class ShareSetupRequest(BaseModel):
    enabled: bool
    password: Optional[str] = Field(default=None, max_length=4096)


class ShareAuthRequest(BaseModel):
    password: str = Field(min_length=1, max_length=4096)
