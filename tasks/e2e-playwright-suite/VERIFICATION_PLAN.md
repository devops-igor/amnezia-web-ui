# E2E Playwright Test Suite — Verification Plan

**Type:** Testing Infrastructure Verification
**Date:** 2026-04-24

---

## Layer 1: Automated Verification (py_bot runs)

| # | Check | Command | Expected |
|---|-------|---------|----------|
| L1-1 | Playwright installed | `pip show playwright` | Version 1.52+ |
| L1-2 | Browser installed | `playwright install --dry-run chromium` | No error |
| L1-3 | pytest-playwright plugin loaded | `pytest --co tests/e2e/` | Lists 36+ tests |
| L1-4 | Import test fixtures | `python -c "from tests.e2e.conftest import *"` | No ImportError |
| L1-5 | All E2E tests pass | `pytest tests/e2e/ -v --base-url https://vpn.dev.drochi.games/` | 36+/36+ passed |
| L1-6 | No stale test data | Check DB for test users/connections after run | No "e2e_test_*" users remain |
| L1-7 | requirements-dev.txt updated | `grep playwright requirements-dev.txt` | Both packages listed |

## Layer 2: Manual Verification (pm_bot runs after deploy)

| # | Check | Steps | Expected |
|---|-------|-------|----------|
| L2-1 | Tests run against live dev server | `pytest tests/e2e/ --base-url https://vpn.dev.drochi.games/` | All pass |
| L2-2 | Headed mode works for debugging | `pytest tests/e2e/test_auth.py --headed` | Browser opens, tests run |
| L2-3 | Test cleanup works | Run full suite, check users table | No test artifacts left |
| L2-4 | Manual UI checklist items not in Playwright | Follow remaining items in VERIFICATION_PLAN.md | All pass |

## Layer 3: Integration Verification

| # | Check | Steps | Expected |
|---|-------|-------|----------|
| L3-1 | E2E after fresh deploy | After deploying new image to dev server, run E2E suite | All pass |
| L3-2 | Config contains real data | During E2E run, verify config modal shows real VPN data | IP addresses, ports, keys present |
| L3-3 | Playwright handles CSRF | Login fixture works without manual CSRF handling | Auto-fetched and included |