"""Leaderboard API route."""

import logging
from datetime import datetime

from fastapi import APIRouter, Request, Depends

from dependencies import get_current_user
from app.utils.helpers import get_leaderboard_entries
from schemas import LeaderboardResponse

logger = logging.getLogger(__name__)
router = APIRouter(tags=["leaderboard"])


@router.get("/api/leaderboard", response_model=LeaderboardResponse)
async def api_leaderboard(request: Request, user: dict = Depends(get_current_user)):
    period = request.query_params.get("period", "all-time")
    if period not in ("all-time", "monthly"):
        period = "all-time"
    monthly_label: str | None = datetime.now().strftime("%B %Y") if period == "monthly" else None
    entries = get_leaderboard_entries(period)
    current_user_rank = None
    for e in entries:
        if e.get("username") == user.get("username"):
            current_user_rank = e["rank"]
            break
    return {
        "period": period,
        "entries": entries,
        "current_user_rank": current_user_rank,
        "monthly_label": monthly_label,
    }
