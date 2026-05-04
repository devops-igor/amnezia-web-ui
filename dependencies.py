"""FastAPI dependency functions for authentication and authorization.

Provides reusable Depends() functions for route protection:
- get_current_user: Returns authenticated user or raises 401
- require_admin: Returns admin/support user or raises 403
- get_current_user_optional: Returns user dict or None for template routes
"""

from fastapi import Depends, HTTPException, Request, status


async def get_current_user(request: Request) -> dict:
    """Get the currently authenticated user from request.

    Raises:
        HTTPException: 401 if not authenticated
    """
    from config import get_db

    user_id = request.session.get("user_id")
    if not user_id:
        raise HTTPException(
            status_code=status.HTTP_401_UNAUTHORIZED,
            detail="Not authenticated",
        )
    db = get_db()
    user = db.get_user(user_id)
    if not user:
        raise HTTPException(
            status_code=status.HTTP_401_UNAUTHORIZED,
            detail="Not authenticated",
        )
    return user


async def require_admin(user: dict = Depends(get_current_user)) -> dict:
    """Require the current user to be an admin or support.

    Raises:
        HTTPException: 403 if user is not admin or support
    """
    if user.get("role") not in ("admin", "support"):
        raise HTTPException(
            status_code=status.HTTP_403_FORBIDDEN,
            detail="Admin access required",
        )
    return user


def get_current_user_optional(request: Request) -> dict | None:
    """Get the currently authenticated user if any, else None.

    For template routes that need to render differently for guests.
    """
    from config import get_db

    user_id = request.session.get("user_id")
    if not user_id:
        return None
    db = get_db()
    return db.get_user(user_id)
