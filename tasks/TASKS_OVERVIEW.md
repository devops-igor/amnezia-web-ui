# Amnezia Web Panel — Improvement Tasks Overview

**Generated:** 2026-04-14 | **Last Updated:** 2026-04-27
**Total Findings:** 47 + 2 post-Phase-1 fixes
**P1 (Critical):** 32 | **P2 (Medium):** 15

---

## Summary by Category

| Category | Count | Priority |
|----------|-------|----------|
| Critical Security | 20 | P1 |
| Critical Bug | 5 | P1 |
| Critical Config | 1 | P1 |
| Bug | 5 | P1 (4), P2 (1) |
| Refactoring/Tech Debt | 14 | P2 |
| Performance | 1 | P2 |
| Testing/Quality | 1 | P2 |

---

## Phase 1 — Critical Security Fixes: ✅ COMPLETE (16/16 + 2 post-fixes)

All 16 security vulnerabilities addressed. Branch: feat/phase1-critical-security. Deployed and verified.

Archived task folders in `_archive/`.

---

## Phase 2 — Critical Bugs & Operational Issues: ✅ COMPLETE (9/9)

All 9 issues addressed across Batches 2A, 2B, 2C. Deployed and verified.

Archived task folders in `_archive/`.

---

## Phase 3 — Bugs & Quick Wins: ✅ COMPLETE (9/9)

All 9 issues addressed across Batches 3A-3E. Branch: feat/phase3-quick-wins. Merged to main (PR #100). Deployed and verified.

Archived task folders in `_archive/`.

---

## Phase 4 — Refactoring & Architecture: 🟡 IN PROGRESS (7/12)

|| # | Slug | Title | Depends On | Issue | Status |
|---|------|-------|------------|-------|--------|
| 36 | pydantic-models-scattered | Pydantic Models to schemas.py | None | #45 | ✅ DONE |
| 37 | auth-check-inconsistency | Auth Check Unification | None | #46 | ✅ DONE |
| 38 | deprecated-startup-event | Lifespan Context Manager | #48 | #66 | Open |
| 39 | background-task-no-supervision | PBT Supervision | #66 | #48 | Open |
| 40 | database-code-duplication | DB Code Duplication | None | #47 | ✅ DONE |
| 41 | duplicated-check-docker | check_docker Dedup | None | #64 | ✅ DONE |
| 42 | telegram-bot-full-db-dump | Telegram Bot DB Dump | None | #58 | Open |
| 43 | missing-db-indexes | Missing DB Indexes | None | #76 | ✅ DONE |
| 44 | background-task-monolith | PBT Monolith → Services | #66 | #52 | Open |
| 45 | god-file-app-py | app.py Split into Modules | #36, #37 | #51 | ✅ DONE (QA APPROVED, PR #108 MERGED) |
| 46 | migration-no-schema-version | Migration Schema Versioning | None | #83 | ✅ DONE |
| 47 | no-security-integration-tests | Security & Integration Tests | Phase 1 & 2 | #86 | Open |

---

## E2E Testing Task: ✅ COMPLETE (PR #103 merged)

| # | Slug | Title | Issue | Status | Folder |
|---|------|-------|-------|--------|--------|
| 48 | e2e-playwright-suite | E2E Playwright Test Suite | #101 | ✅ COMPLETE | `tasks/e2e-playwright-suite` |

---

## Overall Progress

**Phase 4A ✅ (4/12) + Phase 4B-C ✅ (2/12) + Phase 4D ✅ (1/12, QA APPROVED, PR #108 MERGED) = 43/47 issues done.**

Phase 4 remaining: 5 tasks.

---

## Deploy & Verification (2026-04-24)

- Deployed main branch (post-Phase-3 merge) to dev server
- Fixed DB permissions: `chown 100:101 /root/amnezia-panel/panel.db` (appuser UID 100)
- Verification results: 47/49 PASS, 0 FAIL, 2 skipped (password change tests)
- All security checks green: Fernet encryption, CSRF, rate limiting, input validation, XSS prevention, non-root container, template integrity
- PR #102 merged: VERIFICATION_PLAN.md, VERIFICATION_REPORT, pytest-asyncio fix, black formatting fix

---

## Archive

Completed task folders moved to `tasks/_archive/`. Contains 43 folders from Phases 1-3 and batch work.

---

*This file is local-only (gitignored). Do not commit to remote repo.*