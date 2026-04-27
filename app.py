import os
import logging
import secrets
import uuid
import asyncio
from datetime import datetime

from fastapi.responses import JSONResponse
from fastapi.staticfiles import StaticFiles

from fastapi import FastAPI, Request
from starlette.middleware.sessions import SessionMiddleware
import uvicorn

from slowapi.errors import RateLimitExceeded

from starlette_csrf import CSRFMiddleware
import telegram_bot as tg_bot

from app.utils.helpers import (  # noqa: F401 - re-exports for backward compat
    _get_client_ip,
    generate_vpn_link,
    get_leaderboard_entries,
    hash_password,
    _t,
)
from config import (  # noqa: F401 - re-exports for backward compat
    TRANSLATIONS,
    _get_secret_key,
    load_translations,
    get_db,
    init_db,
)

from app.routers.auth import router as auth_router
from app.routers.connections import router as connections_router
from app.routers.pages import router as pages_router
from app.routers.servers import router as servers_router
from app.routers.settings import router as settings_router
from app.routers.share import router as share_router
from app.routers.users import router as users_router
from app.routers.leaderboard import router as leaderboard_router

from app.services.background import (  # noqa: F401 - re-exports for backward compat
    perform_delete_user,
    perform_toggle_user,
    perform_mass_operations,
    sync_users_with_remnawave,
    periodic_background_tasks,
)

# Re-export schemas for backward compatibility (tests import from app)
from schemas import (  # noqa: F401
    ChangePasswordRequest,
    InstallProtocolRequest,
)

# Configure logging
logging.basicConfig(level=logging.INFO)
logger = logging.getLogger(__name__)

app = FastAPI(title="Amnezia Web Panel")


from app.utils.rate_limiter import limiter  # noqa: E402

app.state.limiter = limiter


async def _rate_limit_exceeded_handler(request: Request, exc: RateLimitExceeded) -> JSONResponse:
    """Custom rate limit exceeded handler with i18n and logging."""
    lang = request.cookies.get("lang", "ru")
    logger.warning(
        "Rate limit exceeded: %s %s from %s",
        request.method,
        request.url.path,
        _get_client_ip(request),
    )
    return JSONResponse(
        {"error": _t("rate_limit_exceeded", lang)},
        status_code=429,
    )


app.add_exception_handler(RateLimitExceeded, _rate_limit_exceeded_handler)


async def _unauthorized_handler(request: Request, exc):
    """Redirect unauthenticated HTML requests to /login, return JSON for API requests."""
    from fastapi.responses import RedirectResponse

    accept = request.headers.get("accept", "")
    if "text/html" in accept:
        return RedirectResponse(url="/login", status_code=303)
    return JSONResponse({"detail": "Not authenticated"}, status_code=401)


app.add_exception_handler(401, _unauthorized_handler)

# Password change required middleware
# Blocks all /api/ requests (except auth endpoints) for users who must change their password
# MUST be added BEFORE SessionMiddleware so it runs AFTER SessionMiddleware on request path
# (add_middleware: last added = outermost = runs first on request)
_PASSWORD_CHANGE_ALLOWED_PATHS = {
    "/api/auth/login",
    "/api/auth/change-password",
    "/api/auth/captcha",
}


class PasswordChangeRequiredMiddleware:
    """ASGI middleware that blocks API access for users with password_change_required flag.

    Allows through: /api/auth/login, /api/auth/change-password, /api/auth/captcha,
    all non-API paths (static, pages), and unauthenticated requests.
    """

    def __init__(self, app):
        self.app = app

    async def __call__(self, scope, receive, send):
        if scope["type"] != "http":
            await self.app(scope, receive, send)
            return

        # Peek at the path without constructing a full Request yet
        path = scope.get("path", "")
        if not path.startswith("/api/") or path in _PASSWORD_CHANGE_ALLOWED_PATHS:
            await self.app(scope, receive, send)
            return

        # We need Request for session access - construct it
        from starlette.requests import Request as StarletteRequest

        request = StarletteRequest(scope, receive, send)
        user_id = request.session.get("user_id")
        if not user_id:
            await self.app(scope, receive, send)
            return

        try:
            db = get_db()
            user = db.get_user(user_id)
            if user and user.get("password_change_required", False):
                response = JSONResponse(
                    {"error": "Password change required", "password_change_required": True},
                    status_code=403,
                )
                await response(scope, receive, send)
                return
        except Exception:
            # If session or DB access fails, let the request through
            pass

        await self.app(scope, receive, send)


app.add_middleware(PasswordChangeRequiredMiddleware)

app.add_middleware(SessionMiddleware, secret_key=_get_secret_key())

# Add CSRF protection middleware
# safe_methods: GET, OPTIONS, HEAD, TRACE are considered safe and don't require CSRF
# sensitive_cookies={"session"}: CSRF enforcement only applies when the session cookie
# is present (i.e., the user is authenticated). Unauthenticated requests like login
# are exempt because CSRF protection is for authenticated state-changing requests.
app.add_middleware(
    CSRFMiddleware,
    secret=_get_secret_key(),
    safe_methods={"GET", "OPTIONS", "HEAD", "TRACE"},
    cookie_name="csrftoken",
    cookie_path="/",
    cookie_samesite="lax",
    header_name="x-csrf-token",
    sensitive_cookies={"session"},
)


# Password change required middleware
# Mount static files & templates
app.mount(
    "/static",
    StaticFiles(directory=os.path.join(os.path.dirname(__file__), "static")),
    name="static",
)


# Load translations from config module
load_translations()

# Register extracted route routers
app.include_router(auth_router)
app.include_router(connections_router)
app.include_router(pages_router)
app.include_router(servers_router)
app.include_router(settings_router)
app.include_router(share_router)
app.include_router(users_router)
app.include_router(leaderboard_router)


# ======================== Startup ========================


@app.on_event("startup")
async def startup():
    init_db()
    db = get_db()

    if not db.get_all_users():
        temp_password = secrets.token_urlsafe(12)
        db.create_user(
            {
                "id": str(uuid.uuid4()),
                "username": "admin",
                "password_hash": hash_password(temp_password),
                "role": "admin",
                "enabled": True,
                "password_change_required": True,
                "created_at": datetime.now().isoformat(),
            }
        )
        print(f"\n{'=' * 60}")
        print("  INITIAL ADMIN CREDENTIALS — SAVE THIS NOW")
        print("  Username: admin")
        print(f"  Password: {temp_password}")
        print("  You must change this password on first login.")
        print(f"{'=' * 60}\n")
        logger.info("Default admin created with random password (password_change_required=True)")
    else:
        logger.info("Existing users found, skipping default admin creation")

    # Start periodic background tasks
    asyncio.create_task(periodic_background_tasks())

    # Start Telegram bot if enabled
    tg_cfg = db.get_setting("telegram", {})
    if tg_cfg.get("enabled") and tg_cfg.get("token"):
        logger.info("Starting Telegram bot from saved settings...")
        tg_bot.launch_bot(tg_cfg["token"], db.load_data, generate_vpn_link)


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
