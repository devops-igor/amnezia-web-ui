"""Connection API routes - /api/my/connections/* endpoints."""

import asyncio
import logging
import uuid
from datetime import datetime

from fastapi import APIRouter, Depends, Request
from fastapi.responses import JSONResponse

from app.utils.helpers import _sanitize_error, generate_vpn_link, get_ssh, get_protocol_manager
from config import get_db
from dependencies import get_current_user
from schemas import MyAddConnectionRequest

logger = logging.getLogger(__name__)

router = APIRouter(prefix="/api/my/connections")


@router.get("")
async def api_my_connections(request: Request, user: dict = Depends(get_current_user)):
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


@router.post("/add")
async def api_my_add_connection(
    request: Request, req: MyAddConnectionRequest, user: dict = Depends(get_current_user)
):

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


@router.post("/{connection_id}/config")
async def api_my_connection_config(
    request: Request, connection_id: str, user: dict = Depends(get_current_user)
):
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
        await asyncio.to_thread(ssh.connect)
        # Use appropriate manager for the protocol
        manager = get_protocol_manager(ssh, conn["protocol"])
        config = await asyncio.to_thread(
            manager.get_client_config,
            conn["protocol"],
            conn["client_id"],
            server["host"],
            port,
        )
        await asyncio.to_thread(ssh.disconnect)
        vpn_link = generate_vpn_link(config) if config else ""
        return {"config": config, "vpn_link": vpn_link}
    except Exception as e:
        logger.exception("Error getting my connection config")
        return JSONResponse({"error": _sanitize_error(str(e))}, status_code=500)
