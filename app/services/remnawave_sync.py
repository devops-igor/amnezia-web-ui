"""RemnaWave API sync — synchronize users with external RemnaWave service."""

import logging
import secrets
import uuid
from datetime import datetime

import httpx

from config import get_db
from app.services.user_operations import perform_mass_operations

logger = logging.getLogger(__name__)


async def sync_users_with_remnawave():
    """Sync users with Remnawave external service."""
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
                logger.info("Fetched %s / %s users from Remnawave...", len(rw_users), total_count)

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
                logger.info("Removing %s users deleted in Remnawave", len(to_delete_ids))
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
                    "Executing mass ops for Remnawave sync: toggle=%s, create=%s",
                    len(to_toggle),
                    len(to_create_conns),
                )
                await perform_mass_operations(toggle_uids=to_toggle, create_conns=to_create_conns)

            return synced_count, "Successfully synchronized with Remnawave"

    except Exception as e:
        logger.exception("Synchronization error")
        return 0, f"Error: {str(e)}"
