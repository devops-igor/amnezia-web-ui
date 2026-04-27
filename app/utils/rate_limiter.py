"""Shared rate limiter instance for the Amnezia Web Panel.

This module must be imported by both app.py and route modules
so that @limiter.limit() decorators all register to the same instance.
"""

from slowapi import Limiter

from app.utils.helpers import _get_client_ip

# Single shared Limiter instance — all routers and the app must use this.
# app.py will set app.state.limiter = limiter after creating the FastAPI app.
limiter = Limiter(key_func=_get_client_ip)
