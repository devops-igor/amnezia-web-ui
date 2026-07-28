# Amnezia Web Panel — Task Tracker

**Last updated:** 2026-07-28
**Test count:** 1029 (all passing)
**Current main:** 7a23d74

## Completed Tasks (Recent Sessions)

| # | Issue | Priority | Status | PR | Summary |
|---|-------|----------|--------|-----|---------|
| 314 | leaderboard-monthly-snapshot | P2 | DONE-DONE | #315 | Save monthly leaderboard snapshot before rollover. New `leaderboard_snapshots` table, `save_leaderboard_snapshot()` + `get_leaderboard_snapshot()`, API `period=last-month`, "Last Month" button in frontend, 19 new tests, escapeHtml XSS fix |
| — | dependabot-batch-jul28 | — | DONE-DONE | #308-#313 | 6 Dependabot PRs merged: pip-audit 2.10.1, setup-python v7, websockets 16.1.1, slowapi 0.1.10, click 8.4.2, python-multipart 0.0.32. 1 conflict resolved (#311). |
| 306 | remove-protocol-paths | P3 | DONE-DONE | #307 | Remove dead Protocol Paths settings section. 14 files, -119 lines. |
| 303 | per-connection-traffic | P2 | DONE-DONE | #304+#305 | Per-connection traffic tracking. 5 phases + hotfix. |
| 286 | mtproxyl-migration | P2 | DONE-DONE | #287-#288,#294 | Replace Telemt with MTProxyL. 5 phases. |
| 255 | telemt-status-stopped | P2 | DONE-DONE | — | Fix Telemt containers showing STOPPED. |
| 192 | user-rename-connections | P3 | DONE-DONE | #194 | Rename endpoint + modal on My Connections. |
| 41 | backup-restore-data-json | P2 | DONE-DONE | #193 | Renamed backup file, updated i18n. |

## Open Tasks (Backlog)

| # | Issue | Priority | Status | Summary |
|---|-------|----------|--------|---------|
| 208 | multi-hop-routing | P3 | Not started | Enhancement: Multi-hop routing (chain protocols through intermediate servers) |
| 197 | docker-compose-upgrade | P2 | Not started | Upgrade docker-compose.yml to production standard with BunkerWeb profiles |
| 198 | deployment-documentation | P2 | Not started | Create deployment docs (blocked by #197) |
| 199 | env-and-readme-update | P3 | Not started | Update .env.example and README Docker section (blocked by #197) |

## Active Task Folders

- `tasks/multi-hop-routing/` — Not started (TASK.md only, issue #208)

All other task folders have been archived to `tasks/_archive/`.

## Verification Summary

- 1029 tests pass (pytest --ignore=tests/e2e)
- black + flake8 clean
- No open PRs
- No open Dependabot PRs
- Only `origin/main` remote branch remains (all stale branches cleaned)
- Local working tree: on `main`, clean except TASKS_OVERVIEW.md/WORKLOG.md documentation updates