"""Startup reconciliation — detect and clean up stale protocol connections."""

import logging

from app.utils.helpers import get_protocol_manager, get_ssh
from config import get_db

logger = logging.getLogger(__name__)


def cleanup_stale_protocols() -> None:
    """Check all servers for stale protocol entries and clean up orphaned connections.

    For each server in the DB:
    - SSH in and check which protocol containers exist
    - For any protocol in server.protocols whose container is gone:
      - Delete user_connections for that (server_id, protocol)
      - Remove the protocol from server.protocols
    - Log what was cleaned up

    One unreachable server does NOT block cleanup of others.
    """
    db = get_db()
    servers = db.get_all_servers()

    for server in servers:
        server_id = server["id"]
        protocols = server.get("protocols", {})
        if not protocols:
            continue

        try:
            ssh = get_ssh(server, db=db)
            ssh.connect()

            stale_protos = []
            for proto in protocols:
                try:
                    manager = get_protocol_manager(ssh, proto)
                    if proto == "awg":
                        exists = manager.check_protocol_installed(proto)
                    elif proto == "dns":
                        status = manager.get_server_status(proto)
                        exists = status.get("container_exists", False)
                    else:
                        exists = manager.check_protocol_installed()
                    if not exists:
                        stale_protos.append(proto)
                except Exception as e:
                    logger.warning("Failed to check %s on server %s: %s", proto, server_id, e)

            if stale_protos:
                for proto in stale_protos:
                    deleted = db.delete_connections_by_server_and_protocol(server_id, proto)
                    logger.info(
                        "Startup cleanup: removed %d stale %s connections for server %s",
                        deleted,
                        proto,
                        server_id,
                    )
                    if proto in protocols:
                        del protocols[proto]

                db.update_server(server_id, {"protocols": protocols})

            ssh.disconnect()
        except Exception as e:
            logger.warning(
                "Startup cleanup: could not reach server %s (%s): %s",
                server_id,
                server.get("host", "unknown"),
                e,
            )
