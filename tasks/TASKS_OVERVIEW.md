# Amnezia Web Panel — Improvement Tasks Overview

**Generated:** 2026-04-14 | **Last Updated:** 2026-05-04
**Total Findings:** 53 completed + 26 new from code review + 1 new bug (CIDR)
**New Code Review Issues:** 27 (P0: 3, P1: 5, P2: 10, P3: 7, +1 CIDR bug)

---

## Executive Summary — Code Review Findings (2026-05-02)

**Overall Health:** 6.2/10 — Solid security-minded project (CSRF, rate limiting, encrypted credentials) with three critical vulnerabilities requiring immediate attention: default SECRET_KEY publicly committed, IP spoofing bypass for rate limiting, and open redirect in language-switching route. Architecture is clean but module organization and SSH connection management need improvement.

**Main Themes:**
1. **Critical Security Gaps** — 3 P0 vulnerabilities (default secret key, IP spoofing, open redirect)
2. **Password Hashing Confusion** — bcrypt imported but not used, hand-rolled PBKDF2 instead
3. **Tech Debt Accumulation** — god class, re-export shims, root-level modules, f-string logging
4. **Performance Opportunities** — leaderboard SQL aggregation, SSH connection batching, SQLite connection pooling

**Recommended Implementation Sequence:**
1. **Phase 5A** — Critical Security Fixes (P0: 3 issues) ✅ DONE
2. **Phase 5B** — High Priority Bugs & Security (P1: 5 issues, including #152 CIDR bug)
3. **Phase 5C** — Medium Priority Fixes (P2: 10 issues)
4. **Phase 5D** — Low Priority & Architecture (P3: 7 issues)

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

## Phase 4 — Refactoring & Architecture: ✅ COMPLETE (12/12)

| # | Slug | Title | Depends On | Issue | Status |
|---|------|-------|------------|-------|--------|
| 36 | pydantic-models-scattered | Pydantic Models to schemas.py | None | #45 | ✅ DONE |
| 37 | auth-check-inconsistency | Auth Check Unification | None | #46 | ✅ DONE |
| 38 | deprecated-startup-event | Lifespan Context Manager | #48 | #66 | ✅ DONE (PR #118) |
| 39 | background-task-no-supervision | PBT Supervision | #66 | #48 | ✅ DONE (PR #120) |
| 40 | database-code-duplication | DB Code Duplication | None | #47 | ✅ DONE |
| 41 | duplicated-check-docker | check_docker Dedup | None | #64 | ✅ DONE |
| 42 | telegram-bot-removal | Remove Telegram Bot Feature | None | #58 | ✅ DONE (PR #122) |
| 43 | missing-db-indexes | Missing DB Indexes | None | #76 | ✅ DONE |
| 44 | background-task-monolith | PBT Monolith → Services | #66 | #52 | ✅ DONE (PR #119) |
| 45 | god-file-app-py | app.py Split into Modules | #36, #37 | #51 | ✅ DONE (PR #108 MERGED) |
| 46 | migration-no-schema-version | Migration Schema Versioning | None | #83 | ✅ DONE |
| 47 | no-security-integration-tests | Security & Integration Tests | Phase 1 & 2 | #86 | ✅ DONE (PR #121) |

---

## E2E Testing Tasks: ✅ COMPLETE (5/5)

| # | Slug | Title | Issue | Status | PR | Folder |
|---|------|-------|-------|--------|------|--------|
| 48 | e2e-playwright-suite | E2E Playwright Test Suite | #101 | ✅ COMPLETE | #103 | `tasks/e2e-playwright-suite` |
| 49 | e2e-servers-api-fix | Add GET /api/servers endpoint | #109 | ✅ COMPLETE | #111 | `tasks/e2e-servers-api-fix` |
| 50 | e2e-rate-limit-fix | Disable rate limiting in E2E mode | #107 | ✅ COMPLETE | #110 | `tasks/e2e-rate-limit-fix` |
| 51 | e2e-test-infrastructure | Rewrite E2E conftest.py | #112 | ✅ COMPLETE | #113 | `tasks/e2e-test-infrastructure` |
| 52 | e2e-test-api-keys | Fix API response key mismatches | #114 | ✅ COMPLETE | #115, #116, #117 | `tasks/e2e-test-api-keys` |

---

## Follow-up Issues (Prior)

| Issue | Title | Priority | Status |
|-------|-------|----------|--------|
| #114 | Strip sensitive fields from GET /api/servers | LOW | ✅ DONE (PR #120) |
| #70 | Ghost dependencies in requirements.txt | LOW | ✅ DONE (python-telegram-bot removed in PR #122) |
| #41 | Cleanup: backup/restore still references data.json | LOW | Open |

---

## Phase 5 — Code Review Findings (2026-05-02)

### Phase 5A — Critical Security Fixes (P0): ✅ COMPLETE (3/3)

| # | Slug | Title | Category | Priority | Issue | Status |
|---|------|-------|----------|----------|-------|--------|
| 1 | default-secret-key-exposed | Default SECRET_KEY publicly committed | Security | P0 | #125 | ✅ DONE (PR #154) |
| 2 | x-forwarded-for-spoofing-rate-limit-bypass | X-Forwarded-For bypasses rate limiting | Security | P0 | #126 | ✅ DONE (PR #154) |
| 3 | open-redirect-set-lang | Open redirect in set_lang via Referer | Security | P0 | #127 | ✅ DONE (PR #154) |

### Phase 5B — High Priority Bugs & Security (P1): ✅ COMPLETE (4/4)

| # | Slug | Title | Category | Priority | Issue | Status |
|---|------|-------|----------|----------|-------|--------|
| 4 | ssh-host-key-tofu-no-confirmation | SSH TOFU with no admin confirmation | Security | P1 | #128 | ✅ DONE (PR #155) |
| 6 | api-add-server-race-condition | api_add_server returns wrong ID | Bug | P1 | #130 | ✅ DONE (PR #155) |
| 7 | bcrypt-imported-not-used-handrolled-pbkdf2 | bcrypt imported but PBKDF2 used | Bug | P1 | #131 | ✅ DONE (PR #155) |
| 21 | bcrypt-unused-dependency | bcrypt declared but never called | Tech Debt | P1 | #145 | ✅ DONE (PR #155) |
| — | trusted-proxies-cidr-not-implemented | TRUSTED_PROXIES CIDR not implemented | Bug/Security | P1 | #152 | ✅ DONE (PR #153) |

### Phase 5C — Medium Priority Fixes (P2)

| # | Slug | Title | Category | Priority | Issue | Status |
|---|------|-------|----------|----------|-------|--------|
| 5 | ssl-private-key-in-db | SSL private key stored in DB | Security | P2 | #129 | 🔴 Open |
| 8 | perform-delete-user-ssh-inefficiency | SSH connection per VPN record | Bug | P2 | #132 | ✅ DONE (PR #158, merged) |
| 9 | re-export-shim-app-py | Re-export shim locks backward-compat | Tech Debt | P2 | #133 | ✅ DONE (PR #158, merged) |
| 10 | root-level-manager-modules | Root-level managers bypass package | Tech Debt | P2 | #134 | 🔴 Open |
| 11 | f-string-logging | f-string logging overhead | Tech Debt | P2 | #135 | ✅ DONE (PR #157, merged) |
| 12 | bare-except-pass | Bare except:pass swallows errors | Bug | P2 | #136 | ✅ DONE (PR #157, merged) |
| 15 | leaderboard-aggregation-in-python | Leaderboard fetches all users | Tech Debt | P2 | #139 | 🔴 Open |
| 16 | thread-local-sqlite-churn | SQLite connection churn | Tech Debt | P2 | #140 | 🔴 Open |
| 18 | no-test-for-open-redirect | No test for open redirect | Bug | P2 | #142 | ✅ DONE (PR #154, 6 tests) |
| 19 | no-test-for-ssh-inefficiency | No test for SSH batching | Bug | P2 | #143 | ✅ DONE (PR #158, 9 tests) |
| 22 | pip-audit-production-dependency | pip_audit in production deps | Tech Debt | P2 | #146 | 🔴 Open |
| 24 | internal-project-files-public | Internal files in public repo | Security | P2 | #148 | 🔴 Open |

### Phase 5D — Low Priority & Architecture (P3)

| # | Slug | Title | Category | Priority | Issue | Status |
|---|------|-------|----------|----------|-------|--------|
| 13 | stub-utils-py-root | Stub utils.py duplicates helpers | Tech Debt | P3 | #137 | 🔴 Open |
| 14 | background-task-orchestrator-god-class | God class — split into services | Tech Debt | P3 | #138 | 🔴 Open |
| 17 | server-stats-four-ssh-roundtrips | Four SSH round-trips for stats | Enhancement | P3 | #141 | 🔴 Open |
| 20 | e2e-tests-assert-status-only | E2E tests lack response validation | Enhancement | P3 | #144 | 🔴 Open |
| 23 | no-dependabot-config | No Dependabot config | Documentation | P3 | #147 | 🔴 Open |
| 25 | api-routes-untyped-dicts | API routes return untyped dicts | Enhancement | P3 | #149 | 🔴 Open |
| 26 | dockerfile-no-multi-stage | Dockerfile lacks multi-stage build | Enhancement | P3 | #150 | 🔴 Open |

---

## Dependency Graph (Phase 5)

```
Phase 5A (P0 — no dependencies): ✅ DONE
  #1 default-secret-key-exposed ✅
  #2 x-forwarded-for-spoofing-rate-limit-bypass ✅
  #3 open-redirect-set-lang ✅

Phase 5B (P1):
  #4  ssh-host-key-tofu-no-confirmation (standalone)
  #6  api-add-server-race-condition (standalone)
  #7  bcrypt-imported-not-used (→ #21 bcrypt-unused-dependency)
  #21 bcrypt-unused-dependency (depends on #7)
  #152 trusted-proxies-cidr-not-implemented (standalone)

Phase 5C (P2):
  #18 no-test-for-open-redirect (depends on #3 open-redirect-fix ✅)
  #19 no-test-for-ssh-inefficiency (depends on #8 ssh-batching-fix)
  #9  re-export-shim-app-py (standalone)
  #10 root-level-manager-modules (depends on #9)
  #11 f-string-logging (standalone)
  #12 bare-except-pass (standalone)
  #15 leaderboard-aggregation (standalone)
  #16 thread-local-sqlite-churn (standalone)
  #22 pip-audit-production-dependency (standalone)
  #24 internal-project-files-public (standalone)
  #5  ssl-private-key-in-db (standalone)

Phase 5D (P3):
  #13 stub-utils-py-root (depends on #9)
  #14 background-task-god-class (depends on #8)
  #17 server-stats-four-ssh-roundtrips (standalone)
  #20 e2e-tests-assert-status-only (standalone)
  #23 no-dependabot-config (standalone)
  #25 api-routes-untyped-dicts (standalone)
  #26 dockerfile-no-multi-stage (standalone)
```

---

## Summary by Category

| Category | Count | P0 | P1 | P2 | P3 |
|----------|-------|----|----|----|----|
| Security | 7 | 3 | 1 | 2 | 0 |
| Bug | 7 | 0 | 3 | 4 | 0 |
| Tech Debt | 8 | 0 | 1 | 5 | 2 |
| Enhancement | 4 | 0 | 0 | 0 | 4 |
| Documentation | 1 | 0 | 0 | 0 | 1 |
| Testing | 1 | 0 | 0 | 1 | 0 |
| **Total** | **27** | **3** | **5** | **12** | **7** |

Done: P0 3/3, P1 5/5, P2 6/12 (PR #154: #142; PR #157: #135, #136; PR #158: #132, #133, #143).

---

## Overall Progress

**Previous Phases 1-4 + E2E:** 53/53 issues DONE-DONE.
**Phase 5A:** 3/3 DONE (PR #154). **Phase 5B:** 5/5 DONE (PR #155). **Phase 5C** (in progress): 6/12 P2 done. Remaining open: #129, #134, #139, #140, #146, #148 (P2) + all P3 issues.

---

## Deploy & Verification History

### 2026-05-03 — Phase 5B: P1 High Priority Fixes (Issues #128, #130, #131, #145)
- #128: SSH host key TOFU — two-phase admin confirmation flow (pending_fingerprint_confirmation + confirm-fingerprint)
- #130: api_add_server race condition — use lastrowid instead of server_count-1
- #131: bcrypt password hashing migration — hash_password uses bcrypt.hashpw, verify_password has dual path (bcrypt primary + PBKDF2 legacy), hmac.compare_digest for constant-time comparison
- #145: bcrypt unused dependency — now actually used, stays in requirements.txt
- Additional fix: 72-byte password truncation in bcrypt (prevents ValueError on long passwords)
- Branch: feat/phase5b-high-priority
- 733 tests pass (26 new), black + flake8 clean
- QA APPROVED after timing side-channel fix and frontend two-phase flow
- Live verified on dev server: bcrypt format, PBKDF2 compat, edge cases, two-phase API, browser login
- PR #155: https://github.com/devops-igor/amnezia-web-ui/pull/155 (MERGED to main)

### 2026-05-04 — Issue #152 TRUSTED_PROXIES CIDR Support
- Bug: TRUSTED_PROXIES claimed CIDR support but `peer in TRUSTED_PROXIES` did exact string match — CIDR strings never matched peer IPs
- Fix: ipaddress-based CIDR parsing — `_trusted_proxy_hosts` (set) + `_trusted_proxy_networks` (list). `_get_client_ip()` checks both. Invalid entries logged as warnings, never crash.
- 5 new tests (TestTrustedProxiesCidr). 697→704 tests pass, black + flake8 clean
- QA APPROVED by qa_bot
- PR #153 merged to main: https://github.com/devops-igor/amnezia-web-ui/pull/153
- Deployed to dev server. Live verified: 172.18.0.5 in 172.18.0.0/24 = True.

### 2026-05-04 — Phase 5A Rebase & PR #154
- PR #151 had merge conflicts after CIDR fix merged to main
- Rebased feat/phase5a-critical-security onto main → feat/phase5a-v2 branch
- Resolved 4 conflicts (helpers.py, test_rate_limiting.py, docker-compose.yml, WORKLOG.md)
- 704/704 tests pass, all CI green
- PR #154: https://github.com/devops-igor/amnezia-web-ui/pull/154

### 2026-05-04 — PROD SECRET_KEY Rotation
- Old state: SECRET_KEY auto-generated at first boot, stored in /app/.secret_key inside container (NOT on host volume — lost on container rebuild)
- New state: 64-char hex SECRET_KEY set as env var in docker-compose.yaml (persists across rebuilds)
- DB backed up: /root/amnezia-panel/panel.db.backup-20260502-*
- Decrypted "Sweden" server SSH key with old SECRET_KEY, re-encrypted with new key
- Round-trip verification passed: decrypt(new_key, encrypt(new_key, plaintext)) == plaintext
- Container recreated. Login page loads. SSH key decrypts correctly from DB.
- Sessions invalidated (expected — users re-login required)

### 2026-05-03 — Phase 5A: P0 Critical Security Fixes (Issues #125, #126, #127)
- #125: SECRET_KEY now uses `${SECRET_KEY:?}` syntax in docker-compose.yml (fails if unset), .env.example added
- #126: TRUSTED_PROXIES env var added — X-Forwarded-For only trusted from configured proxy IPs, ignored by default
- #127: Open redirect in set_lang fixed — external URLs stripped to path+query only, 6 new unit tests
- Branch: feat/phase5a-critical-security
- 700 tests pass (8 new), black + flake8 clean
- QA APPROVED by qa_bot (all acceptance criteria met)
- Container verification: SECRET_KEY=*** chars from env, TRUSTED_PROXIES=empty (secure default), open redirect strips external URLs, DB writable
- Browser login test: admin login OK, dashboard, users, settings pages functional
- PR #154: https://github.com/devops-igor/amnezia-web-ui/pull/154 (rebased from #151 after CIDR merge conflict)
- **NOTE:** TRUSTED_PROXIES CIDR support not implemented (#152) — only exact IP matching works, CIDR strings documented but not parsed

### 2026-05-02 — Issue #123 Monthly Leaderboard Reset
- Bug: Monthly leaderboard did not reset April→May. Root cause: rollover logic gated inside `if updates:`
- Fix: Extracted monthly rollover to run unconditionally every sync cycle
- 692/692 tests pass, black + flake8 clean
- QA APPROVED by qa_bot
- PR #124 merged: https://github.com/devops-igor/amnezia-web-ui/pull/124

### 2026-05-02 — Telegram Bot Removal
- Container-level verification: 6/6 checks passed
- PR #122 merged: https://github.com/devops-igor/amnezia-web-ui/pull/122

### 2026-05-01 — Security Integration Tests
- 702 tests pass (20 new security tests)
- PR #121 merged

### 2026-04-30 — Background Task Supervision
- 682 tests pass (17 new supervisor + 1 new API test)
- PR #120 merged

---

## Archive

Completed task folders moved to `tasks/_archive/`. Contains 43+ folders from Phases 1-3 and batch work.

---

*This file is local-only (gitignored). Do not commit to remote repo.*