# Amnezia Web Panel — Improvement Tasks Overview

**Generated:** 2026-04-14 | **Last Updated:** 2026-04-17
**Total Findings:** 47 + 2 post-Phase-1 fixes (#19, #95)
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

## Implementation Order (Recommended Phases)

### Phase 1 — Critical Security Fixes (Do First)

These are directly exploitable vulnerabilities. No other work should proceed until these are addressed.

| # | Slug | Title | Source | Status |
|---|------|-------|--------|--------|
| 1 | ssh-shell-injection | SSH Password Shell Injection | IMP1 §1 | #53 ✅ DONE |
| 2 | sql-injection-column-names | SQL Injection via Dynamic Column Names | IMP1 §2 | #49 ✅ DONE |
| 3 | default-admin-credentials | Hardcoded Admin Credentials (admin/admin) | IMP1 §3 | #54 ✅ DONE |
|| 4 | plaintext-credentials-db | Plaintext SSH Credentials in Database | IMP1 §4 | #55 ✅ DONE |
| 5 | paramiko-auto-add-policy | paramiko AutoAddPolicy MITM | IMP1 §5 | #50 ✅ DONE |
| 6 | ephemeral-secret-key | Ephemeral SECRET_KEY | IMP2 §7 | #56 ✅ DONE |
| 7 | no-csrf-protection | No CSRF Protection | IMP2 §10 | #62 ✅ DONE |
|| 8 | tls-domain-injection | tls_domain Config Injection | IMP3 §14 | #74 ✅ DONE |
|| 9 | wireguard-echo-injection | WireGuard Peer Config echo Injection | IMP3 §15 | #78 ✅ DONE |
|| 10 | configure-container-shell-injection | _configure_container f-string Injection | IMP3 §18 | #84 ✅ DONE |
|| 11 | no-input-validation-pydantic | No Input Validation on Pydantic Models | IMP3 §16 | #71 ✅ DONE |
|| 12 | stored-xss-innerhtml | Stored XSS in users.html (innerHTML) | IMP4 §17 | #80 ✅ DONE ||
|| 13 | stored-xss-onclick | Stored XSS via onclick in users.html | IMP4 §18 | #87 ✅ DONE ||
|| 14 | wireguard-values-unescaped | WireGuard Values Unescaped in server.html | IMP4 §19 | #88 ✅ DONE ||
|| 15 | xray-plaintext-private-key | Xray Private Key in Plaintext | IMP2 §9 | #57 ✅ DONE |
| 16 | telemt-config-no-integrity | Telemt Config No Integrity Checks | IMP4 §24 | #90 🔲 TODO |

### Out-of-Phase UX Fix

| # | Slug | Title | Source | Status |
|---|------|-------|--------|--------|
| UX-1 | telemt-qr-wrong-app | Telemt QR Code Instructions Say Wrong App | GitHub #19 | ✅ DONE (PR #93, deployed) |
| REG-1 | awg2-connection-422 | VALID_PROTOCOLS Missing awg2/awg_legacy/dns | GitHub #95 | ✅ DONE (PR #94, deployed) |

### Phase 1 Progress

**Completed: 15/16 issues (Batches 1A-1H) + 2 post-Phase-1 fixes** ✅ Pushed to `feat/phase1-critical-security` branch

**BUG FIXES (post-commit):** 4 additional fixes required after deployment testing:

| Fix | Commit | Root Cause | Symptom |
|-----|--------|------------|---------|
| CSRF header name mismatch | `7e522a9` | `x-csrftoken` vs `x-csrf-token` | 403 on ALL POSTs |
| CSRF sensitive_cookies | `c4b1ea2` | No `sensitive_cookies` — CSRF on unauthenticated login | Login 403 for all users |
| Paramiko invalid kwargs | `3929b5c` | `host_key_verify`/`progress_handler` are asyncssh, not paramiko | TypeError on SSH connect |
|| CSRF HttpOnly meta tag | `59ff641` | Bunkerweb adds HttpOnly — JS can't read csrf cookie | 403 on authenticated POSTs |
|| create_server() missing strip | `0bb1ad6` | reality_private_key stored in raw DB protocols | Private key leaked if DB compromised (read-path stripping masked it) |

**CI fixes:** `d9ae170` (black formatting match, Pillow CVE-2026-40192, flake8 F824 ignore)

| Batch | Issues | GitHub # | Status |
|-------|--------|----------|--------|
| 1A | ssh-shell-injection + paramiko-auto-add-policy | #53, #50 | ✅ QA Approved, Pushed |
| 1B | sql-injection-column-names | #49 | ✅ QA Approved, Pushed |
| 1C | ephemeral-secret-key + no-csrf-protection | #56, #62 | ✅ QA Approved, Pushed |
| 1D | default-admin-credentials | #54 | ✅ QA Approved, Pushed |

**Remaining: 1/16 issues (Batch 1I)** 🔲 Not yet started

|| Batch | Issues | GitHub # | Depends On |
|-------|--------|----------|------------|
| 1E | plaintext-credentials-db + xray-plaintext-private-key | #55, #57 | ✅ QA Approved, Pushed, Deploy-verified |
| 1F | tls-domain-injection + wireguard-echo-injection + configure-container-shell-injection | #74, #78, #84 | ✅ QA Approved, Pushed, Deploy-verified |
| 1G | no-input-validation-pydantic | #71 | ✅ QA Approved, Pushed, Deploy-verified |
| 1H | stored-xss-innerhtml + stored-xss-onclick + wireguard-values-unescaped | #80, #87, #88 | ✅ QA Approved, Pushed, Deploy-verified |
| 1I | telemt-config-no-integrity | #90 | None |

**Post-Phase-1 Bug Fixes (regressions from Phase 1):**

| Batch | Issue | GitHub # | Status |
|-------|-------|----------|--------|
| UX-1 | telemt-qr-wrong-app | #19 | ✅ PR #93 merged, Deploy-verified |
| REG-1 | VALID_PROTOCOLS missing awg2/awg_legacy/dns (HTTP 422) | #95 | ✅ PR #94 merged, Deploy-verified |

### Phase 2 — Critical Bugs & Operational Issues

These cause incorrect behavior, data corruption, or service degradation.

| # | Slug | Title | Source | Issue |
|---|------|-------|--------|-------|
| 17 | background-tasks-swallow-errors | PBT Swallows Errors, Leaks SSH | IMP1 §6 | #44 |
| 18 | async-ssh-blocks-event-loop | 22 Async Handlers Block Event Loop | IMP3 §12 | #75 |
| 19 | get-next-ip-overflow | _get_next_ip Integer Overflow | IMP3 §13 | #79 |
| 20 | add-client-toctou-race | add_client TOCTOU Race Condition | IMP3 §17 | #85 |
| 21 | fragile-server-indexing-telegram | Fragile Server Indexing (Telegram) | IMP2 §R11 | #65 |
| 22 | get-clients-side-effect | get_clients() Side Effect | IMP4 §22 | #82 |
| 23 | missing-rate-limiting | No Rate Limiting on Login | IMP1 §R8 | #67 |
| 24 | share-endpoint-no-rate-limit | Share Endpoint No Rate Limit | IMP2 §11 | #63 |
| 25 | telegram-bot-leaks-exceptions | Telegram Bot Leaks Exceptions | IMP2 §8 | #61 |

### Phase 3 — Bugs & Quick Wins

Smaller bugs and configuration fixes that are quick to implement.

| # | Slug | Title | Source | Issue |
|---|------|-------|--------|-------|
| 26 | debian-only-docker-install | Debian-Only Docker Install (Telemt) | IMP2 §R14 | #73 |
| 27 | hardcoded-xray-version | Hardcoded Xray v1.8.4 | IMP2 §R15 | #72 |
| 28 | language-default-inconsistency | Language Default Inconsistency | IMP2 §C6 | #69 |
| 29 | format-bytes-duplication | format_bytes Duplication (incl. zero bug) | IMP2 §C7 | #68 |
| 30 | format-bytes-zero-bug | format_bytes Zero/Negative Bug | IMP3 §20 | #77 |
| 31 | dockerfile-runs-as-root | Dockerfile Runs as Root | IMP4 §20 | #81 |
| 32 | docker-compose-missing-config | docker-compose Missing Config | IMP4 §21 | #89 |
| 33 | fragile-server-reindexing | Fragile Server Re-indexing | IMP1 §R7 | #60 |
| 34 | ghost-dependencies | Ghost Dependencies in requirements.txt | IMP2 §C5 | #70 |
| 35 | pydantic-v2-dict-deprecated | .dict() → .model_dump() | IMP2 §R13 | #59 |

### Phase 4 — Refactoring & Architecture

Larger structural changes. Dependencies on earlier phases noted.

| # | Slug | Title | Source | Depends On | Issue |
|---|------|-------|--------|------------|-------|
| 36 | pydantic-models-scattered | Pydantic Models to schemas.py | IMP1 §R3 | None | #45 |
| 37 | auth-check-inconsistency | Auth Check Unification | IMP1 §R4 | None | #46 |
| 38 | deprecated-startup-event | Lifespan Context Manager | IMP2 §R12 | background-task-no-supervision | #66 |
| 39 | background-task-no-supervision | PBT Supervision | IMP1 §R6 | deprecated-startup-event | #48 |
| 40 | database-code-duplication | DB Code Duplication | IMP1 §R5 | None | #47 |
| 41 | duplicated-check-docker | check_docker Dedup | IMP2 §R9 | None | #64 |
| 42 | telegram-bot-full-db-dump | Telegram Bot DB Dump | IMP2 §R10 | None | #58 |
| 43 | missing-db-indexes | Missing DB Indexes | IMP2 §R16 | None | #76 |
| 44 | background-task-monolith | PBT Monolith → Services | IMP1 §R2 | deprecated-startup-event | #52 |
| 45 | god-file-app-py | app.py Split into Modules | IMP1 §R1 | pydantic-models-scattered, auth-check-inconsistency | #51 |
| 46 | migration-no-schema-version | Migration Schema Versioning | IMP4 §23 | None | #83 |
| 47 | no-security-integration-tests | Security & Integration Tests | IMP3 §19 | Phase 1 & 2 fixes | #86 |

---

## Dependency Graph

```
Phase 1 (Security) ── no dependencies, do first
    │
Phase 2 (Critical Bugs) ── no dependencies on Phase 1 technically,
    │                        but should follow for safety
Phase 3 (Quick Wins) ── no dependencies
    │
Phase 4 (Architecture Refactor):
    pydantic-models-scattered ──┐
    auth-check-inconsistency  ──┼──► god-file-app-py
                                │
    deprecated-startup-event ────┼──► background-task-no-supervision
                                │   ► background-task-monolith
                                │
    format-bytes-duplication ───► format-bytes-zero-bug (merge)
```

---

## Notes

- **format-bytes-duplication** and **format-bytes-zero-bug** should be merged into a single implementation (new utils.py module). The zero bug is a subset of the duplication fix.
- **dockerfile-runs-as-root** and **docker-compose-missing-config** should be done together as they affect the same deployment configuration.
- **ephemeral-secret-key** and **docker-compose-missing-config** are closely related — SECRET_KEY belongs in docker-compose.yml.
- **fragile-server-reindexing** and **fragile-server-indexing-telegram** share the root cause (improper ID handling) and should be coordinated.
- **plaintext-credentials-db** and **xray-plaintext-private-key** share the encryption-at-rest requirement and could use the same fernet encryption infrastructure.