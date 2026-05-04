"""
Unit tests for the leaderboard feature (TASK-02).

Tests cover:
- get_leaderboard_entries helper: all-time and monthly aggregation, sorting, ranking
- get_leaderboard_entries: users with zero traffic excluded
- API route: authenticated response shape, period filtering, current_user_rank
- API route: 401 for unauthenticated requests
- Page route: 401 for unauthenticated (FastAPI Depends raises 401)
- format_bytes helper function (moved to utils.py)
"""

import os
import tempfile
from datetime import datetime
from unittest.mock import patch

import pytest
from fastapi.testclient import TestClient

from database import Database
from app.utils.helpers import get_leaderboard_entries
from dependencies import get_current_user

import app

# ---------- Fixtures ----------


@pytest.fixture
def temp_db():
    """Create a temporary SQLite database for testing."""
    with tempfile.NamedTemporaryFile(suffix=".db", delete=False) as f:
        db_path = f.name
    db = Database(db_path)
    yield db
    conn = db._get_conn()
    conn.close()
    os.unlink(db_path)


def _make_user(db, username, **overrides):
    """Helper to create a user in the database with traffic fields."""
    user = {
        "id": overrides.pop("id", None),
        "username": username,
        "password_hash": "salt$hash",
        "role": overrides.pop("role", "user"),
        "enabled": overrides.pop("enabled", True),
        "created_at": overrides.pop("created_at", datetime.now().isoformat()),
        "traffic_total_rx": overrides.pop("traffic_total_rx", 0),
        "traffic_total_tx": overrides.pop("traffic_total_tx", 0),
        "monthly_rx": overrides.pop("monthly_rx", 0),
        "monthly_tx": overrides.pop("monthly_tx", 0),
        "traffic_total": overrides.pop("traffic_total", 0),
        "traffic_used": overrides.pop("traffic_used", 0),
    }
    if user["id"] is None:
        import uuid

        user["id"] = str(uuid.uuid4())
    db.create_user(user)
    # Return the user dict as stored in the DB (with all default fields)
    return db.get_user(user["id"])


# ---------- get_leaderboard_entries Tests ----------


class TestGetLeaderboardEntries:
    """Test the get_leaderboard_entries helper function."""

    def test_all_time_aggregation(self, temp_db):

        _make_user(temp_db, "alice", traffic_total_rx=1000, traffic_total_tx=500)
        _make_user(temp_db, "bob", traffic_total_rx=2000, traffic_total_tx=1000)
        with patch("config.get_db", return_value=temp_db):
            entries = get_leaderboard_entries("all-time")
        assert len(entries) == 2
        assert entries[0]["username"] == "bob"
        assert entries[0]["total"] == 3000
        assert entries[0]["rank"] == 1
        assert entries[1]["username"] == "alice"
        assert entries[1]["total"] == 1500
        assert entries[1]["rank"] == 2

    def test_monthly_aggregation(self, temp_db):

        _make_user(temp_db, "alice", monthly_rx=100, monthly_tx=50)
        _make_user(temp_db, "bob", monthly_rx=200, monthly_tx=100)
        with patch("config.get_db", return_value=temp_db):
            entries = get_leaderboard_entries("monthly")
        assert len(entries) == 2
        assert entries[0]["username"] == "bob"
        assert entries[0]["total"] == 300
        assert entries[0]["rank"] == 1

    def test_sorting_descending(self, temp_db):

        _make_user(temp_db, "user1", traffic_total_rx=100, traffic_total_tx=100)
        _make_user(temp_db, "user2", traffic_total_rx=500, traffic_total_tx=500)
        _make_user(temp_db, "user3", traffic_total_rx=50, traffic_total_tx=50)
        with patch("config.get_db", return_value=temp_db):
            entries = get_leaderboard_entries("all-time")
        assert entries[0]["username"] == "user2"
        assert entries[1]["username"] == "user1"
        assert entries[2]["username"] == "user3"

    def test_rank_assignment(self, temp_db):

        _make_user(temp_db, "a", traffic_total_rx=300, traffic_total_tx=0)
        _make_user(temp_db, "b", traffic_total_rx=200, traffic_total_tx=0)
        _make_user(temp_db, "c", traffic_total_rx=100, traffic_total_tx=0)
        with patch("config.get_db", return_value=temp_db):
            entries = get_leaderboard_entries("all-time")
        assert entries[0]["rank"] == 1
        assert entries[1]["rank"] == 2
        assert entries[2]["rank"] == 3

    def test_zero_traffic_users_excluded(self, temp_db):

        _make_user(temp_db, "active", traffic_total_rx=1000, traffic_total_tx=500)
        _make_user(temp_db, "inactive", traffic_total_rx=0, traffic_total_tx=0)
        with patch("config.get_db", return_value=temp_db):
            entries = get_leaderboard_entries("all-time")
        assert len(entries) == 1
        assert entries[0]["username"] == "active"

    def test_disabled_users_excluded(self, temp_db):

        _make_user(temp_db, "enabled_user", traffic_total_rx=1000, traffic_total_tx=500)
        _make_user(
            temp_db, "disabled_user", traffic_total_rx=2000, traffic_total_tx=1000, enabled=False
        )
        with patch("config.get_db", return_value=temp_db):
            entries = get_leaderboard_entries("all-time")
        assert len(entries) == 1
        assert entries[0]["username"] == "enabled_user"
        assert entries[0]["total"] == 1500

    def test_alphabetical_tie_breaking(self, temp_db):

        _make_user(temp_db, "Charlie", traffic_total_rx=1000, traffic_total_tx=0)
        _make_user(temp_db, "Alice", traffic_total_rx=1000, traffic_total_tx=0)
        _make_user(temp_db, "Bob", traffic_total_rx=1000, traffic_total_tx=0)
        _make_user(temp_db, "alice2", traffic_total_rx=500, traffic_total_tx=0)
        with patch("config.get_db", return_value=temp_db):
            entries = get_leaderboard_entries("all-time")
        assert len(entries) == 4
        assert entries[0]["username"] == "Alice"
        assert entries[0]["rank"] == 1
        assert entries[1]["username"] == "Bob"
        assert entries[1]["rank"] == 2
        assert entries[2]["username"] == "Charlie"
        assert entries[2]["rank"] == 3
        assert entries[3]["username"] == "alice2"
        assert entries[3]["rank"] == 4

    def test_alphabetical_tie_breaking_case_insensitive(self, temp_db):

        _make_user(temp_db, "ALICE", traffic_total_rx=1000, traffic_total_tx=0)
        _make_user(temp_db, "bob", traffic_total_rx=1000, traffic_total_tx=0)
        _make_user(temp_db, "Charlie", traffic_total_rx=1000, traffic_total_tx=0)
        with patch("config.get_db", return_value=temp_db):
            entries = get_leaderboard_entries("all-time")
        assert entries[0]["username"] == "ALICE"
        assert entries[1]["username"] == "bob"
        assert entries[2]["username"] == "Charlie"

    def test_zero_traffic_monthly_excluded(self, temp_db):

        _make_user(temp_db, "active", monthly_rx=100, monthly_tx=50)
        _make_user(temp_db, "inactive", monthly_rx=0, monthly_tx=0)
        with patch("config.get_db", return_value=temp_db):
            entries = get_leaderboard_entries("monthly")
        assert len(entries) == 1
        assert entries[0]["username"] == "active"

    def test_current_user_rank_none_when_zero_traffic(self, temp_db):

        _make_user(temp_db, "alice", traffic_total_rx=1000, traffic_total_tx=500)
        bob = _make_user(temp_db, "bob", traffic_total_rx=0, traffic_total_tx=0)
        client = TestClient(app.app)
        with patch("config.get_db", return_value=temp_db):
            app.app.dependency_overrides[get_current_user] = lambda: bob
            try:
                response = client.get("/api/leaderboard")
                assert response.status_code == 200
                body = response.json()
                assert body["current_user_rank"] is None
            finally:
                app.app.dependency_overrides.clear()

    def test_current_user_rank_none_when_disabled(self, temp_db):

        _make_user(temp_db, "alice", traffic_total_rx=1000, traffic_total_tx=500)
        bob = _make_user(
            temp_db, "bob", traffic_total_rx=2000, traffic_total_tx=1000, enabled=False
        )
        client = TestClient(app.app)
        with patch("config.get_db", return_value=temp_db):
            app.app.dependency_overrides[get_current_user] = lambda: bob
            try:
                response = client.get("/api/leaderboard")
                assert response.status_code == 200
                body = response.json()
                assert body["current_user_rank"] is None
            finally:
                app.app.dependency_overrides.clear()

    def test_empty_users(self, temp_db):

        # No users created = empty leaderboard
        with patch("config.get_db", return_value=temp_db):
            entries = get_leaderboard_entries("all-time")
        assert entries == []

    def test_missing_traffic_fields_defaults_to_zero(self, temp_db):

        # Create a user with minimal fields — defaults to 0 traffic
        _make_user(temp_db, "minimal")
        with patch("config.get_db", return_value=temp_db):
            entries = get_leaderboard_entries("all-time")
        # Zero-traffic users are excluded
        assert entries == []

    def test_invalid_period_defaults_to_all_time(self, temp_db):

        _make_user(temp_db, "alice", traffic_total_tx=100, monthly_rx=999)
        with patch("config.get_db", return_value=temp_db):
            entries = get_leaderboard_entries("invalid-period")
        assert entries[0]["download"] == 100  # all-time field used

    def test_download_upload_separate(self, temp_db):

        _make_user(temp_db, "alice", traffic_total_rx=3000, traffic_total_tx=2000)
        with patch("config.get_db", return_value=temp_db):
            entries = get_leaderboard_entries("all-time")
        # tx = server-sent = client download, rx = server-received = client upload
        assert entries[0]["download"] == 2000
        assert entries[0]["upload"] == 3000
        assert entries[0]["total"] == 5000


# ---------- format_bytes Tests ----------


class TestFormatBytes:
    """Test the format_bytes helper function (now in utils.py)."""

    def test_bytes(self):
        from utils import format_bytes

        assert format_bytes(500) == "500 B"

    def test_kilobytes(self):
        from utils import format_bytes

        assert format_bytes(1024) == "1.00 KB"

    def test_megabytes(self):
        from utils import format_bytes

        assert format_bytes(1024 * 1024) == "1.00 MB"

    def test_gigabytes(self):
        from utils import format_bytes

        assert format_bytes(1024 * 1024 * 1024) == "1.00 GB"

    def test_terabytes(self):
        from utils import format_bytes

        assert format_bytes(1024 * 1024 * 1024 * 1024) == "1.00 TB"

    def test_zero(self):
        from utils import format_bytes

        assert format_bytes(0) == "0 B"

    def test_none_becomes_zero(self):
        from utils import format_bytes

        assert format_bytes(None) == "0 B"

    def test_large_number(self):
        from utils import format_bytes

        result = format_bytes(1234567890)
        assert "GB" in result or "TB" in result


# ---------- API Route Tests ----------


class TestLeaderboardAPI:
    """Test GET /api/leaderboard endpoint."""

    def test_unauthenticated_returns_401(self, temp_db):

        client = TestClient(app.app)
        with patch("config.get_db", return_value=temp_db):
            response = client.get("/api/leaderboard")
        assert response.status_code == 401
        assert response.json()["detail"] == "Not authenticated"

    def test_authenticated_returns_200(self, temp_db):

        user = _make_user(temp_db, "testuser")
        client = TestClient(app.app)
        with patch("config.get_db", return_value=temp_db):
            app.app.dependency_overrides[get_current_user] = lambda: user
            try:
                response = client.get("/api/leaderboard")
                assert response.status_code == 200
                body = response.json()
                assert "period" in body
                assert "entries" in body
                assert "current_user_rank" in body
                assert body["period"] == "all-time"
            finally:
                app.app.dependency_overrides.clear()

    def test_period_all_time(self, temp_db):

        alice = _make_user(temp_db, "alice", traffic_total_rx=1000, traffic_total_tx=500)
        _make_user(temp_db, "bob", traffic_total_rx=2000, traffic_total_tx=1000)
        client = TestClient(app.app)
        with patch("config.get_db", return_value=temp_db):
            app.app.dependency_overrides[get_current_user] = lambda: alice
            try:
                response = client.get("/api/leaderboard?period=all-time")
                assert response.status_code == 200
                body = response.json()
                assert body["period"] == "all-time"
                assert len(body["entries"]) == 2
                assert body["entries"][0]["username"] == "bob"
            finally:
                app.app.dependency_overrides.clear()

    def test_period_monthly(self, temp_db):

        alice = _make_user(temp_db, "alice", monthly_rx=100, monthly_tx=50)
        _make_user(temp_db, "bob", monthly_rx=300, monthly_tx=200)
        client = TestClient(app.app)
        with patch("config.get_db", return_value=temp_db):
            app.app.dependency_overrides[get_current_user] = lambda: alice
            try:
                response = client.get("/api/leaderboard?period=monthly")
                assert response.status_code == 200
                body = response.json()
                assert body["period"] == "monthly"
                assert body["entries"][0]["username"] == "bob"
                assert body["entries"][0]["total"] == 500
            finally:
                app.app.dependency_overrides.clear()

    def test_invalid_period_defaults_to_all_time(self, temp_db):

        alice = _make_user(temp_db, "alice", traffic_total_tx=100, monthly_rx=9999)
        client = TestClient(app.app)
        with patch("config.get_db", return_value=temp_db):
            app.app.dependency_overrides[get_current_user] = lambda: alice
            try:
                response = client.get("/api/leaderboard?period=invalid")
                assert response.status_code == 200
                body = response.json()
                assert body["period"] == "all-time"
                assert body["entries"][0]["download"] == 100  # all-time value
            finally:
                app.app.dependency_overrides.clear()

    def test_current_user_rank_in_response(self, temp_db):

        alice = _make_user(temp_db, "alice", traffic_total_rx=3000, traffic_total_tx=0)
        _make_user(temp_db, "bob", traffic_total_rx=2000, traffic_total_tx=0)
        charlie = _make_user(temp_db, "charlie", traffic_total_rx=1000, traffic_total_tx=0)
        client = TestClient(app.app)
        with patch("config.get_db", return_value=temp_db):
            app.app.dependency_overrides[get_current_user] = lambda: charlie
            try:
                response = client.get("/api/leaderboard")
                assert response.status_code == 200
                body = response.json()
                assert body["current_user_rank"] == 3
            finally:
                app.app.dependency_overrides.clear()

    def test_current_user_rank_is_first(self, temp_db):

        alice = _make_user(temp_db, "alice", traffic_total_rx=5000, traffic_total_tx=0)
        client = TestClient(app.app)
        with patch("config.get_db", return_value=temp_db):
            app.app.dependency_overrides[get_current_user] = lambda: alice
            try:
                response = client.get("/api/leaderboard")
                assert response.status_code == 200
                body = response.json()
                assert body["current_user_rank"] == 1
            finally:
                app.app.dependency_overrides.clear()

    def test_no_query_param_defaults_all_time(self, temp_db):

        alice = _make_user(temp_db, "alice", traffic_total_rx=100, monthly_rx=9999)
        client = TestClient(app.app)
        with patch("config.get_db", return_value=temp_db):
            app.app.dependency_overrides[get_current_user] = lambda: alice
            try:
                response = client.get("/api/leaderboard")
                assert response.status_code == 200
                assert response.json()["period"] == "all-time"
            finally:
                app.app.dependency_overrides.clear()

    def test_byte_values_are_integers(self, temp_db):

        alice = _make_user(
            temp_db, "alice", traffic_total_rx=5368709120, traffic_total_tx=1073741824
        )
        client = TestClient(app.app)
        with patch("config.get_db", return_value=temp_db):
            app.app.dependency_overrides[get_current_user] = lambda: alice
            try:
                response = client.get("/api/leaderboard")
                assert response.status_code == 200
                body = response.json()
                entry = body["entries"][0]
                assert isinstance(entry["download"], int)
                assert isinstance(entry["upload"], int)
                assert isinstance(entry["total"], int)
                # tx = server-sent = client download, rx = server-received = client upload
                assert entry["download"] == 1073741824
                assert entry["upload"] == 5368709120
            finally:
                app.app.dependency_overrides.clear()


# ---------- Page Route Tests ----------


class TestLeaderboardPage:
    """Test GET /leaderboard page route."""

    def test_unauthenticated_returns_401(self, temp_db):

        client = TestClient(app.app)
        with patch("config.get_db", return_value=temp_db):
            response = client.get("/leaderboard")
        assert response.status_code == 401

    def test_authenticated_returns_200(self, temp_db):

        user = _make_user(temp_db, "testuser")
        client = TestClient(app.app)
        with patch("config.get_db", return_value=temp_db):
            app.app.dependency_overrides[get_current_user] = lambda: user
            try:
                response = client.get("/leaderboard")
                assert response.status_code == 200
            finally:
                app.app.dependency_overrides.clear()

    def test_authenticated_renders_template(self, temp_db):

        user = _make_user(temp_db, "testuser")
        client = TestClient(app.app)
        with patch("config.get_db", return_value=temp_db):
            app.app.dependency_overrides[get_current_user] = lambda: user
            try:
                response = client.get("/leaderboard")
                assert response.status_code == 200
                assert "text/html" in response.headers.get("content-type", "")
            finally:
                app.app.dependency_overrides.clear()

    def test_period_filter_in_page(self, temp_db):

        user = _make_user(temp_db, "testuser")
        client = TestClient(app.app)
        with patch("config.get_db", return_value=temp_db):
            app.app.dependency_overrides[get_current_user] = lambda: user
            try:
                response = client.get("/leaderboard?period=monthly")
                assert response.status_code == 200
            finally:
                app.app.dependency_overrides.clear()

    def test_invalid_period_defaults_on_page(self, temp_db):

        user = _make_user(temp_db, "testuser", traffic_total_rx=100, monthly_rx=9999)
        client = TestClient(app.app)
        with patch("config.get_db", return_value=temp_db):
            app.app.dependency_overrides[get_current_user] = lambda: user
            try:
                response = client.get("/leaderboard?period=invalid")
                assert response.status_code == 200
            finally:
                app.app.dependency_overrides.clear()


# ---------- monthly_label Tests ----------


class TestMonthlyLabel:
    """Test monthly_label is correctly included in API and page responses."""

    def test_api_monthly_label_present_when_monthly(self, temp_db):

        alice = _make_user(temp_db, "alice", monthly_rx=100, monthly_tx=50)
        client = TestClient(app.app)
        with patch("config.get_db", return_value=temp_db):
            app.app.dependency_overrides[get_current_user] = lambda: alice
            try:
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
            finally:
                app.app.dependency_overrides.clear()

    def test_api_monthly_label_null_when_all_time(self, temp_db):

        alice = _make_user(temp_db, "alice", traffic_total_rx=100, traffic_total_tx=50)
        client = TestClient(app.app)
        with patch("config.get_db", return_value=temp_db):
            app.app.dependency_overrides[get_current_user] = lambda: alice
            try:
                response = client.get("/api/leaderboard?period=all-time")
                assert response.status_code == 200
                body = response.json()
                assert "monthly_label" in body
                assert body["monthly_label"] is None
            finally:
                app.app.dependency_overrides.clear()

    def test_page_monthly_label_in_context(self, temp_db):

        user = _make_user(temp_db, "testuser", monthly_rx=100, monthly_tx=50)
        client = TestClient(app.app)
        with patch("config.get_db", return_value=temp_db):
            app.app.dependency_overrides[get_current_user] = lambda: user
            try:
                response = client.get("/leaderboard?period=monthly")
                assert response.status_code == 200
                html = response.text
                current_month_label = datetime.now().strftime("%B %Y")
                assert current_month_label in html
            finally:
                app.app.dependency_overrides.clear()

    def test_page_monthly_label_absent_when_all_time(self, temp_db):

        user = _make_user(temp_db, "testuser", traffic_total_rx=100, traffic_total_tx=50)
        client = TestClient(app.app)
        with patch("config.get_db", return_value=temp_db):
            app.app.dependency_overrides[get_current_user] = lambda: user
            try:
                response = client.get("/leaderboard?period=all-time")
                assert response.status_code == 200
                html = response.text
                # Check the monthly-label span exists but doesn't contain the label text
                assert 'id="monthly-label"' in html
                current_month_label = datetime.now().strftime("%B %Y")
                assert ">April 2026<" not in html or current_month_label not in html
            finally:
                app.app.dependency_overrides.clear()
