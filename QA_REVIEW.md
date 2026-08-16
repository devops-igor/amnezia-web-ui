# QA Review: TASK-303 Phase 5 — Tests & i18n (FINAL)

## Status: APPROVED

## Scope
- `templates/my_connections.html` — sort toggle labels now use Jinja-injected i18n strings.
- `translations/{en,ru,fr,fa,zh}.json` — added 4 keys: `sort_by_traffic`, `sort_by_date`, `traffic_per_connection`, `top_consumer`.
- `tests/test_background_orchestrator.py` — added 4 unit tests for per-connection traffic accumulation.

## Test Results
```
$ cd /home/igor/Amnezia-Web-Panel
$ source .venv/bin/activate
$ pytest -v -m "not e2e"
========== 1015 passed, 36 deselected, 1 warning in 69.26s (0:01:09) ===========
```

Targeted orchestrator tests:
```
$ pytest tests/test_background_orchestrator.py -v --tb=short -m "not e2e"
============================== 21 passed in 0.45s ==============================
```

## Linter Results
```
$ black --check .
All done! ✨ 🍰 ✨
100 files would be left unchanged.

$ flake8 .
(no issues)
```

## Security Audit
```
$ pip-audit
No known vulnerabilities found
```

## Security Findings
- **No new MEDIUM+ findings.** Phase 3's XSS sink in `renderConnectionItem()` is now mitigated: all dynamic values (`conn.name`, `conn.server_name`, `conn.protocol`, `conn.id`, `created_at`, protocol label) are wrapped in the global `escapeHtml()` from `templates/base.html:122` before insertion into the innerHTML template. Emoji/static markup is the only literal HTML remaining.
- Sort toggle uses `textContent` (not `innerHTML`) for the translated label, eliminating an injection vector.
- i18n strings are injected via Jinja `{{ _('...') }}` into JavaScript string literals; these are rendered server-side and benefit from Jinja's autoescaping. No `| safe` filter is used on the new labels.
- No hardcoded secrets, no command-injection/path-traversal surface introduced.

## i18n Verification
| Key | en | ru | fr | fa | zh |
|---|---|---|---|---|---|
| `sort_by_traffic` | Sort by traffic | Сортировать по трафику | Trier par trafic | مرتب‌سازی بر اساس ترافیک | 按流量排序 |
| `sort_by_date` | Sort by date | Сортировать по дате | Trier par date | مرتب‌سازی بر اساس تاریخ | 按日期排序 |
| `traffic_per_connection` | Traffic | Трафик | Trafic | ترافیک | 流量 |
| `top_consumer` | Top consumer | Топ потребитель | Top consommateur | بیشترین مصرف | 最高消费 |

- All 4 keys are present in all 5 translation files (project has exactly 5 JSON translation files, not 6).
- Values are properly translated for each locale (not English copies).
- All JSON files parse correctly.
- One key was inserted out of strict alphabetical order: `en.json` places the new keys between `apply_to_all_error` and `speed_limit_config_saved`, breaking the locale's alphabetical sequence (`sort_by_*` should come after `speed_*` / `subtitle_*`). The other four locales are alphabetically correct. This is a **style/non-blocking** issue; it does not affect functionality or translations lookup.

## Unit-Test Verification
| New Test | What it checks | Status |
|---|---|---|
| `test_sync_traffic_accumulates_per_connection_totals` | `traffic_total_rx/tx/total` accumulate (existing + delta) | ✅ |
| `test_sync_traffic_increments_existing_total` | Total is incremented, not replaced | ✅ |
| `test_sync_traffic_multiple_cycles_accumulate` | Two sync cycles add deltas on top of previous totals | ✅ |
| `test_sync_traffic_update_connection_includes_all_fields` | `update_connection()` receives exactly 5 fields: `last_rx`, `last_tx`, `traffic_total_rx`, `traffic_total_tx`, `traffic_total` | ✅ |

All 4 new tests are meaningful, mock SSH/manager/DB appropriately, and pass.

## Functional Verification
- `toggleSortOrder()` uses server-side translated labels via Jinja-injected JS variables (`sortTrafficLabel`, `sortDateLabel`) and sets `textContent` on the sort button. No hardcoded English remains.
- `formatBytes()` used in `my_connections.html` is the shared global helper from `templates/base.html:360`, which guards against null/NaN/negative values.

## Checklist
- [x] 1015 tests pass inside `.venv`
- [x] Black/flake8 clean
- [x] pip-audit clean (no known vulnerabilities)
- [x] All 4 i18n keys present in all 5 translation files
- [x] i18n values are real translations, not English copies
- [x] `toggleSortOrder()` uses Jinja-injected i18n labels
- [x] 4 new accumulation tests are meaningful and pass
- [x] `update_connection()` receives exactly the 5 expected fields
- [x] XSS escaping present on dynamic innerHTML content
- [x] No hardcoded secrets or injection vectors
- [x] Phase 5 implementation matches TASK/DEV_HANDOVER spec

## Notes
- Phase 5 is the FINAL phase of #303. All prior phases (1–4) have been approved; this approval clears the feature branch for PR creation and issue closure.
- Minor non-blocking style item: `translations/en.json` key ordering could be tidied to keep strict alphabetical order; this does not block approval.
