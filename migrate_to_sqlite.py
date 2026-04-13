"""One-time migration script: data.json → SQLite (panel.db).

On app startup, if panel.db doesn't exist but data.json does, run migration:
1. Read data.json
2. Insert all records into SQLite
3. Rename data.json → data.json.bak
4. On failure: leave data.json untouched, raise exception

Can also be run independently: python3 migrate_to_sqlite.py
"""

import json
import logging
import os
import shutil
import sys

logger = logging.getLogger(__name__)


def migrate_if_needed(data_dir: str) -> None:
    """Check if migration is needed and run it.

    Called by app.py on startup. Three cases:
    1. panel.db exists → skip (already migrated)
    2. data.json exists → migrate to panel.db, rename data.json → data.json.bak
    3. Neither exists → skip (fresh install, Database.__init__ will create panel.db)
    """
    data_file = os.path.join(data_dir, "data.json")
    db_path = os.path.join(data_dir, "panel.db")

    if os.path.exists(db_path):
        logger.info("panel.db already exists, skipping migration")
        return

    if not os.path.exists(data_file):
        logger.info("No data.json found. Fresh install — panel.db will be created on first use.")
        return

    logger.info("data.json found without panel.db — running migration")
    migrate_data_json_to_sqlite(data_file, db_path)


def migrate_data_json_to_sqlite(data_file: str, db_path: str) -> None:
    """Migrate data.json to panel.db. Raises on failure.

    Args:
        data_file: Path to data.json
        db_path: Path to panel.db (will be created)
    """
    if not os.path.exists(data_file):
        raise FileNotFoundError(f"data.json not found at {data_file}")

    if os.path.exists(db_path):
        logger.info("panel.db already exists, skipping migration")
        return

    logger.info("Starting migration from %s to %s", data_file, db_path)

    # Read data.json
    with open(data_file, "r", encoding="utf-8") as f:
        data = json.load(f)

    # Set defaults (same as load_data)
    data.setdefault("servers", [])
    data.setdefault("users", [])
    data.setdefault("user_connections", [])
    data.setdefault("connection_creation_log", [])
    data.setdefault(
        "settings",
        {
            "appearance": {"title": "Amnezia", "logo": "❤️", "subtitle": "Web Panel"},
            "sync": {
                "remnawave_url": "",
                "remnawave_api_key": "",
                "remnawave_sync": False,
                "remnawave_sync_users": False,
                "remnawave_create_conns": False,
                "remnawave_server_id": 0,
                "remnawave_protocol": "awg",
            },
            "limits": {
                "max_connections_per_user": 10,
                "connection_rate_limit_count": 5,
                "connection_rate_limit_window": 60,
            },
            "protocol_paths": {
                "telemt_config_dir": "/opt/amnezia/telemt",
            },
        },
    )

    # Import Database and create
    from database import Database

    db = Database(db_path)

    # Use save_data to write everything at once
    try:
        db.save_data(data)
    except Exception as exc:
        # Remove the partially created DB on failure
        if os.path.exists(db_path):
            os.remove(db_path)
        logger.error("Migration failed: %s", exc)
        raise

    # Backup data.json
    backup_path = data_file + ".bak"
    shutil.move(data_file, backup_path)
    logger.info("Migration complete. data.json moved to %s", backup_path)


if __name__ == "__main__":
    logging.basicConfig(level=logging.INFO)

    if getattr(sys, "frozen", False):
        app_path = os.path.dirname(sys.executable)
    else:
        app_path = os.path.dirname(os.path.abspath(__file__))

    data_file = os.path.join(app_path, "data.json")
    db_path = os.path.join(app_path, "panel.db")

    if os.path.exists(db_path):
        print(f"panel.db already exists at {db_path}, nothing to do.")
        sys.exit(0)

    if not os.path.exists(data_file):
        print(f"data.json not found at {data_file}. Nothing to migrate.")
        sys.exit(0)

    print(f"Migrating {data_file} → {db_path} ...")
    migrate_data_json_to_sqlite(data_file, db_path)
    print("Migration complete!")
