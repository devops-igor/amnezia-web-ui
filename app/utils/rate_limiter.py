"""Shared rate limiter instance for the Amnezia Web Panel.

This module must be imported by both app.py and route modules
so that @limiter.limit() decorators all register to the same instance.
"""

import os

from slowapi import Limiter

from app.utils.helpers import _get_client_ip

# When E2E_TESTING=true, disable rate limiting entirely so that
# sequential E2E tests never hit 429s from budget exhaustion.
# Rate limiting is still covered by unit tests (tests/test_rate_limiting.py).
_rate_limiting_enabled = os.environ.get("E2E_TESTING", "").lower() != "true"

# Single shared Limiter instance — all routers and the app must use this.
# app.py will set app.state.limiter = limiter after creating the FastAPI app.
limiter = Limiter(key_func=_get_client_ip, enabled=_rate_limiting_enabled)
