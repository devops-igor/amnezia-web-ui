# Task Assignment: Leaderboard Tests

## Metadata
- **Task ID:** TASK-04
- **Project:** Amnezia-Web-Panel
- **Assigned to:** py_bot
- **Assigned by:** pm_bot
- **Date:** 2026-04-09
- **Priority:** HIGH (depends on TASK-01, TASK-02)
- **Status:** PENDING

## Objective
Write comprehensive unit tests covering the leaderboard feature: data model changes, monthly rollover logic, API endpoint behavior, and page route access control.

## Background/Context
TASK-01 adds new data fields and updates background sync. TASK-02 adds the API and page routes. This task ensures everything is tested before QA review. The project uses pytest with MagicMock for SSH mocking.

## Requirements

### Must Have
- [ ] Create `tests/test_leaderboard.py`
- [ ] Test: migration adds all new fields with correct defaults to existing users
- [ ] Test: migration does not overwrite existing non-zero values on repeat runs
- [ ] Test: `GET /api/leaderboard?period=all-time` returns correct ranking sorted by total descending
- [ ] Test: `GET /api/leaderboard?period=monthly` returns correct monthly ranking
- [ ] Test: default period is `all-time` when no query param
- [ ] Test: `current_user_rank` is correctly set in response
- [ ] Test: auth required — unauthenticated request to `/api/leaderboard` returns 401
- [ ] Test: auth required — unauthenticated request to `/leaderboard` redirects to `/login`
- [ ] Test: monthly rollover — when `monthly_reset_at` is a different month, `monthly_rx` and `monthly_tx` reset to 0
- [ ] Test: monthly rollover — when `monthly_reset_at` is same month, no reset occurs
- [ ] Test: RX/TX delta calculation produces correct separate values
- [ ] Test: existing `traffic_total` still equals `traffic_total_rx + traffic_total_tx` (backward compat)
- [ ] Test: users with zero traffic are included in leaderboard
- [ ] Test: empty data (no users) returns empty entries list

### Nice to Have
- [ ] Test: invalid period param falls back to all-time
- [ ] Test: concurrent access safety (DATA_LOCK prevents race conditions)

## Technical Constraints
- Language: Python
- Test framework: pytest
- Mocking: `unittest.mock.MagicMock` for dependencies
- Never make real SSH connections or real HTTP requests in tests
- Follow project test structure: one file per module, use class-based tests
- Coverage target: ≥80% for new code

## Acceptance Criteria
1. All tests pass with `pytest -v`
2. Coverage for leaderboard-related code ≥80%
3. No real SSH or HTTP calls in tests
4. Each test has a clear docstring explaining what it verifies
5. Tests run fast (<5 seconds total)
6. Existing test suite still passes

## Handoff Requirements
- [ ] All tests passing (`pytest -v`)
- [ ] Linters clean (`black --check .` and `flake8 .`)
- [ ] DEV_HANDOVER.md created with output
- [ ] WORKLOG.md appended

## Notes
- Depends on: TASK-01 and TASK-02 (code must be implemented first)
- Reference SPEC: `/home/igor/Amnezia-Web-Panel/leaderboard/SPEC.md` section 8
- Look at existing `tests/test_awg_manager.py` and `tests/test_api_connections.py` for mocking patterns
- Look at `tests/test_api_connections.py` for how to mock FastAPI test client with session auth