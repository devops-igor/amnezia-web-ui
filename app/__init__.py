"""App package — exports the FastAPI application instance and middlewares."""

import importlib.util
import os
import sys

_root = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
_app_py = os.path.join(_root, "app.py")

# Load root app.py module cleanly using importlib
_spec = importlib.util.spec_from_file_location("_root_app", _app_py)
_mod = importlib.util.module_from_spec(_spec)
sys.modules["_root_app"] = _mod
_spec.loader.exec_module(_mod)

app = _mod.app
lifespan = _mod.lifespan
SetupRedirectMiddleware = _mod.SetupRedirectMiddleware
PasswordChangeRequiredMiddleware = _mod.PasswordChangeRequiredMiddleware
get_db = _mod.get_db

__all__ = [
    "app",
    "lifespan",
    "SetupRedirectMiddleware",
    "PasswordChangeRequiredMiddleware",
    "get_db",
]
