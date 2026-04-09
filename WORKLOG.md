# WORKLOG.md - Append-Only Action Log

**Important:** This file is append-only. Never edit or delete previous entries. Only append new entries at the bottom with a timestamp.

---
2026-04-09 00:54 | pm_bot | TASK_ASSIGNMENT | Amnezia-Web-Panel
Details: Created TASK-002 for fixing incorrect HTTP status code (429→409) and frontend error parsing for duplicate connection names. Regression from previous task. Priority: HIGH. Assigned to py_bot. See TASK.md for full details.
---
---
## 2026-04-09 | py_bot | TASK-002: Fix HTTP 409 + frontend error parsing for duplicate connection names

**Problem:**
- Backend returned HTTP 400 for duplicate names (wrong — should be 409 Conflict)
- Rate limit check also returned 400 instead of 429
- Frontend used `apiCall()` which throws on any non-2xx, showing raw HTTP text like `"Failed to create connection: 429 | Too Many Requests"`

**Changes:**

### `app.py` — `api_my_add_connection()`
- Changed duplicate name check from `status_code=400` → `status_code=409`
- Changed JSON body from `{"error": "...", "duplicate": True}` → `{"error": "duplicate_name", "message": "A connection with this name already exists."}`

### `templates/my_connections.html` — `submitCreateConnection()`
- Replaced `apiCall('/api/my/connections/add', 'POST', payload)` with raw `fetch()` so we can inspect `response.status` before throwing
- Added explicit `response.status === 409 && result?.error === 'duplicate_name'` branch → shows `{{ _('connection_duplicate_error') }}` (localized: "A connection with this name already exists. Please choose a different name.")
- Rate limit (429) continues to work via `result?.retry_after` check
- Non-JSON error responses parse title tag or first 200 chars before throwing

<<<<<<< Updated upstream
**Verification:**
- `python3 -m py_compile app.py` → Syntax OK
- `black app.py` → All done (no reformatting needed)
- Translation key `connection_duplicate_error` already exists in all 5 translation files (en, fa, fr, ru, zh)
- `flake8`/`pytest` not available in environment — skipped

**Definition of Done:** ✅
- Backend returns HTTP 409 for duplicate connection names ✅
- Backend returns JSON: `{"error": "duplicate_name", "message": "A connection with this name already exists."}` ✅
- Frontend displays exactly: "A connection with this name already exists. Please choose a different name." ✅
- Frontend does NOT display raw HTTP status codes ✅
- Code quality checks (black, py_compile) pass ✅
---
2026-04-09 00:58 | qa_bot | QA_REVIEW | TASK-002: APPROVED_WITH_COMMENTS

**Backend (app.py):** ✅ VERIFIED — HTTP 409, exact JSON body correct
**Frontend (my_connections.html):** ✅ VERIFIED — `response.status === 409` check, localized message shown
**Localization:** ✅ VERIFIED — all 5 translation files contain `connection_duplicate_error`
**Code Quality:** ✅ black + py_compile pass; flake8/mypy show pre-existing issues unrelated to this task
**Tests:** ⚠️ 2 tests FAIL — but tests assert OLD behavior (400) vs required (409). Implementation is correct.
  - `test_duplicate_connection_name_returns_json_error` — asserts 409 == 400
  - `test_duplicate_connection_error_message_format` — asserts 409 == 400

**Note:** Tests need updating to assert HTTP 409 and new JSON schema. This is a test maintenance issue, not an implementation defect. See QA_REPORT.md for full details.
---
2026-04-09 01:00 | py_bot | TASK-002 FOLLOW-UP: Fix 2 failing tests in test_api_connections.py

**Background:**
QA review of TASK-002 flagged 2 tests failing — they assert OLD (buggy) behavior:
- `test_duplicate_connection_name_returns_json_error` — asserted status_code == 400
- `test_duplicate_connection_error_message_format` — asserted status_code == 400

**Changes to `tests/test_api_connections.py`:**

### test_duplicate_connection_name_returns_json_error
- `assert response2.status_code == 400` → `assert response2.status_code == 409`
- Changed JSON assertions: `data["duplicate"] is True` → `data["error"] == "duplicate_name"`
- Removed assertion for specific name in message (backend returns generic message)
- Now asserts: `data["error"] == "duplicate_name"`, `"message" in data`, `"already exists" in data["message"]`

### test_duplicate_connection_error_message_format
- `assert response.status_code == 400` → `assert response.status_code == 409`
- Changed JSON assertions from `data["error"]` (string) to `data["error"] == "duplicate_name"` and `data["message"]`
- Removed name-specific checks (message is generic: "A connection with this name already exists.")
- Updated expected_message to match backend: `'A connection with this name already exists.'` (with trailing period)

**Verification:**
- `pytest -v tests/test_api_connections.py` → 3 passed in 0.36s ✅

**Definition of Done:** ✅
- Both tests updated to assert HTTP 409 ✅
- All tests pass: `pytest -v` (3/3 passed) ✅
- WORKLOG.md appended ✅

---
2026-04-09 01:13 | pm_bot | CI_FIX | Amnezia-Web-Panel
Details: Fixed black formatting in test_api_connections.py (single quotes → double quotes). Committed and pushed to fix/duplicate-connection-409-v2. Pipeline re-triggered.
---
2026-04-09 01:29 | Bot | PR_MERGE | fix/duplicate-connection-409-v2 → main
Details: Merged PR #2 into main via auto-merge. Merge commit: 6513b8f3d73ea381313312f74f8578eb450cfeab. PR URL: https://github.com/devops-igor/amnezia-web-ui/pull/2. Branch was CLEAN and MERGEABLE with no conflicts.
---

---
2026-04-09 01:43 | pm_bot | TASK_REOPEN | TASK-002: Amnezia-Web-Panel
Details: Task reopened. Code fix was verified by QA and merged to main, but deployment investigation revealed users still see raw 429 error. Root cause: running container image tag `9093c33` predates the fix (6513b8f). Server uses `main` tag which should auto-update, but image appears stale on pull. Further investigation needed — possibly CI build lag, registry push failure, or image tag resolution issue. All task files left intact for future session to resume.
---
2026-04-09 01:44 | Bot | COMMIT | TASK-002 Reopen
Details: Committed updated TASK.md, WORKLOG.md, COMPLETION_REPORT.md to document task reopen. Commit message: "chore: reopen TASK-002 — deployment investigation found stale container image"
---

---
2026-04-09 02:27 | pm_bot | TASK_ASSIGNMENT | Amnezia-Web-Panel
Details: Created TASK-003 for fixing rate limiter bugs (race condition, log pruning, stale read) using 3-phase lock pattern. Plan: RATE_LIMITER_MITIGATION_PLAN.md. Rate limiter currently disabled (commented out). Assigned to py_bot. See TASK.md for full details.
---
---
## 2026-04-09 | py_bot | TASK-003: Implement 3-phase lock for rate limiter

**Problem:**
Three bugs in the rate limiter for `/api/my/connections/add`:
1. **Race condition** — rate limit check used `load_data()` without `DATA_LOCK`
2. **Log never prunes by age** — `connection_creation_log` only trimmed to last 1000, never by timestamp
3. **Stale read** — two separate `load_data()` calls per request (check vs write) with seconds of SSH work between them

**Changes to `app.py` — `api_my_add_connection()`:**

Restructured into 3-phase lock pattern:

### Phase 1 (inside `async with DATA_LOCK`, milliseconds):
- `current_data = load_data()` inside lock
- Age-based pruning of `connection_creation_log` (entries older than `rate_limit_window` removed)
- Check per-user connection count → 429 if at limit
- Check sliding window rate limit → 429 if at limit  
- Validate server exists, protocol installed, no duplicate name
- `save_data(current_data)` to persist pruned log
- Rate limiter re-enabled (uncommented both blocks)
- Added `logger.warning()` for both rate limit trigger paths

### Phase 2 (no lock, seconds):
- SSH connection + remote client creation (outside lock)

### Phase 3 (inside `async with DATA_LOCK`, milliseconds):
- `write_data = load_data()` → append new connection + log entry → save

**Key details:**
- Removed early `data = load_data()` that was used for rate limiting (now inside lock)
- `user_conns` defined in Phase 1, available for both rate limit check and duplicate name check
- `now` timestamp captured in Phase 1, reused in Phase 3 for consistency
- `-1000` safety cap retained in Phase 3 as secondary protection

**Verification:**
- `python3 -m py_compile app.py` → Syntax OK ✅
- `black app.py` → reformatted ✅
- `pytest -v tests/test_api_connections.py` → 3 passed ✅

**Commit:** `b5a2262` — "fix: implement 3-phase lock for rate limiter"

**Definition of Done:** ✅
- [x] 3-phase lock pattern implemented
- [x] Rate limit check + log pruning inside `DATA_LOCK`
- [x] Age-based pruning of `connection_creation_log`
- [x] `user_conns` defined before duplicate name check
- [x] Rate limiter re-enabled (both blocks uncommented)
- [x] `py_compile` passes
- [x] `black` formatting applied
- [x] Tests pass (3/3)
- [x] Structured logging on rate limit triggers
- [x] WORKLOG.md appended
- [ ] Push to remote (pending — do NOT push yet per task instructions)
---
=======
## 2026-04-10 — QA Review TASK-03

**Reviewer:** qa_bot

**Verdict:** REVIEW_APPROVED

**Summary:** Reviewed TASK-03 implementation (leaderboard frontend — JS fetch, nav link, i18n). All 31 tests pass, Python compiles clean, all i18n keys present in 5 translation files. Nav link visible to all authenticated users. Period toggle works via fetch without page reload. Loading spinner and smooth transitions implemented. Current user row highlighted. Two LOW severity findings: missing responsive CSS for horizontal scroll on mobile, and formatBytes could cap at PB boundary. Neither blocks release.

**Scanner Results:**
- pytest: 31 passed in 1.14s
- py_compile: OK
- git diff awg_manager.py: empty (correct)
- formatBytes function count: 1 (correct)
- i18n files with leaderboard_nav: 5/5

**Findings:**
- LOW: table-container missing overflow-x CSS for responsive scrolling
- LOW: formatBytes could return undefined for exabyte-scale values (theoretical)

**Recommendation:** APPROVED — safe to merge.

>>>>>>> Stashed changes
