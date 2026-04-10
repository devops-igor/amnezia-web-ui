# TASK-2: Make Telemt config directory configurable via admin panel

## Problem

The Telemt config directory `/opt/amnezia/telemt` is hardcoded in 5 places in
`telemt_manager.py`. Users cannot change this path without editing source code.
The admin panel Settings page has no UI for configuring protocol paths.

## Current State

### Hardcoded paths in `telemt_manager.py` (5 occurrences)

| Line | Code | Purpose |
|------|------|---------|
| 100 | `remote_dir = "/opt/amnezia/telemt"` | Install: destination directory |
| 166 | `"cat /opt/amnezia/telemt/config.toml"` | Read: `_get_server_config()` |
| 173 | `"/opt/amnezia/telemt/config.toml"` | Write: `save_server_config()` |
| 199 | `"rm -rf /opt/amnezia/telemt"` | Delete: `remove_container()` |
| 445 | `"/opt/amnezia/telemt/config.toml"` | Write: `toggle_client()` alt path |

### Admin settings system (app.py)

- Settings stored in `data.json` under `data["settings"]`
- Pydantic models in `app.py` lines 674-731 define settings structure
- `/api/settings` GET returns current settings
- `/api/settings/save` POST saves settings via `SaveSettingsRequest` model
- `templates/settings.html` renders the Settings page

### No existing section for protocol paths

The admin Settings page has: Appearance, Captcha, Telegram, SSL, Import/Sync,
Connection Limits, Backup. There is NO section for protocol paths.

## Requirements

### 1. TelemtManager refactoring

- Add `_config_dir()` method returning the configurable path (default: `/opt/amnezia/telemt`)
- Add `_config_path()` method returning `self._config_dir() + "/config.toml"`
- Replace all 5 hardcoded `/opt/amnezia/telemt` occurrences with these methods
- Replace the 2 hardcoded `/opt/amnezia/telemt/config.toml` occurrences with `_config_path()`
- The manager must receive the path from app.py at construction time, NOT read data.json itself

### 2. App.py integration

- Add `TelemtPath` or `ProtocolPath` to `SaveSettingsRequest` Pydantic model (single field: `telemt_config_dir`)
- Default value: `/opt/amnezia/telemt`
- Pass the configured path when constructing `TelemtManager` in `app.py`
- The path is stored in `data.json` under `data["settings"]["protocol_paths"]["telemt_config_dir"]`
- When loading data, if `protocol_paths` or `telemt_config_dir` is missing, default to `/opt/amnezia/telemt`

### 3. Admin panel UI

- Add a "Protocol Paths" section to `templates/settings.html`
- Place it after the "Connection Limits" section, before "Backup"
- Single field: "Telemt Config Directory" with default `/opt/amnezia/telemt`
- Field should be a text input, clearly labeled, with the default value shown
- Use `_()` translation function for labels where other sections do
- Include a small note/tooltip: "Directory on the remote server where Telemt stores its config"
- The section should follow the same visual pattern as existing settings sections

### 4. Translation strings

- Add translation keys for the new UI labels to ALL 5 translation files:
  - `translations/en.json`
  - `translations/ru.json`
  - `translations/de.json`
  - `translations/fr.json`
  - `translations/es.json`
- Key names: `protocol_paths`, `telemt_config_dir`, `telemt_config_dir_hint`
- IMPORTANT: Do NOT use `{{ _('key') or 'fallback' }}` pattern — `_t()` returns raw key when
  missing, which is truthy, so the `or` never triggers. Always add keys to all 5 files.

## Acceptance Criteria

- [ ] All 5 hardcoded `/opt/amnezia/telemt` paths in telemt_manager.py replaced with configurable methods
- [ ] `_config_dir()` and `_config_path()` methods added to TelemtManager
- [ ] TelemtManager.__init__ accepts an optional `config_dir` parameter (default: "/opt/amnezia/telemt")
- [ ] app.py passes the configured path when creating TelemtManager instances
- [ ] Path stored in data.json under `settings.protocol_paths.telemt_config_dir`
- [ ] Missing/empty path defaults to `/opt/amnezia/telemt` (backward compatible)
- [ ] Settings page has a "Protocol Paths" section with the Telemt config dir field
- [ ] Translation keys added to all 5 translation files
- [ ] `python3 -m py_compile app.py` passes
- [ ] `python3 -m py_compile telemt_manager.py` passes
- [ ] Existing installations with no `protocol_paths` in data.json continue working unchanged

## Out of Scope

- Other protocol managers (awg, xray, dns) — will be done in separate tasks
- Docker container name — stays hardcoded as "telemt"
- API URL — stays hardcoded as "http://127.0.0.1:9091"