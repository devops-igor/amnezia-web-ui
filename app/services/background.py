"""Background task functions for user operations, sync, and periodic cleanup."""

import asyncio
import logging
import secrets
import uuid
from datetime import datetime
from typing import List

import httpx

from config import get_db
from app.utils.helpers import get_ssh, get_protocol_manager

logger = logging.getLogger(__name__)


async def perform_delete_user(user_id: str):
    """Delete a user and their connections from all servers."""
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
                await asyncio.to_thread(ssh.connect)
                manager = get_protocol_manager(ssh, uc["protocol"])
                await asyncio.to_thread(manager.remove_client, uc["protocol"], uc["client_id"])
                await asyncio.to_thread(ssh.disconnect)
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


async def periodic_background_tasks():
    """Background task to sync traffic limits and Remnawave every 10 minutes."""
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

            ssh = None
            for server in servers:
                sid = server["id"]
                if sid not in conns_by_server:
                    continue
                try:
                    ssh = get_ssh(server)
                    await asyncio.to_thread(ssh.connect)
                    for proto in ["awg", "awg2", "awg_legacy", "xray", "telemt"]:
                        if proto in server.get("protocols", {}):
                            try:
                                manager = get_protocol_manager(ssh, proto)
                                clients = await asyncio.to_thread(manager.get_clients, proto)
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
                except asyncio.CancelledError:
                    raise
                except Exception as e:
                    sid = server["id"]
                    logger.error(f"Traffic sync error for server {sid}: {e}", exc_info=True)
                finally:
                    if ssh:
                        await asyncio.to_thread(ssh.disconnect)
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
                                except Exception:
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
                                except Exception:
                                    pass

            if to_disable_uids:
                logger.info(f"Traffic limit reached, disabling users: {to_disable_uids}")
                await perform_mass_operations(toggle_uids=[(uid, False) for uid in to_disable_uids])

            # --- 1b. TELEM QUOTA ENFORCEMENT ---
            # Explicitly disable over-quota telemt users (side effect removed from get_clients)
            for server in servers:
                sid = server["id"]
                if "telemt" not in server.get("protocols", {}):
                    continue
                try:
                    ssh = get_ssh(server)
                    await asyncio.to_thread(ssh.connect)
                    manager = get_protocol_manager(ssh, "telemt")
                    disabled = await asyncio.to_thread(manager.disable_overquota_users, "telemt")
                    if disabled:
                        logger.info(
                            f"Disabled {len(disabled)} over-quota users on telemt server {sid}"
                        )
                    await asyncio.to_thread(ssh.disconnect)
                except Exception as e:
                    logger.error(f"Error disabling over-quota users on server {sid}: {e}")

            # --- 2. REMNAWAVE SYNC ---
            logger.info("Starting background Remnawave sync...")
            if db.get_setting("sync", {}).get("remnawave_sync_users"):
                count, msg = await sync_users_with_remnawave()
                logger.info(f"Background Remnawave sync finished: {count} users updated. {msg}")
            else:
                logger.info("Background Remnawave sync skipped (disabled in settings)")

        except asyncio.CancelledError:
            logger.info("Background task cancelled")
            raise
        except Exception as e:
            logger.error(f"Error in periodic_background_tasks: {e}", exc_info=True)

        # Wait 10 minutes before next sync
        await asyncio.sleep(600)
