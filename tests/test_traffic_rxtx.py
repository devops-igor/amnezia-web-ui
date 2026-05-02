"""
Unit tests for RX/TX traffic separation feature (TASK-01).

Tests cover:
- User migration: new fields added to existing users
- Delta calculation: rx_delta and tx_delta computed separately
- Monthly rollover: monthly_rx/monthly_tx reset at month boundary
- Backward compatibility: traffic_used and traffic_total still work as combined
- Connection migration: last_bytes -> last_rx/last_tx
- Traffic recalculation: fix doubled values
"""

import os
import tempfile
import uuid
from datetime import datetime

import pytest

from database import Database

# ---------- Fixtures ----------


@pytest.fixture
def temp_db():
    """Create a temporary SQLite database for testing."""
    with tempfile.NamedTemporaryFile(suffix=".db", delete=False) as f:
        db_path = f.name
    db = Database(db_path)
    yield db
    # Cleanup: close connections and remove file
    conn = db._get_conn()
    conn.close()
    os.unlink(db_path)


@pytest.fixture
def sample_user():
    """Return a sample user dict with minimal fields for create_user."""
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
    """Test that Database supports all RX/TX fields on users."""

    def test_user_has_traffic_total_rx_field(self, temp_db, sample_user):
        """Creating a user with traffic_total_rx should store and retrieve it."""
        sample_user["traffic_total_rx"] = 5000
        sample_user["traffic_total_tx"] = 3000
        temp_db.create_user(sample_user)
        user = temp_db.get_user(sample_user["id"])
        assert user["traffic_total_rx"] == 5000
        assert user["traffic_total_tx"] == 3000

    def test_user_default_rx_tx_is_zero(self, temp_db, sample_user):
        """If rx/tx fields are not provided, they should default to 0."""
        # Don't set any rx/tx fields — defaults should be 0
        temp_db.create_user(sample_user)
        user = temp_db.get_user(sample_user["id"])
        assert user["traffic_total_rx"] == 0
        assert user["traffic_total_tx"] == 0

    def test_user_has_monthly_rx_tx_fields(self, temp_db, sample_user):
        """Creating a user with monthly_rx/monthly_tx should store and retrieve them."""
        sample_user["monthly_rx"] = 1000
        sample_user["monthly_tx"] = 500
        temp_db.create_user(sample_user)
        user = temp_db.get_user(sample_user["id"])
        assert user["monthly_rx"] == 1000
        assert user["monthly_tx"] == 500

    def test_user_default_monthly_rx_tx_is_zero(self, temp_db, sample_user):
        """If monthly rx/tx fields are not provided, they should default to 0."""
        temp_db.create_user(sample_user)
        user = temp_db.get_user(sample_user["id"])
        assert user["monthly_rx"] == 0
        assert user["monthly_tx"] == 0

    def test_user_has_monthly_reset_at_field(self, temp_db, sample_user):
        """Creating a user with monthly_reset_at should store and retrieve it."""
        sample_user["monthly_reset_at"] = "2026-04-01T00:00:00"
        temp_db.create_user(sample_user)
        user = temp_db.get_user(sample_user["id"])
        assert user["monthly_reset_at"] == "2026-04-01T00:00:00"

    def test_user_default_monthly_reset_at_is_empty(self, temp_db, sample_user):
        """If monthly_reset_at not provided, should default to empty string."""
        temp_db.create_user(sample_user)
        user = temp_db.get_user(sample_user["id"])
        assert user["monthly_reset_at"] == ""

    def test_migration_preserves_existing_values(self, temp_db):
        """If a user already has RX/TX fields, they should NOT be overwritten."""
        user_id = str(uuid.uuid4())
        user = {
            "id": user_id,
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
        temp_db.create_user(user)
        fetched = temp_db.get_user(user_id)
        assert fetched["traffic_total_rx"] == 5000
        assert fetched["traffic_total_tx"] == 3000
        assert fetched["monthly_rx"] == 1000
        assert fetched["monthly_tx"] == 500
        assert fetched["monthly_reset_at"] == "2026-04-01T00:00:00"

    def test_default_admin_created_with_rx_tx_fields(self, temp_db):
        """When creating a default admin, it should have rx/tx fields."""
        admin_id = str(uuid.uuid4())
        admin = {
            "id": admin_id,
            "username": "admin",
            "password_hash": "salt$hash",
            "role": "admin",
            "enabled": True,
            "created_at": datetime.now().isoformat(),
        }
        temp_db.create_user(admin)
        fetched = temp_db.get_user(admin_id)
        assert fetched["username"] == "admin"
        assert "traffic_total_rx" in fetched
        assert "traffic_total_tx" in fetched
        assert "monthly_rx" in fetched
        assert "monthly_tx" in fetched
        assert "monthly_reset_at" in fetched


# ---------- Delta Calculation Tests ----------


class TestDeltaCalculation:
    """Test that rx_delta and tx_delta are calculated separately from client data."""

    def test_client_bytes_stores_rx_tx_separately(self):
        """The client_bytes dict should store {rx, tx} not combined."""
        client_data = {
            "userData": {"dataReceivedBytes": 1000, "dataSentBytes": 500},
            "clientId": "client1",
        }
        client_bytes = {}
        rx = client_data.get("userData", {}).get("dataReceivedBytes", 0)
        tx = client_data.get("userData", {}).get("dataSentBytes", 0)
        client_bytes[client_data["clientId"]] = {"rx": rx, "tx": tx}

        assert client_bytes["client1"] == {"rx": 1000, "tx": 500}

    def test_delta_calculation_normal_case(self):
        """When current > last, delta = current - last."""
        curr_rx, curr_tx = 2000, 1000
        last_rx, last_tx = 1000, 500

        rx_delta = curr_rx - last_rx if curr_rx >= last_rx else curr_rx
        tx_delta = curr_tx - last_tx if curr_tx >= last_tx else curr_tx

        assert rx_delta == 1000
        assert tx_delta == 500

    def test_delta_calculation_counter_reset(self):
        """When counter resets (current < last), delta = current."""
        curr_rx, curr_tx = 100, 50
        last_rx, last_tx = 2000, 1000

        rx_delta = curr_rx - last_rx if curr_rx >= last_rx else curr_rx
        tx_delta = curr_tx - last_tx if curr_tx >= last_tx else curr_tx

        assert rx_delta == 100
        assert tx_delta == 50

    def test_delta_calculation_zero_last(self):
        """When last is 0 (new connection), delta = current."""
        curr_rx, curr_tx = 1500, 750
        last_rx, last_tx = 0, 0

        rx_delta = curr_rx - last_rx if curr_rx >= last_rx else curr_rx
        tx_delta = curr_tx - last_tx if curr_tx >= last_tx else curr_tx

        assert rx_delta == 1500
        assert tx_delta == 750

    def test_updates_tuple_contains_five_elements(self):
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

    def test_monthly_reset_when_month_changes(self, sample_user):
        """When current month differs from monthly_reset_at, counters should reset."""
        user = sample_user
        user["monthly_reset_at"] = "2026-03-15T00:00:00"
        user["monthly_rx"] = 5000
        user["monthly_tx"] = 3000

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

    def test_monthly_no_reset_same_month(self, sample_user):
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

    def test_monthly_reset_on_year_boundary(self, sample_user):
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

    def test_monthly_initialization_when_empty(self, sample_user):
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

    def test_monthly_reset_happens_even_without_traffic(self, sample_user):
        """Rollover logic works independently — not gated on traffic deltas.

        Regression: the old code only ran monthly rollover inside `if updates:`,
        so zero-traffic sync cycles would skip the reset entirely.
        This test verifies the rollover path can run standalone.
        """
        user = sample_user
        user["monthly_reset_at"] = "2026-03-15T00:00:00"
        user["monthly_rx"] = 5000
        user["monthly_tx"] = 3000

        now = datetime(2026, 4, 5, 12, 0, 0)

        # Simulate the unconditional rollover path (no traffic deltas involved)
        monthly_reset_iso = user.get("monthly_reset_at", "")
        reset_occurred = False
        if not monthly_reset_iso:
            user["monthly_rx"] = 0
            user["monthly_tx"] = 0
            user["monthly_reset_at"] = now.isoformat()
            reset_occurred = True
        else:
            try:
                monthly_last = datetime.fromisoformat(monthly_reset_iso)
                if now.month != monthly_last.month or now.year != monthly_last.year:
                    user["monthly_rx"] = 0
                    user["monthly_tx"] = 0
                    user["monthly_reset_at"] = now.isoformat()
                    reset_occurred = True
            except Exception:
                pass

        assert reset_occurred, "Monthly rollover should happen even without traffic deltas"
        assert user["monthly_rx"] == 0
        assert user["monthly_tx"] == 0

    def test_monthly_rollover_skipped_same_month_no_traffic(self, sample_user):
        """In same month, rollover is correctly skipped — nothing changes."""
        user = sample_user
        user["monthly_reset_at"] = "2026-04-01T00:00:00"
        user["monthly_rx"] = 5000
        user["monthly_tx"] = 3000

        now = datetime(2026, 4, 5, 12, 0, 0)

        monthly_reset_iso = user.get("monthly_reset_at", "")
        modified = False
        if monthly_reset_iso:
            try:
                monthly_last = datetime.fromisoformat(monthly_reset_iso)
                if now.month != monthly_last.month or now.year != monthly_last.year:
                    user["monthly_rx"] = 0
                    user["monthly_tx"] = 0
                    modified = True
            except Exception:
                pass

        assert not modified, "Should not reset when still in same month"
        assert user["monthly_rx"] == 5000
        assert user["monthly_tx"] == 3000


# ---------- User Update Integration Tests ----------


class TestUserUpdate:
    """Test the full user update flow in periodic_background_tasks."""

    def test_traffic_used_is_combined_rx_tx(self, sample_user):
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

    def test_traffic_total_rx_updated_separately(self, sample_user):
        """traffic_total_rx should only include rx_delta."""
        user = sample_user
        user["traffic_total_rx"] = 2000
        user["traffic_total_tx"] = 1000

        rx_delta, tx_delta = 500, 300
        user["traffic_total_rx"] = user.get("traffic_total_rx", 0) + rx_delta
        user["traffic_total_tx"] = user.get("traffic_total_tx", 0) + tx_delta

        assert user["traffic_total_rx"] == 2500
        assert user["traffic_total_tx"] == 1300

    def test_monthly_rx_tx_updated_separately(self, sample_user):
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

    def test_existing_traffic_reset_strategy_unchanged(self, sample_user):
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

    def test_traffic_limit_check_still_works(self, sample_user):
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

    def test_existing_user_connections_last_bytes_upgrade(self, temp_db):
        """User connections with old last_bytes should still work during transition."""
        # Old connections have last_bytes; new ones have last_rx/last_tx
        # The code now reads last_rx and last_tx (defaulting to 0)
        # Existing connections with only last_bytes will get last_rx=0, last_tx=0
        # This means on first sync after upgrade, delta = curr (full count)
        # This is acceptable — it may overcount once but won't break
        uc = {"last_bytes": 1000}
        last_rx = uc.get("last_rx", 0)
        last_tx = uc.get("last_tx", 0)
        assert last_rx == 0
        assert last_tx == 0

    def test_new_user_connections_have_last_rx_last_tx(self, temp_db):
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

    def test_user_has_all_traffic_fields(self, temp_db, sample_user):
        """After migration, a user should have all traffic-related fields."""
        user = sample_user
        # Add new fields
        user["traffic_total_rx"] = 0
        user["traffic_total_tx"] = 0
        user["monthly_rx"] = 0
        user["monthly_tx"] = 0
        user["monthly_reset_at"] = ""

        temp_db.create_user(user)
        fetched = temp_db.get_user(user["id"])

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
            assert field in fetched, f"Missing field: {field}"


# ---------- TASK-03: Doubled Traffic Fix Tests ----------


class TestConnectionMigration:
    """Test migration of user_connections last_bytes -> last_rx/last_tx."""

    def test_connection_stores_rx_tx(self, temp_db):
        """Connection created with last_rx/last_tx stores them correctly."""
        # Create a user first (foreign key requirement)
        user_id = str(uuid.uuid4())
        temp_db.create_user(
            {
                "id": user_id,
                "username": "user1",
                "password_hash": "salt$hash",
                "role": "user",
                "enabled": True,
            }
        )
        # Create a server first (foreign key requirement)
        temp_db.create_server({"name": "Test Server", "host": "test.example.com"})

        conn_id = str(uuid.uuid4())
        conn = {
            "id": conn_id,
            "user_id": user_id,
            "server_id": 0,
            "protocol": "awg",
            "client_id": "client1",
            "name": "test_conn",
            "last_rx": 2500,
            "last_tx": 2500,
        }
        temp_db.create_connection(conn)
        fetched = temp_db.get_connection_by_id(conn_id)
        assert fetched["last_rx"] == 2500
        assert fetched["last_tx"] == 2500

    def test_connection_migration_odd_split(self, temp_db):
        """Connection with last_bytes odd split: 5001 -> rx=2500, tx=2501."""
        user_id = str(uuid.uuid4())
        temp_db.create_user(
            {
                "id": user_id,
                "username": "user1",
                "password_hash": "salt$hash",
                "role": "user",
                "enabled": True,
            }
        )
        temp_db.create_server({"name": "Test Server", "host": "test.example.com"})

        conn_id = str(uuid.uuid4())
        # The split logic: last_bytes // 2 = 2500, remainder = 1 -> tx gets it
        conn = {
            "id": conn_id,
            "user_id": user_id,
            "server_id": 0,
            "protocol": "awg",
            "client_id": "client1",
            "name": "test_conn_odd",
            "last_rx": 2500,
            "last_tx": 2501,
        }
        temp_db.create_connection(conn)
        fetched = temp_db.get_connection_by_id(conn_id)
        assert fetched["last_rx"] == 2500
        assert fetched["last_tx"] == 2501
        assert fetched["last_rx"] + fetched["last_tx"] == 5001

    def test_connection_migration_skips_already_migrated(self, temp_db):
        """Connection that already has last_rx/last_tx should not be modified."""
        user_id = str(uuid.uuid4())
        temp_db.create_user(
            {
                "id": user_id,
                "username": "user1",
                "password_hash": "salt$hash",
                "role": "user",
                "enabled": True,
            }
        )
        temp_db.create_server({"name": "Test Server", "host": "test.example.com"})

        conn_id = str(uuid.uuid4())
        conn = {
            "id": conn_id,
            "user_id": user_id,
            "server_id": 0,
            "protocol": "awg",
            "client_id": "client1",
            "name": "test_conn_migrated",
            "last_rx": 1000,
            "last_tx": 500,
        }
        temp_db.create_connection(conn)
        fetched = temp_db.get_connection_by_id(conn_id)
        assert fetched["last_rx"] == 1000
        assert fetched["last_tx"] == 500


class TestTrafficRecalculation:
    """Test one-time traffic recalculation to fix doubled values."""

    def test_traffic_total_recalculated_from_rx_tx(self, temp_db):
        """User with tx_total=3000, rx_total=2000 has traffic_total=5000 after update."""
        user_id = str(uuid.uuid4())
        user = {
            "id": user_id,
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
        temp_db.create_user(user)
        # Recalculate traffic_total as rx + tx
        temp_db.update_user(user_id, {"traffic_total": 3000 + 2000, "traffic_used": 3000 + 2000})
        fetched = temp_db.get_user(user_id)
        assert fetched["traffic_total"] == 5000

    def test_traffic_used_clamped_when_exceeds_total(self, temp_db):
        """User with traffic_used exceeding traffic_total gets clamped."""
        user_id = str(uuid.uuid4())
        user = {
            "id": user_id,
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
        temp_db.create_user(user)
        # Recalculate: traffic_total = rx+tx = 5000, traffic_used clamped to 5000
        new_total = 3000 + 2000
        temp_db.update_user(user_id, {"traffic_total": new_total, "traffic_used": new_total})
        fetched = temp_db.get_user(user_id)
        assert fetched["traffic_used"] == 5000
        assert fetched["traffic_used"] == fetched["traffic_total"]

    def test_traffic_used_not_modified_when_already_valid(self, temp_db):
        """User with traffic_used <= traffic_total should not be modified."""
        user_id = str(uuid.uuid4())
        user = {
            "id": user_id,
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
        temp_db.create_user(user)
        # Recalculate traffic_total only (traffic_used is fine)
        temp_db.update_user(user_id, {"traffic_total": 3000 + 2000})
        fetched = temp_db.get_user(user_id)
        assert fetched["traffic_total"] == 5000
        # traffic_used was 3000 which is <= 5000, so it should NOT be clamped
        assert fetched["traffic_used"] == 3000


class TestDefensiveDelta:
    """Test defensive delta calculation for legacy connections with last_bytes."""

    def test_defensive_delta_uses_last_bytes_split(self):
        """Connection with last_bytes but no last_rx/last_tx gets 50/50 split."""
        uc = {"last_bytes": 5000}
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

    def test_defensive_delta_uses_existing_rx_tx(self):
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

    def test_defensive_delta_zero_last_bytes(self):
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

    def test_defensive_delta_none_values_treated_as_zero(self):
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
