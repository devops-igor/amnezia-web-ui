# DEV_HANDOVER: Make Telemt config directory configurable via admin panel

## Files Changed

### 1. telemt_manager.py
- Added `config_dir` parameter to `__init__` (default: `/opt/amnezia/telemt`)
- Added `_config_dir()` method returning the configurable directory path
- Added `_config_path()` method returning `{config_dir}/config.toml`
- Replaced 5 hardcoded `/opt/amnezia/telemt` occurrences:
  - Line 100: `remote_dir = "/opt/amnezia/telemt"` → `remote_dir = self._config_dir()`
  - Line 166: `"cat /opt/amnezia/telemt/config.toml"` → `f"cat {self._config_path()}"`
  - Line 173: `"/opt/amnezia/telemt/config.toml"` → `self._config_path()`
  - Line 199: `"rm -rf /opt/amnezia/telemt"` → `f"rm -rf {self._config_dir()}"`
  - Line 445: `"/opt/amnezia/telemt/config.toml"` → `self._config_path()`

### 2. app.py
- Added `ProtocolPaths` Pydantic model (line ~714) with `telemt_config_dir` field (default: `/opt/amnezia/telemt`)
- Added `protocol_paths: ProtocolPaths = ProtocolPaths()` to `SaveSettingsRequest` model
- Updated `get_protocol_manager()` to read `telemt_config_dir` from `data.json` when protocol is "telemt" and pass it to `TelemtManager`
- Updated `load_data()` to include default `protocol_paths` in settings
- Updated `/api/settings/save` endpoint to persist `protocol_paths` to data.json

### 3. templates/settings.html
- Added "Protocol Paths" section (card) between "Connection Limits" and "Backup"
- Single text input field for `telemt_config_dir` with label, placeholder, and hint
- Updated `saveSettings()` JavaScript function to include `protocol_paths` in the POST body

### 4. Translation files (5 files)
Added 3 keys to each: `protocol_paths`, `telemt_config_dir`, `telemt_config_dir_hint`
- translations/en.json (English)
- translations/ru.json (Russian)
- translations/fr.json (French)
- translations/fa.json (Persian)
- translations/zh.json (Chinese)

Note: The task spec mentioned de.json and es.json, but the actual project has fa.json and zh.json instead.

## Compilation Check

```
$ python3 -m py_compile telemt_manager.py && echo "telemt_manager.py: OK" && python3 -m py_compile app.py && echo "app.py: OK"
telemt_manager.py: OK
app.py: OK
```

## Backward Compatibility

- Default value `/opt/amnezia/telemt` is used when `protocol_paths` or `telemt_config_dir` is missing from data.json
- Existing installations with no `protocol_paths` in data.json continue working unchanged
- `get_protocol_manager()` reads config_dir from data.json at call time, so no changes needed at individual call sites

## Notes for QA

- The Protocol Paths section appears in Settings page after Connection Limits, before Backup
- The field has a default value of `/opt/amnezia/telemt` shown as placeholder
- Changing the path and saving settings will affect all subsequent TelemtManager operations (install, config read/write, remove)
- The path is stored in data.json under `settings.protocol_paths.telemt_config_dir`
- All 5 translation files have the required keys — no missing keys that would cause raw key fallback
