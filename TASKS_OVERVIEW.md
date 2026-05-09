# Amnezia Web Panel — Task Tracker

**Last updated:** 2026-05-08

## Completed Tasks (This Session)

| # | Issue | Priority | Status | PR | Summary |
|---|-------|----------|--------|-----|---------|
| 41 | backup-restore-data-json | P2 | ✅ DONE-DONE | #193 | Renamed `data.json` → `amnezia-backup.json`, updated i18n strings in 5 languages, added 11 tests |
| 192 | user-rename-connections | P3 | ✅ DONE-DONE | #194 | New `POST /api/my/connections/{connection_id}/rename` endpoint, rename button + modal on My Connections page, 7 i18n keys × 5 langs, 7 tests |
| — | rename-modal-fix | — | ✅ DONE-DONE | #195 | Removed `hidden` class from renameModal so openModal() works correctly |
| — | rename-button-inline | — | ✅ DONE-DONE | #196 | Changed `.client-actions` from `max-width:140px; flex-wrap:wrap` to `flex-wrap:nowrap` so buttons are inline |

## Open Tasks (Backlog)

|| # | Issue | Priority | Status | Summary ||
|---|-------|----------|--------|---------|
| 197 | docker-compose-upgrade | P2 | 📋 Not started | Upgrade docker-compose.yml to production standard with BunkerWeb profiles ||
| 198 | deployment-documentation | P2 | 📋 Not started | Create deployment docs for Docker Compose with BunkerWeb (blocked by #197) ||
| 199 | env-and-readme-update | P3 | 📋 Not started | Update .env.example and README Docker section (blocked by #197) ||
| 144 (e2e-tests-assert-status-only) | P3 | 📋 Not started | E2E tests assert HTTP status but rarely validate response shape |

## Task Folders

- `tasks/backup-restore-data-json/` — ✅ Complete (DEV_HANDOVER, QA_REVIEW, TASK)
- `tasks/user-rename-connections/` — ✅ Complete (DEV_HANDOVER, QA_REVIEW, TASK)
- `tasks/e2e-tests-assert-status-only/` — 📋 Not started (TASK only)
- `tasks/docker-compose-upgrade/` — 📋 Not started (TASK only, #197)
- `tasks/deployment-documentation/` — 📋 Not started (TASK only, #198, blocked by #197)
- `tasks/env-and-readme-update/` — 📋 Not started (TASK only, #199, blocked by #197)

## Verification Summary

All completed tasks verified on dev server (vpn.dev.drochi.games):
- #41: Settings page shows "Backup & Restore" + "Download backup", filename `amnezia-backup.json` confirmed
- #192: My Connections page shows ✏️ Rename button inline with Show config, rename modal opens with input field
- Both fixes: deployed to `:main`, CI green, live-tested in browser