"""Pure helper functions — no route handlers, no FastAPI app state.

Dependencies on ``app`` (e.g. ``get_db``, ``TRANSLATIONS``) are resolved
lazily *inside* function bodies so that ``patch.object(app, "get_db", ...)``
continues to work for all consumers.
"""

import base64
import hashlib
import hmac
import ipaddress
import logging
import os
import re

import bcrypt
import credential_crypto
from app.managers import SSHManager, XrayManager
from fastapi import Request
from slowapi.util import get_remote_address

logger = logging.getLogger(__name__)


# ---------------------------------------------------------------------------
# TRUSTED_PROXIES — env-driven, CIDR-aware trusted proxy detection
# ---------------------------------------------------------------------------

_trusted_proxy_hosts: set[ipaddress.IPv4Address | ipaddress.IPv6Address] = set()
_trusted_proxy_networks: list[ipaddress.IPv4Network | ipaddress.IPv6Network] = []


def _parse_trusted_proxies(env_value: str) -> None:
    """Parse ``TRUSTED_PROXIES`` env var into *_hosts* and *_networks*.

    Entries are comma-separated.  Each one is tried as ``ip_network`` first
    (with *strict=False* so that bare host addresses like ``172.16.0.1`` are
    accepted as ``/32`` networks).  Networks are stored in
    ``_trusted_proxy_networks`` while plain addresses (no netmask after the
    network-then-address parse) are stored in ``_trusted_proxy_hosts``.

    Invalid entries are logged as warnings and silently skipped — the env
    var is never allowed to crash the application.
    """
    _trusted_proxy_hosts.clear()
    _trusted_proxy_networks.clear()

    for entry in env_value.split(","):
        entry = entry.strip()
        if not entry:
            continue
        try:
            net = ipaddress.ip_network(entry, strict=False)
        except ValueError:
            logger.warning("Invalid TRUSTED_PROXIES entry %r — skipping", entry)
            continue

        # Distinguish CIDR networks from bare addresses
        if net.prefixlen == net.max_prefixlen:
            # /32 (IPv4) or /128 (IPv6) — treat as a host address
            _trusted_proxy_hosts.add(net.network_address)
        else:
            _trusted_proxy_networks.append(net)


# Eagerly parse the env var at import time
_raw_proxies = os.environ.get("TRUSTED_PROXIES", "").strip()
if _raw_proxies:
    _parse_trusted_proxies(_raw_proxies)
    logger.info(
        "TRUSTED_PROXIES configured: %d host(s), %d network(s)",
        len(_trusted_proxy_hosts),
        len(_trusted_proxy_networks),
    )
else:
    logger.info("TRUSTED_PROXIES not set — X-Forwarded-For will NOT be trusted")


# Patterns to strip from error messages shown to users (security)
_SENSITIVE_PATTERNS = [
    re.compile(r"/[\w/.-]+"),  # File paths
    re.compile(r"\b\d{1,3}(\.\d{1,3}){3}\b"),  # IP addresses
    re.compile(r"\b[\w.-]+@ [\w.-]+\.\w{2,}\b"),  # Email-like patterns
    re.compile(r"\b0x[0-9a-fA-F]+\b"),  # Hex addresses
]


def _get_client_ip(request: Request) -> str:
    """Get client IP, respecting X-Forwarded-For from trusted reverse proxies.

    Only honours ``X-Forwarded-For`` when the direct peer is listed in
    ``TRUSTED_PROXIES`` (exact IP or CIDR network match).  Otherwise the
    header is ignored and the direct peer address is returned.
    """
    peer = get_remote_address(request)
    try:
        peer_ip = ipaddress.ip_address(peer)
    except ValueError:
        return peer

    if peer_ip in _trusted_proxy_hosts or any(peer_ip in net for net in _trusted_proxy_networks):
        forwarded = request.headers.get("X-Forwarded-For")
        if forwarded:
            return forwarded.split(",")[0].strip()
    return peer


def _sanitize_error(message: str, fallback: str = "An unexpected error occurred") -> str:
    """Strip potentially sensitive information from error messages shown to users.

    Logs the full error server-side but returns a sanitized version to the client.
    """
    if not message or message.strip() == "":
        return fallback
    sanitized = message
    for pattern in _SENSITIVE_PATTERNS:
        sanitized = pattern.sub("***", sanitized)
    if not sanitized.strip() or all(c == "*" for c in sanitized):
        return fallback
    return sanitized


def serialize_protocols(protocols: dict) -> dict:
    """Strip sensitive fields from protocols before returning via API.

    This is an additional defense-in-depth layer on top of the stripping
    that already happens in _server_row_to_dict().
    """
    if not isinstance(protocols, dict):
        return protocols
    return credential_crypto.strip_sensitive_protocol_fields(protocols)


def generate_vpn_link(config_text):
    b64 = base64.b64encode(config_text.strip().encode("utf-8")).decode("utf-8")
    return f"vpn://{b64}"


def hash_password(password: str) -> str:
    """Hash a password using bcrypt.

    bcrypt truncates passwords at 72 bytes. We truncate explicitly
    to avoid ValueError on passwords longer than 72 bytes.
    """
    return bcrypt.hashpw(password.encode()[:72], bcrypt.gensalt()).decode()


def verify_password(password: str, password_hash: str) -> bool:
    """Verify a password against a hash (bcrypt or legacy PBKDF2)."""
    # Legacy PBKDF2 hashes use '$' separator with hex salt
    if "$" in password_hash and not password_hash.startswith("$2"):
        try:
            salt, h = password_hash.split("$", 1)
            new_h = hashlib.pbkdf2_hmac("sha256", password.encode(), salt.encode(), 100000)
            return hmac.compare_digest(new_h.hex(), h)
        except (ValueError, TypeError):
            return False
    # bcrypt hashes start with $2a$, $2b$, or $2y$
    try:
        return bcrypt.checkpw(password.encode()[:72], password_hash.encode())
    except (ValueError, TypeError):
        return False


def get_leaderboard_entries(period: str) -> list[dict]:
    """Aggregate traffic data for the leaderboard.

    Args:
        period: "all-time" or "monthly"

    Returns:
        list of dicts with rank, username, download, upload, total.
        Users with zero total traffic or disabled accounts are excluded.
    """
    from config import get_db

    db = get_db()
    return db.get_leaderboard(period)


def _t(text_id, lang="en"):
    from config import TRANSLATIONS

    lang_batch = TRANSLATIONS.get(lang, TRANSLATIONS.get("en", {}))
    return lang_batch.get(text_id, text_id)


def _get_default_lang() -> str:
    """Read the default language from appearance settings, fall back to 'en'."""
    from config import get_db

    try:
        db = get_db()
        appearance = db.get_setting("appearance", {})
        return appearance.get("language", "en")
    except (ValueError, KeyError, TypeError):
        return "en"


def _get_lang(request: Request) -> str:
    """Get language from cookie, falling back to settings-configured default."""
    return request.cookies.get("lang", _get_default_lang())


def get_ssh(server, db=None):
    """Create an SSHManager instance from a server dict.

    Args:
        server: Dict with host, ssh_port, username, password, private_key keys.
        db: Optional Database instance for host key verification. When provided,
            SSHManager will use the stored fingerprint for subsequent connections.
    """
    kwargs = {
        "host": server["host"],
        "port": server.get("ssh_port", 22),
        "username": server["username"],
        "password": server.get("password"),
        "private_key": server.get("private_key"),
    }
    if db is not None:
        kwargs["database"] = db
        kwargs["server_id"] = server.get("id")
    return SSHManager(**kwargs)


def get_protocol_manager(ssh, protocol: str):
    """Create a protocol manager instance for the given SSH connection and protocol."""
    if protocol == "xray":
        return XrayManager(ssh)
    elif protocol == "telemt":
        from app.managers import TelemtManager
        from config import get_db

        db = get_db()
        config_dir = db.get_setting("protocol_paths", {}).get(
            "telemt_config_dir", "/opt/amnezia/telemt"
        )
        return TelemtManager(ssh, config_dir=config_dir)
    elif protocol == "dns":
        from dns_manager import DNSManager

        return DNSManager(ssh)
    from app.managers import AWGManager

    return AWGManager(ssh)
