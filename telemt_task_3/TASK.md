# TASK-3: Fix black formatting failure in telemt_manager.py (CI Lint check)

## Problem

CI pipeline PR #16 Lint check failed. `black --check --diff` reports 1 file needs reformatting:

**File:** `telemt_manager.py`  
**Line range:** around line 176, `save_server_config()` method

### Specific diff from CI:

```diff
-        self.ssh.upload_file_sudo(
-            config_content.replace("\r\n", "\n"), self._config_path()
-        )
+        self.ssh.upload_file_sudo(config_content.replace("\r\n", "\n"), self._config_path())
```

Black wants the `upload_file_sudo` call collapsed to a single line instead of spanning 3 lines.

## Requirements

- Run `black telemt_manager.py` to auto-format the file
- Verify with `black --check --diff telemt_manager.py` that it passes
- Verify `python3 -m py_compile telemt_manager.py` still passes
- Verify `python3 -m py_compile app.py` still passes
- Commit the fix to the existing branch `feat/telemt-configurable-path`

## Acceptance Criteria

- [ ] `black --check --diff telemt_manager.py` passes (exit code 0)
- [ ] `python3 -m py_compile telemt_manager.py` passes
- [ ] `python3 -m py_compile app.py` passes
- [ ] Change committed on branch `feat/telemt-configurable-path`

## Out of Scope

- Any logic changes
- Any changes to other files
- Re-running full CI pipeline (that happens after push)