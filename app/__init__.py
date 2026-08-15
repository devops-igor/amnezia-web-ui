"""App package — exports the FastAPI application instance and middlewares."""

from app.main import (
    app,
    lifespan,
    SetupRedirectMiddleware,
    PasswordChangeRequiredMiddleware,
    _rate_limit_exceeded_handler,
)
from app.core.config import get_db, init_db

__all__ = [
    "app",
    "lifespan",
    "SetupRedirectMiddleware",
    "PasswordChangeRequiredMiddleware",
    "get_db",
    "init_db",
    "_rate_limit_exceeded_handler",
]
