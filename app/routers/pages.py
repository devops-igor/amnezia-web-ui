"""Page routes — HTML page rendering for index, server detail, users, my-connections, leaderboard."""

import logging
from datetime import datetime

from fastapi import APIRouter, Depends, Request
from fastapi.responses import HTMLResponse, RedirectResponse

from app.utils.helpers import get_leaderboard_entries
from app.utils.templates import tpl
from config import get_db
from dependencies import get_current_user, get_current_user_optional

logger = logging.getLogger(__name__)

router = APIRouter()


@router.get("/", response_class=HTMLResponse)
async def index(request: Request, user: dict = Depends(get_current_user)):
    if user["role"] == "user":
        return RedirectResponse(url="/my", status_code=302)
    db = get_db()
    servers = db.get_all_servers()
    return tpl(request, "index.html", servers=servers)


@router.get("/change-password", response_class=HTMLResponse)
async def change_password_page(request: Request):
    """Render the password change page. Supports ?forced=1 for mandatory changes."""
    user = get_current_user_optional(request)
    if not user:
        return RedirectResponse(url="/login", status_code=302)
    forced = request.query_params.get("forced", "0") == "1"
    return tpl(request, "change_password.html", forced=forced)


@router.get("/server/{server_id}", response_class=HTMLResponse)
async def server_detail(request: Request, server_id: int, user: dict = Depends(get_current_user)):
    if user["role"] not in ("admin", "support"):
        return RedirectResponse(url="/my", status_code=302)
    db = get_db()
    server = db.get_server_by_id(server_id)
    if server is None:
        return RedirectResponse(url="/")
    users_list = db.get_all_users()
    return tpl(request, "server.html", server=server, server_id=server_id, users=users_list)


@router.get("/users", response_class=HTMLResponse)
async def users_page(request: Request, user: dict = Depends(get_current_user)):
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


@router.get("/my", response_class=HTMLResponse)
async def my_connections_page(request: Request, user: dict = Depends(get_current_user)):
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


@router.get("/leaderboard", response_class=HTMLResponse)
async def leaderboard_page(request: Request, user: dict = Depends(get_current_user)):
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
