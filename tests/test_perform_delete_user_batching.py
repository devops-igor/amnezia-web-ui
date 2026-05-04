"""Unit tests for perform_delete_user SSH batching (Issue #132).

Tests cover:
- SSH connection count per unique server (not per connection)
- Multiple connections on one server → one SSH session
- Multiple servers → multiple SSH sessions
- Error handling per server (one fails, others continue)
- Return values: True/False
- db.delete_user called after SSH operations
"""

from unittest.mock import MagicMock
from unittest.mock import patch

import pytest

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------


def _make_mock_db(user=None, connections_by_user=None, server_by_id=None):
    """Build a mock DB instance with configurable returns."""
    db = MagicMock()
    db.get_user.return_value = user
    db.get_connections_by_user.return_value = connections_by_user or []
    if server_by_id:
        db.get_server_by_id.side_effect = lambda sid: server_by_id.get(sid)
    return db


async def _fake_to_thread(fn, *args, **kwargs):
    """Run the blocking fn synchronously so MagicMock assertions work."""
    return fn(*args, **kwargs)


# ---------------------------------------------------------------------------
# 1. SSH connect called once per unique server
# ---------------------------------------------------------------------------


class TestSSHConnectPerServer:
    @pytest.mark.asyncio
    async def test_one_connect_per_server_same_server_three_connections(self):
        """3 connections on 1 server → ssh.connect called exactly once."""
        mock_user = {"id": "u1", "username": "testuser"}
        conn1 = {"server_id": "srv1", "protocol": "awg", "client_id": "peer1"}
        conn2 = {"server_id": "srv1", "protocol": "awg", "client_id": "peer2"}
        conn3 = {"server_id": "srv1", "protocol": "xray", "client_id": "peer3"}
        mock_server = {
            "id": "srv1",
            "host": "1.2.3.4",
            "ssh_port": 22,
            "username": "root",
            "password": "secret",
        }

        mock_db = _make_mock_db(
            user=mock_user,
            connections_by_user=[conn1, conn2, conn3],
            server_by_id={"srv1": mock_server},
        )
        mock_ssh = MagicMock()
        mock_manager = MagicMock()

        from app.services.background import perform_delete_user

        with (
            patch("app.services.background.get_db", return_value=mock_db),
            patch("app.services.background.get_ssh", return_value=mock_ssh),
            patch(
                "app.services.background.get_protocol_manager",
                return_value=mock_manager,
            ),
            patch("app.services.background.asyncio.to_thread", side_effect=_fake_to_thread),
        ):
            result = await perform_delete_user("u1")

        assert result is True
        mock_ssh.connect.assert_called_once()
        mock_ssh.disconnect.assert_called_once()
        # remove_client called 3 times (once per connection)
        assert mock_manager.remove_client.call_count == 3

    @pytest.mark.asyncio
    async def test_two_connects_for_two_servers(self):
        """Connections on 2 servers → ssh.connect called twice."""
        mock_user = {"id": "u1", "username": "testuser"}
        conn1 = {"server_id": "srv1", "protocol": "awg", "client_id": "peer1"}
        conn2 = {"server_id": "srv2", "protocol": "xray", "client_id": "peer2"}
        mock_srv1 = {
            "id": "srv1",
            "host": "1.1.1.1",
            "ssh_port": 22,
            "username": "root",
            "password": "p1",
        }
        mock_srv2 = {
            "id": "srv2",
            "host": "2.2.2.2",
            "ssh_port": 22,
            "username": "root",
            "password": "p2",
        }

        mock_db = _make_mock_db(
            user=mock_user,
            connections_by_user=[conn1, conn2],
            server_by_id={"srv1": mock_srv1, "srv2": mock_srv2},
        )

        # Need separate mock_ssh instances per server: two calls to get_ssh
        mock_ssh1 = MagicMock()
        mock_ssh2 = MagicMock()
        mock_manager = MagicMock()

        from app.services.background import perform_delete_user

        with (
            patch("app.services.background.get_db", return_value=mock_db),
            patch(
                "app.services.background.get_ssh",
                side_effect=[mock_ssh1, mock_ssh2],
            ),
            patch(
                "app.services.background.get_protocol_manager",
                return_value=mock_manager,
            ),
            patch("app.services.background.asyncio.to_thread", side_effect=_fake_to_thread),
        ):
            result = await perform_delete_user("u1")

        assert result is True
        mock_ssh1.connect.assert_called_once()
        mock_ssh1.disconnect.assert_called_once()
        mock_ssh2.connect.assert_called_once()
        mock_ssh2.disconnect.assert_called_once()
        assert mock_manager.remove_client.call_count == 2


# ---------------------------------------------------------------------------
# 2. Error handling — per-server isolation
# ---------------------------------------------------------------------------


class TestErrorIsolationPerServer:
    @pytest.mark.asyncio
    async def test_one_server_fails_other_continues(self):
        """If server 1 fails, server 2 still gets processed."""
        mock_user = {"id": "u1", "username": "testuser"}
        conn1 = {"server_id": "srv1", "protocol": "awg", "client_id": "peer1"}
        conn2 = {"server_id": "srv2", "protocol": "xray", "client_id": "peer2"}
        mock_srv1 = {
            "id": "srv1",
            "host": "1.1.1.1",
            "ssh_port": 22,
            "username": "root",
            "password": "p1",
        }
        mock_srv2 = {
            "id": "srv2",
            "host": "2.2.2.2",
            "ssh_port": 22,
            "username": "root",
            "password": "p2",
        }

        mock_db = _make_mock_db(
            user=mock_user,
            connections_by_user=[conn1, conn2],
            server_by_id={"srv1": mock_srv1, "srv2": mock_srv2},
        )

        # server 1 SSH connect raises; server 2 works fine
        mock_ssh1 = MagicMock()
        mock_ssh1.connect.side_effect = RuntimeError("connection refused")
        mock_ssh2 = MagicMock()
        mock_manager = MagicMock()

        from app.services.background import perform_delete_user

        with (
            patch("app.services.background.get_db", return_value=mock_db),
            patch(
                "app.services.background.get_ssh",
                side_effect=[mock_ssh1, mock_ssh2],
            ),
            patch(
                "app.services.background.get_protocol_manager",
                return_value=mock_manager,
            ),
            patch("app.services.background.asyncio.to_thread", side_effect=_fake_to_thread),
        ):
            result = await perform_delete_user("u1")

        assert result is True
        # server 2 still processed
        mock_ssh2.connect.assert_called_once()
        mock_ssh2.disconnect.assert_called_once()
        # server 1 error logged but doesn't crash
        # remove_client only called for server 2's connection
        assert mock_manager.remove_client.call_count == 1

    @pytest.mark.asyncio
    async def test_inner_connection_fails_other_connections_processed(self):
        """If one connection removal fails, other connections on same server still removed."""
        mock_user = {"id": "u1", "username": "testuser"}
        conn1 = {"server_id": "srv1", "protocol": "awg", "client_id": "peer1"}
        conn2 = {"server_id": "srv1", "protocol": "xray", "client_id": "peer2"}
        mock_srv1 = {
            "id": "srv1",
            "host": "1.1.1.1",
            "ssh_port": 22,
            "username": "root",
            "password": "p1",
        }

        mock_db = _make_mock_db(
            user=mock_user,
            connections_by_user=[conn1, conn2],
            server_by_id={"srv1": mock_srv1},
        )
        mock_ssh = MagicMock()
        mock_manager = MagicMock()

        from app.services.background import perform_delete_user

        with (
            patch("app.services.background.get_db", return_value=mock_db),
            patch("app.services.background.get_ssh", return_value=mock_ssh),
            patch(
                "app.services.background.get_protocol_manager",
                return_value=mock_manager,
            ),
            patch("app.services.background.asyncio.to_thread", side_effect=_fake_to_thread),
        ):
            result = await perform_delete_user("u1")

        assert result is True
        assert mock_manager.remove_client.call_count == 2


# ---------------------------------------------------------------------------
# 3. Return values
# ---------------------------------------------------------------------------


class TestReturnValues:
    @pytest.mark.asyncio
    async def test_returns_true_when_user_exists(self):
        """Returns True when user exists and deletion succeeds."""
        mock_user = {"id": "u1", "username": "testuser"}
        mock_db = _make_mock_db(user=mock_user, connections_by_user=[])

        from app.services.background import perform_delete_user

        with patch("app.services.background.get_db", return_value=mock_db):
            result = await perform_delete_user("u1")

        assert result is True
        mock_db.delete_user.assert_called_once_with("u1")

    @pytest.mark.asyncio
    async def test_returns_false_when_user_not_found(self):
        """Returns False when user does not exist."""
        mock_db = _make_mock_db(user=None, connections_by_user=[])

        from app.services.background import perform_delete_user

        with patch("app.services.background.get_db", return_value=mock_db):
            result = await perform_delete_user("nonexistent")

        assert result is False
        mock_db.delete_user.assert_not_called()


# ---------------------------------------------------------------------------
# 4. DB cleanup ordering
# ---------------------------------------------------------------------------


class TestDBCleanupOrdering:
    @pytest.mark.asyncio
    async def test_delete_user_called_after_ssh_operations(self):
        """db.delete_user is called after all SSH operations complete."""
        call_order = []

        mock_user = {"id": "u1", "username": "testuser"}
        conn1 = {"server_id": "srv1", "protocol": "awg", "client_id": "peer1"}
        mock_srv1 = {
            "id": "srv1",
            "host": "1.1.1.1",
            "ssh_port": 22,
            "username": "root",
            "password": "p1",
        }

        mock_db = _make_mock_db(
            user=mock_user,
            connections_by_user=[conn1],
            server_by_id={"srv1": mock_srv1},
        )

        def recording_disconnect():
            call_order.append("disconnect")

        def recording_delete_user(_uid):
            call_order.append("delete_user")

        mock_ssh = MagicMock()
        mock_ssh.disconnect.side_effect = recording_disconnect
        mock_db.delete_user.side_effect = recording_delete_user
        mock_manager = MagicMock()

        from app.services.background import perform_delete_user

        with (
            patch("app.services.background.get_db", return_value=mock_db),
            patch("app.services.background.get_ssh", return_value=mock_ssh),
            patch(
                "app.services.background.get_protocol_manager",
                return_value=mock_manager,
            ),
            patch("app.services.background.asyncio.to_thread", side_effect=_fake_to_thread),
        ):
            await perform_delete_user("u1")

        # disconnect happens before delete_user
        assert call_order == ["disconnect", "delete_user"]


# ---------------------------------------------------------------------------
# 5. Edge cases
# ---------------------------------------------------------------------------


class TestEdgeCases:
    @pytest.mark.asyncio
    async def test_user_with_no_connections(self):
        """User exists but has no connections — still deleted."""
        mock_user = {"id": "u1", "username": "lonely"}
        mock_db = _make_mock_db(user=mock_user, connections_by_user=[])

        from app.services.background import perform_delete_user

        with patch("app.services.background.get_db", return_value=mock_db):
            result = await perform_delete_user("u1")

        assert result is True
        mock_db.delete_user.assert_called_once_with("u1")

    @pytest.mark.asyncio
    async def test_server_not_in_db_skipped(self):
        """Connection references a server_id that doesn't exist — skipped."""
        mock_user = {"id": "u1", "username": "testuser"}
        conn1 = {"server_id": "srv_missing", "protocol": "awg", "client_id": "peer1"}
        conn2 = {"server_id": "srv1", "protocol": "xray", "client_id": "peer2"}
        mock_srv1 = {
            "id": "srv1",
            "host": "1.1.1.1",
            "ssh_port": 22,
            "username": "root",
            "password": "p1",
        }

        mock_db = _make_mock_db(
            user=mock_user,
            connections_by_user=[conn1, conn2],
            server_by_id={"srv1": mock_srv1, "srv_missing": None},
        )
        mock_ssh = MagicMock()
        mock_manager = MagicMock()

        from app.services.background import perform_delete_user

        with (
            patch("app.services.background.get_db", return_value=mock_db),
            patch("app.services.background.get_ssh", return_value=mock_ssh),
            patch(
                "app.services.background.get_protocol_manager",
                return_value=mock_manager,
            ),
            patch("app.services.background.asyncio.to_thread", side_effect=_fake_to_thread),
        ):
            result = await perform_delete_user("u1")

        assert result is True
        # Only one server processed (srv1), not the missing one
        mock_ssh.connect.assert_called_once()
        assert mock_manager.remove_client.call_count == 1
