"""Tests for Client Auto-Suspend (Expiration Dates) and Background Intelligent Automation."""

import pytest
from datetime import datetime, timedelta
from unittest.mock import MagicMock, patch
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
    async def test_dpi_block_auto_rotation(self):
        orchestrator = BackgroundTaskOrchestrator()
        mock_db = MagicMock()
        mock_server = {
            "id": "srv1",
            "host": "1.2.3.4",
            "protocols": {"awg": {"port": 55424}},
        }
        mock_conn = {
            "id": "c1",
            "server_id": "srv1",
            "protocol": "awg",
            "client_id": "peer1",
            "user_id": "u1",
            "awg_mimicry": "auto",
            "traffic_total": 5000,
        }
        mock_db.get_all_servers.return_value = [mock_server]
        mock_db.get_all_connections.return_value = [mock_conn]

        mock_ssh = MagicMock()
        mock_manager = MagicMock()
        # Client was active 20 mins ago and stalled
        stale_time = (datetime.now() - timedelta(minutes=20)).isoformat()
        mock_manager.get_clients.return_value = [
            {
                "clientId": "peer1",
                "userData": {
                    "awg_mimicry": "auto",
                    "latestHandshake": stale_time,
                    "dataReceivedBytes": 1000,
                    "dataSentBytes": 1000,
                },
            }
        ]
        mock_manager.rotate_client_mimicry.return_value = {
            "client_id": "peer1",
            "awg_mimicry": "tls",
            "rotated_at": datetime.now().isoformat(),
            "dpi_blocked": True,
        }

        with (
            patch("app.services.background_orchestrator.get_db", return_value=mock_db),
            patch("app.services.background_orchestrator.get_ssh", return_value=mock_ssh),
            patch(
                "app.services.background_orchestrator.get_protocol_manager",
                return_value=mock_manager,
            ),
        ):
            await orchestrator.check_dpi_blocks()

            mock_manager.rotate_client_mimicry.assert_called_once_with("awg", "peer1")
            mock_db.update_connection.assert_called_once_with("c1", {"awg_mimicry": "tls"})

    @pytest.mark.asyncio
    async def test_network_health_check(self):
        orchestrator = BackgroundTaskOrchestrator()
        health = await orchestrator.check_network_health()
        assert "tls" in health
        assert "quic" in health
        assert "dns" in health
        assert "sip" in health
        assert health["tls"]["status"] in ("operational", "degraded")
        assert "last_checked" in health
