     1|# Amnezia Web Panel — Improvement Tasks Overview
     2|
     3|**Generated:** 2026-04-14 | **Last Updated:** 2026-05-01
     4|**Total Findings:** 47 + 2 post-Phase-1 fixes
     5|**P1 (Critical):** 32 | **P2 (Medium):** 15
     6|
     7|---
     8|
     9|## Summary by Category
    10|
    11||| Category | Count | Priority ||
    12|||----------|-------|----------|
    13||| Critical Security | 20 | P1 |
    14||| Critical Bug | 5 | P1 |
    15||| Critical Config | 1 | P1 |
    16||| Bug | 5 | P1 (4), P2 (1) |
    17||| Refactoring/Tech Debt | 14 | P2 |
    18||| Performance | 1 | P2 |
    19||| Testing/Quality | 1 | P2 |
    20|
    21|---
    22|
    23|## Phase 1 — Critical Security Fixes: ✅ COMPLETE (16/16 + 2 post-fixes)
    24|
    25|All 16 security vulnerabilities addressed. Branch: feat/phase1-critical-security. Deployed and verified.
    26|
    27|Archived task folders in `_archive/`.
    28|
    29|---
    30|
    31|## Phase 2 — Critical Bugs & Operational Issues: ✅ COMPLETE (9/9)
    32|
    33|All 9 issues addressed across Batches 2A, 2B, 2C. Deployed and verified.
    34|
    35|Archived task folders in `_archive/`.
    36|
    37|---
    38|
    39|## Phase 3 — Bugs & Quick Wins: ✅ COMPLETE (9/9)
    40|
    41|All 9 issues addressed across Batches 3A-3E. Branch: feat/phase3-quick-wins. Merged to main (PR #100). Deployed and verified.
    42|
    43|Archived task folders in `_archive/`.
    44|
    45|---
    46|
    47|## Phase 4 — Refactoring & Architecture: 11/12 complete (1 remaining)
    48|
    49||| # | Slug | Title | Depends On | Issue | Status ||
    50|||---|------|-------|------------|-------|--------|
    51||| 36 | pydantic-models-scattered | Pydantic Models to schemas.py | None | #45 | ✅ DONE |
    52||| 37 | auth-check-inconsistency | Auth Check Unification | None | #46 | ✅ DONE |
    53||| 38 | deprecated-startup-event | Lifespan Context Manager | #48 | #66 | ✅ DONE (PR #118) |
    54||| 39 | background-task-no-supervision | PBT Supervision | #66 | #48 | ✅ DONE (PR #120) |
    55||| 40 | database-code-duplication | DB Code Duplication | None | #47 | ✅ DONE |
    56||| 41 | duplicated-check-docker | check_docker Dedup | None | #64 | ✅ DONE |
    57||| 42 | telegram-bot-removal | Remove Telegram Bot Feature | None | #58 | Planning |
    58||| 43 | missing-db-indexes | Missing DB Indexes | None | #76 | ✅ DONE |
    59||| 44 | background-task-monolith | PBT Monolith → Services | #66 | #52 | ✅ DONE (PR #119) |
    60||| 45 | god-file-app-py | app.py Split into Modules | #36, #37 | #51 | ✅ DONE (PR #108 MERGED) |
    61||| 46 | migration-no-schema-version | Migration Schema Versioning | None | #83 | ✅ DONE |
    62||| 47 | no-security-integration-tests | Security & Integration Tests | Phase 1 & 2 | #86 | ✅ DONE (PR #121) |
    63|
    64|---
    65|
    66|## E2E Testing Tasks
    67|
    68||| # | Slug | Title | Issue | Status | PR | Folder ||
    69|||---|------|-------|-------|--------|------|--------|
    70||| 48 | e2e-playwright-suite | E2E Playwright Test Suite | #101 | ✅ COMPLETE | #103 | `tasks/e2e-playwright-suite` |
    71||| 49 | e2e-servers-api-fix | Add GET /api/servers endpoint | #109 | ✅ COMPLETE | #111 | `tasks/e2e-servers-api-fix` |
    72||| 50 | e2e-rate-limit-fix | Disable rate limiting in E2E mode | #107 | ✅ COMPLETE | #110 | `tasks/e2e-rate-limit-fix` |
    73||| 51 | e2e-test-infrastructure | Rewrite E2E conftest.py | #112 | ✅ COMPLETE | #113 | `tasks/e2e-test-infrastructure` |
    74||| 52 | e2e-test-api-keys | Fix API response key mismatches | #114 | ✅ COMPLETE | #115, #116, #117 | `tasks/e2e-test-api-keys` |
    75|
    76|---
    77|
    78|## Follow-up Issues
    79|
    80||| Issue | Title | Priority | Status ||
    81|||-------|-------|----------|--------|
|| #114 | Strip sensitive fields from GET /api/servers | LOW | ✅ DONE (PR #120) ||
|| #41 | Cleanup: backup/restore still references data.json | LOW | Open |
    83|
    84|---
    85|
    86|## Overall Progress
    87|
**51/52 issues done.** Phase 1-3 complete. Phase 4: 11/12 complete.
#42 (telegram-bot-removal): scope changed from "optimize" to "remove entirely." Planning complete, TASK.md ready.
#41 (backup-data-json-cleanup): open, not yet planned.
E2E testing: 5 tasks complete, 31/36 tests passing.
Security integration tests: 20 new tests (PR #121), 702 total.

---

## Deploy & Verification (2026-05-01)

- Deployed feat/security-integration-tests branch to dev server
- 702 tests pass (20 new security tests), black + flake8 clean
- Browser login test passed: admin login, dashboard, users, settings pages all functional
- PR #121: https://github.com/devops-igor/amnezia-web-ui/pull/121

## Deploy & Verification (2026-04-30)

- Deployed feat/background-task-supervision branch to dev server
- BackgroundTaskSupervisor verified in container logs: "Background task supervisor started"
- Sensitive fields (password, private_key) verified absent from GET /api/servers/ response
- Browser login test passed: admin login, dashboard, users page all functional
- 682 tests pass (17 new supervisor + 1 new API test), black + flake8 clean
- PR #120: https://github.com/devops-igor/amnezia-web-ui/pull/120

---

## Deploy & Verification (2026-04-28)
    94|
    95|- Deployed main branch (post-PR #117 merge) to dev server
    96|- E2E verification: 31/36 tests pass, 3 skipped (rate limit E2E mode, no VPN connections), 2 transient errors (BunkerWeb 502)
    97|- Fixes deployed: API key mismatches (#115), password validation (#116), POST trailing slashes (#116), user pagination (#116), connection test self-containment (#117)
    98|- All security checks green: Fernet encryption, CSRF, rate limiting, input validation, XSS prevention, non-root container
    99|
   100|---
   101|
   102|## Archive
   103|
   104|Completed task folders moved to `tasks/_archive/`. Contains 43 folders from Phases 1-3 and batch work.
   105|
   106|---
   107|
   108|*This file is local-only (gitignored). Do not commit to remote repo.*