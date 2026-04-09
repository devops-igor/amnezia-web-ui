# Task Assignment: Leaderboard Backend — API & Page Routes

## Metadata
- **Task ID:** TASK-02
- **Project:** Amnezia-Web-Panel
- **Assigned to:** py_bot
- **Assigned by:** pm_bot
- **Date:** 2026-04-09
- **Priority:** HIGH (depends on TASK-01)
- **Status:** PENDING

## Objective
Add the `/leaderboard` page route and `/api/leaderboard` JSON API endpoint that aggregate traffic data per user and return ranked results.

## Background/Context
TASK-01 adds `traffic_total_rx`, `traffic_total_tx`, `monthly_rx`, `monthly_tx` fields to user data. This task exposes that data through routes. The leaderboard is visible to all logged-in users (admin, support, user roles).

## Requirements

### Must Have
- [ ] Add `GET /leaderboard` page route — renders `leaderboard.html`, requires session auth (all roles)
- [ ] Add `GET /api/leaderboard?period=all-time|monthly` JSON API endpoint — requires session auth (all roles)
- [ ] Default period is `all-time` if query param is missing or invalid
- [ ] All-time leaderboard: aggregate `traffic_total_rx` + `traffic_total_tx` per user, sort descending by total, include username and rank
- [ ] Monthly leaderboard: aggregate `monthly_rx` + `monthly_tx` per user, sort descending by total, include username and rank
- [ ] Include `current_user_rank` in API response — the rank of the logged-in user
- [ ] Include a `format_traffic()` helper function (or reuse existing) to format bytes as human-readable strings for the template
- [ ] Auth check: redirect to `/login` if not authenticated (page route), return 401 (API route)

### Nice to Have
- [ ] Expose `current_user_rank` in the page template context as well

## Technical Constraints
- Language: Python
- Framework: FastAPI
- Auth: check `request.session.get("authenticated")` — same pattern as all other protected routes
- Data: read from `load_data()` — same pattern as existing routes
- Response shape for API (JSON):
```json
{
  "period": "all-time",
  "entries": [
    {
      "rank": 1,
      "username": "alice",
      "download": 5368709120,
      "upload": 1073741824,
      "total": 6442450944
    }
  ],
  "current_user_rank": 3
}
```
- Page route passes the same data to Jinja2 template
- Follow PYTHON_STANDARDS.md: type hints, black, flake8, no business logic in routes (delegate to helper)

## Acceptance Criteria
1. `GET /leaderboard` returns rendered HTML page (200) for authenticated users
2. `GET /leaderboard` redirects to `/login` for unauthenticated users
3. `GET /api/leaderboard?period=all-time` returns JSON with all-time traffic per user, sorted descending
4. `GET /api/leaderboard?period=monthly` returns JSON with current month traffic per user, sorted descending
5. Default period is `all-time` when no query param provided
6. Response includes `current_user_rank` — rank of the logged-in user
7. Byte values are raw integers (not pre-formatted strings)
8. Users with 0 traffic are included in the response (ranked last)
9. Auth check matches existing pattern in the codebase
10. 401 returned for unauthenticated API requests

## Handoff Requirements
- [ ] All tests passing (`pytest -v`)
- [ ] Linters clean (`black --check .` and `flake8 .`)
- [ ] Security scanners clean (`pip-audit`)
- [ ] DEV_HANDOVER.md created with output
- [ ] WORKLOG.md appended

## Notes
- Depends on: TASK-01 (data model must have RX/TX fields)
- Reference SPEC: `/home/igor/Amnezia-Web-Panel/leaderboard/SPEC.md` section 4
- Look at existing routes like `GET /users` or `GET /my` for the auth + data loading pattern
- The route handler should be a thin wrapper — traffic aggregation logic in a helper function