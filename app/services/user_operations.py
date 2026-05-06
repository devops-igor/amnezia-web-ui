"""User server operations — SSH-based delete, toggle, and mass operations."""

import asyncio
import logging
import uuid
from datetime import datetime
from typing import List

from config import get_db
from app.utils.helpers import get_ssh, get_protocol_manager

logger = logging.getLogger(__name__)


async def perform_delete_user(user_id: str):
    """Delete a user and their connections from all servers.

    Groups connections by server_id so that only one SSH session is opened
    per unique server — not one per connection (fixes Issue #132).
    """
    db = get_db()
    user = db.get_user(user_id)
    if not user:
        return False

    user_conns = db.get_connections_by_user(user_id)

    # Group connections by server_id (same pattern as perform_mass_operations)
    server_conns: dict[str, list[dict]] = {}
    for uc in user_conns:
        sid = uc["server_id"]
        if sid not in server_conns:
            server_conns[sid] = []
        server_conns[sid].append(uc)

    for sid, conns in server_conns.items():
        server = db.get_server_by_id(sid)
        if not server:
            continue
        try:
            ssh = get_ssh(server)
            await asyncio.to_thread(ssh.connect)
            for uc in conns:
                manager = get_protocol_manager(ssh, uc["protocol"])
                await asyncio.to_thread(manager.remove_client, uc["protocol"], uc["client_id"])
            await asyncio.to_thread(ssh.disconnect)
        except Exception as e:
            logger.warning(
                "Failed to remove connections on server %s during user delete: %s", sid, e
            )

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
            logger.error("Mass ops failed for server %s: %s", srv_id, e)

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
