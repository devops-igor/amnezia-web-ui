"""Unit tests for BackgroundTaskOrchestrator (TASK: background-task-monolith).

Tests cover:
- Error isolation in run_all()
- Task lifecycle (start/stop)
- Loop timing (_run_loop)
- sync_traffic server processing
- sync_remnawave enable/disable
- CancelledError propagation
"""

import asyncio
from unittest.mock import AsyncMock
from unittest.mock import MagicMock
from unittest.mock import patch

import pytest

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------


@pytest.fixture
def orchestrator():
    """Create a fresh BackgroundTaskOrchestrator for each test."""
    from app.services.background_orchestrator import BackgroundTaskOrchestrator

    return BackgroundTaskOrchestrator()


# ---------------------------------------------------------------------------
# 1. run_all() calls all operations in order
# ---------------------------------------------------------------------------


class TestRunAll:
    @pytest.mark.asyncio
    async def test_run_all_calls_all_operations_in_order(self, orchestrator):
        """run_all() calls sync_traffic then sync_remnawave."""
        orchestrator.sync_traffic = AsyncMock()
        orchestrator.sync_remnawave = AsyncMock()

        await orchestrator.run_all()

        orchestrator.sync_traffic.assert_awaited_once()
        orchestrator.sync_remnawave.assert_awaited_once()

    @pytest.mark.asyncio
    async def test_run_all_continues_after_first_fails(self, orchestrator):
        """Error isolation: if sync_traffic fails, sync_remnawave still runs."""
        orchestrator.sync_traffic = AsyncMock(side_effect=RuntimeError("boom"))
        orchestrator.sync_remnawave = AsyncMock()

        await orchestrator.run_all()

        orchestrator.sync_traffic.assert_awaited_once()
        orchestrator.sync_remnawave.assert_awaited_once()

    @pytest.mark.asyncio
    async def test_run_all_continues_after_second_fails(self, orchestrator):
        """Error isolation: if sync_remnawave fails, no crash."""
        orchestrator.sync_traffic = AsyncMock()
        orchestrator.sync_remnawave = AsyncMock(side_effect=ValueError("nope"))

        # Should not raise
        await orchestrator.run_all()

        orchestrator.sync_traffic.assert_awaited_once()
        orchestrator.sync_remnawave.assert_awaited_once()


# ---------------------------------------------------------------------------
# 2. Task lifecycle
# ---------------------------------------------------------------------------


class TestTaskLifecycle:
    @pytest.mark.asyncio
    async def test_start_creates_asyncio_task(self, orchestrator):
        """start() sets self._task to an asyncio.Task."""
        assert orchestrator._task is None

        # Patch _run_loop so it returns immediately instead of sleeping forever
        orchestrator._run_loop = AsyncMock()

        await orchestrator.start()

        assert orchestrator._task is not None
        assert isinstance(orchestrator._task, asyncio.Task)

    @pytest.mark.asyncio
    async def test_stop_cancels_task(self, orchestrator):
        """stop() cancels the Task and awaits its completion."""
        orchestrator._run_loop = AsyncMock()
        await orchestrator.start()

        task = orchestrator._task
        assert task is not None

        await orchestrator.stop()

        # After stop, the task should be done (cancelled)
        assert task.done()
        assert task.cancelled() or orchestrator._task.done()

    @pytest.mark.asyncio
    async def test_stop_handles_already_done_task(self, orchestrator):
        """stop() is safe to call when _task is already done."""
        orchestrator._run_loop = AsyncMock()
        await orchestrator.start()
        await orchestrator.stop()

        # Second stop should not raise
        await orchestrator.stop()

    @pytest.mark.asyncio
    async def test_stop_handles_no_task(self, orchestrator):
        """stop() is safe to call before start()."""
        await orchestrator.stop()  # should not raise


# ---------------------------------------------------------------------------
# 3. _run_loop timing
# ---------------------------------------------------------------------------


class TestRunLoop:
    @pytest.mark.asyncio
    async def test_run_loop_sleeps_60_then_runs_and_sleeps_600(self, orchestrator):
        """_run_loop() sleeps 60s initially, calls run_all, then sleeps 600s."""
        orchestrator.run_all = AsyncMock()

        # Patch asyncio.sleep so we can count calls without actually waiting.
        # After the second sleep, raise CancelledError to exit the loop.
        sleep_calls = []

        async def fake_sleep(seconds):
            sleep_calls.append(seconds)
            if len(sleep_calls) >= 2:
                raise asyncio.CancelledError()

        with patch("asyncio.sleep", side_effect=fake_sleep):
            try:
                await orchestrator._run_loop()
            except asyncio.CancelledError:
                pass

        # First sleep is 60, second is 600
        assert sleep_calls == [60, 600]
        orchestrator.run_all.assert_awaited_once()


# ---------------------------------------------------------------------------
# 4. CancelledError propagation
# ---------------------------------------------------------------------------


class TestCancelledError:
    @pytest.mark.asyncio
    async def test_cancelled_error_reraised_from_run_loop(self, orchestrator):
        """CancelledError inside _run_loop is re-raised, not caught."""
        orchestrator.run_all = AsyncMock(side_effect=asyncio.CancelledError())

        # Patch sleep so we skip the 60s wait
        with patch("asyncio.sleep", new_callable=AsyncMock):
            with pytest.raises(asyncio.CancelledError):
                await orchestrator._run_loop()


# ---------------------------------------------------------------------------
# 5. sync_traffic processes servers
# ---------------------------------------------------------------------------


class TestSyncTraffic:
    @pytest.mark.asyncio
    async def test_sync_traffic_processes_servers(self, orchestrator):
        """sync_traffic() fetches servers and connections from DB."""
        mock_db = MagicMock()
        mock_server = {
            "id": "srv1",
            "host": "1.2.3.4",
            "protocols": {"awg": {"port": 51820}},
        }
        mock_conn = {
            "id": "conn1",
            "server_id": "srv1",
            "protocol": "awg",
            "client_id": "peer1",
            "user_id": "user1",
            "last_rx": 1000,
            "last_tx": 500,
        }

        mock_db.get_all_servers.return_value = [mock_server]
        mock_db.get_all_connections.return_value = [mock_conn]

        mock_ssh = MagicMock()
        mock_manager = MagicMock()
        mock_manager.get_clients.return_value = [
            {
                "clientId": "peer1",
                "userData": {"dataReceivedBytes": 1500, "dataSentBytes": 700},
            }
        ]

        with (
            patch("app.services.background_orchestrator.get_db", return_value=mock_db),
            patch("app.services.background_orchestrator.get_ssh", return_value=mock_ssh),
            patch(
                "app.services.background_orchestrator.get_protocol_manager",
                return_value=mock_manager,
            ),
            patch(
                "app.services.background_orchestrator.perform_mass_operations",
                new_callable=AsyncMock,
            ) as mock_mass_ops,
        ):
            await orchestrator.sync_traffic()

            # Verify SSH connection was established
            mock_ssh.connect.assert_called_once()
            # Verify manager.get_clients was called
            mock_manager.get_clients.assert_called()
            # Verify SSH was disconnected
            mock_ssh.disconnect.assert_called_once()

    @pytest.mark.asyncio
    async def test_sync_traffic_accumulates_per_connection_totals(self, orchestrator):
        """sync_traffic() accumulates traffic_total_rx/tx/total per connection."""
        mock_db = MagicMock()
        mock_server = {
            "id": "srv1",
            "host": "1.2.3.4",
            "protocols": {"awg": {"port": 51820}},
        }
        mock_conn = {
            "id": "conn1",
            "server_id": "srv1",
            "protocol": "awg",
            "client_id": "peer1",
            "user_id": "user1",
            "last_rx": 1000,
            "last_tx": 500,
            "traffic_total_rx": 2000,
            "traffic_total_tx": 1000,
            "traffic_total": 3000,
        }

        mock_db.get_all_servers.return_value = [mock_server]
        mock_db.get_all_connections.return_value = [mock_conn]
        mock_db.get_connection_by_id.return_value = mock_conn
        mock_db.get_all_users.return_value = []

        mock_ssh = MagicMock()
        mock_manager = MagicMock()
        mock_manager.get_clients.return_value = [
            {
                "clientId": "peer1",
                "userData": {"dataReceivedBytes": 1500, "dataSentBytes": 700},
            }
        ]

        with (
            patch("app.services.background_orchestrator.get_db", return_value=mock_db),
            patch("app.services.background_orchestrator.get_ssh", return_value=mock_ssh),
            patch(
                "app.services.background_orchestrator.get_protocol_manager",
                return_value=mock_manager,
            ),
            patch(
                "app.services.background_orchestrator.perform_mass_operations",
                new_callable=AsyncMock,
            ),
        ):
            await orchestrator.sync_traffic()

            mock_db.update_connection.assert_called_once()
            call_args = mock_db.update_connection.call_args
            assert call_args[0][0] == "conn1"
            assert call_args[0][1]["traffic_total_rx"] == 2500
            assert call_args[0][1]["traffic_total_tx"] == 1200
            assert call_args[0][1]["traffic_total"] == 3700

    @pytest.mark.asyncio
    async def test_sync_traffic_increments_existing_total(self, orchestrator):
        """Existing traffic_total is incremented by delta, not replaced."""
        mock_db = MagicMock()
        mock_server = {
            "id": "srv1",
            "host": "1.2.3.4",
            "protocols": {"awg": {"port": 51820}},
        }
        mock_conn = {
            "id": "conn1",
            "server_id": "srv1",
            "protocol": "awg",
            "client_id": "peer1",
            "user_id": "user1",
            "last_rx": 1000,
            "last_tx": 500,
            "traffic_total_rx": 5000,
            "traffic_total_tx": 3000,
            "traffic_total": 8000,
        }

        mock_db.get_all_servers.return_value = [mock_server]
        mock_db.get_all_connections.return_value = [mock_conn]
        mock_db.get_connection_by_id.return_value = mock_conn
        mock_db.get_all_users.return_value = []

        mock_ssh = MagicMock()
        mock_manager = MagicMock()
        # Delta = 200 rx + 100 tx = 300
        mock_manager.get_clients.return_value = [
            {
                "clientId": "peer1",
                "userData": {"dataReceivedBytes": 1200, "dataSentBytes": 600},
            }
        ]

        with (
            patch("app.services.background_orchestrator.get_db", return_value=mock_db),
            patch("app.services.background_orchestrator.get_ssh", return_value=mock_ssh),
            patch(
                "app.services.background_orchestrator.get_protocol_manager",
                return_value=mock_manager,
            ),
            patch(
                "app.services.background_orchestrator.perform_mass_operations",
                new_callable=AsyncMock,
            ),
        ):
            await orchestrator.sync_traffic()

            call_args = mock_db.update_connection.call_args
            assert call_args[0][1]["traffic_total"] == 8300
            assert call_args[0][1]["traffic_total_rx"] == 5200
            assert call_args[0][1]["traffic_total_tx"] == 3100

    @pytest.mark.asyncio
    async def test_sync_traffic_multiple_cycles_accumulate(self, orchestrator):
        """Multiple sync cycles accumulate per-connection traffic correctly."""
        mock_db = MagicMock()
        mock_server = {
            "id": "srv1",
            "host": "1.2.3.4",
            "protocols": {"awg": {"port": 51820}},
        }
        mock_conn = {
            "id": "conn1",
            "server_id": "srv1",
            "protocol": "awg",
            "client_id": "peer1",
            "user_id": "user1",
            "last_rx": 1000,
            "last_tx": 500,
            "traffic_total_rx": 0,
            "traffic_total_tx": 0,
            "traffic_total": 0,
        }

        mock_db.get_all_servers.return_value = [mock_server]
        mock_db.get_all_connections.return_value = [mock_conn]
        mock_db.get_connection_by_id.return_value = mock_conn
        mock_db.get_all_users.return_value = []

        mock_ssh = MagicMock()
        mock_manager = MagicMock()
        # First sync: bytes move from 1000/500 to 1500/700 (delta 500/200)
        mock_manager.get_clients.return_value = [
            {
                "clientId": "peer1",
                "userData": {"dataReceivedBytes": 1500, "dataSentBytes": 700},
            }
        ]

        with (
            patch("app.services.background_orchestrator.get_db", return_value=mock_db),
            patch("app.services.background_orchestrator.get_ssh", return_value=mock_ssh),
            patch(
                "app.services.background_orchestrator.get_protocol_manager",
                return_value=mock_manager,
            ),
            patch(
                "app.services.background_orchestrator.perform_mass_operations",
                new_callable=AsyncMock,
            ),
        ):
            await orchestrator.sync_traffic()
            first_call = mock_db.update_connection.call_args
            first_fields = first_call[0][1]
            assert first_fields["traffic_total"] == 700
            assert first_fields["traffic_total_rx"] == 500
            assert first_fields["traffic_total_tx"] == 200

            # Simulate persisted state: next sync starts from last_rx/last_tx
            mock_conn.update(
                {
                    "last_rx": first_fields["last_rx"],
                    "last_tx": first_fields["last_tx"],
                    "traffic_total_rx": first_fields["traffic_total_rx"],
                    "traffic_total_tx": first_fields["traffic_total_tx"],
                    "traffic_total": first_fields["traffic_total"],
                }
            )
            mock_manager.get_clients.return_value = [
                {
                    "clientId": "peer1",
                    "userData": {"dataReceivedBytes": 2000, "dataSentBytes": 900},
                }
            ]

            await orchestrator.sync_traffic()
            second_call = mock_db.update_connection.call_args
            second_fields = second_call[0][1]
            assert second_fields["traffic_total"] == 1400
            assert second_fields["traffic_total_rx"] == 1000
            assert second_fields["traffic_total_tx"] == 400

    @pytest.mark.asyncio
    async def test_sync_traffic_update_connection_includes_all_fields(self, orchestrator):
        """The single db.update_connection() call includes all 5 fields."""
        mock_db = MagicMock()
        mock_server = {
            "id": "srv1",
            "host": "1.2.3.4",
            "protocols": {"awg": {"port": 51820}},
        }
        mock_conn = {
            "id": "conn1",
            "server_id": "srv1",
            "protocol": "awg",
            "client_id": "peer1",
            "user_id": "user1",
            "last_rx": 1000,
            "last_tx": 500,
            "traffic_total_rx": 0,
            "traffic_total_tx": 0,
            "traffic_total": 0,
        }

        mock_db.get_all_servers.return_value = [mock_server]
        mock_db.get_all_connections.return_value = [mock_conn]
        mock_db.get_connection_by_id.return_value = mock_conn
        mock_db.get_all_users.return_value = []

        mock_ssh = MagicMock()
        mock_manager = MagicMock()
        mock_manager.get_clients.return_value = [
            {
                "clientId": "peer1",
                "userData": {"dataReceivedBytes": 1500, "dataSentBytes": 700},
            }
        ]

        with (
            patch("app.services.background_orchestrator.get_db", return_value=mock_db),
            patch("app.services.background_orchestrator.get_ssh", return_value=mock_ssh),
            patch(
                "app.services.background_orchestrator.get_protocol_manager",
                return_value=mock_manager,
            ),
            patch(
                "app.services.background_orchestrator.perform_mass_operations",
                new_callable=AsyncMock,
            ),
        ):
            await orchestrator.sync_traffic()

            call_args = mock_db.update_connection.call_args
            fields = call_args[0][1]
            assert set(fields.keys()) == {
                "last_rx",
                "last_tx",
                "traffic_total_rx",
                "traffic_total_tx",
                "traffic_total",
            }
            assert fields["last_rx"] == 1500
            assert fields["last_tx"] == 700


# ---------------------------------------------------------------------------
# 6. sync_remnawave enable/disable
# ---------------------------------------------------------------------------


class TestSyncRemnawave:
    @pytest.mark.asyncio
    async def test_sync_remnawave_runs_when_enabled(self, orchestrator):
        """sync_remnawave() calls sync_users_with_remnawave when enabled."""
        mock_db = MagicMock()
        mock_db.get_setting.return_value = {"remnawave_sync_users": True}

        with (
            patch("app.services.background_orchestrator.get_db", return_value=mock_db),
            patch(
                "app.services.background_orchestrator.sync_users_with_remnawave",
                new_callable=AsyncMock,
            ) as mock_sync,
        ):
            mock_sync.return_value = (5, "ok")
            await orchestrator.sync_remnawave()
            mock_sync.assert_awaited_once()

    @pytest.mark.asyncio
    async def test_sync_remnawave_skips_when_disabled(self, orchestrator):
        """sync_remnawave() skips when remnawave_sync_users is False."""
        mock_db = MagicMock()
        mock_db.get_setting.return_value = {"remnawave_sync_users": False}

        with (
            patch("app.services.background_orchestrator.get_db", return_value=mock_db),
            patch(
                "app.services.background_orchestrator.sync_users_with_remnawave",
                new_callable=AsyncMock,
            ) as mock_sync,
        ):
            await orchestrator.sync_remnawave()
            mock_sync.assert_not_awaited()


# ---------------------------------------------------------------------------
# 7. Monthly Rollover Unconditional — REGRESSION TEST
# ---------------------------------------------------------------------------


class TestMonthlyRolloverUnconditional:
    """Regression: monthly rollover MUST happen even with zero traffic updates.

    Bug: rollover was gated behind `if updates:` (line 124). When no traffic
    deltas existed in a sync cycle, the entire block — including monthly
    rollover — was skipped, so the leaderboard never reset.
    """

    @pytest.mark.asyncio
    async def test_monthly_rollover_without_traffic(self, orchestrator):
        """When sync_traffic() has zero updates, monthly rollover still runs."""
        from datetime import datetime
        from datetime import timedelta

        mock_db = MagicMock()
        mock_server = {
            "id": "srv1",
            "host": "1.2.3.4",
            "protocols": {"awg": {"port": 51820}},
        }
        mock_db.get_all_servers.return_value = [mock_server]
        mock_db.get_all_connections.return_value = []  # no connections → updates stays empty

        # User has monthly_reset_at from last month — needs rollover
        last_month = datetime.now() - timedelta(days=40)
        mock_user = {
            "id": "user1",
            "username": "quiet_user",
            "enabled": True,
            "monthly_rx": 5000,
            "monthly_tx": 3000,
            "monthly_reset_at": last_month.isoformat(),
        }
        mock_db.get_all_users.return_value = [mock_user]

        with (
            patch("app.services.background_orchestrator.get_db", return_value=mock_db),
            patch(
                "app.services.background_orchestrator.perform_mass_operations",
                new_callable=AsyncMock,
            ),
        ):
            await orchestrator.sync_traffic()

            # KEY ASSERTION: update_user MUST have been called (old code skipped)
            assert mock_db.update_user.called, (
                "update_user should have been called for monthly rollover "
                "even when updates list is empty"
            )

            # Find the call that reset monthly counters
            reset_found = False
            for call in mock_db.update_user.call_args_list:
                args, _ = call
                uid = args[0]
                fields = args[1]
                if (
                    uid == "user1"
                    and fields.get("monthly_rx") == 0
                    and fields.get("monthly_tx") == 0
                ):
                    reset_found = True
                    break

            assert reset_found, (
                "update_user should have been called for user1 with "
                "monthly_rx=0 and monthly_tx=0"
            )

    @pytest.mark.asyncio
    async def test_monthly_initialization_without_traffic(self, orchestrator):
        """Users with no monthly_reset_at get initialized even with zero traffic."""
        mock_db = MagicMock()
        mock_server = {
            "id": "srv1",
            "host": "1.2.3.4",
            "protocols": {"awg": {"port": 51820}},
        }
        mock_db.get_all_servers.return_value = [mock_server]
        mock_db.get_all_connections.return_value = []

        mock_user = {
            "id": "user2",
            "username": "new_user",
            "enabled": True,
            "monthly_reset_at": "",  # empty: needs initialization
        }
        mock_db.get_all_users.return_value = [mock_user]

        with (
            patch("app.services.background_orchestrator.get_db", return_value=mock_db),
            patch(
                "app.services.background_orchestrator.perform_mass_operations",
                new_callable=AsyncMock,
            ),
        ):
            await orchestrator.sync_traffic()

            init_found = False
            for call in mock_db.update_user.call_args_list:
                args, _ = call
                uid = args[0]
                fields = args[1]
                if (
                    uid == "user2"
                    and fields.get("monthly_rx") == 0
                    and fields.get("monthly_tx") == 0
                    and isinstance(fields.get("monthly_reset_at"), str)
                ):
                    init_found = True
                    break

            assert init_found, "update_user should have initialized monthly_reset_at for user2"


# ---------------------------------------------------------------------------
# 8. check_expiry
# ---------------------------------------------------------------------------


class TestCheckExpiry:
    @pytest.mark.asyncio
    async def test_check_expiry_adds_expired_user(self, orchestrator):
        """check_expiry() appends uid to to_disable_uids when expired."""
        from datetime import datetime
        from datetime import timedelta

        now = datetime.now()
        yesterday = now - timedelta(days=1)
        to_disable: list = []

        user = {
            "username": "expired_user",
            "enabled": True,
            "expiration_date": yesterday.isoformat(),
        }

        await orchestrator.check_expiry(now, user, "uid1", to_disable)
        assert "uid1" in to_disable

    @pytest.mark.asyncio
    async def test_check_expiry_skips_future_expiry(self, orchestrator):
        """check_expiry() does NOT append uid when not yet expired."""
        from datetime import datetime
        from datetime import timedelta

        now = datetime.now()
        tomorrow = now + timedelta(days=1)
        to_disable: list = []

        user = {"username": "active_user", "enabled": True, "expiration_date": tomorrow.isoformat()}

        await orchestrator.check_expiry(now, user, "uid2", to_disable)
        assert "uid2" not in to_disable

    @pytest.mark.asyncio
    async def test_check_expiry_skips_disabled_user(self, orchestrator):
        """check_expiry() skips users that are already disabled."""
        from datetime import datetime
        from datetime import timedelta

        now = datetime.now()
        yesterday = now - timedelta(days=1)
        to_disable: list = []

        user = {
            "username": "disabled_expired",
            "enabled": False,
            "expiration_date": yesterday.isoformat(),
        }

        await orchestrator.check_expiry(now, user, "uid3", to_disable)
        assert "uid3" not in to_disable
