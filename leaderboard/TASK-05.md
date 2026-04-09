# Task Assignment: Leaderboard UI/UX Improvements

## Metadata
- **Task ID:** TASK-05
- **Project:** Amnezia-Web-Panel
- **Assigned to:** py_bot
- **Assigned by:** pm_bot
- **Date:** 2026-04-10
- **Priority:** MEDIUM
- **Status:** PENDING

## Objective
Improve the leaderboard page to match the project's design language. The current leaderboard uses a plain HTML table that doesn't match the card-based UI of the rest of the app (e.g., users.html).

## Background/Context
The leaderboard page was built functionally but uses a plain table layout. The rest of the project (users page especially) uses a modern card-based design with avatar circles, badges, metadata rows, and consistent button styling. The leaderboard should match this aesthetic.

## Design Reference
- Reference page: `templates/users.html` — card-based grid with `.client-item`, `.client-avatar`, `.client-name`, `.client-meta`
- Reference CSS: `static/css/style.css` — button classes (`.btn`, `.btn-primary`, `.btn-secondary`, `.btn-sm`), badge classes (`.badge`, `.badge-success`, `.badge-info`, `.badge-warn`, `.badge-secondary`, `.badge-danger`), card/item patterns
- CSS variables: `var(--primary)`, `var(--text-muted)`, `var(--bg-secondary)`, `var(--bg-card)`, `var(--border)`, etc.

## Requirements

### Must Have
- [ ] **Responsive overflow** — Add `overflow-x: auto` to `.table-container` for horizontal scroll on mobile (was a qa_bot LOW finding)
- [ ] **Period toggle styling** — Use proper `.btn .btn-sm .btn-primary/.btn-secondary` classes (currently uses inline button styles)
- [ ] **Table styling consistency** — Match the project's table header and cell styles from other pages
- [ ] **"Your rank" card redesign** — Match the project card styling (similar to how users page shows traffic progress bars)
- [ ] **Empty state styling** — Ensure empty state matches project patterns

### Nice to Have
- [ ] Convert to card grid layout (like users.html) — more impactful but significantly more work
- [ ] Add traffic breakdown visualization (download vs upload bars)
- [ ] Add percentage of total traffic indicator

## Technical Constraints
- Only modify: `templates/leaderboard.html` and `static/css/style.css`
- Do NOT change functionality — only visual/design improvements
- Use existing CSS classes from the project (do not invent new ones)
- Ensure mobile responsiveness
- All changes must pass `black --check` and `flake8`

## Acceptance Criteria
1. `.table-container` has `overflow-x: auto` (mobile-friendly horizontal scroll)
2. Period toggle buttons use proper `.btn .btn-sm .btn-primary/.btn-secondary` classes
3. "Your rank" card uses project card styling patterns
4. Table headers and cells match project table styles
5. Empty state matches project empty state patterns
6. All existing functionality preserved (JS fetch, period toggle, auth, i18n)
7. No new CSS classes invented — reuse existing project classes

## Handoff Requirements
- All tests passing (`pytest -v`)
- Linters clean (`black --check .` and `flake8 .`)
- DEV_HANDOVER.md created with output
- WORKLOG.md appended