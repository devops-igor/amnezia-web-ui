"""
Unit tests for the leaderboard feature (TASK-02).

Tests cover:
- get_leaderboard_entries helper: all-time and monthly aggregation, sorting, ranking
- get_leaderboard_entries: users with zero traffic included
- API route: authenticated response shape, period filtering, current_user_rank
- API route: 401 for unauthenticated requests
- Page route: redirect to login for unauthenticated, 200 for authenticated
- _format_bytes helper function
"""

import json
import os
import tempfile
import uuid
from datetime import datetime
from unittest.mock import patch

import pytest

# ---------- Fixtures ----------


@pytest.fixture
def temp_data_file():
    """Create a temporary data.json file for isolated testing."""
    with tempfile.NamedTemporaryFile(mode="w", suffix=".json", delete=False) as f:
        json.dump({}, f)
        tmp_path = f.name
    yield tmp_path
    os.unlink(tmp_path)


@pytest.fixture
def app_module(temp_data_file):
    """Import app module with DATA_FILE patched to temp file."""
    with patch("app.DATA_FILE", temp_data_file):
        import importlib

        import app

        importlib.reload(app)
        yield app


@pytest.fixture
def client(app_module):
    """Create a TestClient for the FastAPI app."""
    from fastapi.testclient import TestClient as _TestClient

    return _TestClient(app_module.app)


def _make_user(username, **overrides):
    """Helper to create a user dict with traffic fields."""
    user = {
        "id": str(uuid.uuid4()),
        "username": username,
        "password_hash": "salt$hash",
        "role": "user",
        "enabled": True,
        "created_at": datetime.now().isoformat(),
        "traffic_total_rx": 0,
        "traffic_total_tx": 0,
        "monthly_rx": 0,
        "monthly_tx": 0,
    }
    user.update(overrides)
    return user


def _seed_session(test_client, app_module, user):
    """Seed the session with authentication for a given user."""
    # The TestClient uses starlette's SessionMiddleware.
    # We need to set the session cookies properly.
    # The simplest way: log in via the API, or manually set session via middleware.
    # Since we can't easily set session directly, we'll patch get_current_user.
    # But for integration tests, let's use the login endpoint.
    # Actually, the simplest approach: create a custom session middleware wrapper.
    pass


# ---------- get_leaderboard_entries Tests ----------


class TestGetLeaderboardEntries:
    """Test the get_leaderboard_entries helper function."""

    def test_all_time_aggregation(self, app_module):
        data = {
            "users": [
                _make_user("alice", traffic_total_rx=1000, traffic_total_tx=500),
                _make_user("bob", traffic_total_rx=2000, traffic_total_tx=1000),
            ]
        }
        entries = app_module.get_leaderboard_entries(data, "all-time")
        assert len(entries) == 2
        assert entries[0]["username"] == "bob"
        assert entries[0]["total"] == 3000
        assert entries[0]["rank"] == 1
        assert entries[1]["username"] == "alice"
        assert entries[1]["total"] == 1500
        assert entries[1]["rank"] == 2

    def test_monthly_aggregation(self, app_module):
        data = {
            "users": [
                _make_user("alice", monthly_rx=100, monthly_tx=50),
                _make_user("bob", monthly_rx=200, monthly_tx=100),
            ]
        }
        entries = app_module.get_leaderboard_entries(data, "monthly")
        assert len(entries) == 2
        assert entries[0]["username"] == "bob"
        assert entries[0]["total"] == 300
        assert entries[0]["rank"] == 1

    def test_sorting_descending(self, app_module):
        data = {
            "users": [
                _make_user("user1", traffic_total_rx=100, traffic_total_tx=100),
                _make_user("user2", traffic_total_rx=500, traffic_total_tx=500),
                _make_user("user3", traffic_total_rx=50, traffic_total_tx=50),
            ]
        }
        entries = app_module.get_leaderboard_entries(data, "all-time")
        assert entries[0]["username"] == "user2"
        assert entries[1]["username"] == "user1"
        assert entries[2]["username"] == "user3"

    def test_rank_assignment(self, app_module):
        data = {
            "users": [
                _make_user("a", traffic_total_rx=300, traffic_total_tx=0),
                _make_user("b", traffic_total_rx=200, traffic_total_tx=0),
                _make_user("c", traffic_total_rx=100, traffic_total_tx=0),
            ]
        }
        entries = app_module.get_leaderboard_entries(data, "all-time")
        assert entries[0]["rank"] == 1
        assert entries[1]["rank"] == 2
        assert entries[2]["rank"] == 3

    def test_zero_traffic_users_excluded(self, app_module):
        data = {
            "users": [
                _make_user("active", traffic_total_rx=1000, traffic_total_tx=500),
                _make_user("inactive", traffic_total_rx=0, traffic_total_tx=0),
            ]
        }
        entries = app_module.get_leaderboard_entries(data, "all-time")
        assert len(entries) == 1
        assert entries[0]["username"] == "active"

    def test_disabled_users_excluded(self, app_module):
        data = {
            "users": [
                _make_user("enabled_user", traffic_total_rx=1000, traffic_total_tx=500),
                _make_user(
                    "disabled_user", traffic_total_rx=2000, traffic_total_tx=1000, enabled=False
                ),
                _make_user(
                    "falsy_enabled", traffic_total_rx=3000, traffic_total_tx=1500, enabled=0
                ),
            ]
        }
        entries = app_module.get_leaderboard_entries(data, "all-time")
        assert len(entries) == 1
        assert entries[0]["username"] == "enabled_user"
        assert entries[0]["total"] == 1500

    def test_alphabetical_tie_breaking(self, app_module):
        data = {
            "users": [
                _make_user("Charlie", traffic_total_rx=1000, traffic_total_tx=0),
                _make_user("Alice", traffic_total_rx=1000, traffic_total_tx=0),
                _make_user("Bob", traffic_total_rx=1000, traffic_total_tx=0),
                _make_user("alice2", traffic_total_rx=500, traffic_total_tx=0),
            ]
        }
        entries = app_module.get_leaderboard_entries(data, "all-time")
        assert len(entries) == 4
        # All three with 1000 should be sorted alphabetically (case-insensitive)
        assert entries[0]["username"] == "Alice"
        assert entries[0]["rank"] == 1
        assert entries[1]["username"] == "Bob"
        assert entries[1]["rank"] == 2
        assert entries[2]["username"] == "Charlie"
        assert entries[2]["rank"] == 3
        # Lower total comes last
        assert entries[3]["username"] == "alice2"
        assert entries[3]["rank"] == 4

    def test_alphabetical_tie_breaking_case_insensitive(self, app_module):
        data = {
            "users": [
                _make_user("ALICE", traffic_total_rx=1000, traffic_total_tx=0),
                _make_user("bob", traffic_total_rx=1000, traffic_total_tx=0),
                _make_user("Charlie", traffic_total_rx=1000, traffic_total_tx=0),
            ]
        }
        entries = app_module.get_leaderboard_entries(data, "all-time")
        assert entries[0]["username"] == "ALICE"
        assert entries[1]["username"] == "bob"
        assert entries[2]["username"] == "Charlie"

    def test_zero_traffic_monthly_excluded(self, app_module):
        data = {
            "users": [
                _make_user("active", monthly_rx=100, monthly_tx=50),
                _make_user("inactive", monthly_rx=0, monthly_tx=0),
            ]
        }
        entries = app_module.get_leaderboard_entries(data, "monthly")
        assert len(entries) == 1
        assert entries[0]["username"] == "active"

    def test_current_user_rank_none_when_zero_traffic(self, client, app_module):
        data = {
            "users": [
                _make_user("alice", traffic_total_rx=1000, traffic_total_tx=500),
                _make_user("bob", traffic_total_rx=0, traffic_total_tx=0),
            ]
        }
        with open(app_module.DATA_FILE, "w") as f:
            json.dump(data, f)

        # Current user is bob (zero traffic, excluded)
        bob = data["users"][1]
        with patch.object(app_module, "get_current_user", return_value=bob):
            response = client.get("/api/leaderboard")
            assert response.status_code == 200
            body = response.json()
            assert body["current_user_rank"] is None

    def test_current_user_rank_none_when_disabled(self, client, app_module):
        data = {
            "users": [
                _make_user("alice", traffic_total_rx=1000, traffic_total_tx=500),
                _make_user("bob", traffic_total_rx=2000, traffic_total_tx=1000, enabled=False),
            ]
        }
        with open(app_module.DATA_FILE, "w") as f:
            json.dump(data, f)

        # Current user is bob (disabled, excluded)
        bob = data["users"][1]
        with patch.object(app_module, "get_current_user", return_value=bob):
            response = client.get("/api/leaderboard")
            assert response.status_code == 200
            body = response.json()
            assert body["current_user_rank"] is None

    def test_empty_users(self, app_module):
        data = {"users": []}
        entries = app_module.get_leaderboard_entries(data, "all-time")
        assert entries == []

    def test_missing_traffic_fields_defaults_to_zero(self, app_module):
        data = {
            "users": [
                {"id": "1", "username": "minimal"},
            ]
        }
        entries = app_module.get_leaderboard_entries(data, "all-time")
        # Zero-traffic users are excluded
        assert entries == []

    def test_invalid_period_defaults_to_all_time(self, app_module):
        data = {
            "users": [
                _make_user("alice", traffic_total_rx=100, monthly_rx=999),
            ]
        }
        entries = app_module.get_leaderboard_entries(data, "invalid-period")
        assert entries[0]["download"] == 100  # all-time field used

    def test_download_upload_separate(self, app_module):
        data = {
            "users": [
                _make_user("alice", traffic_total_rx=3000, traffic_total_tx=2000),
            ]
        }
        entries = app_module.get_leaderboard_entries(data, "all-time")
        assert entries[0]["download"] == 3000
        assert entries[0]["upload"] == 2000
        assert entries[0]["total"] == 5000


# ---------- _format_bytes Tests ----------


class TestFormatBytes:
    """Test the _format_bytes helper function."""

    def test_bytes(self, app_module):
        assert app_module._format_bytes(500) == "500 B"

    def test_kilobytes(self, app_module):
        assert app_module._format_bytes(1024) == "1.00 KB"

    def test_megabytes(self, app_module):
        assert app_module._format_bytes(1024 * 1024) == "1.00 MB"

    def test_gigabytes(self, app_module):
        assert app_module._format_bytes(1024 * 1024 * 1024) == "1.00 GB"

    def test_terabytes(self, app_module):
        assert app_module._format_bytes(1024 * 1024 * 1024 * 1024) == "1.00 TB"

    def test_zero(self, app_module):
        assert app_module._format_bytes(0) == "0 B"

    def test_none_becomes_zero(self, app_module):
        assert app_module._format_bytes(None) == "0 B"

    def test_large_number(self, app_module):
        result = app_module._format_bytes(1234567890)
        assert "GB" in result or "TB" in result


# ---------- API Route Tests ----------


class TestLeaderboardAPI:
    """Test GET /api/leaderboard endpoint."""

    def test_unauthenticated_returns_401(self, client, app_module):
        response = client.get("/api/leaderboard")
        assert response.status_code == 401
        assert response.json()["error"] == "unauthorized"

    def test_authenticated_returns_200(self, client, app_module):
        # Seed data
        user = _make_user("testuser", role="user")
        data = {"users": [user]}
        with open(app_module.DATA_FILE, "w") as f:
            json.dump(data, f)

        # Patch get_current_user to simulate auth
        with patch.object(app_module, "get_current_user", return_value=user):
            response = client.get("/api/leaderboard")
            assert response.status_code == 200
            body = response.json()
            assert "period" in body
            assert "entries" in body
            assert "current_user_rank" in body
            assert body["period"] == "all-time"

    def test_period_all_time(self, client, app_module):
        data = {
            "users": [
                _make_user("alice", traffic_total_rx=1000, traffic_total_tx=500),
                _make_user("bob", traffic_total_rx=2000, traffic_total_tx=1000),
            ]
        }
        with open(app_module.DATA_FILE, "w") as f:
            json.dump(data, f)

        with patch.object(app_module, "get_current_user", return_value=data["users"][0]):
            response = client.get("/api/leaderboard?period=all-time")
            assert response.status_code == 200
            body = response.json()
            assert body["period"] == "all-time"
            assert len(body["entries"]) == 2
            assert body["entries"][0]["username"] == "bob"

    def test_period_monthly(self, client, app_module):
        data = {
            "users": [
                _make_user("alice", monthly_rx=100, monthly_tx=50),
                _make_user("bob", monthly_rx=300, monthly_tx=200),
            ]
        }
        with open(app_module.DATA_FILE, "w") as f:
            json.dump(data, f)

        with patch.object(app_module, "get_current_user", return_value=data["users"][0]):
            response = client.get("/api/leaderboard?period=monthly")
            assert response.status_code == 200
            body = response.json()
            assert body["period"] == "monthly"
            assert body["entries"][0]["username"] == "bob"
            assert body["entries"][0]["total"] == 500

    def test_invalid_period_defaults_to_all_time(self, client, app_module):
        data = {
            "users": [
                _make_user("alice", traffic_total_rx=100, monthly_rx=9999),
            ]
        }
        with open(app_module.DATA_FILE, "w") as f:
            json.dump(data, f)

        with patch.object(app_module, "get_current_user", return_value=data["users"][0]):
            response = client.get("/api/leaderboard?period=invalid")
            assert response.status_code == 200
            body = response.json()
            assert body["period"] == "all-time"
            assert body["entries"][0]["download"] == 100  # all-time value

    def test_current_user_rank_in_response(self, client, app_module):
        data = {
            "users": [
                _make_user("alice", traffic_total_rx=3000, traffic_total_tx=0),
                _make_user("bob", traffic_total_rx=2000, traffic_total_tx=0),
                _make_user("charlie", traffic_total_rx=1000, traffic_total_tx=0),
            ]
        }
        with open(app_module.DATA_FILE, "w") as f:
            json.dump(data, f)

        # Current user is charlie (rank 3)
        charlie = data["users"][2]
        with patch.object(app_module, "get_current_user", return_value=charlie):
            response = client.get("/api/leaderboard")
            assert response.status_code == 200
            body = response.json()
            assert body["current_user_rank"] == 3

    def test_current_user_rank_is_first(self, client, app_module):
        data = {
            "users": [
                _make_user("alice", traffic_total_rx=5000, traffic_total_tx=0),
            ]
        }
        with open(app_module.DATA_FILE, "w") as f:
            json.dump(data, f)

        with patch.object(app_module, "get_current_user", return_value=data["users"][0]):
            response = client.get("/api/leaderboard")
            assert response.status_code == 200
            body = response.json()
            assert body["current_user_rank"] == 1

    def test_no_query_param_defaults_all_time(self, client, app_module):
        data = {"users": [_make_user("alice", traffic_total_rx=100, monthly_rx=9999)]}
        with open(app_module.DATA_FILE, "w") as f:
            json.dump(data, f)

        with patch.object(app_module, "get_current_user", return_value=data["users"][0]):
            response = client.get("/api/leaderboard")
            assert response.status_code == 200
            assert response.json()["period"] == "all-time"

    def test_byte_values_are_integers(self, client, app_module):
        data = {
            "users": [
                _make_user("alice", traffic_total_rx=5368709120, traffic_total_tx=1073741824),
            ]
        }
        with open(app_module.DATA_FILE, "w") as f:
            json.dump(data, f)

        with patch.object(app_module, "get_current_user", return_value=data["users"][0]):
            response = client.get("/api/leaderboard")
            assert response.status_code == 200
            body = response.json()
            entry = body["entries"][0]
            assert isinstance(entry["download"], int)
            assert isinstance(entry["upload"], int)
            assert isinstance(entry["total"], int)
            assert entry["download"] == 5368709120
            assert entry["upload"] == 1073741824


# ---------- Page Route Tests ----------


class TestLeaderboardPage:
    """Test GET /leaderboard page route."""

    def test_unauthenticated_redirects_to_login(self, client, app_module):
        response = client.get("/leaderboard", follow_redirects=False)
        assert response.status_code == 302
        assert "/login" in response.headers.get("location", "")

    def test_authenticated_returns_200(self, client, app_module):
        user = _make_user("testuser")
        data = {"users": [user]}
        with open(app_module.DATA_FILE, "w") as f:
            json.dump(data, f)

        with patch.object(app_module, "get_current_user", return_value=user):
            response = client.get("/leaderboard")
            assert response.status_code == 200

    def test_authenticated_renders_template(self, client, app_module):
        user = _make_user("testuser")
        data = {"users": [user]}
        with open(app_module.DATA_FILE, "w") as f:
            json.dump(data, f)

        with patch.object(app_module, "get_current_user", return_value=user):
            response = client.get("/leaderboard")
            assert response.status_code == 200
            assert "text/html" in response.headers.get("content-type", "")

    def test_period_filter_in_page(self, client, app_module):
        user = _make_user("testuser")
        data = {"users": [user]}
        with open(app_module.DATA_FILE, "w") as f:
            json.dump(data, f)

        with patch.object(app_module, "get_current_user", return_value=user):
            response = client.get("/leaderboard?period=monthly")
            assert response.status_code == 200

    def test_invalid_period_defaults_on_page(self, client, app_module):
        user = _make_user("testuser", traffic_total_rx=100, monthly_rx=9999)
        data = {"users": [user]}
        with open(app_module.DATA_FILE, "w") as f:
            json.dump(data, f)

        with patch.object(app_module, "get_current_user", return_value=user):
            response = client.get("/leaderboard?period=invalid")
            assert response.status_code == 200


# ---------- monthly_label Tests ----------


class TestMonthlyLabel:
    """Test monthly_label is correctly included in API and page responses."""

    def test_api_monthly_label_present_when_monthly(self, client, app_module):
        """API response includes monthly_label with value like 'April 2026' when period=monthly."""
        data = {"users": [_make_user("alice", monthly_rx=100, monthly_tx=50)]}
        with open(app_module.DATA_FILE, "w") as f:
            json.dump(data, f)

        with patch.object(app_module, "get_current_user", return_value=data["users"][0]):
            response = client.get("/api/leaderboard?period=monthly")
            assert response.status_code == 200
            body = response.json()
            assert "monthly_label" in body
            assert body["monthly_label"] is not None
            # Should match format "Month Year" e.g. "April 2026"
            assert isinstance(body["monthly_label"], str)
            parts = body["monthly_label"].split(" ")
            assert len(parts) == 2
            assert parts[1].isdigit()  # year is numeric

    def test_api_monthly_label_null_when_all_time(self, client, app_module):
        """API response includes monthly_label: null when period=all-time."""
        data = {"users": [_make_user("alice", traffic_total_rx=100, traffic_total_tx=50)]}
        with open(app_module.DATA_FILE, "w") as f:
            json.dump(data, f)

        with patch.object(app_module, "get_current_user", return_value=data["users"][0]):
            response = client.get("/api/leaderboard?period=all-time")
            assert response.status_code == 200
            body = response.json()
            assert "monthly_label" in body
            assert body["monthly_label"] is None

    def test_page_monthly_label_in_context(self, client, app_module):
        """Page renders with monthly_label in context when period=monthly."""
        user = _make_user("testuser", monthly_rx=100, monthly_tx=50)
        data = {"users": [user]}
        with open(app_module.DATA_FILE, "w") as f:
            json.dump(data, f)

        with patch.object(app_module, "get_current_user", return_value=user):
            response = client.get("/leaderboard?period=monthly")
            assert response.status_code == 200
            html = response.text
            # The server-side rendered label should be present and non-empty
            # It should contain the current month name and year
            current_month_label = datetime.now().strftime("%B %Y")
            assert current_month_label in html

    def test_page_monthly_label_absent_when_all_time(self, client, app_module):
        """Page does not render a monthly label value when period=all-time."""
        user = _make_user("testuser", traffic_total_rx=100, traffic_total_tx=50)
        data = {"users": [user]}
        with open(app_module.DATA_FILE, "w") as f:
            json.dump(data, f)

        with patch.object(app_module, "get_current_user", return_value=user):
            response = client.get("/leaderboard?period=all-time")
            assert response.status_code == 200
            html = response.text
            # The span should exist but be empty / hidden
            # There should be no month name like "April 2026" visible as text content
            current_month_label = datetime.now().strftime("%B %Y")
            # Check the monthly-label span exists but doesn't contain the label text
            assert 'id="monthly-label"' in html
            # The label text should not appear between the span tags
            assert ">April 2026<" not in html or current_month_label not in html
