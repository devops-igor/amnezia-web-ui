"""Template rendering utilities — shared Jinja2Templates instance and tpl() helper."""

import json
import os

from fastapi.templating import Jinja2Templates

from utils import format_bytes
from app.utils.helpers import _get_lang, _t
from config import get_db, TRANSLATIONS
from dependencies import get_current_user_optional

# Shared Jinja2 templates instance — routers import this.
templates = Jinja2Templates(
    directory=os.path.join(os.path.dirname(os.path.dirname(os.path.dirname(__file__))), "templates")
)


# Telegram bot references resolved lazily to avoid circular imports at module level.
def _get_tg_bot():
    import telegram_bot as tg_bot

    return tg_bot


def tpl(request, template, **kwargs):
    """Render a Jinja2 template with the standard context variables."""
    db = get_db()
    settings = db.get_all_settings()
    lang = _get_lang(request)
    tg_bot = _get_tg_bot()
    ctx = {
        "request": request,
        "current_user": get_current_user_optional(request),
        "site_settings": settings.get("appearance", {}),
        "captcha_settings": settings.get("captcha", {}),
        "telegram_settings": settings.get("telegram", {}),
        "bot_running": tg_bot.is_running(),
        "lang": lang,
        "_": lambda text_id: _t(text_id, lang),
        "translations_json": json.dumps(TRANSLATIONS.get(lang, TRANSLATIONS.get("en", {}))),
        "all_translations_json": json.dumps(TRANSLATIONS),
        "format_bytes": format_bytes,
        "csrf_token": request.cookies.get("csrftoken", ""),
    }
    ctx.update(kwargs)
    return templates.TemplateResponse(template, ctx)
