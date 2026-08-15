"""App package — exports the FastAPI application instance and middlewares."""

from app.main import app, lifespan, SetupRedirectMiddleware, PasswordChangeRequiredMiddleware
from app.core.config import get_db

__all__ = [
    "app",
    "lifespan",
    "SetupRedirectMiddleware",
    "PasswordChangeRequiredMiddleware",
    "get_db",
]
