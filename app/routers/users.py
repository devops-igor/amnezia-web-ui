"""User API routes - all /api/users/* endpoints for managing users and their connections."""

import asyncio
import logging
import secrets
import uuid
from datetime import datetime

from fastapi import APIRouter, Request, Depends
from fastapi.responses import JSONResponse

from config import get_db
from dependencies import require_admin, get_current_user
from schemas import AddUserRequest, UpdateUserRequest, ToggleUserRequest, AddUserConnectionRequest
from app.utils.helpers import (
    _sanitize_error,
    generate_vpn_link,
    get_ssh,
    get_protocol_manager,
    _t,
    _get_lang,
    hash_password,
)

logger = logging.getLogger(__name__)
router = APIRouter(prefix="/api/users")


@router.get("")
async def api_list_users(
    request: Request,
    search: str = "",
    page: int = 1,
    size: int = 10,
    user: dict = Depends(require_admin),
):
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


@router.post("/add")
async def api_add_user(request: Request, req: AddUserRequest, user: dict = Depends(require_admin)):
    try:
        db = get_db()
        lang = _get_lang(request)
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
                await asyncio.to_thread(ssh.connect)
                manager = get_protocol_manager(ssh, req.protocol)
                conn_result = await asyncio.to_thread(
                    manager.add_client, req.protocol, conn_name, server["host"], port
                )
                await asyncio.to_thread(ssh.disconnect)

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


@router.post("/{user_id}/update")
async def api_update_user(
    request: Request, user_id: str, req: UpdateUserRequest, user: dict = Depends(require_admin)
):
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
                from app import perform_toggle_user

                await perform_toggle_user(user_id, True)

        return {"status": "success"}
    except Exception as e:
        logger.exception("Error updating user")
        return JSONResponse({"error": _sanitize_error(str(e))}, status_code=500)


@router.post("/{user_id}/delete")
async def api_delete_user(request: Request, user_id: str, user: dict = Depends(require_admin)):
    lang = _get_lang(request)
    if user["id"] == user_id:
        return JSONResponse({"error": _t("cannot_delete_self", lang)}, status_code=400)
    try:
        from app import perform_delete_user

        success = await perform_delete_user(user_id)
        if not success:
            return JSONResponse({"error": "User not found"}, status_code=404)
        return {"status": "success"}
    except Exception as e:
        logger.exception("Error deleting user")
        return JSONResponse({"error": _sanitize_error(str(e))}, status_code=500)


@router.post("/{user_id}/toggle")
async def api_toggle_user(
    request: Request, user_id: str, req: ToggleUserRequest, user: dict = Depends(require_admin)
):
    try:
        from app import perform_toggle_user

        success = await perform_toggle_user(user_id, req.enabled)
        if not success:
            return JSONResponse({"error": "User not found"}, status_code=404)
        return {"status": "success", "enabled": req.enabled}
    except Exception as e:
        logger.exception("Error toggling user")
        return JSONResponse({"error": _sanitize_error(str(e))}, status_code=500)


@router.post("/{user_id}/connections/add")
async def api_add_user_connection(
    request: Request,
    user_id: str,
    req: AddUserConnectionRequest,
    user: dict = Depends(require_admin),
):
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


@router.get("/{user_id}/connections")
async def api_get_user_connections(
    request: Request, user_id: str, user: dict = Depends(get_current_user)
):
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
