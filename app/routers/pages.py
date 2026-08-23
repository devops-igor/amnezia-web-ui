"""Page routes â€” HTML page rendering for index, server detail, users, my-connections, leaderboard."""

import logging
from datetime import datetime

from fastapi import APIRouter, Depends, Request
from fastapi.responses import HTMLResponse, RedirectResponse

from app.utils.helpers import get_leaderboard_entries, sanitize_server_for_user
from app.utils.templates import tpl
from app.core.config import get_db
from app.core.dependencies import get_current_user, get_current_user_optional

logger = logging.getLogger(__name__)

router = APIRouter()


@router.get("/setup", response_class=HTMLResponse)
async def setup_page(request: Request):
    """First-run setup wizard page â€” only accessible when no users exist."""
    db = get_db()
    if db.get_all_users():
        return RedirectResponse(url="/login", status_code=302)
    return tpl(request, "setup.html")


@router.get("/", response_class=HTMLResponse)
async def index(request: Request, user: dict = Depends(get_current_user)):
    if user["role"] == "user":
        return RedirectResponse(url="/my", status_code=302)
    db = get_db()
    servers = db.get_all_servers()
    from app.services.background_orchestrator import BackgroundTaskOrchestrator

    reachability = BackgroundTaskOrchestrator.get_cached_server_reachability()
    return tpl(request, "index.html", servers=servers, server_reachability=reachability)


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
    from app.services.background_orchestrator import BackgroundTaskOrchestrator

    reachability = BackgroundTaskOrchestrator.get_cached_server_reachability()
    server_reach = reachability.get(server_id) or reachability.get(str(server_id))
    return tpl(
        request,
        "server.html",
        server=server,
        server_id=server_id,
        users=users_list,
        server_reachability=reachability,
        reachability=server_reach,
    )


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
    from app.services.background_orchestrator import BackgroundTaskOrchestrator

    db = get_db()
    raw_servers = db.get_all_servers()
    cached_reach = BackgroundTaskOrchestrator.get_cached_server_reachability()

    servers_map = {}
    sanitized_servers = []
    simplified_reachability = {}
    for srv in raw_servers:
        sid = srv.get("id")
        r_info = cached_reach.get(sid) or cached_reach.get(str(sid))
        s_clean = sanitize_server_for_user(srv, r_info)
        sanitized_servers.append(s_clean)
        servers_map[sid] = s_clean
        simplified_reachability[sid] = {
            "status": s_clean["status"],
            "latency_ms": s_clean["latency_ms"],
            "last_checked": s_clean["last_checked"],
            "reachable": s_clean["reachable"],
        }

    conns = db.get_connections_by_user(user["id"])
    for c in conns:
        sid = c.get("server_id", 0)
        srv_clean = servers_map.get(sid)
        if srv_clean:
            c["server_name"] = srv_clean["name"]
            c["server_status"] = srv_clean["status"]
            c["server_reachable"] = srv_clean["reachable"]
        else:
            c["server_name"] = f"Server #{sid}" if sid else "Unknown"
            c["server_status"] = "unknown"
            c["server_reachable"] = False

    return tpl(
        request,
        "my_connections.html",
        connections=conns,
        servers=sanitized_servers,
        server_reachability=simplified_reachability,
    )


@router.get("/leaderboard", response_class=HTMLResponse)
async def leaderboard_page(request: Request, user: dict = Depends(get_current_user)):
    period = request.query_params.get("period", "all-time")
    if period not in ("all-time", "monthly", "last-month"):
        period = "all-time"
    if period == "monthly":
        monthly_label = datetime.now().strftime("%B %Y")
    elif period == "last-month":
        now = datetime.now()
        prev_month = 12 if now.month == 1 else now.month - 1
        prev_year = now.year if now.month > 1 else now.year - 1
        monthly_label = datetime(prev_year, prev_month, 1).strftime("%B %Y")
    else:
        monthly_label = None
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
