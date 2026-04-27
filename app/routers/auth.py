"""Auth routes — login page, logout, language switching, CAPTCHA, and password change."""

import io
import logging

from fastapi import APIRouter, Depends, Request
from fastapi.responses import HTMLResponse, JSONResponse, RedirectResponse, StreamingResponse

from app.utils.helpers import hash_password, verify_password, _get_lang, _t
from app.utils.templates import tpl
from config import get_db
from dependencies import get_current_user, get_current_user_optional
from schemas import ChangePasswordRequest, LoginRequest

try:
    from multicolorcaptcha import CaptchaGenerator
except ImportError:
    CaptchaGenerator = None

from app.utils.rate_limiter import limiter

logger = logging.getLogger(__name__)

router = APIRouter()


# ---- Page routes ----


@router.get("/login", response_class=HTMLResponse)
async def login_page(request: Request):
    user = get_current_user_optional(request)
    if user:
        return RedirectResponse(url="/", status_code=302)
    return tpl(request, "login.html")


@router.get("/set_lang/{lang}")
async def set_lang(lang: str, request: Request):
    ref = request.headers.get("referer", "/")
    response = RedirectResponse(url=ref)
    response.set_cookie(key="lang", value=lang, max_age=31536000)
    return response


@router.get("/logout")
async def logout(request: Request):
    request.session.clear()
    return RedirectResponse(url="/login", status_code=302)


# ---- Auth API ----


@router.get("/api/auth/captcha")
async def api_captcha(request: Request):
    if not CaptchaGenerator:
        return JSONResponse({"error": "multicolorcaptcha is not installed"}, status_code=500)

    # 2 is a multiplier for the image resolution size
    generator = CaptchaGenerator(2)
    captcha = generator.gen_captcha_image(difficult_level=2)
    request.session["captcha_answer"] = captcha.characters

    img_bytes = io.BytesIO()
    captcha.image.save(img_bytes, format="PNG")
    img_bytes.seek(0)

    return StreamingResponse(img_bytes, media_type="image/png")


@router.post("/api/auth/login")
@limiter.limit("5/minute")
async def api_login(request: Request, req: LoginRequest):
    db = get_db()
    captcha_settings = db.get_setting("captcha", {})
    if captcha_settings.get("enabled") is True:
        answer = request.session.get("captcha_answer")
        lang = _get_lang(request)
        if not answer or not req.captcha or answer.lower() != req.captcha.lower():
            request.session.pop("captcha_answer", None)
            return JSONResponse({"error": _t("invalid_captcha", lang)}, status_code=400)
        request.session.pop("captcha_answer", None)

    user = db.get_user_by_username(req.username)
    if user and verify_password(req.password, user["password_hash"]):
        lang = _get_lang(request)
        if not user.get("enabled", True):
            return JSONResponse({"error": _t("account_disabled", lang)}, status_code=403)
        request.session["user_id"] = user["id"]
        return {
            "status": "success",
            "role": user["role"],
            "password_change_required": user.get("password_change_required", False),
        }
    lang = _get_lang(request)
    return JSONResponse({"error": _t("invalid_login", lang)}, status_code=401)


@router.post("/api/auth/change-password")
async def api_change_password(
    request: Request, req: ChangePasswordRequest, user: dict = Depends(get_current_user)
):
    """Change password for the currently authenticated user.

    Clears password_change_required flag on success.
    """

    if not verify_password(req.current_password, user["password_hash"]):
        return JSONResponse({"error": "Current password is incorrect"}, status_code=400)

    if req.new_password != req.confirm_password:
        return JSONResponse({"error": "New passwords do not match"}, status_code=400)

    if len(req.new_password) < 8:
        return JSONResponse({"error": "Password must be at least 8 characters"}, status_code=400)

    db = get_db()
    db.update_user(
        user["id"],
        {
            "password_hash": hash_password(req.new_password),
            "password_change_required": False,
        },
    )
    logger.info(f"User '{user['username']}' changed their password")
    return {"status": "success"}
