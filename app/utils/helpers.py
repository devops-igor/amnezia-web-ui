"""Pure helper functions — no route handlers, no FastAPI app state.

Dependencies on ``app`` (e.g. ``get_db``, ``TRANSLATIONS``) are resolved
lazily *inside* function bodies so that ``patch.object(app, "get_db", ...)``
continues to work for all consumers.
"""

import base64
import hashlib
import logging
import re
import secrets

import credential_crypto
from fastapi import Request
from slowapi.util import get_remote_address

logger = logging.getLogger(__name__)


# Patterns to strip from error messages shown to users (security)
_SENSITIVE_PATTERNS = [
    re.compile(r"/[\w/.-]+"),  # File paths
    re.compile(r"\b\d{1,3}(\.\d{1,3}){3}\b"),  # IP addresses
    re.compile(r"\b[\w.-]+@ [\w.-]+\.\w{2,}\b"),  # Email-like patterns
    re.compile(r"\b0x[0-9a-fA-F]+\b"),  # Hex addresses
]


def _get_client_ip(request: Request) -> str:
    """Get client IP, respecting X-Forwarded-For from reverse proxy."""
    forwarded = request.headers.get("X-Forwarded-For")
    if forwarded:
        return forwarded.split(",")[0].strip()
    return get_remote_address(request)


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
    salt = secrets.token_hex(16)
    h = hashlib.pbkdf2_hmac("sha256", password.encode(), salt.encode(), 100000)
    return f"{salt}${h.hex()}"


def verify_password(password: str, password_hash: str) -> bool:
    try:
        salt, h = password_hash.split("$", 1)
        new_h = hashlib.pbkdf2_hmac("sha256", password.encode(), salt.encode(), 100000)
        return new_h.hex() == h
    except Exception:
        return False


def get_leaderboard_entries(period: str) -> list[dict]:
    """Aggregate traffic data for the leaderboard.

    Args:
        period: "all-time" or "monthly"

    Returns:
        list of dicts with rank, username, download, upload, total.
        Users with zero total traffic or disabled accounts are excluded.
    """
    from app import get_db

    db = get_db()
    users = db.get_all_users()
    entries = []
    for u in users:
        if u.get("enabled", True) is not True:
            continue
        if period == "monthly":
            download = u.get("monthly_tx", 0)
            upload = u.get("monthly_rx", 0)
        else:
            download = u.get("traffic_total_tx", 0)
            upload = u.get("traffic_total_rx", 0)
        total = download + upload
        if total == 0:
            continue
        entries.append(
            {
                "rank": 0,
                "username": u.get("username", ""),
                "download": download,
                "upload": upload,
                "total": total,
            }
        )
    entries.sort(key=lambda e: (-e["total"], e["username"].lower()))
    for i, e in enumerate(entries):
        e["rank"] = i + 1
    return entries


def _t(text_id, lang="en"):
    from app import TRANSLATIONS

    lang_batch = TRANSLATIONS.get(lang, TRANSLATIONS.get("en", {}))
    return lang_batch.get(text_id, text_id)


def _get_default_lang() -> str:
    """Read the default language from appearance settings, fall back to 'en'."""
    from app import get_db

    try:
        db = get_db()
        appearance = db.get_setting("appearance", {})
        return appearance.get("language", "en")
    except Exception:
        return "en"


def _get_lang(request: Request) -> str:
    """Get language from cookie, falling back to settings-configured default."""
    return request.cookies.get("lang", _get_default_lang())
