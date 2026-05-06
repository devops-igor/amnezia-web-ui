# flake8: noqa: F401
"""Background task orchestration — thin re-export layer.

Functions moved to dedicated service modules:
- app.services.user_operations (delete, toggle, mass ops)
- app.services.remnawave_sync (RemnaWave API sync)
"""

from app.services.user_operations import (
    perform_delete_user,
    perform_toggle_user,
    perform_mass_operations,
)
from app.services.remnawave_sync import sync_users_with_remnawave

__all__ = [
    "perform_delete_user",
    "perform_toggle_user",
    "perform_mass_operations",
    "sync_users_with_remnawave",
]
