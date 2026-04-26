"""Configuration and initialization for the Amnezia Web Panel."""

import json
import logging
import os
import secrets
from pathlib import Path
from typing import Optional

from database import Database

logger = logging.getLogger(__name__)

# ======================== Paths ========================

DATA_DIR = os.path.dirname(os.path.abspath(__file__))
DB_PATH = os.path.join(DATA_DIR, "panel.db")

# ======================== SECRET_KEY ========================


def _get_secret_key() -> str:
    """Get or generate a persistent SECRET_KEY.

    Priority:
    1. SECRET_KEY environment variable (highest priority, for production/Docker)
    2. .secret_key file in application directory (persistence across restarts)
    3. Generate new key and save to file (first boot scenario, logs warning)

    The file-based approach ensures the key persists across container restarts
    when using Docker volumes.
    """
    # 1. Check environment variable first
    env_key = os.environ.get("SECRET_KEY")
    if env_key:
        logger.info("Using SECRET_KEY from environment variable")
        return env_key

    # 2. Check for existing key file
    key_file = Path(DATA_DIR) / ".secret_key"
    if key_file.exists():
        try:
            stored_key = key_file.read_text().strip()
            if stored_key:
                logger.info("Loaded SECRET_KEY from persistent storage")
                return stored_key
        except Exception as e:
            logger.warning(f"Failed to read SECRET_KEY from file: {e}")

    # 3. Generate new key and save to file
    new_key = secrets.token_hex(32)
    try:
        key_file.write_text(new_key)
        # Ensure file permissions are restrictive (owner read/write only)
        os.chmod(key_file, 0o600)
        logger.warning(
            "Generated new SECRET_KEY on first boot. "
            "Set SECRET_KEY environment variable for production to prevent this warning. "
            "Key stored in: %s",
            key_file,
        )
    except Exception as e:
        logger.error("Failed to save generated SECRET_KEY to file: %s", e)

    return new_key


# ======================== Translations ========================

TRANSLATIONS: dict = {}


def load_translations():
    trans_dir = os.path.join(DATA_DIR, "translations")
    if os.path.exists(trans_dir):
        for f in os.listdir(trans_dir):
            if f.endswith(".json"):
                lang = f.split(".")[0]
                try:
                    with open(os.path.join(trans_dir, f), "r", encoding="utf-8") as tf:
                        TRANSLATIONS[lang] = json.load(tf)
                except Exception as e:
                    logger.error(f"Error loading translation {f}: {e}")
    logger.info(f"Loaded translations: {list(TRANSLATIONS.keys())}")


# ======================== Database ========================

_db_instance: Optional[Database] = None


def get_db() -> Database:
    """Return the singleton Database instance, creating it if needed."""
    global _db_instance
    if _db_instance is None:
        _db_instance = Database(DB_PATH, secret_key=_get_secret_key())
    return _db_instance


def init_db():
    """Initialize the database and run migration if needed."""
    import migrate_to_sqlite

    migrate_to_sqlite.migrate_if_needed(DATA_DIR)
    get_db()