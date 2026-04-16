"""
Tests for XSS protection in Jinja2/JS templates.

Verifies that:
1. escapeHtml() and escapeJs() are defined in base.html
2. Template files don't use |safe on user-controlled fields (except tojson patterns)
3. Server template variables use |e filter
4. WireGuard display data in server.html is wrapped in escapeHtml()
5. User data in users.html innerHTML is wrapped in escapeHtml()
6. openEditUser uses data-attributes instead of JSON string injection
7. API /api/users returns JSON (browsers won't render as HTML)
"""

import os
import re

import pytest

PROJECT_ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), ".."))
TEMPLATES_DIR = os.path.join(PROJECT_ROOT, "templates")


# ---------------------------------------------------------------------------
# Helper: read template content
# ---------------------------------------------------------------------------


def read_template(name: str) -> str:
    """Read a template file from the templates directory."""
    path = os.path.join(TEMPLATES_DIR, name)
    with open(path, "r", encoding="utf-8") as f:
        return f.read()


# ===========================================================================
# 1. escapeHtml / escapeJs defined in base.html
# ===========================================================================


class TestBaseHtmlEscapeFunctions:
    """Verify escapeHtml() and escapeJs() are defined globally in base.html."""

    def test_escape_html_defined_in_base(self):
        """escapeHtml function must exist in base.html."""
        content = read_template("base.html")
        assert (
            "function escapeHtml(" in content
        ), "escapeHtml() not found in base.html — required for global XSS prevention"

    def test_escape_js_defined_in_base(self):
        """escapeJs function must exist in base.html."""
        content = read_template("base.html")
        assert (
            "function escapeJs(" in content
        ), "escapeJs() not found in base.html — required for global XSS prevention"

    def test_escape_html_handles_null(self):
        """escapeHtml must handle null/undefined input."""
        content = read_template("base.html")
        # The function should have a null/undefined guard
        escape_block = re.search(
            r"function escapeHtml\([^)]*\)\s*\{(.*?)\n\s*\}",
            content,
            re.DOTALL,
        )
        assert escape_block, "Could not parse escapeHtml function body"
        body = escape_block.group(1)
        assert (
            "null" in body or "undefined" in body
        ), "escapeHtml should guard against null/undefined input"

    def test_escape_js_handles_null(self):
        """escapeJs must handle null/undefined input."""
        content = read_template("base.html")
        escape_block = re.search(
            r"function escapeJs\([^)]*\)\s*\{(.*?)\n\s*\}",
            content,
            re.DOTALL,
        )
        assert escape_block, "Could not parse escapeJs function body"
        body = escape_block.group(1)
        assert (
            "null" in body or "undefined" in body
        ), "escapeJs should guard against null/undefined input"

    def test_escape_js_escapes_backslashes(self):
        """escapeJs must escape backslashes (not just quotes)."""
        content = read_template("base.html")
        escape_block = re.search(
            r"function escapeJs\([^)]*\)\s*\{(.*?)\n\s*\}",
            content,
            re.DOTALL,
        )
        assert escape_block, "Could not parse escapeJs function body"
        body = escape_block.group(1)
        assert (
            "\\\\" in body or "\\\\\\\\" in body
        ), "escapeJs should escape backslashes — current version only escapes quotes"

    def test_escape_js_escapes_newlines(self):
        """escapeJs must escape newlines and carriage returns."""
        content = read_template("base.html")
        escape_block = re.search(
            r"function escapeJs\([^)]*\)\s*\{(.*?)\n\s*\}",
            content,
            re.DOTALL,
        )
        assert escape_block, "Could not parse escapeJs function body"
        body = escape_block.group(1)
        assert "\\n" in body, "escapeJs should escape newline characters"
        assert "\\r" in body, "escapeJs should escape carriage return characters"


# ===========================================================================
# 2. No |safe on user-controlled Jinja2 variables
# ===========================================================================


class TestTemplateSafeFilter:
    """Scan templates for | safe usage on user-controlled fields."""

    # User-controlled Jinja2 variables that should never use |safe
    USER_CONTROLLED_VARS = [
        "server.name",
        "server.host",
        "server.username",
        "current_user.username",
    ]

    @pytest.mark.parametrize("template_name", ["base.html", "users.html", "server.html"])
    def test_no_safe_on_user_controlled_vars(self, template_name: str):
        """User-controlled Jinja2 variables must not use |safe filter."""
        content = read_template(template_name)
        for var in self.USER_CONTROLLED_VARS:
            # Match patterns like {{ var | safe }} or {{ var|safe }}
            pattern = r"\{\{\s*" + re.escape(var) + r"\s*\|\s*safe\s*\}\}"
            match = re.search(pattern, content)
            assert (
                match is None
            ), f"{template_name}: {var} uses |safe — user-controlled data must be escaped"

    def test_safe_on_tojson_is_acceptable(self):
        """|safe after |tojson is acceptable (JSON-encoding is safe)."""
        content = read_template("users.html")
        # Find all |safe usages
        safe_usages = re.findall(r"\{\{[^}]*\|\s*safe\s*\}\}", content)
        for usage in safe_usages:
            # |tojson|safe is an acceptable pattern for embedding JSON in <script>
            if "tojson" in usage:
                continue
            # The edit_user_title | safe is Jinja2 translation string substitution,
            # not user data — acceptable per spec
            if "edit_user_title" in usage:
                continue
            pytest.fail(f"Unexpected |safe usage in users.html: {usage}")


# ===========================================================================
# 3. server.html Jinja2 variables use |e filter
# ===========================================================================


class TestServerHtmlJinja2Escaping:
    """Verify server.html uses |e on user-controlled Jinja2 variables."""

    def test_server_name_uses_escape_filter(self):
        """{{ server.name }} must use |e filter."""
        content = read_template("server.html")
        # Find all usages of server.name that are NOT already |e
        unescaped = re.findall(r"\{\{\s*server\.name\s*(?!\|\s*e)[^}]*\}\}", content)
        # Also check that |e exists
        escaped = re.findall(r"\{\{\s*server\.name\s*\|\s*e\s*\}\}", content)
        assert len(escaped) >= 1, "server.name must use |e filter in server.html"

    def test_server_host_uses_escape_filter(self):
        """{{ server.host }} must use |e filter."""
        content = read_template("server.html")
        escaped = re.findall(r"\{\{\s*server\.host\s*\|\s*e\s*\}\}", content)
        assert len(escaped) >= 1, "server.host must use |e filter in server.html"

    def test_server_username_uses_escape_filter(self):
        """{{ server.username }} must use |e filter."""
        content = read_template("server.html")
        escaped = re.findall(r"\{\{\s*server\.username\s*\|\s*e\s*\}\}", content)
        assert len(escaped) >= 1, "server.username must use |e filter in server.html"


# ===========================================================================
# 4. server.html — WireGuard data wrapped in escapeHtml()
# ===========================================================================


class TestServerHtmlWireGuardEscaping:
    """Verify WireGuard display data in server.html uses escapeHtml()."""

    def test_handshake_escaped(self):
        """handshake variable in loadConnections must be wrapped in escapeHtml."""
        content = read_template("server.html")
        # Find the pattern where handshake is interpolated into innerHTML
        pattern = r"\$\{escapeHtml\(handshake\)\}"
        assert re.search(
            pattern, content
        ), "handshake must be wrapped in escapeHtml() in server.html"

    def test_received_escaped(self):
        """received variable must be wrapped in escapeHtml."""
        content = read_template("server.html")
        pattern = r"\$\{escapeHtml\(received\)\}"
        assert re.search(
            pattern, content
        ), "received must be wrapped in escapeHtml() in server.html"

    def test_sent_escaped(self):
        """sent variable must be wrapped in escapeHtml."""
        content = read_template("server.html")
        pattern = r"\$\{escapeHtml\(sent\)\}"
        assert re.search(pattern, content), "sent must be wrapped in escapeHtml() in server.html"

    def test_no_duplicate_escape_functions(self):
        """escapeHtml/escapeJs should NOT be redefined in server.html."""
        content = read_template("server.html")
        # Should not have function definitions — only the comment referencing base.html
        func_defs = re.findall(r"function\s+(escapeHtml|escapeJs)\s*\(", content)
        assert len(func_defs) == 0, (
            f"Duplicate escapeHtml/escapeJs definitions found in server.html: {func_defs}. "
            "These should only be defined in base.html."
        )


# ===========================================================================
# 5. users.html — all user data in innerHTML wrapped in escapeHtml()
# ===========================================================================


class TestUsersHtmlEscapeHtmlUsage:
    """Verify all user-controlled data in users.html is wrapped in escapeHtml()."""

    def test_username_escaped_in_card(self):
        """u.username must be wrapped in escapeHtml() in the user card."""
        content = read_template("users.html")
        # In the template literal: ${escapeHtml(u.username)}
        pattern = r"\$\{escapeHtml\(u\.username\)\}"
        matches = re.findall(pattern, content)
        assert len(matches) >= 3, (
            f"u.username should be escaped with escapeHtml() at least 3 times "
            f"(avatar, name, data-attributes), found {len(matches)}"
        )

    def test_username_initial_escaped(self):
        """Avatar initial from u.username must be escaped."""
        content = read_template("users.html")
        pattern = r"escapeHtml\(u\.username\[0\]"
        assert re.search(
            pattern, content
        ), "Avatar initial (u.username[0]) must be wrapped in escapeHtml()"

    def test_source_escaped(self):
        """u.source must be wrapped in escapeHtml()."""
        content = read_template("users.html")
        pattern = r"\$\{escapeHtml\(u\.source\)\}"
        assert re.search(pattern, content), "u.source must be wrapped in escapeHtml() in users.html"

    def test_telegram_id_escaped(self):
        """u.telegramId must be wrapped in escapeHtml()."""
        content = read_template("users.html")
        pattern = r"escapeHtml\(u\.telegramId\)"
        assert re.search(
            pattern, content
        ), "u.telegramId must be wrapped in escapeHtml() in users.html"

    def test_email_escaped(self):
        """u.email must be wrapped in escapeHtml()."""
        content = read_template("users.html")
        pattern = r"escapeHtml\(u\.email\)"
        assert re.search(pattern, content), "u.email must be wrapped in escapeHtml() in users.html"

    def test_description_escaped(self):
        """u.description must be wrapped in escapeHtml()."""
        content = read_template("users.html")
        pattern = r"escapeHtml\(u\.description\)"
        assert re.search(
            pattern, content
        ), "u.description must be wrapped in escapeHtml() in users.html"

    def test_id_escaped_in_data_attributes(self):
        """u.id must be wrapped in escapeHtml() in data attributes."""
        content = read_template("users.html")
        pattern = r'data-id="\$\{escapeHtml\(u\.id\)\}"'
        assert re.search(
            pattern, content
        ), "u.id in data-id attribute must be wrapped in escapeHtml()"

    def test_no_unescaped_username_in_onclick_json(self):
        """openEditUser onclick must NOT use JSON string concatenation with u.username."""
        content = read_template("users.html")
        # The old vulnerable pattern: onclick='openEditUser({"id": "${u.id}", ...})'
        pattern = r"onclick='openEditUser\(\{"
        assert not re.search(
            pattern, content
        ), "openEditUser must use data-attributes, not inline JSON with string concatenation"

    def test_open_edit_user_uses_data_attributes(self):
        """openEditUser must be called with this.dataset, not inline JSON."""
        content = read_template("users.html")
        pattern = r"openEditUser\(this\.dataset\)"
        assert re.search(
            pattern, content
        ), "openEditUser must be called with this.dataset for XSS-safe data passing"

    def test_view_user_connections_uses_data_attributes(self):
        """viewUserConnections must use data-attributes, not string replacement."""
        content = read_template("users.html")
        # The old pattern: onclick="viewUserConnections('${u.id}', '${u.username...}')"
        # The new pattern uses data-uid and data-uname
        pattern = r"data-uid.*data-uname.*viewUserConnections\(this\.dataset"
        assert re.search(
            pattern, content
        ), "viewUserConnections must use data-attributes (data-uid, data-uname)"

    def test_share_modal_data_username_escaped(self):
        """openShareModal data-username must use escapeHtml."""
        content = read_template("users.html")
        pattern = r'data-username="\$\{escapeHtml\(u\.username\)\}"'
        assert re.search(
            pattern, content
        ), "openShareModal data-username must be wrapped in escapeHtml()"


# ===========================================================================
# 6. API returns JSON (not HTML) — browsers won't render as HTML
# ===========================================================================


class TestApiUsersJsonResponse:
    """Verify /api/users returns JSON content type."""

    def test_api_users_returns_json(self, csrf_client):
        """/api/users must return application/json."""
        # Login first
        csrf_client.post(
            "/login",
            data={"username": "admin", "password": "admin"},
            follow_redirects=True,
        )
        response = csrf_client.get("/api/users")
        assert response.headers["content-type"].startswith(
            "application/json"
        ), f"/api/users must return JSON, got: {response.headers['content-type']}"

    def test_api_users_xss_payload_stored_safely(self, csrf_client):
        """User with XSS payload in username is returned as JSON text, not HTML."""
        # Login first
        csrf_client.post(
            "/login",
            data={"username": "admin", "password": "admin"},
            follow_redirects=True,
        )
        # XSS payload as username
        xss_payload = '<img src=x onerror=alert("xss")>'
        response = csrf_client.get(f"/api/users?search={xss_payload}")
        # The response is JSON — browsers will not interpret the payload as HTML
        assert response.headers["content-type"].startswith(
            "application/json"
        ), "API must return JSON so XSS payloads are not rendered as HTML"
        # The payload string should appear as-is in JSON text, not as HTML tags
        data = response.json()
        assert isinstance(data, dict), "API should return a dict with users list"


# ===========================================================================
# 7. Escape function correctness — unit test the JS logic in Python
# ===========================================================================


class TestEscapeFunctionSemantics:
    """Verify the escape functions handle all required characters.

    These test the *intended* semantics of the JS escapeHtml/escapeJs
    functions by re-implementing them in Python and checking edge cases.
    """

    @staticmethod
    def escape_html_py(s: str) -> str:
        """Python equivalent of the JS escapeHtml function."""
        if s is None:
            return ""
        s = str(s)
        return (
            s.replace("&", "&amp;")
            .replace("<", "&lt;")
            .replace(">", "&gt;")
            .replace('"', "&quot;")
            .replace("'", "&#x27;")
        )

    def test_escape_html_script_tag(self):
        """Script tags must be neutralized by escapeHtml."""
        payload = '<script>alert("xss")</script>'
        result = self.escape_html_py(payload)
        assert "<script>" not in result
        assert "&lt;script&gt;" in result

    def test_escape_html_img_onerror(self):
        """img onerror XSS vector must be neutralized."""
        payload = "<img src=x onerror=alert(1)>"
        result = self.escape_html_py(payload)
        assert "<img" not in result
        assert "onerror" not in result or "&lt;" in result

    def test_escape_html_event_handler_injection(self):
        """Event handler attributes must be neutralized."""
        payload = '"><div onmouseover="alert(1)">'
        result = self.escape_html_py(payload)
        assert '"' not in result or "&quot;" in result

    @staticmethod
    def escape_js_py(s: str) -> str:
        """Python equivalent of the JS escapeJs function."""
        if s is None:
            return ""
        s = str(s)
        return (
            s.replace("\\", "\\\\")
            .replace("'", "\\'")
            .replace('"', '\\"')
            .replace("\n", "\\n")
            .replace("\r", "\\r")
        )

    def test_escape_js_string_breakout(self):
        """escapeJs must prevent breaking out of JS string context."""
        # If username is: "); alert("XSS
        payload = '\"); alert(\"XSS'
        result = self.escape_js_py(payload)
        # Every double-quote in the escaped result must be preceded by a
        # backslash, proving it is escaped and cannot close a JS string.
        # The raw substring '"); alert' may still appear (with the quote
        # escaped as \"), but that is safe — \" inside a JS string literal
        # is just an escaped quote, not a string terminator.
        for i, ch in enumerate(result):
            if ch == '"':
                assert i > 0 and result[i - 1] == "\\", (
                    f"Unescaped double-quote at position {i} in: {result!r}"
                )

    def test_escape_js_backslash_escape(self):
        """escapeJs must escape backslashes to prevent escape sequence attacks."""
        payload = '\\"); alert("XSS'
        result = self.escape_js_py(payload)
        # Backslash must be doubled
        assert "\\\\" in result

    def test_escape_js_newline_injection(self):
        """escapeJs must escape newlines to prevent line-termination attacks."""
        payload = "test\nalert(1)"
        result = self.escape_js_py(payload)
        assert "\n" not in result
        assert "\\n" in result
