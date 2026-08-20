"""Tests for Client Auto-Suspend (Expiration Dates) and Server Reachability Monitoring."""

from datetime import datetime, timedelta
from unittest.mock import AsyncMock, MagicMock, patch
import pytest

from app.services.background_orchestrator import BackgroundTaskOrchestrator


class TestClientAutoSuspend:
    """Test user expiration handling with expires_at and expiration_date."""

    @pytest.mark.asyncio
    async def test_check_expiry_expires_at_field(self):
        orchestrator = BackgroundTaskOrchestrator()
        now = datetime.now()
        yesterday = now - timedelta(days=1)
        to_disable = []

        user = {
            "username": "expired_via_expires_at",
            "enabled": True,
            "expires_at": yesterday.isoformat(),
        }

        await orchestrator.check_expiry(now, user, "u100", to_disable)
        assert "u100" in to_disable

    @pytest.mark.asyncio
    async def test_check_expiry_unexpired_user(self):
        orchestrator = BackgroundTaskOrchestrator()
        now = datetime.now()
        tomorrow = now + timedelta(days=1)
        to_disable = []

        user = {
            "username": "valid_user",
            "enabled": True,
            "expires_at": tomorrow.isoformat(),
        }

        await orchestrator.check_expiry(now, user, "u200", to_disable)
        assert "u200" not in to_disable

    @pytest.mark.asyncio
    async def test_server_reachability_tcp_fallback(self):
        orchestrator = BackgroundTaskOrchestrator()
        mock_db = MagicMock()
        mock_db.get_all_servers.return_value = [
            {"id": 1, "host": "1.2.3.4", "ssh_port": 22},
            {"id": 2, "host": "5.6.7.8", "ssh_port": 2222},
        ]
        mock_db.get_all_connections.return_value = []

        mock_writer = MagicMock()
        mock_writer.close = MagicMock()
        mock_writer.wait_closed = AsyncMock()

        async def fake_open_connection(host, port):
            if host == "1.2.3.4":
                return MagicMock(), mock_writer
            raise ConnectionRefusedError("Connection refused")

        with (
            patch("app.services.background_orchestrator.get_db", return_value=mock_db),
            patch("asyncio.open_connection", side_effect=fake_open_connection),
        ):
            results = await orchestrator.check_server_reachability()
            assert results[1]["reachable"] is True
            assert results[1]["latency_ms"] >= 0
            assert results[1]["error"] == ""
            assert results[2]["reachable"] is False
            assert "Connection refused" in results[2]["error"]

            cached = BackgroundTaskOrchestrator.get_cached_server_reachability()
            assert cached[1]["reachable"] is True

    @pytest.mark.asyncio
    async def test_server_reachability_awg_protocol(self):
        orchestrator = BackgroundTaskOrchestrator()
        mock_db = MagicMock()
        mock_server = {
            "id": 1,
            "host": "1.2.3.4",
            "protocols": {
                "awg": {
                    "installed": True,
                    "port": 55424,
                    "public_key": "srvpub123",
                    "psk": "psk123",
                    "awg_params": {"h1": "1000"},
                }
            },
        }
        mock_db.get_all_servers.return_value = [mock_server]
        mock_db.get_all_connections.return_value = []

        mock_reach = {
            "reachable": True,
            "latency_ms": 12,
            "protocol": "awg",
            "handshake_complete": True,
            "profile": "default",
            "last_checked": datetime.now().isoformat(),
            "error": "",
        }
        mock_trials = {
            "tls": {"reachable": True, "latency_ms": 10},
            "quic": {"reachable": True, "latency_ms": 12},
            "dns": {"reachable": True, "latency_ms": 11},
            "sip": {"reachable": True, "latency_ms": 15},
        }

        with (
            patch("app.services.background_orchestrator.get_db", return_value=mock_db),
            patch(
                "app.managers.awg_health.check_awg_reachability",
                new_callable=AsyncMock,
                return_value=mock_reach,
            ),
            patch(
                "app.managers.awg_health.run_auto_trial_profiles",
                new_callable=AsyncMock,
                return_value=mock_trials,
            ),
        ):
            results = await orchestrator.check_server_reachability()
            assert results[1]["reachable"] is True
            assert results[1]["latency_ms"] == 12
            assert results[1]["protocol"] == "awg"
            assert "auto_trials" in results[1]
            assert results[1]["auto_trials"]["tls"]["reachable"] is True
