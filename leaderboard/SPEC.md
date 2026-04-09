# SPEC: Traffic Leaderboard

**Project:** Amnezia-Web-Panel
**Feature Folder:** `leaderboard/`
**Author:** pm_bot
**Date:** 2026-04-09
**Status:** DRAFT

---

## 1. Goal

Add a publicly visible leaderboard page showing which users have consumed the most VPN traffic. All logged-in users (any role: admin, support, user) can view it. The leaderboard motivates engagement and gives admins a quick overview of top consumers.

---

## 2. Scope

### In Scope
- New dedicated page at `/leaderboard`
- New navigation link in base template header
- Leaderboard table showing: rank, username, total download, total upload, combined total
- Two time views: **All-time** and **Current month**
- Route handler in `app.py`
- New Jinja2 template `leaderboard.html`
- i18n support for all 5 languages (en, ru, fr, zh, fa)
- Consistent glassmorphism UI style matching existing pages
- Responsive layout (mobile-friendly)
- Unit tests

### Out of Scope
- Telegram bot integration (web only for now)
- Admin-only visibility toggle
- Per-protocol breakdown
- Historical months (only current month, not arbitrary month selection)
- API endpoint for programmatic access
- CSV/PDF export

---

## 3. Data Model — Current State

The system already tracks traffic per user in `data.json`:

```
user.traffic_used    — resettable traffic (resets on daily/weekly/monthly strategy)
user.traffic_total   — cumulative all-time traffic (never resets)
user.last_reset_at   — ISO timestamp of last reset
user.traffic_reset_strategy — "never" | "daily" | "weekly" | "monthly"
```

Traffic deltas are calculated from protocol managers' `get_clients()` output:
- `userData.dataReceivedBytes` (download / RX)
- `userData.dataSentBytes` (upload / TX)

These are summed per client in the background sync task (`periodic_background_tasks`, runs every ~10 min) and added to `traffic_used` and `traffic_total`.

### Problem: No Separate Download/Upload Tracking

Currently, RX and TX are **combined** into a single delta before being stored:

```python
# app.py line 811-813
rx = c.get("userData", {}).get("dataReceivedBytes", 0)
tx = c.get("userData", {}).get("dataSentBytes", 0)
client_bytes[c.get("clientId")] = rx + tx  # <-- combined
```

This means the leaderboard **cannot** show separate download/upload columns without a data model change.

### Required Data Model Changes

Add two new fields to each user in `data.json`:

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `traffic_total_rx` | int | 0 | Cumulative all-time download bytes |
| `traffic_total_tx` | int | 0 | Cumulative all-time upload bytes |

Add two fields for monthly tracking (current month):

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `monthly_rx` | int | 0 | Download bytes for current month |
| `monthly_tx` | int | 0 | Upload bytes for current month |
| `monthly_reset_at` | str | `""` | ISO timestamp of last monthly reset (for rollover detection) |

### Data Migration

In the existing `migrate_data()` function, add:
- Set `traffic_total_rx = 0` and `traffic_total_tx = 0` if missing
- Set `monthly_rx = 0`, `monthly_tx = 0`, `monthly_reset_at = ""` if missing
- Existing `traffic_total` value remains as the combined total (backward compat)
- New traffic will populate RX/TX separately going forward

### Background Sync Update

In `periodic_background_tasks()`, change the delta calculation to track RX/TX separately:

```python
# BEFORE (line 813):
client_bytes[c.get("clientId")] = rx + tx

# AFTER:
client_bytes[c.get("clientId")] = {
    "rx": rx,
    "tx": tx,
}
```

Then in the user update loop, store deltas separately:

```python
# Instead of:
u["traffic_used"] += delta
u["traffic_total"] += delta

# Do:
u["traffic_total"] += rx_delta + tx_delta
u["traffic_total_rx"] += rx_delta
u["traffic_total_tx"] += tx_delta
u["monthly_rx"] += rx_delta
u["monthly_tx"] += tx_delta
```

Monthly rollover: check if `monthly_reset_at` month differs from current month. If so, reset `monthly_rx` and `monthly_tx` to 0, update `monthly_reset_at`.

---

## 4. API

### New Page Route

```
GET /leaderboard?period=all-time|monthly
```

- Requires session auth (all roles)
- Default period: `all-time`
- Returns rendered `leaderboard.html`

### New API Route

```
GET /api/leaderboard?period=all-time|monthly
```

- Requires session auth (all roles)
- Returns JSON for potential future use

### Response Shape

```json
{
  "period": "all-time",
  "entries": [
    {
      "rank": 1,
      "username": "alice",
      "download": 5368709120,
      "upload": 1073741824,
      "total": 6442450944
    }
  ],
  "current_user_rank": 3
}
```

Byte values are raw bytes. Frontend formats them (e.g., "5.00 GB").

---

## 5. Frontend

### Navigation

Add "Leaderboard" link to the nav bar in `templates/base.html`:
- Icon: trophy or chart icon (use existing icon style)
- Position: after "Settings" link, before logout
- Visible to all authenticated users

### Template: `templates/leaderboard.html`

Extends `base.html`. Layout:

```
+--------------------------------------------------+
|  Leaderboard                                      |
|                                                   |
|  [All-time]  [Current month]      ← tab toggle   |
|                                                   |
|  Rank | Username | Download | Upload | Total     |
|  -----|----------|----------|--------|---------  |
|  1    | alice    | 5.00 GB  | 1.00 GB| 6.00 GB  |
|  2    | bob      | 3.20 GB  | 0.80 GB| 4.00 GB  |
|  ...                                              |
+--------------------------------------------------+
```

- Highlight current logged-in user's row (subtle accent)
- Show "current_user_rank" below the table if user is not in top display
- Format bytes as human-readable (B, KB, MB, GB, TB) with 2 decimal places
- Glassmorphism card style matching `templates/settings.html` / `templates/users.html`
- Period toggle: tabs or pill buttons, no page reload (JS fetch to `/api/leaderboard?period=`)
- Empty state: "No traffic data yet" message
- Responsive: horizontal scroll on small screens, or stack columns

### Byte Formatting (JS)

Add a reusable `formatBytes(n)` function in `base.html` or inline:
```javascript
function formatBytes(bytes) {
    if (bytes === 0) return '0 B';
    const k = 1024;
    const sizes = ['B', 'KB', 'MB', 'GB', 'TB'];
    const i = Math.floor(Math.log(bytes) / Math.log(k));
    return parseFloat((bytes / Math.pow(k, i)).toFixed(2)) + ' ' + sizes[i];
}
```

---

## 6. i18n

New keys needed in all 5 translation files (`translations/{en,ru,fr,zh,fa}.json`):

| Key | en | ru | fr | zh | fa |
|-----|----|----|----|----|-----|
| `leaderboard` | Leaderboard | Рейтинг | Classement | 排行榜 | تابلوی رتبه‌بندی |
| `leaderboard.all_time` | All-time | За всё время | Tout le temps | 全部时间 | تمام زمان‌ها |
| `leaderboard.monthly` | Current month | Текущий месяц | Mois en cours | 当月 | ماه جاری |
| `leaderboard.rank` | Rank | Место | Rang | 排名 | رتبه |
| `leaderboard.download` | Download | Загрузка | Téléchargement | 下载 | دانلود |
| `leaderboard.upload` | Upload | Отдача | Envoi | 上传 | آپلود |
| `leaderboard.total` | Total | Всего | Total | 总计 | مجموع |
| `leaderboard.username` | Username | Пользователь | Nom d'utilisateur | 用户名 | نام کاربری |
| `leaderboard.no_data` | No traffic data yet | Пока нет данных о трафике | Pas encore de données de trafic | 暂无流量数据 | هنوز داده ترافیکی نیست |
| `leaderboard.your_rank` | Your rank: {rank} | Ваше место: {rank} | Votre rang : {rank} | 你的排名: {rank} | رتبه شما: {rank} |

---

## 7. Files to Create/Modify

### New Files
| File | Description |
|------|-------------|
| `templates/leaderboard.html` | Leaderboard page template |
| `tests/test_leaderboard.py` | Unit tests for leaderboard route and API |

### Modified Files
| File | Change |
|------|--------|
| `app.py` | Add 2 new routes (page + API), update `periodic_background_tasks()` for RX/TX separation, update `migrate_data()` for new fields, add `format_traffic()` helper |
| `templates/base.html` | Add leaderboard nav link |
| `translations/en.json` | Add leaderboard i18n keys |
| `translations/ru.json` | Add leaderboard i18n keys |
| `translations/fr.json` | Add leaderboard i18n keys |
| `translations/zh.json` | Add leaderboard i18n keys |
| `translations/fa.json` | Add leaderboard i18n keys |

---

## 8. Acceptance Criteria

1. **New page** — `/leaderboard` renders for all logged-in users (all roles)
2. **All-time view** — Shows all users ranked by `traffic_total`, with separate download/upload/total columns, descending by total
3. **Monthly view** — Shows users ranked by current month's traffic (`monthly_rx` + `monthly_tx`), resets at month boundary
4. **Data accuracy** — All-time total column matches existing `traffic_total` field for backward compatibility
5. **Separate RX/TX** — Download and upload columns show `traffic_total_rx` and `traffic_total_tx` respectively
6. **Current user highlight** — Logged-in user's row is visually distinct
7. **Current user rank** — If user is not in visible entries, rank shown below table
8. **Navigation** — Leaderboard link visible in nav for all authenticated users
9. **i18n** — All labels translated in 5 languages, RTL works for Persian
10. **Responsive** — Usable on mobile (horizontal scroll or stacked)
11. **Empty state** — Clean message when no traffic data exists
12. **Migration** — Existing data.json loads without error; new fields default to 0
13. **Backward compat** — `traffic_total` still populated (not removed); existing features unaffected
14. **Tests** — Route tests (auth required, both periods, data shape), API tests, monthly rollover logic test
15. **No security regression** — No new unauthorized access; leaderboard requires session auth

---

## 9. Implementation Notes

### Performance
- Leaderboard aggregation is done in-memory from `data.json` (same as all other routes)
- User count expected to be small (<1000), so no pagination needed initially
- If user count grows, add server-side pagination later

### Monthly Reset Logic
- On each background sync run, check if current month > `monthly_reset_at` month
- If rollover detected: set `monthly_rx = 0`, `monthly_tx = 0`, `monthly_reset_at = now`
- This happens inside the existing `periodic_background_tasks()` function
- NOT based on `traffic_reset_strategy` — that field controls `traffic_used` reset for limits, which is a separate concern

### Order of Implementation
1. Data model changes + migration
2. Background sync RX/TX separation
3. Monthly reset logic in background sync
4. API route `/api/leaderboard`
5. Page route `GET /leaderboard`
6. Template `leaderboard.html`
7. Nav link in `base.html`
8. i18n keys
9. Tests
10. Manual visual QA

---

## 10. Risks & Mitigations

| Risk | Likelihood | Mitigation |
|------|-----------|------------|
| RX/TX split breaks existing traffic_used logic | Medium | Keep `traffic_used` and `traffic_total` unchanged; add new fields alongside |
| Large data.json causes slow leaderboard | Low | In-memory aggregation is fast for <1000 users; add pagination if needed |
| Monthly reset race condition | Low | Protected by existing DATA_LOCK |
| Persian RTL layout breaks table | Medium | Test RTL specifically; use `dir="auto"` on table |