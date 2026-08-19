"""Tests for Client Auto-Suspend (Expiration Dates) and Background Intelligent Automation."""

import pytest
from datetime import datetime, timedelta
from unittest.mock import AsyncMock, MagicMock, patch
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
    async def test_server_reachability(self):
        orchestrator = BackgroundTaskOrchestrator()
        mock_db = MagicMock()
        mock_db.get_all_servers.return_value = [
            {"id": 1, "host": "1.2.3.4", "ssh_port": 22},
            {"id": 2, "host": "5.6.7.8", "ssh_port": 2222},
        ]

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
    async def test_auto_trial_handshake_locking(self):
        orchestrator = BackgroundTaskOrchestrator()
        mock_db = MagicMock()
        mock_server = {
            "id": 1,
            "host": "1.2.3.4",
            "protocols": {"awg": {"port": 55424}},
        }
        mock_conn = {
            "id": "c1",
            "server_id": 1,
            "protocol": "awg",
            "client_id": "main_peer",
            "user_id": "u1",
            "awg_mimicry": "auto",
        }
        mock_db.get_all_servers.return_value = [mock_server]
        mock_db.get_connections_by_server_and_protocol.return_value = [mock_conn]
        mock_db.get_user.return_value = {"id": "u1", "username": "alice", "awg_mimicry": "auto"}

        mock_ssh = MagicMock()
        mock_ssh.run_sudo_command.return_value = ("trial_quic_pubkey\t1724108400\n", "", 0)
        mock_manager = MagicMock()

        # Two trial peers: TLS (no handshake) and QUIC (has handshake)
        mock_manager.get_clients.return_value = [
            {
                "clientId": "trial_tls_pubkey",
                "userData": {
                    "clientName": "Alice (TLS)",
                    "trial_profile": "tls",
                    "trial_for": "u1",
                    "latestHandshake": "",
                    "dataReceivedBytes": 0,
                },
            },
            {
                "clientId": "trial_quic_pubkey",
                "userData": {
                    "clientName": "Alice (QUIC)",
                    "trial_profile": "quic",
                    "trial_for": "u1",
                    "latestHandshake": "1 minute ago",
                    "dataReceivedBytes": 1500,
                },
            },
        ]

        with (
            patch("app.services.background_orchestrator.get_db", return_value=mock_db),
            patch("app.services.background_orchestrator.get_ssh", return_value=mock_ssh),
            patch(
                "app.services.background_orchestrator.get_protocol_manager",
                return_value=mock_manager,
            ),
        ):
            await orchestrator.check_auto_trial_handshakes()

            # Profile QUIC locked in
            mock_db.update_connection.assert_called_once_with("c1", {"awg_mimicry": "quic"})
            mock_db.update_user.assert_called_once_with("u1", {"awg_mimicry": "quic"})
            # Other trial peer deleted
            mock_manager.remove_client.assert_called_once_with("awg", "trial_tls_pubkey")

    @pytest.mark.asyncio
    async def test_auto_trial_expired_cleanup(self):
        orchestrator = BackgroundTaskOrchestrator()
        mock_db = MagicMock()
        mock_server = {
            "id": 1,
            "host": "1.2.3.4",
            "protocols": {"awg": {"port": 55424}},
        }
        mock_db.get_all_servers.return_value = [mock_server]

        mock_ssh = MagicMock()
        mock_ssh.run_sudo_command.return_value = ("", "", 0)
        mock_manager = MagicMock()

        expired_time = (datetime.now() - timedelta(hours=25)).isoformat()
        mock_manager.get_clients.return_value = [
            {
                "clientId": "old_trial_pubkey",
                "userData": {
                    "clientName": "Bob (TLS)",
                    "trial_profile": "tls",
                    "trial_for": "u2",
                    "trial_created_at": expired_time,
                    "latestHandshake": "",
                    "dataReceivedBytes": 0,
                },
            }
        ]

        with (
            patch("app.services.background_orchestrator.get_db", return_value=mock_db),
            patch("app.services.background_orchestrator.get_ssh", return_value=mock_ssh),
            patch(
                "app.services.background_orchestrator.get_protocol_manager",
                return_value=mock_manager,
            ),
        ):
            await orchestrator.check_auto_trial_handshakes()
            mock_manager.remove_client.assert_called_once_with("awg", "old_trial_pubkey")
