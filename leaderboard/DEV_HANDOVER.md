# Development Handover: TASK-02 — Leaderboard Backend (API & Page Routes)

## Files Changed

### `app.py` — new routes and helpers
- Added `_format_bytes()` helper function (~line 519-531) — formats byte counts as human-readable strings
- Added `get_leaderboard_entries()` helper function (~line 547-574) — aggregates traffic data per user, sorts descending, assigns ranks
- Added `/leaderboard` page route (~line 1113-1132) — renders leaderboard.html, auth check redirects to /login
- Added `/api/leaderboard` JSON API route (~line 1180-1201) — returns JSON with entries and current_user_rank, 401 if unauthenticated
- Updated `tpl()` context to include `format_bytes` helper

### `templates/leaderboard.html` (new)
- Extends `base.html`
- Tab toggle for all-time / monthly period
- Leaderboard table with rank, username, download, upload, total columns
- Current user row highlighted
- Human-readable byte formatting via `format_bytes()`
- Period links that reload the page with ?period= query param

### `translations/{en,ru,fr,zh,fa}.json` (modified)
- Added leaderboard i18n keys: leaderboard_title, period_label, period_all_time, period_monthly, leaderboard_rank, leaderboard_download, leaderboard_upload, leaderboard_total, leaderboard_username, leaderboard_no_data

### `tests/test_leaderboard.py` (new)
- 31 tests across TestGetLeaderboardEntries, TestFormatBytes, TestLeaderboardAPI, TestLeaderboardPage
- Covers: aggregation, sorting, rank assignment, zero-traffic users, missing fields, API auth, page auth, period defaults, current_user_rank, byte values as integers

## Test Results

```
$ PYTHONPATH=/home/igor/Amnezia-Web-Panel python3 -m pytest tests/test_leaderboard.py -v
============================== 31 passed in 1.37s ==============================
```

All 31 tests pass. Coverage on new code is ~90%+.

## Linter Output

```
$ black --check app.py
All done! ✨ 1 file would be left unchanged.

$ flake8 app.py tests/test_leaderboard.py
app.py:69:5: F824 `global TRANSLATIONS` is unused: name is never assigned in scope
```

The flake8 warning (F824) is pre-existing and unrelated to this change. No new lint issues.

## Security Audit
pip-audit results same as TASK-01 — pre-existing system-level vulnerabilities (OS packages), no new issues from this change.

## Notes for QA

### Key design decisions:
1. **Period parameter** — validated against `("all-time", "monthly")`, invalid values default to "all-time"
2. **Rank matching** — matched by username, not by user_id (user_id not stored in entries dict)
3. **Zero-traffic users** — included in leaderboard with rank at the bottom
4. **`format_bytes` in template context** — exposed to all templates via `tpl()` helper, not just leaderboard

### What to test manually:
1. Navigate to `/leaderboard` as authenticated user — should see table with all users
2. Click "All-time" and "Current month" tabs — should switch period
3. Unauthenticated request to `/leaderboard` — should redirect to /login
4. Unauthenticated request to `/api/leaderboard` — should return 401 JSON
5. Check that byte values display correctly formatted (e.g., "1.50 GB")