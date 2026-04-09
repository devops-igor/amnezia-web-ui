# Task Assignment: Leaderboard Frontend — Template, Nav & i18n

## Metadata
- **Task ID:** TASK-03
- **Project:** Amnezia-Web-Panel
- **Assigned to:** py_bot
- **Assigned by:** pm_bot
- **Date:** 2026-04-09
- **Priority:** HIGH (depends on TASK-02)
- **Status:** PENDING

## Objective
Create the leaderboard page UI (template, styling, navigation link, internationalization) that consumes the routes from TASK-02.

## Background/Context
TASK-02 adds the backend routes. This task builds the user-facing page — a glassmorphism-styled leaderboard table with all-time/current month toggle, showing rank, username, download, upload, and total traffic columns.

## Requirements

### Must Have
- [ ] Create `templates/leaderboard.html` extending `base.html`
- [ ] Page layout: centered glassmorphism card matching the style of `settings.html` and `users.html`
- [ ] Period toggle: two tab/pill buttons at the top — "All-time" and "Current month"
- [ ] Period toggle switches data without full page reload (JS fetch to `/api/leaderboard?period=`)
- [ ] Data table with columns: Rank, Username, Download, Upload, Total
- [ ] Data sorted by Total descending (already sorted by backend)
- [ ] Highlight current logged-in user's row with subtle accent (e.g., slightly different background or border)
- [ ] Show "Your rank: X" below the table if logged-in user is not in the visible set
- [ ] Add `formatBytes()` JS function in `base.html` (or inline) for human-readable byte formatting (B, KB, MB, GB, TB, 2 decimal places)
- [ ] Empty state: show "No traffic data yet" message when entries list is empty
- [ ] Add Leaderboard navigation link in `templates/base.html` header, after Settings, visible to all authenticated users
- [ ] Icon for nav link: use an existing emoji or SVG icon from the project (e.g., trophy 🏆)
- [ ] Responsive: horizontal scroll on small screens if table overflows

- [ ] Add i18n keys to all 5 translation files (`translations/{en,ru,fr,zh,fa}.json`):

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

### Nice to Have
- [ ] Smooth transition/animation when switching between periods
- [ ] Loading spinner while fetching data
- [ ] Persian (fa) RTL layout tested with `dir="auto"` on the table

## Technical Constraints
- Language: Python (Jinja2 templates), JavaScript (vanilla), CSS
- Template engine: Jinja2, extend `base.html`
- JS: vanilla only — no frameworks. Use `fetch()` for API calls
- CSS: match existing glassmorphism style in `static/css/style.css`
- i18n: use existing `t()` function in base.html template (or the project's translation mechanism)
- RTL: the project already supports RTL for Persian — follow the same pattern

## Acceptance Criteria
1. `/leaderboard` renders a styled page matching the project's glassmorphism design
2. Table shows: rank, username, download (formatted), upload (formatted), total (formatted)
3. Period toggle switches between all-time and monthly without page reload
4. Current user's row is visually distinct (highlighted)
5. Nav link appears in header for all authenticated users
6. All i18n keys present in 5 translation files, labels rendered in current language
7. RTL layout works for Persian (fa)
8. Empty state message shown when no traffic data
9. Responsive on mobile (table doesn't break layout)
10. `formatBytes()` correctly formats 0 → "0 B", 1073741824 → "1.00 GB", etc.

## Handoff Requirements
- [ ] All tests passing (`pytest -v`)
- [ ] Linters clean (`black --check .` and `flake8 .`)
- [ ] DEV_HANDOVER.md created with output
- [ ] WORKLOG.md appended

## Notes
- Depends on: TASK-02 (routes must exist for the page to work)
- Reference SPEC: `/home/igor/Amnezia-Web-Panel/leaderboard/SPEC.md` sections 5 and 6
- Look at `templates/settings.html` or `templates/users.html` for glassmorphism card style reference
- Look at `templates/base.html` for nav structure and how to add the link
- The project uses a `t(key)` function for translations — check how it's called in other templates