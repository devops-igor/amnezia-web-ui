"""app/main.py — thin entry-point that still executes the root-level app.py.

During the incremental split this is a pass-through so `python -m app` works.
Once the split is complete this module will host FastAPI app creation.
"""

import os

_root = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
_app_py = os.path.join(_root, "app.py")
with open(_app_py, "r", encoding="utf-8") as _f:
    _source = _f.read()

_exec_ns = globals()
_exec_ns["__file__"] = _app_py
_exec_ns.pop("__cached__", None)
exec(compile(_source, _app_py, "exec"), _exec_ns)
