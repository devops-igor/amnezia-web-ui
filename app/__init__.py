import os

# Package shim: transparently load the root-level app.py into this package
# namespace so that `import app` and `patch.object(app, "get_db", ...)` keep
# working throughout the incremental split.

_root = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
_app_py = os.path.join(_root, "app.py")

with open(_app_py, "r", encoding="utf-8") as _f:
    _source = _f.read()

_exec_ns = globals()
_exec_ns["__file__"] = _app_py
_exec_ns["__name__"] = "app"
_exec_ns.pop("__cached__", None)

exec(compile(_source, _app_py, "exec"), _exec_ns)
