## 2026-04-10 — Fix Telemt proxy links default port (443 → 18443)

**Task:** Change default external port for Telemt proxy from 443 to 18443 across three files.

**Changes made:**
1. `telemt_manager.py:78` — `port="443"` → `port="18443"`
2. `templates/server.html:818` — `portInput.value = '443'` → `'18443'` (also updated hint text)
3. `protocol_telemt/config.toml:28` — `public_port = 443` → `18443`

**Verification:** `python3 -m py_compile telemt_manager.py` passed.

**Time:** ~5 minutes.

| 2026-04-10 05:03 | pm_bot | SMOKE_TEST | `python3 -m py_compile telemt_manager.py` PASSED. Git diff confirmed: exactly 3 files, 4 lines changed, no unintended modifications. Internal ports (443) intact. |
| 2026-04-10 05:06 | qa_bot | REVIEW_APPROVED | QA review complete. All 3 changes verified correct. All 5 internal port references confirmed unchanged. Verdict: APPROVED. |
| 2026-04-10 05:12 | qa_bot | REVIEW_APPROVED | QA review completed. All 3 changes verified correct. All 5 unchanged items verified intact. Port flow analysis confirmed end-to-end correctness. No regressions detected. Verdict: APPROVED. See QA_REVIEW.md for details. |
| 2026-04-10 05:11 | git_bot | COMMIT | Created feature branch feat/telemt-default-port-18443, committed 3 files. Commit message amended by pm_bot to fix typo. |
| 2026-04-10 05:15 | pm_bot | PUSH + PR | Pushed branch to origin, created PR #15: https://github.com/devops-igor/amnezia-web-ui/pull/15 |
