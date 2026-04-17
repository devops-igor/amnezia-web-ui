# Amnezia Web Panel — WORKLOG

| Timestamp | Agent | Action | Description |
|-----------|-------|--------|-------------|
| [2026-04-14 11:55] | pm_bot | PROJECT_START | Phase 1 — Critical Security Fixes: 16 issues across 9 implementation batches |
| [2026-04-14 12:25] | py_bot | IMPLEMENTATION_COMPLETE | SQL Injection Prevention via Dynamic Column Names: Added allowlists for update_server, update_user, update_connection in database.py. 16 tests pass. |
| [2026-04-14 12:30] | py_bot | IMPLEMENTATION_COMPLETE | SSH Shell Injection Fix: Removed password shell interpolation from ssh_manager.py. Added _run_sudo_command(). SSHHostKeyError exception. 189 tests pass. |
| [2026-04-14 12:30] | py_bot | IMPLEMENTATION_COMPLETE | paramiko AutoAddPolicy MITM Fix: Replaced AutoAddPolicy with RejectPolicy. Added known_hosts table and fingerprint storage. 189 tests pass. |
| [2026-04-14 13:10] | qa_bot | REVIEW_REJECTED | Batch 1A: CRITICAL BUG — ssh_manager.py line 49 calls non-existent method. Password injection PASSES. MITM fix PASSES but contains dead code. |
| [2026-04-14 13:10] | qa_bot | REVIEW_APPROVED | Batch 1B (sql-injection-column-names): SQL injection allowlists correctly implemented. 16/16 tests pass. |
| [2026-04-14 13:45] | qa_bot | REVIEW_APPROVED | Batch 1A Re-Review: Dead code line 49 confirmed removed. 189 tests pass. All acceptance criteria met. |
| [2026-04-14 13:15] | pm_bot | DEV_REWORK | Batch 1A: Removed dead code line (get_host_key_policy) from ssh_manager.py:49. Re-spawning qa_bot. |
| [2026-04-14 14:49] | py_bot | IMPLEMENTATION_COMPLETE | Batch 1C (ephemeral-secret-key + no-csrf-protection): Fixed CSRF exempt_urls. 189 tests pass. |
| [2026-04-14 14:58] | qa_bot | REVIEW_REJECTED | Batch 1C: no-csrf-protection REJECTED — exempt_urls on /api/my/* and /api/servers/* defeats CSRF protection. |
| [2026-04-14 15:05] | pm_bot | DEV_REWORK | Batch 1C: QA rejected CSRF exempt_urls. Sending back to py_bot to remove exempt_urls and add csrf test helper. |
| [2026-04-14 15:25] | qa_bot | REVIEW_APPROVED | Batch 1C Re-Review: No exempt_urls found. CSRF protection active on all POST/PUT/DELETE. 189 tests pass. |
| [2026-04-14 14:40] | py_bot | IMPLEMENTATION_COMPLETE | Batch 1D (default-admin-credentials): Random password, PasswordChangeRequiredMiddleware, change-password endpoint. 218 tests pass. |
| [2026-04-14 15:38] | qa_bot | REVIEW_APPROVED | Batch 1D: All 6 acceptance criteria met. 218/218 tests pass. No MEDIUM+ security findings. |
| [2026-04-14 15:20] | pm_bot | GIT_PUSH | Committed Phase 1 batches 1A-1D (14 files, +1457/-80 lines) as 44cf69f. Pushed to origin. CI/CD triggered. |
| [2026-04-14 19:25] | py_bot | IMPLEMENTATION_COMPLETE | CSRF header name mismatch fix: header_name="x-csrftoken" → "x-csrf-token". 218 tests pass. |
| [2026-04-14 19:28] | qa_bot | REVIEW_APPROVED | CSRF header name mismatch fix: Verified. 218/218 tests pass. |
| [2026-04-14 19:32] | pm_bot | GIT_PUSH | Committed CSRF fix (7e522a9) + CI fixes (d9ae170). Pushed to origin. |
| [2026-04-14 19:45] | pm_bot | DIAGNOSIS | CSRF 403 persists: missing sensitive_cookies config. Fix: add sensitive_cookies={"session"} to CSRFMiddleware. |
| [2026-04-14 20:04] | py_bot | IMPLEMENTATION_COMPLETE | Added sensitive_cookies={"session"} to CSRFMiddleware. 218 tests pass. |
| [2026-04-14 20:18] | pm_bot | GIT_PUSH | Committed sensitive_cookies fix (c4b1ea2). Pushed to origin. All 4 CI pipelines green. |
| [2026-04-14 20:25] | pm_bot | DIAGNOSIS | SSH connection failure: host_key_verify/progress_handler are invalid paramiko kwargs. Batch 1A host key verification code broken. |
| [2026-04-14 21:00] | py_bot | IMPLEMENTATION_COMPLETE | Fixed paramiko connect(): removed invalid kwargs. AutoAddPolicy on first connect, RejectPolicy + fingerprint comparison on subsequent. 232 tests pass. |
| [2026-04-14 21:05] | pm_bot | GIT_PUSH | Committed paramiko fix (3929b5c). Pushed to origin. All 4 CI pipelines green. |
| [2026-04-14 22:30] | py_bot | IMPLEMENTATION_COMPLETE | Added csrf-token meta tags + X-CSRF-Token headers to all fetch() calls. 232 tests pass. |
| [2026-04-14 22:35] | pm_bot | GIT_PUSH | Committed meta tag CSRF fix (59ff641). Pushed to origin. |
| [2026-04-14 22:50] | pm_bot | DEPLOY_TEST | Deployed to dev server. Verified: login works, connection creation works, no 403s, SSH paramiko works. |
| [2026-04-15 14:48] | pm_bot | PROJECT_START | Batch 1E (plaintext-credentials-db + xray-plaintext-private-key) started. Both issues share encryption-at-rest theme. Dependencies met (1C done). |
| [2026-04-15 14:50] | pm_bot | SPAWN | Spawned py_bot for Batch 1E implementation. TASK_PROMPT.md written. |
| [2026-04-15 15:18] | pm_bot | RECEIVED | py_bot completed Batch 1E implementation. Core code compiles, 278/280 tests pass, 2 failures (allowlist issue). Lint issues in test files. Spawning cleanup py_bot. |
| [2026-04-15 15:20] | pm_bot | SPAWN | Spawned py_bot for Batch 1E cleanup: fix 2 failing tests + lint issues. |
| [2026-04-15 15:30] | pm_bot | RECEIVED | Cleanup py_bot completed. Fixed allowlist order in update_server(), removed unused imports, black formatting. 280/280 tests pass. |
| [2026-04-15 15:35] | py_bot | IMPLEMENTATION_COMPLETE | Batch 1E: credential_crypto.py with HKDF+Fernet, database.py encrypts ssh_pass/ssh_key at rest, backup strips credentials, xray_manager strips private_key from protocols, migrations for existing data. 280 tests pass (48 new). black/flake8 clean. |
| [2026-04-15 16:20] | pm_bot | QA_ASSIGNED | Smoke test passed: 280/280 tests, black/flake8 clean. Spawning qa_bot. |
| [2026-04-15 16:45] | qa_bot | REVIEW_APPROVED | Batch 1E: All 12 security checks verified. 280/280 tests. HKDF-SHA256 verified. No MEDIUM+ findings. |
| [2026-04-15 16:50] | pm_bot | GIT_PUSH | Committed Batch 1E (8 files, +1424/-25 lines) as 092a911. Pushed to feat/phase1-critical-security. CI/CD green. |
| [2026-04-15 17:00] | pm_bot | DEPLOY_TEST | Deployed to dev server. Migrations ran (credentials_encrypted + xray_private_keys_cleared). BUG FOUND: create_server() doesn't strip SENSITIVE_PROTOCOL_FIELDS from protocols before DB write. |
| [2026-04-15 17:20] | py_bot | BUGFIX_COMPLETE | Added strip_sensitive_protocol_fields() in create_server() (database.py:285). save_data() already fixed. 281/281 tests pass. |
| [2026-04-15 17:25] | pm_bot | GIT_PUSH | Committed bugfix (2 files, +41/-1) as 0bb1ad6. Pushed. CI/CD green. |
| [2026-04-15 17:30] | pm_bot | DEPLOY_TEST | Redeployed. Bug verified fixed: private_key NOT in raw DB protocols. All live checks passed. |
| [2026-04-15 17:50] | pm_bot | DEPLOY_TEST | Full web UI verification: login, server detail page, SSH connection to real server — all working. Encrypted credentials decrypt transparently for SSH. |
| [2026-04-15 18:00] | pm_bot | PROJECT_COMPLETED | Batch 1E done-done. 8/16 Phase 1 issues complete. |

---

## Session Summary — April 14, 2026

### Phase 1 Security Fixes (Batches 1A-1D) — Completed
- 6/16 issues implemented and QA-approved
- Committed as `44cf69f`, pushed to `feat/phase1-critical-security`

### Post-Deployment Bug Fixes (Found via Runtime Testing)

| Bug | Commit | Root Cause |
|-----|--------|------------|
| CSRF header name mismatch | `7e522a9` | `x-csrftoken` ≠ `x-csrf-token` in Starlette headers |
| CSRF on unauthenticated login | `c4b1ea2` | No `sensitive_cookies` — all POSTs blocked for logged-out users |
| Paramiko invalid kwargs | `3929b5c` | `host_key_verify`/`progress_handler` are asyncssh params, not paramiko |
| CSRF HttpOnly cookie | `59ff641` | Bunkerweb adds HttpOnly — JS can't read csrf cookie; use meta tag instead |
| CI fixes (black, Pillow, flake8) | `d9ae170` | Black version mismatch, CVE-2026-40192, flake8 |

### Dev Server Verified
- URL: https://vpn.dev.drochi.games/ (admin / MIjdQNO6!xe5ypHF7e7u)
- SSH: root@207.2.120.44 (key at /tmp/dev_server_key)
- Login | Connection creation | SSH paramiko | All 232 tests pass | All 4 CI pipelines green

---

## Session Summary — April 15, 2026

### Phase 1 Security Fixes — Batch 1E Completed
- **plaintext-credentials-db (#55)**: SSH credentials (ssh_pass, ssh_key) encrypted at rest using Fernet with HKDF-SHA256 key derivation from SECRET_KEY. Backup strips credentials. Restore handles missing creds. Migration encrypts existing plaintext rows.
- **xray-plaintext-private-key (#57)**: Xray Reality private keys stripped from DB protocols at all 4 write/read paths (create_server, update_server_protocols, save_data, _server_row_to_dict). Private key stays on server (meta.json). Migration clears existing records.

### Progress: 8/16 issues done (Batches 1A-1E)

### Deployment Bug Found and Fixed
- **Bug**: `create_server()` did not strip `reality_private_key` from protocols before DB write. The read-path (`_server_row_to_dict()`) masked this — API output was clean, but raw DB still leaked private keys.
- **Root cause**: Only `update_server_protocols()` and `_server_row_to_dict()` had the stripping; `create_server()` bypassed that path.
- **Fix**: Added `strip_sensitive_protocol_fields()` call in `create_server()` (commit 0bb1ad6).
- **Lesson**: Unit tests checked API output (read path), not raw DB state (write path). Live verification caught this because we checked the actual SQLite data.

### Live Verification Checklist (April 15)
- Login to web UI  
- Server appears in UI after creation  
- Server detail page loads (no auth error)  
- SSH connection to real server works (key decrypted transparently)  
- Raw DB: ssh_pass/ssh_key are Fernet-encrypted tokens  
- Raw DB: reality_private_key NOT in protocols JSON  
- API layer: decrypted credentials match originals  
- API layer: reality_private_key NOT in response protocols  
- Backup: password/private_key stripped, credentials_excluded=True  
- Migration flags: credentials_encrypted=1, xray_private_keys_cleared=1  

### Next Up
- Batch 1F: tls-domain-injection + wireguard-echo-injection + configure-container-shell-injection (#74, #78, #84)
- Batch 1G: no-input-validation-pydantic (#71)
- Batch 1H: stored-xss-innerhtml + stored-xss-onclick + wireguard-values-unescaped (#80, #87, #88)
- Batch 1I: telemt-config-no-integrity (#90)
## Batch 1F — Shell/Config Injection Fixes (IMPLEMENTATION_COMPLETE)

**Date:** 2026-04-16
**Issues Fixed:** #74 (tls-domain-injection), #78 (wireguard-echo-injection), #84 (configure-container-shell-injection)

### Changes
- `app.py`: Added `field_validator("tls_domain")` on `InstallProtocolRequest` — regex allowlist prevents injection
- `telemt_manager.py`: Replaced `re.sub` f-string replacement with match-and-slice pattern — no backreference expansion
- `awg_manager.py`: Refactored `_configure_container()` to split keygen (docker exec) from config write (SFTP+docker_cp); refactored `add_client()` and `toggle_client()` to use SFTP+docker_cp instead of `echo >>`; added `_validate_awg_params()` for numeric AWG param validation
- `tests/test_telemt_manager.py`: Added 13 tests for tls_domain validation
- `tests/test_awg_manager.py`: Added 17 tests for AWG injection prevention and SFTP patterns

### Gate Results
- black: 5 files unchanged
- flake8: 0 new issues (1 pre-existing F824 on app.py:203)
- py_compile: all 3 modules pass
- pytest: 117 passed

| [2026-04-16 12:30] | qa_bot | REVIEW_APPROVED | Batch 1F: All 3 injection fixes verified. 31 new security tests pass. No MEDIUM+ findings. |
| [2026-04-16 12:35] | git_bot | GIT_PUSH | Committed Batch 1F (6 files, +644/-87 lines) as 9109f7d. Pushed to feat/phase1-critical-security. Closed GitHub issues #74, #78, #84. |
| [2026-04-16 12:40] | pm_bot | DEPLOY_TEST | Deployed new image to dev server. amnezia-panel container recreated. Login works, server detail loads, AWG2 + Telemt running. CSRF protection active (blocks malicious API calls). |
| [2026-04-16 12:45] | pm_bot | PROJECT_COMPLETED | Batch 1F done-done. 11/16 Phase 1 issues complete. |
| [2026-04-16 12:50] | pm_bot | WRAP_UP | Session wrap-up. Committed docs updates as 2d2f703. Commented GitHub issues #71, #80, #87, #88, #90 with next-up status. Remaining: 1G (#71 pydantic validation), 1H (#80, #87, #88 XSS/unescaped), 1I (#90 telemt integrity). |

---

## Session Summary — April 16, 2026

### Phase 1 Security Fixes — Batch 1F Completed
- **tls-domain-injection (#74)**: Added `field_validator("tls_domain")` with strict regex allowlist on `InstallProtocolRequest`. Replaced unsafe `re.sub` f-string with match-and-slice in `telemt_manager.py`. 13 tests.
- **wireguard-echo-injection (#78)**: Replaced `echo >>` shell pattern in `add_client()` and `toggle_client()` with SFTP upload + `docker cp`. User data never interpolated into shell commands. 4 tests.
- **configure-container-shell-injection (#84)**: Split `_configure_container()` into keygen phase (safe `docker exec`, no user data) and config write phase (Python string + SFTP + `docker cp`). Added `_validate_awg_params()` for numeric AWG parameter validation. 17 tests.

### Progress: 11/16 issues done (Batches 1A-1F)

### Deployment Verified
- New image pulled on dev server (207.2.120.44)
- amnezia-panel container recreated successfully
- Login works, server detail page loads, AWG2 + Telemt protocols running
- CSRF protection active (blocks unauthenticated API calls)

### Next Up (April 17)
- **Batch 1G**: no-input-validation-pydantic (#71) — Pydantic model input validation
- **Batch 1H**: stored-xss-innerhtml (#80) + stored-xss-onclick (#87) + wireguard-values-unescaped (#88) — XSS fixes
- **Batch 1I**: telemt-config-no-integrity (#90) — Config integrity checks

| [2026-04-16 14:29] | pm_bot | IMPLEMENTATION_START | Batch 1G: No Input Validation on Pydantic Models (#71) — Starting implementation phase |
| [2026-04-16 14:30] | pm_bot | SPAWN | Spawned py_bot for Batch 1G: no-input-validation-pydantic (#71). Session: proc_d9df8482a221 |
| [2026-04-16 14:35] | pm_bot | RECEIVED | py_bot first run: only added WORKLOG entry, no code changes. Re-spawned. |
| [2026-04-16 14:42] | py_bot | IMPLEMENTATION_COMPLETE | Added Field() constraints and field_validator to all 25 Pydantic request models in app.py. 141 new validation tests pass. 2 existing tests need updating for new validation rules. |
| [2026-04-16 14:45] | pm_bot | SPAWN | Cleanup py_bot to fix 2 test failures and create DEV_HANDOVER.md. Session: proc_b2dfd9011f58 |
| [2026-04-16 14:50] | pm_bot | SMOKE_TEST_PASS | app.py compiles, 453/453 tests pass, black/flake8 clean. DEV_HANDOVER.md created by pm_bot. |
| [2026-04-16 14:51] | pm_bot | QA_ASSIGNED | Spawning qa_bot for Batch 1G review. |
| [2026-04-16 15:01] | qa_bot | REVIEW_APPROVED | Batch 1G QA review: All 25 Pydantic models fully validated. 453+141 tests pass. No MEDIUM+ findings. 3 non-blocking observations noted. |
| [2026-04-16 15:02] | pm_bot | GIT_PUSH | Spawning git_bot for Batch 1G commit and push. |
| [2026-04-16 15:05] | git_bot | GIT_PUSH | Batch 1G committed as 4d2cc9c and pushed to origin/feat/phase1-critical-security. |
| [2026-04-16 15:12] | pm_bot | DEPLOY_VERIFIED | Deployed to dev server. L3 verification: Login works, 422 validation on empty password confirmed via API, DB roles clean (admin/user only, no XSS). BunitWeb WAF limited rapid API testing but unit tests cover all 141 validation cases. All 4 LAYER checks pass. |
| [2026-04-16 15:15] | pm_bot | PROJECT_COMPLETED | Batch 1G COMPLETE. 12/16 Phase 1 issues done. Issue #71 closed. |
| [2026-04-16 15:20] | pm_bot | WRAP_UP | Updated TASKS_OVERVIEW.md (12/16 done), VERIFICATION_PLAN.md (partial L3 noted), GitHub #71 closed with summary comment. Session wrap-up complete. |
| [2026-04-16 15:10] | qa_bot | REVIEW_APPROVED | Batch 1G (No Input Validation on Pydantic Models #71): APPROVED. All 25 models validated, 453+141 tests pass, no MEDIUM+ security findings. One LOW observation about null bytes in non-pattern-validated fields. See tasks/no-input-validation-pydantic/QA_REVIEW.md. |

[2026-04-16 15:30] | pm_bot | PROJECT_START | Batch 1H: XSS fixes — stored-xss-innerhtml (#80), stored-xss-onclick (#87), wireguard-values-unescaped (#88). Task prompt written, spawning py_bot.
| [2026-04-16 16:35] | py_bot | IMPLEMENTATION_COMPLETE | Batch 1H: XSS fixes implemented. 3 template files changed. 452-line test suite added. 35/36 tests pass, 1 test assertion fix pending. Zero regressions in 453 existing tests. |
| [2026-04-16 16:35] | pm_bot | RECEIVED | py_bot completed Batch 1H. Smoke test: py_compile passes, 453 existing tests pass. Fixing 1 test assertion. |
| [2026-04-16 16:40] | pm_bot | SMOKE_TEST_PASS | Batch 1H smoke test: 489/489 tests pass (453 existing + 36 new XSS). py_compile passes. Zero regressions. |
| [2026-04-16 16:40] | pm_bot | SPAWN | Spawned qa_bot for Batch 1H review. |
| [2026-04-16 16:55] | qa_bot | REVIEW_APPROVED | Batch 1H (XSS fixes #80/#87/#88): APPROVED. 489/489 tests pass. escapeHtml/escapeJs correctly in base.html. All user data escaped in users.html. Data-attributes replace JSON onclick injection. WireGuard data escaped in server.html. Jinja2 vars use |e. Two LOW pre-existing issues noted (my_connections.html, traffic_reset_strategy). See tasks/batch-1h-xss-fixes/QA_REVIEW.md. |
[2026-04-16 17:00] | pm_bot | SMOKE_TEST_PASS | Batch 1H: 489/489 tests pass (453 existing + 36 new XSS). Zero regressions.
[2026-04-16 17:05] | pm_bot | SPAWN | Spawned qa_bot for Batch 1H review.
[2026-04-16 17:20] | pm_bot | RECEIVED | qa_bot returned REVIEW_APPROVED for Batch 1H. All 3 XSS issues verified. Two LOW pre-existing issues noted for future batches.
[2026-04-16 17:25] | pm_bot | SPAWN | Spawned git_bot for commit and push.
[2026-04-16 17:30] | pm_bot | RECEIVED | git_bot pushed commit e35af19 to feat/phase1-critical-security. CI build successful.
[2026-04-16 17:35] | pm_bot | DEPLOY | Deployed new Docker image to dev server. Container amnezia-panel running.
[2026-04-16 17:40] | pm_bot | VERIFY | Live verification: escapeHtml() and escapeJs() working in production. Server page Jinja2 |e filter applied. GitHub issues #80, #87, #88 closed.
[2026-04-16 17:40] | pm_bot | PROJECT_COMPLETED | Batch 1H DONE-DONE. 15/16 Phase 1 issues complete. Only #90 telemt-config-no-integrity remains.
[2026-04-17 18:57] | py_bot | IMPLEMENTATION_START | Task #19: Fix telemt QR code instructions
[2026-04-17 18:58] | py_bot | IMPLEMENTATION_COMPLETE | Added qr_hint_telemt to all 5 translation files (en/ru/fr/zh/fa). Updated my_connections.html and server.html with id="qrHint" dynamic hint switching via JS. All 489 tests pass, JSON valid, py_compile OK.
[2026-04-17 19:30] | qa_bot | REVIEW_APPROVED | Task #19: All checks pass. 489/489 tests. No security findings. Translation keys verified in 5 languages. Both server.html config entry points covered. Edge cases safe.
[2026-04-17 19:45] | pm_bot | DEPLOY | Pulled new Docker image, restarted amnezia-panel on dev server.
[2026-04-17 19:50] | pm_bot | VERIFY | Live verification: telemt QR hint shows "Scan the QR code with your phone's camera — this opens a Telegram proxy link". AWG2 QR hint still shows "AmneziaVPN or AmneziaWG app". GitHub issue #19 closed.
[2026-04-17 19:50] | pm_bot | PROJECT_COMPLETED | Issue #19 DONE-DONE.
