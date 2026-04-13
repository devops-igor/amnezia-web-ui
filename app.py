import os
import sys
import json
import logging
import base64
import hashlib
import secrets
import uuid
import asyncio
from datetime import datetime
import io
from fastapi.responses import (
    JSONResponse,
    RedirectResponse,
    HTMLResponse,
    StreamingResponse,
    Response,
)
from fastapi.staticfiles import StaticFiles
from fastapi.templating import Jinja2Templates
from fastapi import FastAPI, Request, Query, UploadFile, File
from starlette.middleware.sessions import SessionMiddleware
from pydantic import BaseModel
from typing import Optional, List
import uvicorn
import httpx
import re

try:
    from multicolorcaptcha import CaptchaGenerator
except ImportError:
    CaptchaGenerator = None

from ssh_manager import SSHManager
from awg_manager import AWGManager
from xray_manager import XrayManager
from database import Database
import telegram_bot as tg_bot

# Configure logging
logging.basicConfig(level=logging.INFO)
logger = logging.getLogger(__name__)

app = FastAPI(title="Amnezia Web Panel")
app.add_middleware(
    SessionMiddleware, secret_key=os.environ.get("SECRET_KEY", secrets.token_hex(32))
)

# Mount static files & templates
app.mount(
    "/static",
    StaticFiles(directory=os.path.join(os.path.dirname(__file__), "static")),
    name="static",
)
templates = Jinja2Templates(directory=os.path.join(os.path.dirname(__file__), "templates"))

if getattr(sys, "frozen", False):
    application_path = os.path.dirname(sys.executable)
else:
    application_path = os.path.dirname(__file__)

DATA_DIR = application_path
DB_PATH = os.path.join(DATA_DIR, "panel.db")


# ======================== Translations ========================
TRANSLATIONS = {}


def load_translations():
    global TRANSLATIONS
    trans_dir = os.path.join(os.path.dirname(__file__), "translations")
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


def _t(text_id, lang="en"):
    lang_batch = TRANSLATIONS.get(lang, TRANSLATIONS.get("en", {}))
    return lang_batch.get(text_id, text_id)


load_translations()


# ======================== Helpers ========================

_db_instance: Optional[Database] = None


def get_db() -> Database:
    """Return the singleton Database instance, creating it if needed."""
    global _db_instance
    if _db_instance is None:
        _db_instance = Database(DB_PATH)
    return _db_instance


def init_db():
    """Initialize the database and run migration if needed."""
    import migrate_to_sqlite

    migrate_to_sqlite.migrate_if_needed(DATA_DIR)
    get_db()


# Patterns to strip from error messages shown to users (security)
_SENSITIVE_PATTERNS = [
    re.compile(r"/[\w/.-]+"),  # File paths
    re.compile(r"\b\d{1,3}(\.\d{1,3}){3}\b"),  # IP addresses
    re.compile(r"\b[\w.-]+@ [\w.-]+\.\w{2,}\b"),  # Email-like patterns
    re.compile(r"\b0x[0-9a-fA-F]+\b"),  # Hex addresses
]


def _sanitize_error(message: str, fallback: str = "An unexpected error occurred") -> str:
    """Strip potentially sensitive information from error messages shown to users.
    Logs the full error server-side but returns a sanitized version to the client.
    """
    if not message or message.strip() == "":
        return fallback
    sanitized = message
    for pattern in _SENSITIVE_PATTERNS:
        sanitized = pattern.sub("***", sanitized)
    # If the entire message was redacted, use fallback
    if not sanitized.strip() or all(c == "*" for c in sanitized):
        return fallback
    return sanitized


def get_ssh(server):
    return SSHManager(
        host=server["host"],
        port=server.get("ssh_port", 22),
        username=server["username"],
        password=server.get("password"),
        private_key=server.get("private_key"),
    )


def get_protocol_manager(ssh, protocol: str):
    if protocol == "xray":
        return XrayManager(ssh)
    elif protocol == "telemt":
        from telemt_manager import TelemtManager

        db = get_db()
        config_dir = db.get_setting("protocol_paths", {}).get(
            "telemt_config_dir", "/opt/amnezia/telemt"
        )
        return TelemtManager(ssh, config_dir=config_dir)
    elif protocol == "dns":
        from dns_manager import DNSManager

        return DNSManager(ssh)
    from awg_manager import AWGManager

    return AWGManager(ssh)


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


async def perform_delete_user(user_id: str):
    db = get_db()
    user = db.get_user(user_id)
    if not user:
        return False
    # Remove user's connections from servers
    user_conns = db.get_connections_by_user(user_id)
    for uc in user_conns:
        try:
            sid = uc["server_id"]
            server = db.get_server_by_id(sid)
            if server:
                ssh = get_ssh(server)
                ssh.connect()
                manager = get_protocol_manager(ssh, uc["protocol"])
                manager.remove_client(uc["protocol"], uc["client_id"])
                ssh.disconnect()
        except Exception as e:
            logger.warning(f"Failed to remove connection {uc['client_id']} during user delete: {e}")
    db.delete_user(user_id)
    return True


async def perform_toggle_user(user_id: str, enabled: bool):
    """Enable or disable a user by setting their enabled flag."""
    db = get_db()
    user = db.get_user(user_id)
    if not user:
        return False
    db.update_user(user_id, {"enabled": enabled})
    return True


async def perform_mass_operations(
    delete_uids: List[str] = None, toggle_uids: List[tuple] = None, create_conns: List[dict] = None
):
    """
    Executes multiple SSH operations efficiently.
    Uses DB directly for data access.
    """
    db = get_db()
    server_ops = {}

    def get_ops(sid):
        if sid not in server_ops:
            server_ops[sid] = {"delete": [], "toggle": [], "create": []}
        return server_ops[sid]

    if delete_uids:
        for uid in delete_uids:
            conns = db.get_connections_by_user(uid)
            for c in conns:
                get_ops(c["server_id"])["delete"].append(c)

    if toggle_uids:
        for uid, enabled in toggle_uids:
            conns = db.get_connections_by_user(uid)
            for c in conns:
                get_ops(c["server_id"])["toggle"].append((c, enabled))

    if create_conns:
        for req in create_conns:
            get_ops(req["server_id"])["create"].append(req)

    async def run_server_ops(srv_id, ops):
        server = db.get_server_by_id(srv_id)
        if server is None:
            return

        try:
            ssh = get_ssh(server)
            await asyncio.to_thread(ssh.connect)

            # 1. Deletes
            for c in ops["delete"]:
                manager = get_protocol_manager(ssh, c["protocol"])
                await asyncio.to_thread(manager.remove_client, c["protocol"], c["client_id"])
                db.delete_connection(c["id"])

            # 2. Toggles (just toggle the actual wireguard peer)
            for c, enabled in ops["toggle"]:
                manager = get_protocol_manager(ssh, c["protocol"])
                await asyncio.to_thread(
                    manager.toggle_client, c["protocol"], c["client_id"], enabled
                )

            # 3. Creates
            for c_req in ops["create"]:
                proto_info = server.get("protocols", {}).get(c_req["protocol"], {})
                port = proto_info.get("port", "55424")
                manager = get_protocol_manager(ssh, c_req["protocol"])
                res = await asyncio.to_thread(
                    manager.add_client, c_req["protocol"], c_req["name"], server["host"], port
                )

                if res.get("client_id"):
                    new_conn = {
                        "id": str(uuid.uuid4()),
                        "user_id": c_req["user_id"],
                        "server_id": srv_id,
                        "protocol": c_req["protocol"],
                        "client_id": res["client_id"],
                        "name": c_req["name"],
                        "created_at": datetime.now().isoformat(),
                    }
                    db.create_connection(new_conn)

            await asyncio.to_thread(ssh.disconnect)
        except Exception as e:
            logger.error(f"Mass ops failed for server {srv_id}: {e}")

    # Run all servers in parallel
    tasks = [run_server_ops(sid, ops) for sid, ops in server_ops.items()]
    if tasks:
        await asyncio.gather(*tasks)

    # 4. Final user-level cleanup (delete/toggle users metadata)
    if delete_uids:
        for uid in delete_uids:
            db.delete_user(uid)
    if toggle_uids:
        for uid, enabled in toggle_uids:
            db.update_user(uid, {"enabled": enabled})

    return True


async def sync_users_with_remnawave():
    db = get_db()
    settings = db.get_all_settings()
    sync_settings = settings.get("sync", {})
    if not sync_settings.get("remnawave_sync_users"):
        return 0, "Synchronization is disabled in settings"

    url = sync_settings.get("remnawave_url")
    api_key = sync_settings.get("remnawave_api_key")
    if not url or not api_key:
        return 0, "Remnawave URL or API Key not configured"

    api_url = url.rstrip("/") + "/api/users"
    headers = {"Authorization": f"Bearer {api_key}"}

    try:
        rw_users = []
        async with httpx.AsyncClient(timeout=30.0) as client:
            page_size = 50
            current_start = 0
            while True:
                resp = await client.get(
                    f"{api_url}?size={page_size}&start={current_start}", headers=headers
                )
                if resp.status_code != 200:
                    return 0, f"Remnawave API error: {resp.status_code} {resp.text}"

                page_data = resp.json()
                response_obj = page_data.get("response", {})
                page_users = response_obj.get("users", [])
                total_count = response_obj.get("total", 0)

                if not page_users:
                    break

                rw_users.extend(page_users)
                logger.info(f"Fetched {len(rw_users)} / {total_count} users from Remnawave...")

                if len(rw_users) >= total_count or len(page_users) == 0:
                    break

                current_start += len(page_users)

            rw_uuids = {u["uuid"] for u in rw_users}

            # 1. Handle deletion (users that have remnawave_uuid but are no longer in Remnawave)
            to_delete_ids = []
            all_users = db.get_all_users()
            for u in all_users:
                if u.get("remnawave_uuid") and u["remnawave_uuid"] not in rw_uuids:
                    to_delete_ids.append(u["id"])

            if to_delete_ids:
                logger.info(f"Removing {len(to_delete_ids)} users deleted in Remnawave")
                await perform_mass_operations(delete_uids=to_delete_ids)

            # 2. Sync / Create users
            synced_count = 0
            to_toggle = []  # list of (user_id, enabled)
            to_create_conns = []  # list of dicts

            for rw_u in rw_users:
                local_u = db.get_user_by_remnawave_uuid(rw_u["uuid"])
                if not local_u:
                    local_u = db.get_user_by_username(rw_u["username"])

                is_active = rw_u.get("status") == "ACTIVE"

                if local_u:
                    updates = {
                        "username": rw_u["username"],
                        "remnawave_uuid": rw_u["uuid"],
                    }
                    if rw_u.get("telegramId") is not None:
                        updates["telegramId"] = rw_u.get("telegramId")
                    if rw_u.get("email") is not None:
                        updates["email"] = rw_u.get("email")
                    if rw_u.get("description") is not None:
                        updates["description"] = rw_u.get("description")

                    if local_u.get("enabled", True) != is_active:
                        to_toggle.append((local_u["id"], is_active))

                    db.update_user(local_u["id"], updates)
                    synced_count += 1
                else:
                    new_id = str(uuid.uuid4())
                    new_user = {
                        "id": new_id,
                        "username": rw_u["username"],
                        "password_hash": "",
                        "role": "user",
                        "telegramId": rw_u.get("telegramId"),
                        "email": rw_u.get("email"),
                        "description": rw_u.get("description"),
                        "enabled": is_active,
                        "created_at": datetime.now().isoformat(),
                        "remnawave_uuid": rw_u["uuid"],
                        "share_enabled": False,
                        "share_token": secrets.token_urlsafe(16),
                        "share_password_hash": None,
                    }
                    db.create_user(new_user)

                    if sync_settings.get("remnawave_create_conns"):
                        sid = sync_settings.get("remnawave_server_id")
                        if sid is not None:
                            to_create_conns.append(
                                {
                                    "user_id": new_id,
                                    "server_id": sid,
                                    "protocol": sync_settings.get("remnawave_protocol", "awg"),
                                    "name": f"{rw_u['username']}_vpn",
                                }
                            )
                    synced_count += 1

            # Execute all collected mass operations
            if to_toggle or to_create_conns:
                logger.info(
                    f"Executing mass ops for Remnawave sync: toggle={len(to_toggle)}, create={len(to_create_conns)}"
                )
                await perform_mass_operations(toggle_uids=to_toggle, create_conns=to_create_conns)

            return synced_count, "Successfully synchronized with Remnawave"

    except Exception as e:
        logger.exception("Synchronization error")
        return 0, f"Error: {str(e)}"


def get_current_user(request: Request):
    user_id = request.session.get("user_id")
    if not user_id:
        return None
    db = get_db()
    return db.get_user(user_id)


def _format_bytes(n: int) -> str:
    """Format byte count as human-readable string (e.g. '1.5 GB')."""
    if n is None:
        n = 0
    for unit in ["B", "KB", "MB", "GB", "TB", "PB"]:
        if abs(n) < 1024.0:
            if unit == "B":
                return f"{int(n)} {unit}"
            return f"{n:.2f} {unit}"
        n /= 1024.0
    return f"{n:.2f} EB"


def tpl(request, template, **kwargs):
    db = get_db()
    settings = db.get_all_settings()
    lang = request.cookies.get("lang", "en")
    ctx = {
        "request": request,
        "current_user": get_current_user(request),
        "site_settings": settings.get("appearance", {}),
        "captcha_settings": settings.get("captcha", {}),
        "telegram_settings": settings.get("telegram", {}),
        "bot_running": tg_bot.is_running(),
        "lang": lang,
        "_": lambda text_id: _t(text_id, lang),
        "translations_json": json.dumps(TRANSLATIONS.get(lang, TRANSLATIONS.get("en", {}))),
        "all_translations_json": json.dumps(TRANSLATIONS),
        "format_bytes": _format_bytes,
    }
    ctx.update(kwargs)
    return templates.TemplateResponse(template, ctx)


def get_leaderboard_entries(period: str) -> list[dict]:
    """Aggregate traffic data for the leaderboard.

    Args:
        period: "all-time" or "monthly"

    Returns:
        list of dicts with rank, username, download, upload, total.
        Users with zero total traffic or disabled accounts are excluded.
    """
    db = get_db()
    users = db.get_all_users()
    entries = []
    for u in users:
        # Skip disabled users (enabled is explicitly False or non-truthy)
        if u.get("enabled", True) is not True:
            continue
        if period == "monthly":
            download = u.get("monthly_tx", 0)  # server-sent = client download
            upload = u.get("monthly_rx", 0)  # server-received = client upload
        else:
            download = u.get("traffic_total_tx", 0)  # server-sent = client download
            upload = u.get("traffic_total_rx", 0)  # server-received = client upload
        total = download + upload
        # Skip zero-traffic users
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
    # Sort by descending total, then ascending username (case-insensitive)
    entries.sort(key=lambda e: (-e["total"], e["username"].lower()))
    for i, e in enumerate(entries):
        e["rank"] = i + 1
    return entries


# ======================== Pydantic Models ========================


class LoginRequest(BaseModel):
    username: str
    password: str
    captcha: Optional[str] = None


class AddServerRequest(BaseModel):
    host: str = ""
    ssh_port: int = 22
    username: str = ""
    password: str = ""
    private_key: str = ""
    name: str = ""


class InstallProtocolRequest(BaseModel):
    protocol: str = "awg"
    port: str = "55424"
    tls_emulation: Optional[bool] = None
    tls_domain: Optional[str] = None
    max_connections: Optional[int] = None


class ProtocolRequest(BaseModel):
    protocol: str = "awg"


class AddConnectionRequest(BaseModel):
    protocol: str = "awg"
    name: str = "Connection"
    user_id: Optional[str] = None
    telemt_quota: Optional[str] = None
    telemt_max_ips: Optional[int] = None
    telemt_expiry: Optional[str] = None


class EditConnectionRequest(BaseModel):
    protocol: str = "telemt"
    client_id: str = ""
    telemt_quota: Optional[str] = None
    telemt_max_ips: Optional[int] = None
    telemt_expiry: Optional[str] = None


class ConnectionActionRequest(BaseModel):
    protocol: str = "awg"
    client_id: str = ""


class ToggleConnectionRequest(BaseModel):
    protocol: str = "awg"
    client_id: str = ""
    enable: bool = True


class AddUserRequest(BaseModel):
    username: str
    password: str
    role: str = "user"
    telegramId: Optional[str] = None
    email: Optional[str] = None
    description: Optional[str] = None
    traffic_limit: Optional[float] = 0
    traffic_reset_strategy: Optional[str] = "never"
    server_id: Optional[int] = None
    protocol: Optional[str] = None
    connection_name: Optional[str] = None
    expiration_date: Optional[str] = None


class ServerConfigSaveRequest(BaseModel):
    protocol: str
    config: str


class AppearanceSettings(BaseModel):
    title: str = "Amnezia"
    logo: str = "🛡"
    subtitle: str = "Web Panel"


class SyncSettings(BaseModel):
    remnawave_url: str = ""
    remnawave_api_key: str = ""
    remnawave_sync: bool = False
    remnawave_sync_users: bool = False
    remnawave_create_conns: bool = False
    remnawave_server_id: int = 0
    remnawave_protocol: str = "awg"


class CaptchaSettings(BaseModel):
    enabled: bool = False


class SSLSettings(BaseModel):
    enabled: bool = False
    domain: str = ""
    cert_path: str = ""
    key_path: str = ""
    cert_text: str = ""
    key_text: str = ""
    panel_port: int = 5000


class TelegramSettings(BaseModel):
    token: str = ""
    enabled: bool = False


class ConnectionLimits(BaseModel):
    max_connections_per_user: int = 10
    connection_rate_limit_count: int = 5
    connection_rate_limit_window: int = 60


class ProtocolPaths(BaseModel):
    telemt_config_dir: str = "/opt/amnezia/telemt"


class UpdateUserRequest(BaseModel):
    telegramId: Optional[str] = None
    email: Optional[str] = None
    description: Optional[str] = None
    traffic_limit: Optional[float] = 0
    traffic_reset_strategy: Optional[str] = None
    expiration_date: Optional[str] = None
    password: Optional[str] = None


class SaveSettingsRequest(BaseModel):
    appearance: AppearanceSettings
    sync: SyncSettings
    captcha: CaptchaSettings
    telegram: TelegramSettings
    ssl: SSLSettings
    limits: ConnectionLimits = ConnectionLimits()
    protocol_paths: ProtocolPaths = ProtocolPaths()


class ToggleUserRequest(BaseModel):
    enabled: bool


class AddUserConnectionRequest(BaseModel):
    server_id: int
    protocol: str = "awg"
    name: str = "VPN Connection"
    client_id: Optional[str] = None
    telemt_quota: Optional[str] = None
    telemt_max_ips: Optional[int] = None
    telemt_expiry: Optional[str] = None


class ShareSetupRequest(BaseModel):
    enabled: bool
    password: Optional[str] = None


class ShareAuthRequest(BaseModel):
    password: str


# ======================== Startup ========================


@app.on_event("startup")
async def startup():
    init_db()
    db = get_db()

    if not db.get_all_users():
        db.create_user(
            {
                "id": str(uuid.uuid4()),
                "username": "admin",
                "password_hash": hash_password("admin"),
                "role": "admin",
                "enabled": True,
                "created_at": datetime.now().isoformat(),
            }
        )
        logger.info("Default admin created (admin / admin)")

    # Start periodic background tasks
    asyncio.create_task(periodic_background_tasks())

    # Start Telegram bot if enabled
    tg_cfg = db.get_setting("telegram", {})
    if tg_cfg.get("enabled") and tg_cfg.get("token"):
        logger.info("Starting Telegram bot from saved settings...")
        tg_bot.launch_bot(tg_cfg["token"], db.load_data, generate_vpn_link)


async def periodic_background_tasks():
    """Background task to sync traffic limits and Remnawave every 10 minutes"""
    while True:
        try:
            # We wait before the first sync to let the app settle
            await asyncio.sleep(60)

            # --- 1. TRAFFIC SYNC & LIMITS ---
            logger.info("Starting background traffic sync...")
            db = get_db()

            servers = db.get_all_servers()
            all_conns = db.get_all_connections()

            conns_by_server = {}
            for uc in all_conns:
                sid = uc["server_id"]
                conns_by_server.setdefault(sid, []).append(uc)

            updates = []

            for server in servers:
                sid = server["id"]
                if sid not in conns_by_server:
                    continue
                try:
                    ssh = get_ssh(server)
                    ssh.connect()
                    for proto in ["awg", "awg2", "awg_legacy", "xray", "telemt"]:
                        if proto in server.get("protocols", {}):
                            try:
                                manager = get_protocol_manager(ssh, proto)
                                clients = manager.get_clients(proto)
                            except Exception as e:
                                logger.error(
                                    f"get_clients failed for server {sid} proto {proto}: {e}"
                                )
                                continue
                            client_bytes = {}
                            for c in clients:
                                rx = c.get("userData", {}).get("dataReceivedBytes", 0)
                                tx = c.get("userData", {}).get("dataSentBytes", 0)
                                client_bytes[c.get("clientId")] = {"rx": rx, "tx": tx}

                            for uc in conns_by_server[sid]:
                                if uc["protocol"] == proto and uc["client_id"] in client_bytes:
                                    curr_rx = client_bytes[uc["client_id"]]["rx"]
                                    curr_tx = client_bytes[uc["client_id"]]["tx"]
                                    last_rx = uc.get("last_rx")
                                    last_tx = uc.get("last_tx")
                                    if last_rx is None and last_tx is None:
                                        last_bytes = uc.get("last_bytes", 0)
                                        last_rx = last_bytes // 2
                                        last_tx = last_bytes - last_rx
                                    else:
                                        last_rx = last_rx or 0
                                        last_tx = last_tx or 0
                                    rx_delta = curr_rx - last_rx if curr_rx >= last_rx else curr_rx
                                    tx_delta = curr_tx - last_tx if curr_tx >= last_tx else curr_tx
                                    updates.append((uc["id"], rx_delta, tx_delta, curr_rx, curr_tx))
                except Exception as e:
                    sid = server["id"]
                    logger.error(f"Traffic sync err server {sid}: {e}")
                    ssh.disconnect()
                    continue
                ssh.disconnect()
            to_disable_uids = []
            if updates:
                now = datetime.now()
                users_map = {u["id"]: u for u in db.get_all_users()}

                for uc_id, rx_delta, tx_delta, curr_rx, curr_tx in updates:
                    uc = db.get_connection_by_id(uc_id)
                    if uc:
                        # Update connection's last_rx/last_tx
                        db.update_connection(uc_id, {"last_rx": curr_rx, "last_tx": curr_tx})
                        uid = uc["user_id"]
                        if uid in users_map:
                            u = users_map[uid]
                            # Check if reset is needed BEFORE adding new consumption
                            strategy = u.get("traffic_reset_strategy", "never")
                            last_reset_iso = u.get("last_reset_at")

                            reset_needed = False
                            if strategy != "never" and last_reset_iso:
                                try:
                                    last = datetime.fromisoformat(last_reset_iso)
                                    if strategy == "daily":
                                        reset_needed = now.date() > last.date()
                                    elif strategy == "weekly":
                                        reset_needed = (
                                            now.isocalendar()[1] != last.isocalendar()[1]
                                            or now.year != last.year
                                        )
                                    elif strategy == "monthly":
                                        reset_needed = (
                                            now.month != last.month or now.year != last.year
                                        )
                                except:
                                    pass

                            if reset_needed:
                                logger.info(
                                    f"Resetting traffic for user {u['username']} (strategy: {strategy})"
                                )
                                db.update_user(
                                    uid,
                                    {
                                        "traffic_used": 0,
                                        "last_reset_at": now.isoformat(),
                                    },
                                )
                                u["traffic_used"] = 0
                                u["last_reset_at"] = now.isoformat()

                            # Monthly rollover for monthly_rx and monthly_tx
                            monthly_reset_iso = u.get("monthly_reset_at", "")
                            if not monthly_reset_iso:
                                db.update_user(
                                    uid,
                                    {
                                        "monthly_rx": 0,
                                        "monthly_tx": 0,
                                        "monthly_reset_at": now.isoformat(),
                                    },
                                )
                                logger.debug(
                                    f"Initialized monthly traffic for user {u['username']}"
                                )
                            else:
                                try:
                                    monthly_last = datetime.fromisoformat(monthly_reset_iso)
                                    if (
                                        now.month != monthly_last.month
                                        or now.year != monthly_last.year
                                    ):
                                        db.update_user(
                                            uid,
                                            {
                                                "monthly_rx": 0,
                                                "monthly_tx": 0,
                                                "monthly_reset_at": now.isoformat(),
                                            },
                                        )
                                        u["monthly_rx"] = 0
                                        u["monthly_tx"] = 0
                                        u["monthly_reset_at"] = now.isoformat()
                                        logger.debug(
                                            f"Monthly rollover for user {u['username']} "
                                            f"(was {monthly_reset_iso})"
                                        )
                                except Exception:
                                    pass

                            # Update both resettable and total traffic (combined RX+TX)
                            delta = rx_delta + tx_delta
                            new_used = u.get("traffic_used", 0) + delta
                            new_total = u.get("traffic_total", 0) + delta

                            # Update separate RX/TX totals
                            new_total_rx = u.get("traffic_total_rx", 0) + rx_delta
                            new_total_tx = u.get("traffic_total_tx", 0) + tx_delta

                            # Update monthly RX/TX
                            new_monthly_rx = u.get("monthly_rx", 0) + rx_delta
                            new_monthly_tx = u.get("monthly_tx", 0) + tx_delta

                            db.update_user(
                                uid,
                                {
                                    "traffic_used": new_used,
                                    "traffic_total": new_total,
                                    "traffic_total_rx": new_total_rx,
                                    "traffic_total_tx": new_total_tx,
                                    "monthly_rx": new_monthly_rx,
                                    "monthly_tx": new_monthly_tx,
                                },
                            )

                            # Update local cache
                            u["traffic_used"] = new_used
                            u["traffic_total"] = new_total
                            u["traffic_total_rx"] = new_total_rx
                            u["traffic_total_tx"] = new_total_tx
                            u["monthly_rx"] = new_monthly_rx
                            u["monthly_tx"] = new_monthly_tx
                            logger.debug(
                                f"Traffic updated for {u['username']}: "
                                f"rx={rx_delta}, tx={tx_delta}, "
                                f"total_rx={new_total_rx}, total_tx={new_total_tx}"
                            )

                            limit = u.get("traffic_limit", 0)
                            if limit > 0 and new_used >= limit and u.get("enabled", True):
                                if uid not in to_disable_uids:
                                    to_disable_uids.append(uid)

                            # Check expiration date
                            exp_str = u.get("expiration_date")
                            if exp_str and u.get("enabled", True):
                                try:
                                    exp_date = datetime.fromisoformat(exp_str)
                                    if now > exp_date:
                                        logger.info(
                                            f"Subscription expired for user {u['username']} (expired at {exp_str})"
                                        )
                                        if uid not in to_disable_uids:
                                            to_disable_uids.append(uid)
                                except:
                                    pass

            if to_disable_uids:
                logger.info(f"Traffic limit reached, disabling users: {to_disable_uids}")
                await perform_mass_operations(toggle_uids=[(uid, False) for uid in to_disable_uids])

            # --- 2. REMNAWAVE SYNC ---
            logger.info("Starting background Remnawave sync...")
            if db.get_setting("sync", {}).get("remnawave_sync_users"):
                count, msg = await sync_users_with_remnawave()
                logger.info(f"Background Remnawave sync finished: {count} users updated. {msg}")
            else:
                logger.info("Background Remnawave sync skipped (disabled in settings)")

        except Exception as e:
            logger.error(f"Error in periodic_background_tasks: {e}")

        # Wait 10 minutes before next sync
        await asyncio.sleep(600)


# ======================== PAGE ROUTES ========================


@app.get("/login", response_class=HTMLResponse)
async def login_page(request: Request):
    if get_current_user(request):
        return RedirectResponse(url="/", status_code=302)
    return tpl(request, "login.html")


@app.get("/set_lang/{lang}")
async def set_lang(lang: str, request: Request):
    ref = request.headers.get("referer", "/")
    response = RedirectResponse(url=ref)
    response.set_cookie(key="lang", value=lang, max_age=31536000)
    return response


@app.get("/logout")
async def logout(request: Request):
    request.session.clear()
    return RedirectResponse(url="/login", status_code=302)


@app.get("/", response_class=HTMLResponse)
async def index(request: Request):
    user = get_current_user(request)
    if not user:
        return RedirectResponse(url="/login", status_code=302)
    if user["role"] == "user":
        return RedirectResponse(url="/my", status_code=302)
    db = get_db()
    servers = db.get_all_servers()
    return tpl(request, "index.html", servers=servers)


@app.get("/server/{server_id}", response_class=HTMLResponse)
async def server_detail(request: Request, server_id: int):
    user = get_current_user(request)
    if not user:
        return RedirectResponse(url="/login", status_code=302)
    if user["role"] not in ("admin", "support"):
        return RedirectResponse(url="/my", status_code=302)
    db = get_db()
    server = db.get_server_by_id(server_id)
    if server is None:
        return RedirectResponse(url="/")
    users_list = db.get_all_users()
    return tpl(request, "server.html", server=server, server_id=server_id, users=users_list)


@app.get("/users", response_class=HTMLResponse)
async def users_page(request: Request):
    user = get_current_user(request)
    if not user:
        return RedirectResponse(url="/login", status_code=302)
    if user["role"] not in ("admin", "support"):
        return RedirectResponse(url="/my", status_code=302)
    db = get_db()
    users_list = db.get_all_users()
    # Count connections per user
    conns = db.get_all_connections()
    for u in users_list:
        u["connections_count"] = sum(1 for c in conns if c["user_id"] == u["id"])
    servers = db.get_all_servers()
    return tpl(request, "users.html", users=users_list, servers=servers)


@app.get("/my", response_class=HTMLResponse)
async def my_connections_page(request: Request):
    user = get_current_user(request)
    if not user:
        return RedirectResponse(url="/login", status_code=302)
    db = get_db()
    conns = db.get_connections_by_user(user["id"])
    # Enrich with server names
    for c in conns:
        sid = c.get("server_id", 0)
        srv = db.get_server_by_id(sid)
        if srv:
            c["server_name"] = srv.get("name", srv.get("host", ""))
        else:
            c["server_name"] = "Unknown"
    # Add explicit id to each server for template
    servers = db.get_all_servers()
    return tpl(request, "my_connections.html", connections=conns, servers=servers)


@app.get("/leaderboard", response_class=HTMLResponse)
async def leaderboard_page(request: Request):
    user = get_current_user(request)
    if not user:
        return RedirectResponse(url="/login", status_code=302)
    period = request.query_params.get("period", "all-time")
    if period not in ("all-time", "monthly"):
        period = "all-time"
    monthly_label: str | None = datetime.now().strftime("%B %Y") if period == "monthly" else None
    entries = get_leaderboard_entries(period)
    current_user_rank = None
    for e in entries:
        if e.get("username") == user.get("username"):
            current_user_rank = e["rank"]
            break
    return tpl(
        request,
        "leaderboard.html",
        entries=entries,
        period=period,
        current_user_rank=current_user_rank,
        monthly_label=monthly_label,
    )


# ======================== AUTH API ========================


@app.get("/api/auth/captcha")
async def api_captcha(request: Request):
    if not CaptchaGenerator:
        return JSONResponse({"error": "multicolorcaptcha is not installed"}, status_code=500)

    # 2 is a multiplier for the image resolution size
    generator = CaptchaGenerator(2)
    captcha = generator.gen_captcha_image(difficult_level=2)
    request.session["captcha_answer"] = captcha.characters

    img_bytes = io.BytesIO()
    captcha.image.save(img_bytes, format="PNG")
    img_bytes.seek(0)

    return StreamingResponse(img_bytes, media_type="image/png")


@app.post("/api/auth/login")
async def api_login(request: Request, req: LoginRequest):
    db = get_db()
    captcha_settings = db.get_setting("captcha", {})
    if captcha_settings.get("enabled") is True:
        answer = request.session.get("captcha_answer")
        lang = request.cookies.get("lang", "ru")
        if not answer or not req.captcha or answer.lower() != req.captcha.lower():
            request.session.pop("captcha_answer", None)
            return JSONResponse({"error": _t("invalid_captcha", lang)}, status_code=400)
        request.session.pop("captcha_answer", None)

    user = db.get_user_by_username(req.username)
    if user and verify_password(req.password, user["password_hash"]):
        lang = request.cookies.get("lang", "ru")
        if not user.get("enabled", True):
            return JSONResponse({"error": _t("account_disabled", lang)}, status_code=403)
        request.session["user_id"] = user["id"]
        return {"status": "success", "role": user["role"]}
    lang = request.cookies.get("lang", "ru")
    return JSONResponse({"error": _t("invalid_login", lang)}, status_code=401)


@app.get("/api/leaderboard")
async def api_leaderboard(request: Request):
    user = get_current_user(request)
    if not user:
        return JSONResponse({"error": "unauthorized"}, status_code=401)
    period = request.query_params.get("period", "all-time")
    if period not in ("all-time", "monthly"):
        period = "all-time"
    monthly_label: str | None = datetime.now().strftime("%B %Y") if period == "monthly" else None
    entries = get_leaderboard_entries(period)
    current_user_rank = None
    for e in entries:
        if e.get("username") == user.get("username"):
            current_user_rank = e["rank"]
            break
    return JSONResponse(
        {
            "period": period,
            "entries": entries,
            "current_user_rank": current_user_rank,
            "monthly_label": monthly_label,
        }
    )


# ======================== SERVER API (admin/support) ========================


def _check_admin(request):
    user = get_current_user(request)
    if not user or user["role"] not in ("admin", "support"):
        return None
    return user


@app.post("/api/servers/add")
async def api_add_server(request: Request, req: AddServerRequest):
    if not _check_admin(request):
        return JSONResponse({"error": "Forbidden"}, status_code=403)
    try:
        host = req.host.strip()
        username = req.username.strip()
        name = req.name.strip() or host
        if not host or not username:
            return JSONResponse({"error": "Host and username are required"}, status_code=400)
        if not req.password and not req.private_key:
            return JSONResponse({"error": "Password or SSH key is required"}, status_code=400)

        ssh = SSHManager(host, req.ssh_port, username, req.password, req.private_key)
        try:
            ssh.connect()
            server_info = ssh.test_connection()
            ssh.disconnect()
        except Exception as e:
            return JSONResponse(
                {"error": f"Connection failed: {_sanitize_error(str(e))}"}, status_code=400
            )

        server = {
            "name": name,
            "host": host,
            "ssh_port": req.ssh_port,
            "username": username,
            "password": req.password,
            "private_key": req.private_key,
            "server_info": server_info,
            "protocols": {},
        }
        db = get_db()
        db.create_server(server)
        server_count = db.get_server_count()
        return {
            "status": "success",
            "server_id": server_count - 1,
            "server_info": server_info,
        }
    except Exception as e:
        logger.exception("Error adding server")
        return JSONResponse({"error": _sanitize_error(str(e))}, status_code=500)


@app.post("/api/servers/{server_id}/delete")
async def api_delete_server(request: Request, server_id: int):
    if not _check_admin(request):
        return JSONResponse({"error": "Forbidden"}, status_code=403)
    try:
        db = get_db()
        if db.get_server_by_id(server_id) is None:
            return JSONResponse({"error": "Server not found"}, status_code=404)
        db.delete_server_by_index(server_id)
        return {"status": "success"}
    except Exception as e:
        return JSONResponse({"error": _sanitize_error(str(e))}, status_code=500)


@app.post("/api/servers/{server_id}/reboot")
async def api_reboot_server(request: Request, server_id: int):
    if not _check_admin(request):
        return JSONResponse({"error": "Forbidden"}, status_code=403)
    try:
        db = get_db()
        server = db.get_server_by_id(server_id)
        if server is None:
            return JSONResponse({"error": "Server not found"}, status_code=404)
        ssh = get_ssh(server)
        ssh.connect()
        try:
            ssh.run_sudo_command("nohup reboot > /dev/null 2>&1 &")
        except Exception:
            pass
        try:
            ssh.disconnect()
        except:
            pass
        return {"status": "success"}
    except Exception as e:
        logger.exception("Error rebooting server")
        return JSONResponse({"error": _sanitize_error(str(e))}, status_code=500)


@app.post("/api/servers/{server_id}/clear")
async def api_clear_server(request: Request, server_id: int):
    if not _check_admin(request):
        return JSONResponse({"error": "Forbidden"}, status_code=403)
    try:
        db = get_db()
        server = db.get_server_by_id(server_id)
        if server is None:
            return JSONResponse({"error": "Server not found"}, status_code=404)
        ssh = get_ssh(server)
        ssh.connect()
        containers = [
            "amnezia-awg",
            "amnezia-awg2",
            "amnezia-awg-legacy",
            "amnezia-xray",
            "telemt",
            "amnezia-dns",
        ]
        for c in containers:
            ssh.run_sudo_command(f"docker stop {c} || true")
            ssh.run_sudo_command(f"docker rm {c} || true")
        ssh.run_sudo_command("docker network rm amnezia-dns-net || true")
        ssh.run_sudo_command("rm -rf /opt/amnezia")

        db.update_server(server["id"], {"protocols": {}})
        ssh.disconnect()
        return {"status": "success"}
    except Exception as e:
        logger.exception("Error clearing server")
        return JSONResponse({"error": _sanitize_error(str(e))}, status_code=500)


@app.post("/api/servers/{server_id}/stats")
async def api_server_stats(request: Request, server_id: int):
    if not _check_admin(request):
        return JSONResponse({"error": "Forbidden"}, status_code=403)
    try:
        db = get_db()
        server = db.get_server_by_id(server_id)
        if server is None:
            return JSONResponse({"error": "Server not found"}, status_code=404)
        ssh = get_ssh(server)
        ssh.connect()
        stats = {}
        out, _, _ = ssh.run_command(
            "top -bn1 | grep 'Cpu(s)' | awk '{print $2}' | cut -d'%' -f1 2>/dev/null || "
            "awk '{u=$2+$4; t=$2+$4+$5; if(NR==1){pu=u;pt=t} else printf \"%.1f\", (u-pu)/(t-pt)*100}' "
            "<(grep 'cpu ' /proc/stat) <(sleep 0.5 && grep 'cpu ' /proc/stat) 2>/dev/null"
        )
        try:
            stats["cpu"] = round(float(out.strip().split("\n")[0]), 1)
        except (ValueError, IndexError):
            stats["cpu"] = 0
        out, _, _ = ssh.run_command("free -b | awk 'NR==2{printf \"%d %d\", $3, $2}'")
        try:
            parts = out.strip().split()
            used, total = int(parts[0]), int(parts[1])
            stats.update(
                ram_used=used,
                ram_total=total,
                ram_percent=round(used / total * 100, 1) if total > 0 else 0,
            )
        except (ValueError, IndexError):
            stats.update(ram_used=0, ram_total=0, ram_percent=0)
        out, _, _ = ssh.run_command("df -B1 / | awk 'NR==2{printf \"%d %d\", $3, $2}'")
        try:
            parts = out.strip().split()
            used, total = int(parts[0]), int(parts[1])
            stats.update(
                disk_used=used,
                disk_total=total,
                disk_percent=round(used / total * 100, 1) if total > 0 else 0,
            )
        except (ValueError, IndexError):
            stats.update(disk_used=0, disk_total=0, disk_percent=0)
        out, _, _ = ssh.run_command(
            "DEV=$(ip route | awk '/default/ {print $5}' | head -1); "
            'cat /proc/net/dev | awk -v dev="$DEV:" \'$1==dev{printf "%d %d", $2, $10}\''
        )
        try:
            parts = out.strip().split()
            stats["net_rx"], stats["net_tx"] = int(parts[0]), int(parts[1])
        except (ValueError, IndexError):
            stats["net_rx"] = stats["net_tx"] = 0
        out, _, _ = ssh.run_command("uptime -p 2>/dev/null || uptime")
        stats["uptime"] = out.strip()
        ssh.disconnect()
        return stats
    except Exception as e:
        logger.exception("Error getting server stats")
        return JSONResponse({"error": _sanitize_error(str(e))}, status_code=500)


@app.post("/api/servers/{server_id}/check")
async def api_check_server(request: Request, server_id: int):
    if not _check_admin(request):
        return JSONResponse({"error": "Forbidden"}, status_code=403)
    try:
        db = get_db()
        server = db.get_server_by_id(server_id)
        if server is None:
            return JSONResponse({"error": "Server not found"}, status_code=404)
        ssh = get_ssh(server)
        ssh.connect()
        # Just use awg's docker checker since it uses the same command
        manager = get_protocol_manager(ssh, "awg")
        status = {
            "connection": "ok",
            "docker_installed": manager.check_docker_installed(),
            "protocols": {},
        }

        changed = False
        if "protocols" not in server:
            server["protocols"] = {}

        import concurrent.futures

        def check_proto(proto):
            try:
                p_manager = get_protocol_manager(ssh, proto)
                result = p_manager.get_server_status(proto)
                db_proto = server.get("protocols", {}).get(proto, {})
                if not result.get("port") and db_proto.get("port"):
                    result["port"] = db_proto["port"]
                return proto, result, None
            except Exception as e:
                return proto, None, str(e)

        with concurrent.futures.ThreadPoolExecutor(max_workers=6) as executor:
            futures = [
                executor.submit(check_proto, p)
                for p in ["awg", "awg2", "awg_legacy", "xray", "telemt", "dns"]
            ]
            for future in concurrent.futures.as_completed(futures):
                proto, result, err = future.result()
                if err:
                    status["protocols"][proto] = {"error": err}
                else:
                    status["protocols"][proto] = result
                    if result.get("container_exists"):
                        if proto not in server["protocols"]:
                            server["protocols"][proto] = {
                                "installed": True,
                                "port": result.get("port", "55424"),
                                "awg_params": result.get("awg_params", {}),
                            }
                            changed = True
                    else:
                        if proto in server["protocols"]:
                            del server["protocols"][proto]
                            changed = True

        if changed:
            db.update_server(server["id"], {"protocols": server["protocols"]})

        ssh.disconnect()
        return status
    except Exception as e:
        logger.exception("Error checking server")
        return JSONResponse(
            {"error": _sanitize_error(str(e)), "connection": "failed"}, status_code=500
        )


@app.post("/api/servers/{server_id}/install")
async def api_install_protocol(request: Request, server_id: int, req: InstallProtocolRequest):
    if not _check_admin(request):
        return JSONResponse({"error": "Forbidden"}, status_code=403)
    try:
        db = get_db()
        server = db.get_server_by_id(server_id)
        if server is None:
            return JSONResponse({"error": "Server not found"}, status_code=404)
        if req.protocol not in ["awg", "awg2", "awg_legacy", "xray", "telemt", "dns"]:
            return JSONResponse({"error": "Invalid protocol type"}, status_code=400)

        ssh = get_ssh(server)
        ssh.connect()
        manager = get_protocol_manager(ssh, req.protocol)

        # Pass parameters to installer
        if req.protocol == "telemt":
            result = manager.install_protocol(
                protocol_type=req.protocol,
                port=req.port,
                tls_emulation=req.tls_emulation if req.tls_emulation is not None else True,
                tls_domain=req.tls_domain,
                max_connections=req.max_connections if req.max_connections is not None else 0,
            )
        elif req.protocol == "xray":
            result = manager.install_protocol(port=req.port)
        else:
            result = manager.install_protocol(req.protocol, port=req.port)

        new_protocols = dict(server.get("protocols", {}))
        new_protocols[req.protocol] = {
            "installed": True,
            "port": req.port,
            "awg_params": result.get("awg_params", {}),
        }
        db.update_server(server["id"], {"protocols": new_protocols})
        ssh.disconnect()
        return result
    except Exception as e:
        logger.exception("Error installing protocol")
        return JSONResponse({"error": _sanitize_error(str(e))}, status_code=500)


@app.post("/api/servers/{server_id}/uninstall")
async def api_uninstall_protocol(request: Request, server_id: int, req: ProtocolRequest):
    if not _check_admin(request):
        return JSONResponse({"error": "Forbidden"}, status_code=403)
    try:
        db = get_db()
        server = db.get_server_by_id(server_id)
        if server is None:
            return JSONResponse({"error": "Server not found"}, status_code=404)
        ssh = get_ssh(server)
        ssh.connect()
        manager = get_protocol_manager(ssh, req.protocol)
        if req.protocol == "xray":
            manager.remove_container()
        else:
            manager.remove_container(req.protocol)
        new_protocols = dict(server.get("protocols", {}))
        if req.protocol in new_protocols:
            del new_protocols[req.protocol]
            db.update_server(server["id"], {"protocols": new_protocols})
        ssh.disconnect()
        return {"status": "success"}
    except Exception as e:
        logger.exception("Error uninstalling protocol")
        return JSONResponse({"error": _sanitize_error(str(e))}, status_code=500)


CONTAINER_NAMES = {
    "awg": "amnezia-awg",
    "awg2": "amnezia-awg2",
    "awg_legacy": "amnezia-awg-legacy",
    "xray": "amnezia-xray",
    "telemt": "telemt",
    "dns": "amnezia-dns",
}


@app.post("/api/servers/{server_id}/container/toggle")
async def api_container_toggle(request: Request, server_id: int, req: ProtocolRequest):
    """Start or stop a protocol Docker container."""
    if not _check_admin(request):
        return JSONResponse({"error": "Forbidden"}, status_code=403)
    try:
        db = get_db()
        server = db.get_server_by_id(server_id)
        if server is None:
            return JSONResponse({"error": "Server not found"}, status_code=404)
        container = CONTAINER_NAMES.get(req.protocol)
        if not container:
            return JSONResponse({"error": "Unknown protocol"}, status_code=400)
        ssh = get_ssh(server)
        ssh.connect()
        # Check current state
        out, _, _ = ssh.run_sudo_command(
            f"docker inspect -f '{{{{.State.Running}}}}' {container} 2>/dev/null"
        )
        is_running = out.strip().lower() == "true"
        if is_running:
            ssh.run_sudo_command(f"docker stop {container}")
            action = "stopped"
        else:
            ssh.run_sudo_command(f"docker start {container}")
            action = "started"
        ssh.disconnect()
        return {"status": "success", "action": action, "container": container}
    except Exception as e:
        logger.exception("Error toggling container")
        return JSONResponse({"error": _sanitize_error(str(e))}, status_code=500)


@app.post("/api/servers/{server_id}/server_config")
async def api_server_config(request: Request, server_id: int, req: ProtocolRequest):
    """Get the raw server-side WireGuard/Xray configuration."""
    if not _check_admin(request):
        return JSONResponse({"error": "Forbidden"}, status_code=403)
    try:
        db = get_db()
        server = db.get_server_by_id(server_id)
        if server is None:
            return JSONResponse({"error": "Server not found"}, status_code=404)
        ssh = get_ssh(server)
        ssh.connect()
        if req.protocol == "xray":
            mgr = XrayManager(ssh)
            data_json = mgr._get_server_json()
            import json as _json

            config = _json.dumps(data_json, indent=2, ensure_ascii=False) if data_json else ""
        elif req.protocol == "telemt":
            from telemt_manager import TelemtManager

            mgr = TelemtManager(ssh)
            config = mgr._get_server_config()
        else:
            mgr = AWGManager(ssh)
            config = mgr._get_server_config(req.protocol)
        ssh.disconnect()
        return {"config": config}
    except Exception as e:
        logger.exception("Error getting server config")
        return JSONResponse({"error": _sanitize_error(str(e))}, status_code=500)


@app.post("/api/servers/{server_id}/server_config/save")
async def api_server_config_save(request: Request, server_id: int, req: ServerConfigSaveRequest):
    """Save the raw server-side WireGuard/Xray configuration and apply changes."""
    if not _check_admin(request):
        return JSONResponse({"error": "Forbidden"}, status_code=403)
    try:
        db = get_db()
        server = db.get_server_by_id(server_id)
        if server is None:
            return JSONResponse({"error": "Server not found"}, status_code=404)
        ssh = get_ssh(server)
        ssh.connect()
        if req.protocol == "xray":
            mgr = XrayManager(ssh)
            import json as _json

            try:
                data_json = _json.loads(req.config)
            except Exception:
                ssh.disconnect()
                return JSONResponse({"error": "Invalid JSON format"}, status_code=400)
            mgr._save_server_json(data_json)
        elif req.protocol == "telemt":
            from telemt_manager import TelemtManager

            mgr = TelemtManager(ssh)
            mgr.save_server_config(req.protocol, req.config)
        else:
            mgr = AWGManager(ssh)
            mgr.save_server_config(req.protocol, req.config)
        ssh.disconnect()
        return {"status": "success"}
    except Exception as e:
        logger.exception("Error saving server config")
        return JSONResponse({"error": _sanitize_error(str(e))}, status_code=500)


@app.get("/api/servers/{server_id}/connections")
async def api_get_connections(
    request: Request, server_id: int, protocol: str = Query(default="awg")
):
    if not protocol:
        protocol = "awg"
    if not _check_admin(request):
        return JSONResponse({"error": "Forbidden"}, status_code=403)
    try:
        db = get_db()
        server = db.get_server_by_id(server_id)
        if server is None:
            return JSONResponse({"error": "Server not found"}, status_code=404)
        ssh = get_ssh(server)
        ssh.connect()
        manager = get_protocol_manager(ssh, protocol)
        clients = manager.get_clients(protocol)
        ssh.disconnect()

        # Enrich with user info from user_connections
        user_conns = db.get_connections_by_server_and_protocol(server_id, protocol)
        users = db.get_all_users()
        users_map = {u["id"]: u for u in users}
        for client in clients:
            cid = client.get("clientId", "")
            for uc in user_conns:
                if uc.get("client_id") == cid:
                    uid = uc.get("user_id")
                    u = users_map.get(uid)
                    if u:
                        client["assigned_user"] = u["username"]
                        client["assigned_user_id"] = uid
                    break
        return {"clients": clients}
    except Exception as e:
        logger.exception("Error getting connections")
        return JSONResponse({"error": _sanitize_error(str(e))}, status_code=500)


@app.post("/api/servers/{server_id}/connections/add")
async def api_add_connection(request: Request, server_id: int, req: AddConnectionRequest):
    if not _check_admin(request):
        return JSONResponse({"error": "Forbidden"}, status_code=403)
    try:
        db = get_db()
        server = db.get_server_by_id(server_id)
        if server is None:
            return JSONResponse({"error": "Server not found"}, status_code=404)
        proto_info = server.get("protocols", {}).get(req.protocol, {})
        port = proto_info.get("port", "55424")
        ssh = get_ssh(server)
        ssh.connect()
        manager = get_protocol_manager(ssh, req.protocol)

        if req.protocol == "telemt":
            result = manager.add_client(
                req.protocol,
                req.name,
                server["host"],
                port,
                telemt_quota=req.telemt_quota,
                telemt_max_ips=req.telemt_max_ips,
                telemt_expiry=req.telemt_expiry,
            )
        else:
            result = manager.add_client(req.protocol, req.name, server["host"], port)
        ssh.disconnect()

        if result.get("config"):
            result["vpn_link"] = generate_vpn_link(result["config"])
        else:
            # API call failed — do not write to data.json, return error
            error_msg = result.get("error", "Failed to create connection")
            logger.error(f"Failed to add connection for {req.name}: {error_msg}")
            return JSONResponse({"error": error_msg}, status_code=500)

        # Link connection to user if specified
        if req.user_id:
            conn = {
                "id": str(uuid.uuid4()),
                "user_id": req.user_id,
                "server_id": server_id,
                "protocol": req.protocol,
                "client_id": result["client_id"],
                "name": req.name,
                "created_at": datetime.now().isoformat(),
            }
            db.create_connection(conn)

        return result
    except Exception as e:
        logger.exception("Error adding connection")
        return JSONResponse({"error": _sanitize_error(str(e))}, status_code=500)


@app.post("/api/servers/{server_id}/connections/remove")
async def api_remove_connection(request: Request, server_id: int, req: ConnectionActionRequest):
    if not _check_admin(request):
        return JSONResponse({"error": "Forbidden"}, status_code=403)
    try:
        db = get_db()
        server = db.get_server_by_id(server_id)
        if server is None:
            return JSONResponse({"error": "Server not found"}, status_code=404)
        if not req.client_id:
            return JSONResponse({"error": "Client ID is required"}, status_code=400)
        ssh = get_ssh(server)
        ssh.connect()
        manager = get_protocol_manager(ssh, req.protocol)
        manager.remove_client(req.protocol, req.client_id)
        ssh.disconnect()
        # Remove from user_connections
        db.delete_connection_by_client_id(req.client_id, server_id)
        return {"status": "success"}
    except Exception as e:
        logger.exception("Error removing connection")
        return JSONResponse({"error": _sanitize_error(str(e))}, status_code=500)


@app.post("/api/servers/{server_id}/connections/edit")
async def api_edit_connection(request: Request, server_id: int, req: EditConnectionRequest):
    if not _check_admin(request):
        return JSONResponse({"error": "Forbidden"}, status_code=403)
    try:
        db = get_db()
        server = db.get_server_by_id(server_id)
        if server is None:
            return JSONResponse({"error": "Server not found"}, status_code=404)

        ssh = get_ssh(server)
        ssh.connect()
        manager = get_protocol_manager(ssh, req.protocol)

        edit_params = {}
        if req.protocol == "telemt":
            edit_params["telemt_quota"] = req.telemt_quota
            edit_params["telemt_max_ips"] = req.telemt_max_ips
            edit_params["telemt_expiry"] = req.telemt_expiry

        result = manager.edit_client(req.protocol, req.client_id, edit_params)
        ssh.disconnect()
        return result
    except Exception as e:
        logger.exception("Error editing connection")
        return JSONResponse({"error": _sanitize_error(str(e))}, status_code=500)


@app.post("/api/servers/{server_id}/connections/config")
async def api_get_connection_config(request: Request, server_id: int, req: ConnectionActionRequest):
    user = get_current_user(request)
    if not user:
        return JSONResponse({"error": "Forbidden"}, status_code=403)
    try:
        db = get_db()
        server = db.get_server_by_id(server_id)
        if server is None:
            return JSONResponse({"error": "Server not found"}, status_code=404)
        # Users can only view their own connections
        if user["role"] == "user":
            all_conns = db.get_connections_by_server_and_protocol(server_id, req.protocol)
            owned = any(
                c
                for c in all_conns
                if c.get("client_id") == req.client_id and c.get("user_id") == user["id"]
            )
            if not owned:
                return JSONResponse({"error": "Forbidden"}, status_code=403)
        if not req.client_id:
            return JSONResponse({"error": "Client ID is required"}, status_code=400)
        proto_info = server.get("protocols", {}).get(req.protocol, {})
        port = proto_info.get("port", "55424")
        ssh = get_ssh(server)
        ssh.connect()
        manager = get_protocol_manager(ssh, req.protocol)
        config = manager.get_client_config(req.protocol, req.client_id, server["host"], port)
        ssh.disconnect()
        vpn_link = generate_vpn_link(config) if config else ""
        return {"config": config, "vpn_link": vpn_link}
    except Exception as e:
        logger.exception("Error getting connection config")
        return JSONResponse({"error": _sanitize_error(str(e))}, status_code=500)


@app.post("/api/servers/{server_id}/connections/toggle")
async def api_toggle_connection(request: Request, server_id: int, req: ToggleConnectionRequest):
    if not _check_admin(request):
        return JSONResponse({"error": "Forbidden"}, status_code=403)
    try:
        db = get_db()
        server = db.get_server_by_id(server_id)
        if server is None:
            return JSONResponse({"error": "Server not found"}, status_code=404)
        if not req.client_id:
            return JSONResponse({"error": "Client ID is required"}, status_code=400)
        ssh = get_ssh(server)
        ssh.connect()
        manager = get_protocol_manager(ssh, req.protocol)
        manager.toggle_client(req.protocol, req.client_id, req.enable)
        ssh.disconnect()
        status = "enabled" if req.enable else "disabled"
        return {"status": "success", "enabled": req.enable, "message": f"Connection {status}"}
    except Exception as e:
        logger.exception("Error toggling connection")
        return JSONResponse({"error": _sanitize_error(str(e))}, status_code=500)


# ======================== USER API (admin only) ========================


@app.get("/api/users")
async def api_list_users(request: Request, search: str = "", page: int = 1, size: int = 10):
    if not _check_admin(request):
        return JSONResponse({"error": "Forbidden"}, status_code=403)
    db = get_db()
    all_users = db.get_all_users()
    conns = db.get_all_connections()

    # Filter
    filtered = []
    search_lower = search.lower()
    for u in all_users:
        if search:
            match = (
                search_lower in u["username"].lower()
                or (u.get("email") and search_lower in u["email"].lower())
                or (u.get("telegramId") and search_lower in str(u["telegramId"]).lower())
            )
            if not match:
                continue
        filtered.append(u)

    total = len(filtered)
    start = (page - 1) * size
    end = start + size
    page_items = filtered[start:end]

    users = []
    for u in page_items:
        users.append(
            {
                "id": u["id"],
                "username": u["username"],
                "role": u["role"],
                "enabled": u.get("enabled", True),
                "created_at": u.get("created_at", ""),
                "telegramId": u.get("telegramId"),
                "email": u.get("email"),
                "description": u.get("description"),
                "connections_count": sum(1 for c in conns if c["user_id"] == u["id"]),
                "traffic_used": u.get("traffic_used", 0),
                "traffic_total": u.get("traffic_total", 0),
                "traffic_limit": u.get("traffic_limit", 0),
                "traffic_reset_strategy": u.get("traffic_reset_strategy", "never"),
                "last_reset_at": u.get("last_reset_at"),
                "share_enabled": u.get("share_enabled", False),
                "share_token": u.get("share_token"),
                "has_share_password": bool(u.get("share_password_hash")),
                "source": "Remnawave" if u.get("remnawave_uuid") else "Local",
            }
        )
    return {
        "users": users,
        "total": total,
        "page": page,
        "size": size,
        "pages": (total + size - 1) // size,
    }


@app.post("/api/users/add")
async def api_add_user(request: Request, req: AddUserRequest):
    cur = get_current_user(request)
    if not cur or cur["role"] != "admin":
        return JSONResponse({"error": "Forbidden"}, status_code=403)
    try:
        db = get_db()
        lang = request.cookies.get("lang", "ru")
        # Check duplicate
        existing = db.get_user_by_username(req.username)
        if existing:
            return JSONResponse({"error": _t("user_exists", lang)}, status_code=400)
        if req.role not in ("admin", "support", "user"):
            return JSONResponse({"error": "Invalid role"}, status_code=400)
        new_user = {
            "id": str(uuid.uuid4()),
            "username": req.username,
            "password_hash": hash_password(req.password),
            "role": req.role,
            "telegramId": req.telegramId,
            "email": req.email,
            "description": req.description,
            "traffic_limit": int(req.traffic_limit * 1024**3) if req.traffic_limit else 0,
            "traffic_reset_strategy": req.traffic_reset_strategy or "never",
            "traffic_used": 0,
            "traffic_total": 0,
            "last_reset_at": datetime.now().isoformat(),
            "expiration_date": req.expiration_date,
            "enabled": True,
            "created_at": datetime.now().isoformat(),
            "remnawave_uuid": None,
            "share_enabled": False,
            "share_token": secrets.token_urlsafe(16),
            "share_password_hash": None,
        }
        db.create_user(new_user)

        result = {"status": "success", "user_id": new_user["id"]}

        # Auto-create connection if server & protocol specified
        if req.server_id is not None and req.protocol:
            server = db.get_server_by_id(req.server_id)
            if server is not None:
                proto_info = server.get("protocols", {}).get(req.protocol, {})
                port = proto_info.get("port", "55424")
                conn_name = req.connection_name or f"{req.username}_vpn"
                ssh = get_ssh(server)
                ssh.connect()
                manager = get_protocol_manager(ssh, req.protocol)
                conn_result = manager.add_client(req.protocol, conn_name, server["host"], port)
                ssh.disconnect()

                if conn_result.get("config"):
                    conn = {
                        "id": str(uuid.uuid4()),
                        "user_id": new_user["id"],
                        "server_id": req.server_id,
                        "protocol": req.protocol,
                        "client_id": conn_result["client_id"],
                        "name": conn_name,
                        "created_at": datetime.now().isoformat(),
                    }
                    db.create_connection(conn)
                    result["connection_created"] = True
                    if conn_result.get("config"):
                        result["config"] = conn_result["config"]
                        result["vpn_link"] = generate_vpn_link(conn_result["config"])
                else:
                    # API call failed — skip writing connection, include error in response
                    error_msg = conn_result.get("error", "Failed to create auto-connection")
                    logger.warning(
                        f"Auto-connection creation failed for user {new_user['username']}: {error_msg}"
                    )
                    result["connection_created"] = False
                    result["connection_error"] = error_msg
        return result
    except Exception as e:
        logger.exception("Error adding user")
        return JSONResponse({"error": _sanitize_error(str(e))}, status_code=500)


@app.post("/api/users/{user_id}/update")
async def api_update_user(request: Request, user_id: str, req: UpdateUserRequest):
    if not _check_admin(request):
        return JSONResponse({"error": "Forbidden"}, status_code=403)
    try:
        db = get_db()
        user = db.get_user(user_id)
        if not user:
            return JSONResponse({"error": "User not found"}, status_code=404)

        updates = {}
        if req.telegramId is not None:
            updates["telegramId"] = req.telegramId
        if req.email is not None:
            updates["email"] = req.email
        if req.description is not None:
            updates["description"] = req.description
        if req.traffic_limit is not None:
            new_limit = int(req.traffic_limit * 1024**3)
            updates["traffic_limit"] = new_limit

        if req.traffic_reset_strategy is not None:
            updates["traffic_reset_strategy"] = req.traffic_reset_strategy
            updates["last_reset_at"] = datetime.now().isoformat()

        if req.expiration_date is not None:
            updates["expiration_date"] = req.expiration_date or None

        if req.password:
            updates["password_hash"] = hash_password(req.password)

        if updates:
            db.update_user(user_id, updates)

        # Auto re-enable if traffic limit increased beyond usage
        if req.traffic_limit is not None:
            new_limit = int(req.traffic_limit * 1024**3)
            if (
                new_limit > 0
                and user.get("traffic_used", 0) < new_limit
                and not user.get("enabled", True)
            ):
                await perform_toggle_user(user_id, True)

        return {"status": "success"}
    except Exception as e:
        logger.exception("Error updating user")
        return JSONResponse({"error": _sanitize_error(str(e))}, status_code=500)


@app.post("/api/users/{user_id}/delete")
async def api_delete_user(request: Request, user_id: str):
    cur = get_current_user(request)
    if not cur or cur["role"] != "admin":
        return JSONResponse({"error": "Forbidden"}, status_code=403)
    lang = request.cookies.get("lang", "ru")
    if cur["id"] == user_id:
        return JSONResponse({"error": _t("cannot_delete_self", lang)}, status_code=400)
    try:
        success = await perform_delete_user(user_id)
        if not success:
            return JSONResponse({"error": "User not found"}, status_code=404)
        return {"status": "success"}
    except Exception as e:
        logger.exception("Error deleting user")
        return JSONResponse({"error": _sanitize_error(str(e))}, status_code=500)


@app.post("/api/users/{user_id}/toggle")
async def api_toggle_user(request: Request, user_id: str, req: ToggleUserRequest):
    cur = get_current_user(request)
    if not cur or cur["role"] != "admin":
        return JSONResponse({"error": "Forbidden"}, status_code=403)
    try:
        success = await perform_toggle_user(user_id, req.enabled)
        if not success:
            return JSONResponse({"error": "User not found"}, status_code=404)
        return {"status": "success", "enabled": req.enabled}
    except Exception as e:
        logger.exception("Error toggling user")
        return JSONResponse({"error": _sanitize_error(str(e))}, status_code=500)


@app.post("/api/users/{user_id}/connections/add")
async def api_add_user_connection(request: Request, user_id: str, req: AddUserConnectionRequest):
    if not _check_admin(request):
        return JSONResponse({"error": "Forbidden"}, status_code=403)
    try:
        db = get_db()
        user = db.get_user(user_id)
        if not user:
            return JSONResponse({"error": "User not found"}, status_code=404)
        server = db.get_server_by_id(req.server_id)
        if server is None:
            return JSONResponse({"error": "Server not found"}, status_code=404)
        proto_info = server.get("protocols", {}).get(req.protocol, {})
        port = proto_info.get("port", "55424")
        ssh = get_ssh(server)
        await asyncio.to_thread(ssh.connect)
        manager = get_protocol_manager(ssh, req.protocol)

        if req.client_id:
            # Use existing client
            target_client_id = req.client_id
            # Retrieve config for existing client
            config = await asyncio.to_thread(
                manager.get_client_config, req.protocol, req.client_id, server["host"], port
            )
            result = {"client_id": target_client_id, "config": config}
        else:
            # Create new client
            result = await asyncio.to_thread(
                manager.add_client, req.protocol, req.name, server["host"], port
            )

        await asyncio.to_thread(ssh.disconnect)

        if result.get("config"):
            conn = {
                "id": str(uuid.uuid4()),
                "user_id": user_id,
                "server_id": req.server_id,
                "protocol": req.protocol,
                "client_id": result["client_id"],
                "name": req.name,
                "created_at": datetime.now().isoformat(),
            }
            db.create_connection(conn)

            resp = {"status": "success"}
            resp["config"] = result["config"]
            resp["vpn_link"] = generate_vpn_link(result["config"])
        else:
            # API call failed — do not write to data.json
            error_msg = result.get("error", "Failed to create connection")
            logger.error(f"Failed to create user connection for {req.name}: {error_msg}")
            resp = {"status": "error", "error": error_msg}
        return resp
    except Exception as e:
        logger.exception("Error adding user connection")
        return JSONResponse({"error": _sanitize_error(str(e))}, status_code=500)


@app.get("/api/users/{user_id}/connections")
async def api_get_user_connections(request: Request, user_id: str):
    user = get_current_user(request)
    if not user:
        return JSONResponse({"error": "Forbidden"}, status_code=403)
    # Users can only see their own, admin/support can see all
    if user["role"] == "user" and user["id"] != user_id:
        return JSONResponse({"error": "Forbidden"}, status_code=403)
    db = get_db()
    conns = db.get_connections_by_user(user_id)
    for c in conns:
        sid = c.get("server_id", 0)
        srv = db.get_server_by_id(sid)
        if srv:
            c["server_name"] = srv.get("name", "")
    return {"connections": conns}


# ======================== MY CONNECTIONS API (for user role) ========================


class MyAddConnectionRequest(BaseModel):
    server_id: int
    protocol: str = "awg"
    name: str = "Connection"
    telemt_quota: Optional[str] = None
    telemt_max_ips: Optional[int] = None
    telemt_expiry: Optional[str] = None


@app.get("/api/my/connections")
async def api_my_connections(request: Request):
    user = get_current_user(request)
    if not user:
        return JSONResponse({"error": "Forbidden"}, status_code=403)
    db = get_db()
    conns = db.get_connections_by_user(user["id"])
    for c in conns:
        sid = c.get("server_id", 0)
        srv = db.get_server_by_id(sid)
        if srv:
            c["server_name"] = srv.get("name", srv.get("host", ""))
        else:
            c["server_name"] = "Unknown"

    # Include effective limits for the frontend
    settings = db.get_all_settings()
    global_limits = settings.get("limits", {})
    user_limits = user.get("limits", {})
    effective_limits = {
        "max_connections": user_limits.get(
            "max_connections_per_user", global_limits.get("max_connections_per_user", 10)
        ),
        "current_connections": len(conns),
    }

    return {"connections": conns, "limits": effective_limits}


@app.post("/api/my/connections/add")
async def api_my_add_connection(request: Request, req: MyAddConnectionRequest):
    user = get_current_user(request)
    if not user:
        return JSONResponse({"error": "Forbidden"}, status_code=403)

    # Validate user account status
    if not user.get("enabled", True):
        return JSONResponse({"error": "Account is disabled"}, status_code=403)

    # Check expiration
    exp_str = user.get("expiration_date")
    if exp_str:
        try:
            exp_date = datetime.fromisoformat(exp_str)
            if datetime.now() > exp_date:
                return JSONResponse({"error": "Account expired"}, status_code=403)
        except Exception:
            pass  # Invalid date format, ignore

    # Check traffic limit
    traffic_limit = user.get("traffic_limit", 0)
    traffic_used = user.get("traffic_used", 0)
    if traffic_limit > 0 and traffic_used >= traffic_limit:
        return JSONResponse({"error": "Traffic limit exceeded"}, status_code=403)

    # ---- Rate Limiting & Connection Limits ----
    db = get_db()

    # Resolve limits
    settings = db.get_all_settings()
    global_limits = settings.get("limits", {})
    user_limits = user.get("limits", {})
    max_conns_per_user = user_limits.get(
        "max_connections_per_user", global_limits.get("max_connections_per_user", 10)
    )
    rate_limit_count = user_limits.get(
        "connection_rate_limit_count", global_limits.get("connection_rate_limit_count", 5)
    )
    rate_limit_window = user_limits.get(
        "connection_rate_limit_window", global_limits.get("connection_rate_limit_window", 60)
    )

    # Check per-user connection count
    user_conns = db.get_connections_by_user(user["id"])
    if len(user_conns) >= max_conns_per_user:
        logger.warning(
            f"Rate limit triggered (max connections): user_id={user['id']}, "
            f"current={len(user_conns)}, limit={max_conns_per_user}"
        )
        return JSONResponse(
            {
                "error": f"Maximum connections limit reached ({max_conns_per_user})",
                "limit": max_conns_per_user,
                "current": len(user_conns),
            },
            status_code=428,
        )

    # Check time-based rate limiting (sliding window)
    now = datetime.now()
    recent = db.get_recent_connections_log(user["id"], rate_limit_window)
    if len(recent) >= rate_limit_count:
        oldest = min(recent, key=lambda e: e["created_at"])
        try:
            oldest_ts = datetime.fromisoformat(oldest["created_at"])
            retry_after = int(rate_limit_window - (now - oldest_ts).total_seconds()) + 1
        except Exception:
            retry_after = rate_limit_window
        logger.warning(
            f"Rate limit triggered (sliding window): user_id={user['id']}, "
            f"recent_count={len(recent)}, limit={rate_limit_count}, window={rate_limit_window}s"
        )
        return JSONResponse(
            {
                "error": f"Connection rate limit exceeded ({rate_limit_count} per {rate_limit_window}s)",
                "retry_after": retry_after,
            },
            status_code=428,
            headers={"Retry-After": str(retry_after)},
        )

    # Validate server exists
    server = db.get_server_by_id(req.server_id)
    if server is None:
        return JSONResponse({"error": "Server not found"}, status_code=404)

    # Verify protocol is installed
    proto_info = server.get("protocols", {}).get(req.protocol, {})
    if not proto_info or not proto_info.get("installed", False):
        return JSONResponse(
            {"error": f"Protocol {req.protocol} is not installed on this server"},
            status_code=400,
        )

    # Check for duplicate connection name
    existing_names = {c.get("name", "") for c in user_conns}
    if req.name in existing_names:
        return JSONResponse(
            {
                "error": "duplicate_name",
                "message": "A connection with this name already exists.",
            },
            status_code=409,
        )

    port = proto_info.get("port", "55424")

    # Prune old connection log entries
    db.prune_connection_log(1000)

    ssh = None
    try:
        ssh = get_ssh(server)
        await asyncio.to_thread(ssh.connect)
        manager = get_protocol_manager(ssh, req.protocol)

        # Create client on remote server
        if req.protocol == "telemt":
            result = await asyncio.to_thread(
                manager.add_client,
                req.protocol,
                req.name,
                server["host"],
                port,
                telemt_quota=req.telemt_quota,
                telemt_max_ips=req.telemt_max_ips,
                telemt_expiry=req.telemt_expiry,
            )
        else:
            result = await asyncio.to_thread(
                manager.add_client, req.protocol, req.name, server["host"], port
            )

        if result.get("client_id"):
            new_conn = {
                "id": str(uuid.uuid4()),
                "user_id": user["id"],
                "server_id": req.server_id,
                "protocol": req.protocol,
                "client_id": result["client_id"],
                "name": req.name,
                "created_at": now.isoformat(),
            }
            db.create_connection(new_conn)
            db.log_connection_creation(user["id"])

            # Enrich connection with server_name for frontend
            new_conn["server_name"] = server.get("name", server.get("host", "Unknown"))

            # Build response
            response = {
                "status": "success",
                "connection": new_conn,
                "client_id": result["client_id"],
            }
            if result.get("config"):
                response["config"] = result["config"]
                response["vpn_link"] = generate_vpn_link(result["config"])
            return response
        else:
            return JSONResponse({"error": "Failed to create connection on server"}, status_code=500)
    except Exception as e:
        logger.exception("Error in api_my_add_connection")
        safe_msg = _sanitize_error(str(e), "Failed to create connection")
        return JSONResponse({"error": safe_msg}, status_code=500)
    finally:
        if ssh:
            await asyncio.to_thread(ssh.disconnect)


@app.post("/api/users/{user_id}/share/setup")
async def api_user_share_setup(user_id: str, req: ShareSetupRequest, request: Request):
    if not _check_admin(request):
        return JSONResponse({"error": "Forbidden"}, status_code=403)
    db = get_db()
    user = db.get_user(user_id)
    if not user:
        return JSONResponse({"error": "User not found"}, status_code=404)

    updates = {"share_enabled": req.enabled}
    if not user.get("share_token"):
        updates["share_token"] = secrets.token_urlsafe(16)
    if req.password:
        updates["share_password_hash"] = hash_password(req.password)
    elif req.password == "":  # Clear
        updates["share_password_hash"] = None

    db.update_user(user_id, updates)
    # Refresh user to get current share_token
    user = db.get_user(user_id)
    return {"status": "success", "share_token": user.get("share_token")}


@app.get("/share/{token}", response_class=HTMLResponse)
async def share_page(token: str, request: Request):
    db = get_db()
    user = db.get_user_by_share_token(token)
    if not user or not user.get("share_enabled"):
        lang = request.cookies.get("lang", "ru")
        return HTMLResponse(
            f"<h1>{_t('share_not_found', lang)}</h1><p>{_t('share_not_found_desc', lang)}</p>",
            status_code=404,
        )

    auth_session_key = f"share_auth_{token}"
    need_password = bool(user.get("share_password_hash")) and not request.session.get(
        auth_session_key
    )

    return tpl(
        request, "user_share.html", share_user=user, need_password=need_password, token=token
    )


@app.post("/api/share/{token}/auth")
async def api_share_auth(token: str, req: ShareAuthRequest, request: Request):
    db = get_db()
    user = db.get_user_by_share_token(token)
    if not user or not user.get("share_enabled"):
        return JSONResponse({"error": "Link expired or disabled"}, status_code=404)

    if verify_password(req.password, user.get("share_password_hash", "")):
        request.session[f"share_auth_{token}"] = True
        return {"status": "success"}
    else:
        lang = request.cookies.get("lang", "ru")
        return JSONResponse({"error": _t("wrong_share_password", lang)}, status_code=401)


@app.get("/api/share/{token}/connections")
async def api_share_connections(token: str, request: Request):
    db = get_db()
    user = db.get_user_by_share_token(token)
    if not user or not user.get("share_enabled"):
        return JSONResponse({"error": "Forbidden"}, status_code=403)

    if user.get("share_password_hash"):
        if not request.session.get(f"share_auth_{token}"):
            return JSONResponse({"error": "Unauthorized"}, status_code=401)

    conns = [dict(c) for c in db.get_connections_by_user(user["id"])]
    for c in conns:
        sid = c["server_id"]
        srv = db.get_server_by_id(sid)
        if srv:
            c["server_name"] = srv.get("name") or srv["host"]
        else:
            c["server_name"] = "Unknown"

    return {"connections": conns, "username": user["username"]}


@app.post("/api/share/{token}/config/{connection_id}")
async def api_share_config(token: str, connection_id: str, request: Request):
    db = get_db()
    user = db.get_user_by_share_token(token)
    if not user or not user.get("share_enabled"):
        return JSONResponse({"error": "Forbidden"}, status_code=403)

    if user.get("share_password_hash"):
        if not request.session.get(f"share_auth_{token}"):
            return JSONResponse({"error": "Unauthorized"}, status_code=401)

    conn = db.get_connection_by_id(connection_id)
    if not conn or conn.get("user_id") != user["id"]:
        return JSONResponse({"error": "Not found"}, status_code=404)

    try:
        sid = conn["server_id"]
        server = db.get_server_by_id(sid)
        if server is None:
            return JSONResponse({"error": "Server not found"}, status_code=404)
        proto_info = server.get("protocols", {}).get(conn["protocol"], {})
        port = proto_info.get("port", "55424")
        ssh = get_ssh(server)
        ssh.connect()
        # Use appropriate manager for the protocol
        manager = get_protocol_manager(ssh, conn["protocol"])
        config = manager.get_client_config(
            conn["protocol"], conn["client_id"], server["host"], port
        )
        ssh.disconnect()
        vpn_link = generate_vpn_link(config) if config else ""
        return {"config": config, "vpn_link": vpn_link}
    except Exception as e:
        logger.exception("Error getting shared config")
        return JSONResponse({"error": _sanitize_error(str(e))}, status_code=500)


@app.post("/api/my/connections/{connection_id}/config")
async def api_my_connection_config(request: Request, connection_id: str):
    user = get_current_user(request)
    if not user:
        return JSONResponse({"error": "Forbidden"}, status_code=403)
    try:
        db = get_db()
        conn = db.get_connection_by_id(connection_id)
        if not conn or conn.get("user_id") != user["id"]:
            return JSONResponse({"error": "Connection not found"}, status_code=404)
        sid = conn["server_id"]
        server = db.get_server_by_id(sid)
        if server is None:
            return JSONResponse({"error": "Server not found"}, status_code=404)
        proto_info = server.get("protocols", {}).get(conn["protocol"], {})
        port = proto_info.get("port", "55424")
        ssh = get_ssh(server)
        ssh.connect()
        # Use appropriate manager for the protocol
        manager = get_protocol_manager(ssh, conn["protocol"])
        config = manager.get_client_config(
            conn["protocol"], conn["client_id"], server["host"], port
        )
        ssh.disconnect()
        vpn_link = generate_vpn_link(config) if config else ""
        return {"config": config, "vpn_link": vpn_link}
    except Exception as e:
        logger.exception("Error getting my connection config")
        return JSONResponse({"error": _sanitize_error(str(e))}, status_code=500)


@app.get("/settings")
async def settings_page(request: Request):
    user = _check_admin(request)
    if not user:
        return RedirectResponse("/login")
    db = get_db()
    return tpl(
        request, "settings.html", settings=db.get_all_settings(), servers=db.get_all_servers()
    )


@app.get("/api/settings")
async def api_get_settings(request: Request):
    if not _check_admin(request):
        return JSONResponse({"error": "Forbidden"}, status_code=403)
    db = get_db()
    return db.get_all_settings()


@app.post("/api/settings/save")
async def save_settings(request: Request, payload: SaveSettingsRequest):
    if not _check_admin(request):
        return JSONResponse({"error": "Forbidden"}, status_code=403)
    db = get_db()
    db.update_setting("appearance", payload.appearance.dict())
    db.update_setting("sync", payload.sync.dict())
    db.update_setting("captcha", payload.captcha.dict())
    db.update_setting("telegram", payload.telegram.dict())
    db.update_setting("ssl", payload.ssl.dict())
    db.update_setting("limits", payload.limits.dict())
    db.update_setting("protocol_paths", payload.protocol_paths.dict())
    logger.info("Settings saved (including captcha and telegram)")

    # Handle bot start/stop based on new telegram settings
    tg_cfg = payload.telegram
    if tg_cfg.enabled and tg_cfg.token:
        if not tg_bot.is_running():
            logger.info("Starting Telegram bot (settings save)...")
            tg_bot.launch_bot(tg_cfg.token, db.load_data, generate_vpn_link)
    else:
        if tg_bot.is_running():
            logger.info("Stopping Telegram bot (settings save)...")
            asyncio.create_task(tg_bot.stop_bot())

    return {"status": "success", "bot_running": tg_bot.is_running()}


@app.post("/api/settings/telegram/toggle")
async def api_telegram_toggle(request: Request):
    """Quick enable/disable of the bot without a full settings save."""
    if not _check_admin(request):
        return JSONResponse({"error": "Forbidden"}, status_code=403)
    db = get_db()
    tg_cfg = db.get_setting("telegram", {})
    token = tg_cfg.get("token", "")
    if not token:
        return JSONResponse({"error": "Telegram token not set in settings"}, status_code=400)

    if tg_bot.is_running():
        await tg_bot.stop_bot()
        db.update_setting("telegram", {**tg_cfg, "enabled": False})
        return {"status": "stopped", "bot_running": False}
    else:
        tg_bot.launch_bot(token, db.load_data, generate_vpn_link)
        db.update_setting("telegram", {**tg_cfg, "enabled": True})
        return {"status": "started", "bot_running": True}


@app.post("/api/settings/sync_now")
async def api_sync_now(request: Request):
    if not _check_admin(request):
        return JSONResponse({"error": "Forbidden"}, status_code=403)
    count, msg = await sync_users_with_remnawave()
    return {"status": "success", "count": count, "message": msg}


@app.post("/api/settings/sync_delete")
async def api_sync_delete(request: Request):
    if not _check_admin(request):
        return JSONResponse({"error": "Forbidden"}, status_code=403)
    db = get_db()
    all_users = db.get_all_users()
    to_delete_ids = [u["id"] for u in all_users if u.get("remnawave_uuid")]
    if to_delete_ids:
        await perform_mass_operations(delete_uids=to_delete_ids)
    return {"status": "success", "count": len(to_delete_ids)}


@app.get("/api/servers/{server_id}/{protocol}/clients")
async def api_get_server_clients(request: Request, server_id: int, protocol: str):
    if not _check_admin(request):
        return JSONResponse({"error": "Forbidden"}, status_code=403)
    try:
        db = get_db()
        server = db.get_server_by_id(server_id)
        if server is None:
            return JSONResponse({"error": "Server not found"}, status_code=404)
        ssh = get_ssh(server)
        ssh.connect()
        manager = get_protocol_manager(ssh, protocol)
        clients = manager.get_clients(protocol)
        ssh.disconnect()

        # Filter: only show clients that are not assigned to anyone in the panel
        assigned_conns = db.get_connections_by_server_and_protocol(server_id, protocol)
        assigned_ids = {c["client_id"] for c in assigned_conns}

        filtered = []
        for c in clients:
            if c["clientId"] not in assigned_ids:
                filtered.append(
                    {
                        "id": c["clientId"],
                        "name": c.get("userData", {}).get("clientName", "Unnamed"),
                    }
                )

        return {"clients": filtered}
    except Exception as e:
        logger.exception("Error getting server clients")
        return JSONResponse({"error": _sanitize_error(str(e))}, status_code=500)


@app.get("/api/settings/backup/download")
async def api_backup_download(request: Request):
    if not _check_admin(request):
        return JSONResponse({"error": "Forbidden"}, status_code=403)
    try:
        db = get_db()
        backup_data = db.load_data()
        backup_json = json.dumps(backup_data, indent=2, ensure_ascii=False)
        return Response(
            content=backup_json,
            media_type="application/json",
            headers={"Content-Disposition": "attachment; filename=data.json"},
        )
    except Exception as e:
        logger.exception("Error creating backup")
        return JSONResponse({"error": _sanitize_error(str(e))}, status_code=500)


@app.post("/api/settings/backup/restore")
async def api_backup_restore(request: Request, file: UploadFile = File(...)):
    if not _check_admin(request):
        return JSONResponse({"error": "Forbidden"}, status_code=403)
    try:
        content = await file.read()
        if not content:
            return JSONResponse({"error": "Empty file"}, status_code=400)

        try:
            backup_data = json.loads(content)
        except json.JSONDecodeError:
            return JSONResponse({"error": "Invalid JSON format"}, status_code=400)

        # Basic structure validation
        required_keys = ["servers", "users"]
        missing = [k for k in required_keys if k not in backup_data]
        if missing:
            return JSONResponse(
                {"error": f'Invalid structure. Missing keys: {", ".join(missing)}'}, status_code=400
            )

        # Ensure types are correct
        if not isinstance(backup_data["servers"], list) or not isinstance(
            backup_data["users"], list
        ):
            return JSONResponse(
                {"error": "Invalid structure: servers and users must be lists"}, status_code=400
            )

        # Save the new data
        db = get_db()
        db.save_data(backup_data)

        # In a real app we might want to restart or re-init background tasks
        return {"status": "success"}
    except Exception as e:
        logger.exception("Error during restore")
        return JSONResponse({"error": _sanitize_error(str(e))}, status_code=500)


if __name__ == "__main__":
    db = get_db()
    settings = db.get_all_settings()
    ssl_conf = settings.get("ssl", {})

    cert_file = ssl_conf.get("cert_path")
    key_file = ssl_conf.get("key_path")

    # If text is provided, create temporary files
    temp_dir = os.path.join(os.getcwd(), "ssl_temp")
    if ssl_conf.get("enabled"):
        if ssl_conf.get("cert_text") or ssl_conf.get("key_text"):
            if not os.path.exists(temp_dir):
                os.makedirs(temp_dir)

            if ssl_conf.get("cert_text"):
                cert_file = os.path.join(temp_dir, "cert.pem")
                with open(cert_file, "w") as f:
                    f.write(ssl_conf["cert_text"].strip() + "\n")

            if ssl_conf.get("key_text"):
                key_file = os.path.join(temp_dir, "key.pem")
                with open(key_file, "w") as f:
                    f.write(ssl_conf["key_text"].strip() + "\n")

    uvicorn_kwargs = {"app": app, "host": "0.0.0.0", "port": ssl_conf.get("panel_port", 5000)}

    if ssl_conf.get("enabled") and cert_file and key_file:
        if os.path.exists(cert_file) and os.path.exists(key_file):
            logger.info(
                f"Starting panel with HTTPS enabled on domain: {ssl_conf.get('domain')} at port {uvicorn_kwargs['port']}"
            )
            uvicorn_kwargs["ssl_certfile"] = cert_file
            uvicorn_kwargs["ssl_keyfile"] = key_file
        else:
            logger.error("SSL certificates not found at specified paths. Starting with HTTP.")

    uvicorn.run(**uvicorn_kwargs)
