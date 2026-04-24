# TASK: E2E Test Suite (Playwright) + Manual UI Checklist

**Type:** Implementation (Testing Infrastructure)
**Priority:** P2 (Quality)
**Status:** 🔲 TODO
**Created:** 2026-04-24

---

## Overview

Add a hybrid E2E testing approach to the project:

1. **Automated Playwright test suite** covering critical admin/user flows
2. **Manual UI checklist** for visual/UX verification that automation can't catch

Both should be runnable after every major deploy to the dev server.

---

## Part 1: Automated E2E Tests (Playwright)

### Tech Stack
- **Playwright for Python** (compatible with existing pytest setup)
- Run against dev server (https://vpn.dev.drochi.games/) or locally
- Headless by default, headed mode for debugging

### Test Scenarios

#### Authentication Flow
| # | Test | Steps | Assertion |
|---|------|-------|-----------|
| E2E-1 | Login page loads | GET /login | Page title contains "Login", username/password fields present |
| E2E-2 | Login success | Enter admin/password, submit | Redirected to server list, "admin" name in nav |
| E2E-3 | Login failure | Enter admin/wrong_password, submit | Error message shown, stays on login page |
| E2E-4 | Rate limiting | 6 rapid failed logins | 429 on 6th attempt |
| E2E-5 | CSRF protection | POST to /api/auth/login without CSRF token | 403 Forbidden |
| E2E-6 | Logout | Click "Logout" | Redirected to /login, session cleared |

#### Server Management Flow (Admin)
| # | Test | Steps | Assertion |
|---|------|-------|-----------|
| E2E-7 | Server list loads | Navigate to / | Server cards rendered with host, protocol badges |
| E2E-8 | Server detail page | Click "Manage" on server | Detail page with stats, protocol cards, connections |
| E2E-9 | Check services | Click "Check" on server detail | Docker/SSH status indicators update |
| E2E-10 | Install protocol | Click "Install" on AWG card, enter port | Protocol shows "RUNNING" after install completes |
| E2E-11 | Stop container | Click "Stop" on running protocol | Status changes to "STOPPED" |
| E2E-12 | Start container | Click "Start" on stopped protocol | Status changes to "RUNNING" |
| E2E-13 | Server stats | Click "Stats" | CPU/RAM/Disk values shown (not all zero) |

#### Connection Management Flow (Admin)
| # | Test | Steps | Assertion |
|---|------|-------|-----------|
| E2E-14 | List connections | Click "Connections" on protocol card | Connection list with names, traffic |
| E2E-15 | Add connection | Click "Add", enter name, select user | Connection appears in list |
| E2E-16 | View config | Click config button on connection | Modal shows config text + QR code + VPN link |
| E2E-17 | Toggle connection | Click enable/disable toggle | Status updates, peer active/inactive |
| E2E-18 | Delete connection | Click delete, confirm | Connection removed |

#### User Management Flow (Admin)
| # | Test | Steps | Assertion |
|---|------|-------|-----------|
| E2E-19 | Users list loads | Navigate to /users | Table with usernames, roles, traffic |
| E2E-20 | Add user | Click "Add User", fill form, submit | User appears in table |
| E2E-21 | Edit user | Click edit, change email, save | Email updated in table |
| E2E-22 | Toggle user | Click enable/disable toggle | Status changes |
| E2E-23 | Add connection to user | Click connections icon, add | Connection shown under user |
| E2E-24 | Delete user | Click delete, confirm | User removed from table |
| E2E-25 | XSS prevention | Enter `<script>alert(1)</script>` in username | 422 validation error (alphanumeric only) |

#### My Connections Flow (User role)
| # | Test | Steps | Assertion |
|---|------|-------|-----------|
| E2E-26 | User login | Login as non-admin user | Redirected to /my (My Connections) |
| E2E-27 | Create connection | Click "Create Connection" | Connection created successfully |
| E2E-28 | View own config | Click config on own connection | Modal shows config text |
| E2E-29 | Role-based access | Navigate to /users as user | Redirected to /my or 403 |

#### Settings Flow (Admin)
| # | Test | Steps | Assertion |
|---|------|-------|-----------|
| E2E-30 | Settings page loads | Navigate to /settings | All sections visible |
| E2E-31 | Change title | Edit title, save, verify nav | Title updated in header |
| E2E-32 | Toggle captcha | Disable captcha, save | Setting saved |
| E2E-33 | Download backup | Click "Download Backup" | JSON file downloaded |

#### Share Flow
| # | Test | Steps | Assertion |
|---|------|-------|-----------|
| E2E-34 | Enable share | Click share icon on user, enable | Share URL shown |
| E2E-35 | Access share link | Open share URL in new context | Password prompt shown |
| E2E-36 | Download shared config | Authenticate share, get config | Config file content |

### Architecture

```
tests/e2e/
  conftest.py          -- Playwright fixtures, browser context, auth helpers
  test_auth.py         -- E2E-1 to E2E-6
  test_servers.py      -- E2E-7 to E2E-13
  test_connections.py  -- E2E-14 to E2E-18
  test_users.py        -- E2E-19 to E2E-25
  test_my_connections.py  -- E2E-26 to E2E-29
  test_settings.py     -- E2E-30 to E2E-33
  test_share.py        -- E2E-34 to E2E-36
```

### Configuration

- `playwright.config.py` or `pytest.ini` section for base_url, browser (chromium), headless
- Environment variables: `E2E_BASE_URL`, `E2E_ADMIN_USER`, `E2E_ADMIN_PASS`
- Fixtures handle CSRF token fetch + session setup
- Test data cleanup in teardown (delete test users/connections created during run)

### Dependencies to Add

```
# requirements-dev.txt
playwright==1.52.0
pytest-playwright==0.7.0
```

Post-install: `playwright install chromium`

---

## Part 2: Manual UI Checklist

Already created at `/home/igor/Amnezia-Web-Panel/VERIFICATION_PLAN.md` (92 tests).
Playwright covers ~36 critical flows. Manual checklist covers the rest:

- Visual layout / alignment issues
- Language switching (all 5 languages)
- Theme toggle (dark/light)
- Settings page visual check
- Error states (network timeout, SSH failure)
- Responsive behavior
- QR code rendering
- Toast notifications appearance

---

## Acceptance Criteria

1. Playwright installed and runnable via `pytest tests/e2e/ --base-url https://vpn.dev.drochi.games/`
2. All 36 E2E test scenarios pass against dev server
3. Test data is cleaned up after each run (no stale test users/connections)
4. CSRF token handling works automatically in fixtures
5. `requirements-dev.txt` updated with playwright deps
6. Manual checklist updated to reference automated tests (mark what's covered)
7. CI can optionally run E2E tests (not blocking, but available)

## Dependencies

- None (no Phase 4 blockers)
- Dev server must be accessible and have at least 1 server with AWG2 + Telemt running

## Estimated Effort

- Playwright setup + conftest: ~30 min
- Auth tests: ~20 min
- Server tests: ~30 min
- Connection tests: ~30 min
- User tests: ~30 min
- My Connections + Settings + Share: ~40 min
- Total: ~3-4 hours py_bot work