# Task Assignment: Data Model & Background Sync — RX/TX Separation

## Metadata
- **Task ID:** TASK-01
- **Project:** Amnezia-Web-Panel
- **Assigned to:** py_bot
- **Assigned by:** pm_bot
- **Date:** 2026-04-09
- **Priority:** CRITICAL (blocks TASK-02, TASK-03, TASK-04)
- **Status:** PENDING

## Objective
Add separate download (RX) and upload (TX) tracking to the user data model and update the background traffic sync to populate these new fields.

## Background/Context
Currently, the background sync in `periodic_background_tasks()` (app.py ~line 811) combines RX and TX into a single delta before storing:
```python
rx = c.get("userData", {}).get("dataReceivedBytes", 0)
tx = c.get("userData", {}).get("dataSentBytes", 0)
client_bytes[c.get("clientId")] = rx + tx  # combined!
```
The leaderboard feature needs separate download/upload columns, so we must store RX and TX separately alongside the existing combined fields.

## Requirements

### Must Have
- [ ] Add new fields to each user dict in data.json: `traffic_total_rx` (int, default 0), `traffic_total_tx` (int, default 0), `monthly_rx` (int, default 0), `monthly_tx` (int, default 0), `monthly_reset_at` (str, default "")
- [ ] Add migration in `migrate_data()` function for all new fields (set defaults if missing, do NOT overwrite existing values)
- [ ] Change `client_bytes` dict in `periodic_background_tasks()` from `{client_id: int}` to `{client_id: {"rx": int, "tx": int}}`
- [ ] Update all downstream code that reads `client_bytes` — specifically the delta calculation loop (~line 817-824) and the user update loop (~line 840-877)
- [ ] In the user update loop, populate `traffic_total_rx += rx_delta`, `traffic_total_tx += tx_delta`, `monthly_rx += rx_delta`, `monthly_tx += tx_delta` in addition to existing `traffic_used` and `traffic_total` updates
- [ ] Add monthly rollover logic: if `monthly_reset_at` is empty OR its month differs from current month, reset `monthly_rx` and `monthly_tx` to 0, set `monthly_reset_at = now.isoformat()`
- [ ] Existing `traffic_used` and `traffic_total` fields must continue to work exactly as before (combined RX+TX) — no regressions
- [ ] The `traffic_reset_strategy` logic (daily/weekly/monthly resets of `traffic_used`) must remain unchanged

### Nice to Have
- [ ] Log a debug message when monthly rollover occurs for a user

## Technical Constraints
- Language: Python
- Framework: FastAPI
- Data store: data.json (dict-of-dicts, no ORM)
- Concurrency: protected by `DATA_LOCK` (asyncio.Lock)
- Follow project style: black (line-length 100), flake8
- Type hints required on all public function signatures
- Never swallow exceptions — log all errors

## Acceptance Criteria
1. `migrate_data()` adds all 5 new fields to existing users without errors (0 values, empty string for `monthly_reset_at`)
2. `periodic_background_tasks()` correctly calculates `rx_delta` and `tx_delta` separately from protocol managers' `get_clients()` output
3. `traffic_total_rx` matches sum of all RX deltas, `traffic_total_tx` matches sum of all TX deltas
4. `monthly_rx` and `monthly_tx` reset to 0 at month boundary in `monthly_reset_at`
5. Existing `traffic_used` and `traffic_total` continue to be updated correctly (combined RX+TX)
6. No race conditions — all writes protected by `DATA_LOCK`
7. Existing tests still pass (`pytest -v`)

## Handoff Requirements
- [ ] All tests passing (`pytest -v`)
- [ ] Linters clean (`black --check .` and `flake8 .`)
- [ ] Security scanners clean (`pip-audit`)
- [ ] DEV_HANDOVER.md created with output
- [ ] WORKLOG.md appended

## Notes
- Reference SPEC: `/home/igor/Amnezia-Web-Panel/leaderboard/SPEC.md` sections 3 and 9
- This is the foundational task — TASK-02 through TASK-04 depend on the data model changes here
- Check the existing `migrate_data()` function (~line 720 in app.py) for the pattern to follow
- The `periodic_background_tasks()` function starts ~line 780 in app.py