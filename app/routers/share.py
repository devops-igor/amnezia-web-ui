"""Share API routes - share token access and user share setup endpoints."""

import asyncio
import logging
import secrets

from fastapi import APIRouter, Depends, Request
from fastapi.responses import HTMLResponse, JSONResponse

from app.utils.helpers import (
    _get_lang,
    _sanitize_error,
    _t,
    generate_vpn_link,
    get_ssh,
    get_protocol_manager,
    hash_password,
    verify_password,
)
from app.utils.rate_limiter import limiter
from app.utils.templates import tpl
from config import get_db
from dependencies import require_admin
from schemas import ShareAuthRequest, ShareSetupRequest

logger = logging.getLogger(__name__)

router = APIRouter(tags=["share"])


@router.post("/api/users/{user_id}/share/setup")
async def api_user_share_setup(
    user_id: str, req: ShareSetupRequest, request: Request, user: dict = Depends(require_admin)
):
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


@router.get("/share/{token}", response_class=HTMLResponse)
@limiter.limit("10/minute")
async def share_page(token: str, request: Request):
    db = get_db()
    user = db.get_user_by_share_token(token)
    if not user or not user.get("share_enabled"):
        lang = _get_lang(request)
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


@router.post("/api/share/{token}/auth")
@limiter.limit("10/minute")
async def api_share_auth(token: str, req: ShareAuthRequest, request: Request):
    db = get_db()
    user = db.get_user_by_share_token(token)
    if not user or not user.get("share_enabled"):
        return JSONResponse({"error": "Link expired or disabled"}, status_code=404)

    if verify_password(req.password, user.get("share_password_hash", "")):
        request.session[f"share_auth_{token}"] = True
        return {"status": "success"}
    else:
        lang = _get_lang(request)
        return JSONResponse({"error": _t("wrong_share_password", lang)}, status_code=401)


@router.get("/api/share/{token}/connections")
@limiter.limit("20/minute")
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


@router.post("/api/share/{token}/config/{connection_id}")
@limiter.limit("10/minute")
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
        logger.exception("Error getting shared config")
        return JSONResponse({"error": _sanitize_error(str(e))}, status_code=500)
