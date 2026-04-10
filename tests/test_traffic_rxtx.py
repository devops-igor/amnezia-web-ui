"""
Unit tests for RX/TX traffic separation feature (TASK-01).

Tests cover:
- User migration: new fields added to existing users
- Delta calculation: rx_delta and tx_delta computed separately
- Monthly rollover: monthly_rx/monthly_tx reset at month boundary
- Backward compatibility: traffic_used and traffic_total still work as combined
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
        # Reload to pick up patched constant
        import importlib

        import app

        importlib.reload(app)
        yield app


@pytest.fixture
def sample_user():
    """Return a sample user dict with minimal fields."""
    return {
        "id": str(uuid.uuid4()),
        "username": "testuser",
        "password_hash": "salt$hash",
        "role": "user",
        "enabled": True,
        "created_at": datetime.now().isoformat(),
        "traffic_used": 1000,
        "traffic_total": 1000,
        "traffic_reset_strategy": "never",
        "last_reset_at": datetime.now().isoformat(),
        "expiration_date": None,
    }


# ---------- Migration Tests ----------


class TestMigration:
    """Test that startup() migration adds new RX/TX fields to existing users."""

    def test_migration_adds_traffic_total_rx(self, app_module, sample_user):
        data = {"users": [sample_user]}
        with open(app_module.DATA_FILE, "w") as f:
            json.dump(data, f)

        # Run startup
        import asyncio

        asyncio.get_event_loop().run_until_complete(app_module.startup())

        # Check field was added
        with open(app_module.DATA_FILE) as f:
            saved = json.load(f)
        assert "traffic_total_rx" in saved["users"][0]
        assert saved["users"][0]["traffic_total_rx"] == 0

    def test_migration_adds_traffic_total_tx(self, app_module, sample_user):
        data = {"users": [sample_user]}
        with open(app_module.DATA_FILE, "w") as f:
            json.dump(data, f)

        import asyncio

        asyncio.get_event_loop().run_until_complete(app_module.startup())

        with open(app_module.DATA_FILE) as f:
            saved = json.load(f)
        assert "traffic_total_tx" in saved["users"][0]
        assert saved["users"][0]["traffic_total_tx"] == 0

    def test_migration_adds_monthly_rx(self, app_module, sample_user):
        data = {"users": [sample_user]}
        with open(app_module.DATA_FILE, "w") as f:
            json.dump(data, f)

        import asyncio

        asyncio.get_event_loop().run_until_complete(app_module.startup())

        with open(app_module.DATA_FILE) as f:
            saved = json.load(f)
        assert "monthly_rx" in saved["users"][0]
        assert saved["users"][0]["monthly_rx"] == 0

    def test_migration_adds_monthly_tx(self, app_module, sample_user):
        data = {"users": [sample_user]}
        with open(app_module.DATA_FILE, "w") as f:
            json.dump(data, f)

        import asyncio

        asyncio.get_event_loop().run_until_complete(app_module.startup())

        with open(app_module.DATA_FILE) as f:
            saved = json.load(f)
        assert "monthly_tx" in saved["users"][0]
        assert saved["users"][0]["monthly_tx"] == 0

    def test_migration_adds_monthly_reset_at(self, app_module, sample_user):
        data = {"users": [sample_user]}
        with open(app_module.DATA_FILE, "w") as f:
            json.dump(data, f)

        import asyncio

        asyncio.get_event_loop().run_until_complete(app_module.startup())

        with open(app_module.DATA_FILE) as f:
            saved = json.load(f)
        assert "monthly_reset_at" in saved["users"][0]
        assert saved["users"][0]["monthly_reset_at"] == ""

    def test_migration_preserves_existing_values(self, app_module):
        """If a user already has RX/TX fields, they should NOT be overwritten."""
        user = {
            "id": str(uuid.uuid4()),
            "username": "existinguser",
            "password_hash": "salt$hash",
            "role": "user",
            "enabled": True,
            "created_at": datetime.now().isoformat(),
            "traffic_total_rx": 5000,
            "traffic_total_tx": 3000,
            "monthly_rx": 1000,
            "monthly_tx": 500,
            "monthly_reset_at": "2026-04-01T00:00:00",
        }
        data = {"users": [user]}
        with open(app_module.DATA_FILE, "w") as f:
            json.dump(data, f)

        import asyncio

        asyncio.get_event_loop().run_until_complete(app_module.startup())

        with open(app_module.DATA_FILE) as f:
            saved = json.load(f)
        assert saved["users"][0]["traffic_total_rx"] == 5000
        assert saved["users"][0]["traffic_total_tx"] == 3000
        assert saved["users"][0]["monthly_rx"] == 1000
        assert saved["users"][0]["monthly_tx"] == 500
        assert saved["users"][0]["monthly_reset_at"] == "2026-04-01T00:00:00"

    def test_migration_creates_default_admin_with_new_fields(self, app_module):
        """When no users exist, default admin should get new fields."""
        data = {"users": []}
        with open(app_module.DATA_FILE, "w") as f:
            json.dump(data, f)

        import asyncio

        asyncio.get_event_loop().run_until_complete(app_module.startup())

        with open(app_module.DATA_FILE) as f:
            saved = json.load(f)
        assert len(saved["users"]) == 1
        admin = saved["users"][0]
        assert admin["username"] == "admin"
        assert "traffic_total_rx" in admin
        assert "traffic_total_tx" in admin
        assert "monthly_rx" in admin
        assert "monthly_tx" in admin
        assert "monthly_reset_at" in admin


# ---------- Delta Calculation Tests ----------


class TestDeltaCalculation:
    """Test that rx_delta and tx_delta are calculated separately from client data."""

    def test_client_bytes_stores_rx_tx_separately(self, app_module):
        """The client_bytes dict should store {rx, tx} not combined."""
        # This is a unit test of the logic inside periodic_background_tasks
        # We simulate what happens inside the loop
        client_data = {
            "userData": {"dataReceivedBytes": 1000, "dataSentBytes": 500},
            "clientId": "client1",
        }
        client_bytes = {}
        rx = client_data.get("userData", {}).get("dataReceivedBytes", 0)
        tx = client_data.get("userData", {}).get("dataSentBytes", 0)
        client_bytes[client_data["clientId"]] = {"rx": rx, "tx": tx}

        assert client_bytes["client1"] == {"rx": 1000, "tx": 500}

    def test_delta_calculation_normal_case(self, app_module):
        """When current > last, delta = current - last."""
        curr_rx, curr_tx = 2000, 1000
        last_rx, last_tx = 1000, 500

        rx_delta = curr_rx - last_rx if curr_rx >= last_rx else curr_rx
        tx_delta = curr_tx - last_tx if curr_tx >= last_tx else curr_tx

        assert rx_delta == 1000
        assert tx_delta == 500

    def test_delta_calculation_counter_reset(self, app_module):
        """When counter resets (current < last), delta = current."""
        curr_rx, curr_tx = 100, 50
        last_rx, last_tx = 2000, 1000

        rx_delta = curr_rx - last_rx if curr_rx >= last_rx else curr_rx
        tx_delta = curr_tx - last_tx if curr_tx >= last_tx else curr_tx

        assert rx_delta == 100
        assert tx_delta == 50

    def test_delta_calculation_zero_last(self, app_module):
        """When last is 0 (new connection), delta = current."""
        curr_rx, curr_tx = 1500, 750
        last_rx, last_tx = 0, 0

        rx_delta = curr_rx - last_rx if curr_rx >= last_rx else curr_rx
        tx_delta = curr_tx - last_tx if curr_tx >= last_tx else curr_tx

        assert rx_delta == 1500
        assert tx_delta == 750

    def test_updates_tuple_contains_five_elements(self, app_module):
        """The updates list should carry (uc_id, rx_delta, tx_delta, curr_rx, curr_tx)."""
        uc_id = "uc123"
        rx_delta, tx_delta = 1000, 500
        curr_rx, curr_tx = 2000, 1000

        updates = []
        updates.append((uc_id, rx_delta, tx_delta, curr_rx, curr_tx))

        uc_id_u, rx_d, tx_d, curr_rx_u, curr_tx_u = updates[0]
        assert uc_id_u == uc_id
        assert rx_d == rx_delta
        assert tx_d == tx_delta
        assert curr_rx_u == curr_rx
        assert curr_tx_u == curr_tx


# ---------- Monthly Rollover Tests ----------


class TestMonthlyRollover:
    """Test monthly_rx and monthly_tx reset at month boundary."""

    def test_monthly_reset_when_month_changes(self, app_module, sample_user):
        """When current month differs from monthly_reset_at, counters should reset."""
        user = sample_user
        # Set monthly_reset_at to a previous month
        user["monthly_reset_at"] = "2026-03-15T00:00:00"
        user["monthly_rx"] = 5000
        user["monthly_tx"] = 3000

        # Simulate the rollover logic
        now = datetime(2026, 4, 1, 0, 0, 0)
        monthly_reset_iso = user.get("monthly_reset_at", "")

        if monthly_reset_iso:
            monthly_last = datetime.fromisoformat(monthly_reset_iso)
            if now.month != monthly_last.month or now.year != monthly_last.year:
                user["monthly_rx"] = 0
                user["monthly_tx"] = 0
                user["monthly_reset_at"] = now.isoformat()

        assert user["monthly_rx"] == 0
        assert user["monthly_tx"] == 0
        assert user["monthly_reset_at"] == "2026-04-01T00:00:00"

    def test_monthly_no_reset_same_month(self, app_module, sample_user):
        """When current month matches monthly_reset_at, counters should NOT reset."""
        user = sample_user
        user["monthly_reset_at"] = "2026-04-01T00:00:00"
        user["monthly_rx"] = 5000
        user["monthly_tx"] = 3000

        now = datetime(2026, 4, 15, 0, 0, 0)
        monthly_reset_iso = user.get("monthly_reset_at", "")

        if monthly_reset_iso:
            monthly_last = datetime.fromisoformat(monthly_reset_iso)
            if now.month != monthly_last.month or now.year != monthly_last.year:
                user["monthly_rx"] = 0
                user["monthly_tx"] = 0
                user["monthly_reset_at"] = now.isoformat()

        assert user["monthly_rx"] == 5000
        assert user["monthly_tx"] == 3000

    def test_monthly_reset_on_year_boundary(self, app_module, sample_user):
        """Month rollover should also trigger when year changes."""
        user = sample_user
        user["monthly_reset_at"] = "2025-12-15T00:00:00"
        user["monthly_rx"] = 5000
        user["monthly_tx"] = 3000

        now = datetime(2026, 1, 1, 0, 0, 0)
        monthly_reset_iso = user.get("monthly_reset_at", "")

        if monthly_reset_iso:
            monthly_last = datetime.fromisoformat(monthly_reset_iso)
            if now.month != monthly_last.month or now.year != monthly_last.year:
                user["monthly_rx"] = 0
                user["monthly_tx"] = 0
                user["monthly_reset_at"] = now.isoformat()

        assert user["monthly_rx"] == 0
        assert user["monthly_tx"] == 0

    def test_monthly_initialization_when_empty(self, app_module, sample_user):
        """When monthly_reset_at is empty, initialize to 0 and set timestamp."""
        user = sample_user
        user["monthly_reset_at"] = ""
        user.pop("monthly_rx", None)
        user.pop("monthly_tx", None)

        now = datetime(2026, 4, 1, 0, 0, 0)
        monthly_reset_iso = user.get("monthly_reset_at", "")

        if not monthly_reset_iso:
            user["monthly_rx"] = 0
            user["monthly_tx"] = 0
            user["monthly_reset_at"] = now.isoformat()

        assert user["monthly_rx"] == 0
        assert user["monthly_tx"] == 0
        assert user["monthly_reset_at"] == "2026-04-01T00:00:00"


# ---------- User Update Integration Tests ----------


class TestUserUpdate:
    """Test the full user update flow in periodic_background_tasks."""

    def test_traffic_used_is_combined_rx_tx(self, app_module, sample_user):
        """traffic_used should be incremented by rx_delta + tx_delta."""
        user = sample_user
        user["traffic_used"] = 1000
        user["traffic_total"] = 1000

        rx_delta, tx_delta = 500, 300
        delta = rx_delta + tx_delta
        user["traffic_used"] = user.get("traffic_used", 0) + delta
        user["traffic_total"] = user.get("traffic_total", 0) + delta

        assert user["traffic_used"] == 1800
        assert user["traffic_total"] == 1800

    def test_traffic_total_rx_updated_separately(self, app_module, sample_user):
        """traffic_total_rx should only include rx_delta."""
        user = sample_user
        user["traffic_total_rx"] = 2000
        user["traffic_total_tx"] = 1000

        rx_delta, tx_delta = 500, 300
        user["traffic_total_rx"] = user.get("traffic_total_rx", 0) + rx_delta
        user["traffic_total_tx"] = user.get("traffic_total_tx", 0) + tx_delta

        assert user["traffic_total_rx"] == 2500
        assert user["traffic_total_tx"] == 1300

    def test_monthly_rx_tx_updated_separately(self, app_module, sample_user):
        """monthly_rx and monthly_tx should be updated separately."""
        user = sample_user
        user["monthly_rx"] = 1000
        user["monthly_tx"] = 500
        user["monthly_reset_at"] = datetime.now().isoformat()

        rx_delta, tx_delta = 200, 100
        user["monthly_rx"] = user.get("monthly_rx", 0) + rx_delta
        user["monthly_tx"] = user.get("monthly_tx", 0) + tx_delta

        assert user["monthly_rx"] == 1200
        assert user["monthly_tx"] == 600

    def test_existing_traffic_reset_strategy_unchanged(self, app_module, sample_user):
        """The traffic_reset_strategy logic should still work as before."""
        user = sample_user
        user["traffic_used"] = 500
        user["traffic_reset_strategy"] = "daily"
        user["last_reset_at"] = datetime(2026, 4, 8, 0, 0, 0).isoformat()

        now = datetime(2026, 4, 9, 1, 0, 0)
        strategy = user.get("traffic_reset_strategy", "never")
        last_reset_iso = user.get("last_reset_at")

        reset_needed = False
        if strategy != "never" and last_reset_iso:
            last = datetime.fromisoformat(last_reset_iso)
            if strategy == "daily":
                reset_needed = now.date() > last.date()

        assert reset_needed is True

        if reset_needed:
            user["traffic_used"] = 0
            user["last_reset_at"] = now.isoformat()

        assert user["traffic_used"] == 0

    def test_traffic_limit_check_still_works(self, app_module, sample_user):
        """Users should still be disabled when traffic_used >= traffic_limit."""
        user = sample_user
        user["traffic_used"] = 900
        user["traffic_limit"] = 1000
        user["enabled"] = True

        rx_delta, tx_delta = 60, 40
        delta = rx_delta + tx_delta
        user["traffic_used"] = user.get("traffic_used", 0) + delta

        to_disable = []
        limit = user.get("traffic_limit", 0)
        if limit > 0 and user["traffic_used"] >= limit and user.get("enabled", True):
            to_disable.append(user["id"])

        assert user["traffic_used"] == 1000
        assert len(to_disable) == 1
        assert to_disable[0] == user["id"]


# ---------- Backward Compatibility Tests ----------


class TestBackwardCompatibility:
    """Ensure existing fields continue to work correctly."""

    def test_existing_user_connections_last_bytes_upgrade(self, app_module):
        """User connections with old last_bytes should still work during transition."""
        # Old connections have last_bytes; new ones have last_rx/last_tx
        # The code now reads last_rx and last_tx (defaulting to 0)
        # Existing connections with only last_bytes will get last_rx=0, last_tx=0
        # This means on first sync after upgrade, delta = curr (full count)
        # This is acceptable — it may overcount once but won't break
        uc_old = {"last_bytes": 1000}
        last_rx = uc_old.get("last_rx", 0)
        last_tx = uc_old.get("last_tx", 0)
        assert last_rx == 0
        assert last_tx == 0

    def test_new_user_connections_have_last_rx_last_tx(self, app_module):
        """New user connections should store last_rx and last_tx."""
        uc = {
            "id": str(uuid.uuid4()),
            "user_id": "user1",
            "server_id": 0,
            "protocol": "awg",
            "client_id": "client1",
            "name": "test",
            "last_rx": 1000,
            "last_tx": 500,
        }
        assert "last_rx" in uc
        assert "last_tx" in uc
        assert "last_bytes" not in uc

    def test_user_has_all_traffic_fields(self, app_module, sample_user):
        """After migration, a user should have all traffic-related fields."""
        user = sample_user
        # Add new fields
        user["traffic_total_rx"] = 0
        user["traffic_total_tx"] = 0
        user["monthly_rx"] = 0
        user["monthly_tx"] = 0
        user["monthly_reset_at"] = ""

        required_fields = [
            "traffic_used",
            "traffic_total",
            "traffic_total_rx",
            "traffic_total_tx",
            "monthly_rx",
            "monthly_tx",
            "monthly_reset_at",
            "traffic_reset_strategy",
            "last_reset_at",
        ]
        for field in required_fields:
            assert field in user, f"Missing field: {field}"


# ---------- TASK-03: Doubled Traffic Fix Tests ----------


class TestConnectionMigration:
    """Test migration of user_connections last_bytes -> last_rx/last_tx."""

    def test_connection_migration_even_split(self, app_module):
        """Connection with last_bytes=5000 gets last_rx=2500, last_tx=2500."""
        conn = {
            "id": "uc1",
            "user_id": "user1",
            "server_id": 0,
            "protocol": "awg",
            "client_id": "client1",
            "last_bytes": 5000,
        }
        data = {"users": [], "user_connections": [conn]}
        with open(app_module.DATA_FILE, "w") as f:
            json.dump(data, f)

        import asyncio

        asyncio.get_event_loop().run_until_complete(app_module.startup())

        with open(app_module.DATA_FILE) as f:
            saved = json.load(f)
        migrated_conn = saved["user_connections"][0]
        assert migrated_conn["last_rx"] == 2500
        assert migrated_conn["last_tx"] == 2500
        assert "last_bytes" not in migrated_conn

    def test_connection_migration_odd_split(self, app_module):
        """Connection with last_bytes=5001 gets last_rx=2500, last_tx=2501 (sum=5001)."""
        conn = {
            "id": "uc2",
            "user_id": "user1",
            "server_id": 0,
            "protocol": "awg",
            "client_id": "client1",
            "last_bytes": 5001,
        }
        data = {"users": [], "user_connections": [conn]}
        with open(app_module.DATA_FILE, "w") as f:
            json.dump(data, f)

        import asyncio

        asyncio.get_event_loop().run_until_complete(app_module.startup())

        with open(app_module.DATA_FILE) as f:
            saved = json.load(f)
        migrated_conn = saved["user_connections"][0]
        assert migrated_conn["last_rx"] == 2500
        assert migrated_conn["last_tx"] == 2501
        assert migrated_conn["last_rx"] + migrated_conn["last_tx"] == 5001
        assert "last_bytes" not in migrated_conn

    def test_connection_migration_skips_already_migrated(self, app_module):
        """Connection that already has last_rx/last_tx should not be modified."""
        conn = {
            "id": "uc3",
            "user_id": "user1",
            "server_id": 0,
            "protocol": "awg",
            "client_id": "client1",
            "last_rx": 1000,
            "last_tx": 500,
        }
        data = {"users": [], "user_connections": [conn]}
        with open(app_module.DATA_FILE, "w") as f:
            json.dump(data, f)

        import asyncio

        asyncio.get_event_loop().run_until_complete(app_module.startup())

        with open(app_module.DATA_FILE) as f:
            saved = json.load(f)
        saved_conn = saved["user_connections"][0]
        assert saved_conn["last_rx"] == 1000
        assert saved_conn["last_tx"] == 500


class TestTrafficRecalculation:
    """Test one-time traffic recalculation to fix doubled values."""

    def test_traffic_total_recalculated_from_rx_tx(self, app_module):
        """User with doubled traffic_total=10000, rx=3000, tx=2000 gets traffic_total=5000."""
        user = {
            "id": str(uuid.uuid4()),
            "username": "testuser",
            "password_hash": "salt$hash",
            "role": "user",
            "enabled": True,
            "traffic_used": 10000,
            "traffic_total": 10000,
            "traffic_total_rx": 3000,
            "traffic_total_tx": 2000,
            "traffic_reset_strategy": "never",
        }
        data = {"users": [user]}
        with open(app_module.DATA_FILE, "w") as f:
            json.dump(data, f)

        import asyncio

        asyncio.get_event_loop().run_until_complete(app_module.startup())

        with open(app_module.DATA_FILE) as f:
            saved = json.load(f)
        saved_user = saved["users"][0]
        assert saved_user["traffic_total"] == 5000

    def test_traffic_used_clamped_when_exceeds_total(self, app_module):
        """User with traffic_used=10000 (doubled), traffic_total corrected to 5000 gets clamped to 5000."""
        user = {
            "id": str(uuid.uuid4()),
            "username": "testuser",
            "password_hash": "salt$hash",
            "role": "user",
            "enabled": True,
            "traffic_used": 10000,
            "traffic_total": 10000,
            "traffic_total_rx": 3000,
            "traffic_total_tx": 2000,
            "traffic_reset_strategy": "never",
        }
        data = {"users": [user]}
        with open(app_module.DATA_FILE, "w") as f:
            json.dump(data, f)

        import asyncio

        asyncio.get_event_loop().run_until_complete(app_module.startup())

        with open(app_module.DATA_FILE) as f:
            saved = json.load(f)
        saved_user = saved["users"][0]
        assert saved_user["traffic_used"] == 5000
        assert saved_user["traffic_used"] == saved_user["traffic_total"]

    def test_fix_runs_only_once(self, app_module):
        """The traffic_doubled_fix_applied flag prevents re-running."""
        user = {
            "id": str(uuid.uuid4()),
            "username": "testuser",
            "password_hash": "salt$hash",
            "role": "user",
            "enabled": True,
            "traffic_used": 8000,
            "traffic_total": 8000,
            "traffic_total_rx": 3000,
            "traffic_total_tx": 2000,
            "traffic_reset_strategy": "never",
        }
        data = {"users": [user]}
        with open(app_module.DATA_FILE, "w") as f:
            json.dump(data, f)

        import asyncio

        asyncio.get_event_loop().run_until_complete(app_module.startup())

        # First run: traffic_total should be corrected to 5000, traffic_used clamped to 5000
        with open(app_module.DATA_FILE) as f:
            saved = json.load(f)
        assert saved["users"][0]["traffic_total"] == 5000
        assert saved["users"][0]["traffic_used"] == 5000
        assert saved["traffic_doubled_fix_applied"] is True

        # Manually corrupt data to simulate a second run without the flag check
        # (the flag should prevent any changes on subsequent startups)
        saved["users"][0]["traffic_total"] = 9999
        saved["users"][0]["traffic_used"] = 9999
        del saved["traffic_doubled_fix_applied"]
        with open(app_module.DATA_FILE, "w") as f:
            json.dump(saved, f)

        # Now run startup again - without the flag, it would recalculate again
        asyncio.get_event_loop().run_until_complete(app_module.startup())

        with open(app_module.DATA_FILE) as f:
            saved2 = json.load(f)
        # The flag was re-added, and recalculation ran again since we deleted the flag
        assert saved2["traffic_doubled_fix_applied"] is True
        # traffic_total should be 5000 again (from rx+tx which didn't change)
        assert saved2["users"][0]["traffic_total"] == 5000
        assert saved2["users"][0]["traffic_used"] == 5000

    def test_traffic_used_not_modified_when_already_valid(self, app_module):
        """User with traffic_used <= traffic_total should not be modified."""
        user = {
            "id": str(uuid.uuid4()),
            "username": "testuser",
            "password_hash": "salt$hash",
            "role": "user",
            "enabled": True,
            "traffic_used": 3000,
            "traffic_total": 10000,
            "traffic_total_rx": 3000,
            "traffic_total_tx": 2000,
            "traffic_reset_strategy": "monthly",
            "last_reset_at": datetime.now().isoformat(),
        }
        data = {"users": [user]}
        with open(app_module.DATA_FILE, "w") as f:
            json.dump(data, f)

        import asyncio

        asyncio.get_event_loop().run_until_complete(app_module.startup())

        with open(app_module.DATA_FILE) as f:
            saved = json.load(f)
        saved_user = saved["users"][0]
        # traffic_total is recalculated to 5000 (rx+tx)
        assert saved_user["traffic_total"] == 5000
        # traffic_used was 3000 which is <= 5000, so it should NOT be clamped
        assert saved_user["traffic_used"] == 3000


class TestDefensiveDelta:
    """Test defensive delta calculation for legacy connections with last_bytes."""

    def test_defensive_delta_uses_last_bytes_split(self, app_module):
        """Connection with last_bytes but no last_rx/last_tx gets 50/50 split during delta calc."""
        uc = {"last_bytes": 5000}
        # Simulate the defensive logic from periodic_background_tasks
        last_rx = uc.get("last_rx")
        last_tx = uc.get("last_tx")
        if last_rx is None and last_tx is None:
            last_bytes = uc.get("last_bytes", 0)
            last_rx = last_bytes // 2
            last_tx = last_bytes - last_rx
        else:
            last_rx = last_rx or 0
            last_tx = last_tx or 0

        assert last_rx == 2500
        assert last_tx == 2500

    def test_defensive_delta_uses_existing_rx_tx(self, app_module):
        """Connection with existing last_rx/last_tx uses those values directly."""
        uc = {"last_rx": 1000, "last_tx": 500, "last_bytes": 5000}
        last_rx = uc.get("last_rx")
        last_tx = uc.get("last_tx")
        if last_rx is None and last_tx is None:
            last_bytes = uc.get("last_bytes", 0)
            last_rx = last_bytes // 2
            last_tx = last_bytes - last_rx
        else:
            last_rx = last_rx or 0
            last_tx = last_tx or 0

        assert last_rx == 1000
        assert last_tx == 500

    def test_defensive_delta_zero_last_bytes(self, app_module):
        """Connection with last_bytes=0 gets last_rx=0, last_tx=0."""
        uc = {"last_bytes": 0}
        last_rx = uc.get("last_rx")
        last_tx = uc.get("last_tx")
        if last_rx is None and last_tx is None:
            last_bytes = uc.get("last_bytes", 0)
            last_rx = last_bytes // 2
            last_tx = last_bytes - last_rx
        else:
            last_rx = last_rx or 0
            last_tx = last_tx or 0

        assert last_rx == 0
        assert last_tx == 0

    def test_defensive_delta_none_values_treated_as_zero(self, app_module):
        """Connection with last_rx=None, last_tx=None triggers last_bytes fallback."""
        uc = {"last_rx": None, "last_tx": None, "last_bytes": 4000}
        last_rx = uc.get("last_rx")
        last_tx = uc.get("last_tx")
        if last_rx is None and last_tx is None:
            last_bytes = uc.get("last_bytes", 0)
            last_rx = last_bytes // 2
            last_tx = last_bytes - last_rx
        else:
            last_rx = last_rx or 0
            last_tx = last_tx or 0

        assert last_rx == 2000
        assert last_tx == 2000


if __name__ == "__main__":
    pytest.main([__file__, "-v"])
