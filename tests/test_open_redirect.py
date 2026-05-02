"""Tests for open redirect vulnerability prevention.

Verifies that the set_lang endpoint does not redirect to external URLs,
preventing phishing attacks via the Referer header.
"""

import os
import tempfile

from fastapi.testclient import TestClient

from database import Database

TEST_SECRET_KEY = "test-super-secret-key-for-testing-purposes!"


class TestSetLangRedirect:
    """Tests for set_lang redirect validation."""

    def setup_method(self):
        self.tmp_db = tempfile.NamedTemporaryFile(suffix=".db", delete=False)
        self.tmp_db_path = self.tmp_db.name
        self.tmp_db.close()
        os.environ["SECRET_KEY"] = TEST_SECRET_KEY
        self.db = Database(self.tmp_db_path, secret_key=TEST_SECRET_KEY)

    def teardown_method(self):
        conn = self.db._get_conn()
        conn.close()
        os.unlink(self.tmp_db_path)

    def test_set_lang_rejects_external_redirect(self):
        """Referer with external URL should redirect to path only, not external site."""
        import app

        client = TestClient(app.app, follow_redirects=False)
        response = client.get(
            "/set_lang/en",
            headers={"Referer": "https://evil.com/steal"},
        )
        assert response.status_code in (302, 303, 307)
        location = response.headers.get("location", "")
        assert location.startswith(
            "/"
        ), f"Open redirect to: {location}. Expected relative path only."
        assert "evil.com" not in location

    def test_set_lang_accepts_relative_path(self):
        """Referer with relative path should redirect to that path."""
        import app

        client = TestClient(app.app, follow_redirects=False)
        response = client.get(
            "/set_lang/en",
            headers={"Referer": "/dashboard"},
        )
        assert response.status_code in (302, 303, 307)
        assert response.headers["location"] == "/dashboard"

    def test_set_lang_default_no_referer(self):
        """Missing Referer should default to '/'."""
        import app

        client = TestClient(app.app, follow_redirects=False)
        response = client.get("/set_lang/en")
        assert response.status_code in (302, 303, 307)
        assert response.headers["location"] == "/"

    def test_set_lang_strips_external_host_keeps_path(self):
        """Same-origin URL with full path should strip to path+query."""
        import app

        client = TestClient(app.app, follow_redirects=False)
        response = client.get(
            "/set_lang/en",
            headers={"Referer": "https://panel.example.com/users?page=2"},
        )
        assert response.status_code in (302, 303, 307)
        location = response.headers["location"]
        assert location == "/users?page=2", f"Expected /users?page=2, got {location}"

    def test_set_lang_rejects_javascript_scheme(self):
        """javascript: scheme in Referer should not be followed."""
        import app

        client = TestClient(app.app, follow_redirects=False)
        response = client.get(
            "/set_lang/en",
            headers={"Referer": "javascript:alert(1)"},
        )
        assert response.status_code in (302, 303, 307)
        location = response.headers["location"]
        assert not location.startswith(
            "javascript"
        ), f"Redirect to {location} allows javascript: scheme"

    def test_set_lang_cookie_is_set(self):
        """Language cookie should still be set after redirect validation."""
        import app

        client = TestClient(app.app, follow_redirects=False)
        response = client.get(
            "/set_lang/ru",
            headers={"Referer": "/"},
        )
        # Check that the lang cookie is set
        cookies_header = response.headers.get("set-cookie", "")
        assert "lang=" in cookies_header, f"lang cookie not found in: {cookies_header}"
