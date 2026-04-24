# Amnezia Web Panel — Post-Deploy Manual Verification Plan

**Purpose:** Repeatable smoke test suite to run after every major deployment.
**Target:** https://vpn.dev.drochi.games/
**Admin credentials:** admin / MIjdQNO6!xe5ypHF7e7u

---

## Pre-Flight Checks

| # | Check | Command / Action | Expected | Pass? |
|---|-------|-------------------|----------|-------|
| P1 | Container running | `docker ps --filter name=amnezia-panel` | Status: Up | |
| P2 | Health endpoint | `curl -sk https://vpn.dev.drochi.games/ -o /dev/null -w '%{http_code}'` | 302 (redirect to login) | |
| P3 | No crash loops | `docker logs amnezia-panel --tail 20 2>&1` | No Traceback, no FATAL | |
| P4 | DB accessible | `docker exec amnezia-panel python -c "import database; db=database.Database('panel.db'); print(len(db.get_all_servers()))"` | Integer (number of servers) | |

---

## 1 — Authentication & Security

| # | Test | Steps | Expected Result | Pass? |
|---|------|-------|-----------------|-------|
| A1 | Login page loads | Open `/login` | Login form rendered with captcha | |
| A2 | Login success | Enter admin / MIjdQNO6!xe5ypHF7e7u, solve captcha, submit | Redirected to index, server list shown | |
| A3 | Login failure | Enter admin / WRONG_PASSWORD, submit | Error "Invalid credentials", no redirect | |
| A4 | Rate limiting | Hit login 6 times rapidly with wrong password | 429 or rate-limit message on 6th attempt | |
| A5 | CSRF protection | From browser DevTools: `fetch('/api/auth/login',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({username:'admin',password:'test'})})` | 403 Forbidden (no CSRF token) | |
| A6 | Logout | Click logout | Redirected to `/login`, session cleared | |
| A7 | Force password change (if applicable) | Login with a user that has `password_change_required=True` | Redirected to change-password page | |
| A8 | Change password | Navigate to change password, enter current + new password | Password changed, logged in with new password | |

---

## 2 — Server Management

| # | Test | Steps | Expected Result | Pass? |
|---|------|-------|-----------------|-------|
| S1 | Server list loads | Click "Servers" in nav | Server cards appear with protocol badges | |
| S2 | Add server (password auth) | Click "Add Server" → enter SSH host/port/user/password (use test server) → submit | Server created in list, SSH connection test passes | |
| S3 | Add server (key auth) | Click "Add Server" → toggle to key auth → enter SSH host/port/user + private key → submit | Server created, SSH connects | |
| S4 | Add server (invalid creds) | Enter valid host but wrong password | Error message, server NOT created | |
| S5 | Server detail page | Click on existing server card | Server detail page loads, shows host/protocols/stats | |
| S6 | Check services | Click "Check Services" button on server detail | Docker + protocol statuses update (green/red indicators) | |
| S7 | Install protocol (AWG2) | Select AWG2, enter port (e.g. 51820), click Install | Progress shown, container starts, status turns green | |
| S8 | Install protocol (Telemt) | Select Telemt, enter port/TLS domain/max connections, click Install | Container starts, status green | |
| S9 | Container toggle | Click Start/Stop on a running container | Container stops, status updates to red. Click again → starts, status green | |
| S10 | Server stats | Click "Stats" on server detail | CPU/RAM/Disk/Uptime values shown (not 0 or "N/A") | |
| S11 | Reboot server | Click "Reboot" on server detail, confirm | Success message, server comes back online after ~30s | |
| S12 | View server config | Click "Config" on protocol card | Raw config shown in modal (WireGuard/Xray/Telemt format) | |
| S13 | Delete server | Click "Delete Server", confirm | Server removed from list, all connections cleaned up | |

---

## 3 — Connection Management (Admin)

| # | Test | Steps | Expected Result | Pass? |
|---|------|-------|-----------------|-------|
| C1 | List connections | On server detail, open Connections tab | List of existing connections shown | |
| C2 | Add connection (with user) | Click "Add Connection" → select protocol → enter name → select user → submit | Connection created, appears in list with user assigned | |
| C3 | Add connection (no user) | Click "Add Connection" → enter name → no user selection → submit | Connection created, shown without user | |
| C4 | Connection config | Click config icon on connection | Modal shows: config text, VPN link, QR code (not for Telemt) | |
| C5 | Toggle connection | Click enable/disable toggle on a connection | Peer disabled/enabled on server, status updates | |
| C6 | Edit connection (Telemt) | Click edit on a Telemt connection → change quota/expiry → save | Values updated, reflected in list | |
| C7 | Remove connection | Click delete on a connection → confirm | Connection removed from server and DB | |

---

## 4 — User Management (Admin)

| # | Test | Steps | Expected Result | Pass? |
|---|------|-------|-----------------|-------|
| U1 | Users list | Navigate to `/users` | User table with pagination/search | |
| U2 | Add user (basic) | Click "Add User" → fill username/password/role → submit | User created, appears in table | |
| U3 | Add user (full) | Add user with TG/email/description/traffic limit/reset strategy/expiry → submit | All fields saved, visible in edit view | |
| U4 | Add user with auto-connection | Add user → check "Create connection" → select server + protocol → submit | User created WITH an active connection | |
| U5 | Edit user | Click edit on a user → change email/description → save | Fields updated in table | |
| U6 | Toggle user | Click enable/disable on a user | User status toggles, all their WireGuard peers disabled/enabled | |
| U7 | Search users | Type in search box | Table filters in real time | |
| U8 | Delete user | Click delete on a user → confirm | User removed, all their connections deleted from servers | |
| U9 | XSS prevention | Edit user → enter `<script>alert(1)</script>` in description field → save | Script NOT executed, shown as escaped text | |

---

## 5 — My Connections (User Role)

| # | Test | Steps | Expected Result | Pass? |
|---|------|-------|-----------------|-------|
| M1 | User login | Login as a non-admin user (role=user) | Redirected to `/my` (My Connections) | |
| M2 | Connection list | View own connections | Only own connections shown | |
| M3 | Create connection | Click "Create Connection" → select server + protocol → submit | Connection created, peer added on server | |
| M4 | Rate limiting | Create connections rapidly (more than limit) | 429 after rate limit exceeded | |
| M5 | Config/QR download | Click config icon on own connection | Modal shows config text, VPN link, QR code | |

---

## 6 — Share Feature

| # | Test | Steps | Expected Result | Pass? |
|---|------|-------|-----------------|-------|
| SH1 | Enable share | On user page, click Share → enable with password → submit | Share URL shown | |
| SH2 | Access share link | Open `/share/{token}` in incognito browser | Password prompt shown | |
| SH3 | Share auth | Enter share password → submit | Connections list shown | |
| SH4 | Download config | Click config on shared connection | Config file downloads | |
| SH5 | Disable share | Disable share on user → reopen share link | 404 or "Share not found" | |

---

## 7 — Leaderboard

| # | Test | Steps | Expected Result | Pass? |
|---|------|-------|-----------------|-------|
| L1 | Leaderboard loads | Navigate to `/leaderboard` | Table with rankings, traffic columns | |
| L2 | All-time / monthly toggle | Switch between all-time and monthly | Data updates, own rank shown in card | |

---

## 8 — Settings (Admin)

| # | Test | Steps | Expected Result | Pass? |
|---|------|-------|-----------------|-------|
| ST1 | Settings page loads | Navigate to `/settings` | All settings sections visible | |
| ST2 | Appearance — title | Change panel title → save → check nav bar | Title updated in nav | |
| ST3 | Appearance — language | Switch language to Russian → save | UI labels switch to Russian | |
| ST4 | Captcha toggle | Disable captcha → save → logout → login | No captcha shown on login form | |
| ST5 | Re-enable captcha | Enable captcha → save → logout → login | Captcha shown again | |
| ST6 | Telegram bot | Enter valid bot token → click Start → wait 3s | Status shows "Running", bot responds to `/start` in Telegram | |
| ST7 | Telegram bot stop | Click Stop | Status shows "Stopped" | |
| ST8 | Backup download | Click "Download Backup" | JSON file downloads with server/user/connection data | |
| ST9 | Backup restore | Upload previously downloaded backup | Data restored (test on a fresh DB) | |
| ST10 | Connection limits | Change max connections per user → save | New limit enforced on next connection creation | |

---

## 9 — Telegram Bot (if enabled)

| # | Test | Steps | Expected Result | Pass? |
|---|------|-------|-----------------|-------|
| T1 | Bot starts | Start bot in Settings → open Telegram → send `/start` | Bot replies with greeting + inline keyboard with connections | |
| T2 | Get config | Tap a connection button in Telegram | Bot sends: header message, config text, VPN link (if applicable), .conf file attachment | |
| T3 | Refresh | Tap "🔄 Refresh" | Bot reloads connections list | |
| T4 | Error handling | Disconnect server from Telegram (simulate error) → try to get config | Bot sends generic error message (no traceback/SQL leaked) | |

---

## 10 — Security & Hardening Checks

| # | Test | Steps | Expected Result | Pass? |
|---|------|-------|-----------------|-------|
| SEC1 | No plaintext creds in DB | `docker exec amnezia-panel python -c "import sqlite3; c=sqlite3.connect('panel.db'); r=c.execute('SELECT ssh_pass,ssh_key FROM servers LIMIT 1').fetchone(); print(r[0][:20] if r else 'no servers')" ` | Fernet token (starts with `gAAAAA`) or `[encrypted]`, NOT plaintext | |
| SEC2 | No private keys in protocols | `docker exec amnezia-panel python -c "import sqlite3,json; c=sqlite3.connect('panel.db'); r=c.execute('SELECT protocols FROM servers LIMIT 1').fetchone(); p=json.loads(r[0]); print('private_key' in p.get('xray',{}))" ` | False (private_key stripped) | |
| SEC3 | CSRF on POST without token | `curl -sk -X POST https://vpn.dev.drochi.games/api/auth/login -H 'Content-Type: application/json' -d '{"username":"admin","password":"test"}'` | 403 Forbidden | |
| SEC4 | XSS in user fields | Edit user description → enter `<img src=x onerror=alert(1)>` → save | Not executed, shown as escaped text | |
| SEC5 | Input validation | Try adding server with empty host or port "abc" | 422 validation error, no server created | |
| SEC6 | Rate limit on login | 6 rapid failed logins → check 6th response | 429 or rate limiting message | |
| SEC7 | Role-based access | Login as user role → try accessing `/users` page | 403 Forbidden or redirect to `/my` | |

---

## 11 — Data Integrity

| # | Test | Steps | Expected Result | Pass? |
|---|------|-------|-----------------|-------|
| D1 | Server ID stability | Delete a server (not the last one) → check remaining server IDs in DB | IDs unchanged (no renumbering) | |
| D2 | Cascading delete | Delete a user → check DB for orphan connections | No connections remain with deleted user's ID | |
| D3 | Connection peer cleanup | Delete a connection → check server container for peer config | Peer removed from WireGuard/Xray config | |
| D4 | Backup completeness | Download backup → inspect JSON | Contains: settings, servers (encrypted creds), users, connections | |
| D5 | Template integrity | `docker exec amnezia-panel python -c "from integrity import verify_integrity; print(verify_integrity('protocol_telemt/config.toml'))" ` | True | |

---

## 12 — Background Tasks

| # | Test | Steps | Expected Result | Pass? |
|---|------|-------|-----------------|-------|
| BG1 | Traffic sync | Wait 10 minutes → check user traffic_used in DB | Values updated (non-zero if active) | |
| BG2 | Over-quota disable | Set user traffic_limit below traffic_used → wait 10 min | User disabled, peers deactivated on servers | |
| BG3 | Expiry disable | Set user expiration to past date → wait 10 min | User disabled | |

---

## 13 — Language & Localization

| # | Test | Steps | Expected Result | Pass? |
|---|------|-------|-----------------|-------|
| I18N1 | English | Set language to English → verify all pages | All labels in English | |
| I18N2 | Russian | Set language to Russian → verify all pages | All labels in Russian | |
| I18N3 | Persian (RTL) | Set language to Persian → verify page layout | RTL layout direction, Persian labels | |
| I18N4 | Fallback | Set language to unsupported → reload | Falls back to English (or last valid) | |

---

## 14 — Edge Cases & Regression

| # | Test | Steps | Expected Result | Pass? |
|---|------|-------|-----------------|-------|
| E1 | Duplicate server add | Add same SSH host twice | Either error or second server created (verify behavior is intentional) | |
| E2 | Empty forms | Submit add-server with all fields blank | 422 validation errors, no server created | |
| E3 | Very long input | Enter 1000+ char description for user | Truncated or accepted but not causing DB error | |
| E4 | Unicode in names | Create user with name "Тест 用户 مرحبا" | Created successfully, displayed correctly | |
| E5 | Telemt config special chars | Add Telemt connection with special chars in name | Config generated correctly, no injection | |
| E6 | AWG2 full subnet | On /24 subnet, add 254 connections → try 255th | Clear error message, no crash | |

---

## Verification Summary

| Section | Tests | Critical (must pass for deploy) |
|---------|-------|--------------------------------|
| Pre-Flight | 4 | All 4 |
| Auth & Security | 8 | A1-A6 |
| Server Mgmt | 13 | S1-S3, S6-S8 |
| Connections | 7 | C2, C4 |
| User Mgmt | 9 | U1-U3, U9 |
| My Connections | 5 | M1-M3 |
| Share | 5 | SH1-SH3 |
| Leaderboard | 2 | L1 |
| Settings | 10 | ST1-ST3, ST6 |
| Telegram Bot | 4 | T1-T2 (if bot configured) |
| Security Hardening | 7 | SEC1-SEC7 |
| Data Integrity | 5 | D1-D3, D5 |
| Background Tasks | 3 | BG1 (informal) |
| Localization | 4 | I18N1-I18N2 |
| Edge Cases | 6 | E2, E5 |
| **Total** | **92** | **~35 critical** |

---

## How to Use This Plan

1. **After every deploy:** Run Pre-Flight (P1-P4) + Critical tests (~35 items)
2. **After security patches:** Run full Security Hardening (SEC1-SEC7) + Auth (A1-A8)
3. **Before major releases:** Run complete 92-test suite
4. **After DB/backup changes:** Run Data Integrity (D1-D5)
5. **After UI changes:** Run Localization (I18N1-I18N4)

Mark each test PASS/FAIL. Any FAIL in critical tests = rollback.

---

*Last updated: 2026-04-24*
*Generated by: pm_bot*