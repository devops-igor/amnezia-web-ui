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
    if period not in ("all-time", "monthly", "last-month"):
        period = "all-time"
    if period == "monthly":
        monthly_label = datetime.now().strftime("%B %Y")
    elif period == "last-month":
        now = datetime.now()
        prev_month = 12 if now.month == 1 else now.month - 1
        prev_year = now.year if now.month > 1 else now.year - 1
        monthly_label = datetime(prev_year, prev_month, 1).strftime("%B %Y")
    else:
        monthly_label = None
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
