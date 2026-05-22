"""Unit tests for AWG protocol migration and normalize_protocol."""

import pytest
from unittest.mock import MagicMock

from schemas import normalize_protocol
from config import migrate_awg_protocol_names


class TestNormalizeProtocol:
    """Tests for normalize_protocol() in schemas.py."""

    def test_normalize_awg2_returns_awg(self):
        """normalize_protocol('awg2') returns 'awg'."""
        assert normalize_protocol("awg2") == "awg"

    def test_normalize_awg_legacy_returns_awg(self):
        """normalize_protocol('awg_legacy') returns 'awg'."""
        assert normalize_protocol("awg_legacy") == "awg"

    def test_normalize_xray_passthrough(self):
        """normalize_protocol('xray') returns 'xray' (passthrough)."""
        assert normalize_protocol("xray") == "xray"

    def test_normalize_awg_passthrough(self):
        """normalize_protocol('awg') returns 'awg' (already correct)."""
        assert normalize_protocol("awg") == "awg"

    def test_normalize_telemt_passthrough(self):
        """normalize_protocol('telemt') returns 'telemt' (passthrough)."""
        assert normalize_protocol("telemt") == "telemt"

    def test_normalize_unknown_passthrough(self):
        """normalize_protocol('unknown_proto') returns 'unknown_proto' (passthrough)."""
        assert normalize_protocol("unknown_proto") == "unknown_proto"


class TestMigrateAWGProtocolNames:
    """Tests for migrate_awg_protocol_names() in config.py."""

    @pytest.fixture(autouse=True)
    def _setup_db_mock(self, monkeypatch):
        """Mock the database so we don't touch real data."""
        self.mock_db = MagicMock()
        monkeypatch.setattr("config.get_db", lambda: self.mock_db)

    def _setup_mock_server(self, protocols: dict) -> dict:
        """Create a mock server dict with given protocols."""
        return {"id": 1, "protocols": protocols}

    def test_migrates_awg2_server_to_awg(self):
        """Server with protocols: {'awg2': {...}} gets migrated to {'awg': {...}}."""
        server = self._setup_mock_server({"awg2": {"installed": True, "port": "55424"}})
        self.mock_db.get_all_servers.return_value = [server]
        self.mock_db._connection.return_value.__enter__.return_value = MagicMock()

        migrate_awg_protocol_names()

        self.mock_db.update_server.assert_called_once()
        call_args = self.mock_db.update_server.call_args
        assert call_args[0][0] == 1  # server id
        updated_protocols = call_args[0][1]["protocols"]
        assert "awg2" not in updated_protocols
        assert "awg" in updated_protocols

    def test_migrates_awg_legacy_server_to_awg(self):
        """Server with protocols: {'awg_legacy': {...}} gets migrated to {'awg': {...}}."""
        server = self._setup_mock_server({"awg_legacy": {"installed": True, "port": "55424"}})
        self.mock_db.get_all_servers.return_value = [server]
        self.mock_db._connection.return_value.__enter__.return_value = MagicMock()

        migrate_awg_protocol_names()

        self.mock_db.update_server.assert_called_once()
        call_args = self.mock_db.update_server.call_args
        assert call_args[0][0] == 1
        updated_protocols = call_args[0][1]["protocols"]
        assert "awg_legacy" not in updated_protocols
        assert "awg" in updated_protocols

    def test_awg_server_stays_unchanged(self):
        """Server with protocols: {'awg': {...}} stays unchanged."""
        server = self._setup_mock_server({"awg": {"installed": True, "port": "55424"}})
        self.mock_db.get_all_servers.return_value = [server]
        self.mock_db._connection.return_value.__enter__.return_value = MagicMock()

        migrate_awg_protocol_names()

        self.mock_db.update_server.assert_not_called()

    def test_migrates_connection_awg2_to_awg(self):
        """Connection with protocol: 'awg2' gets migrated to 'awg'."""
        server = self._setup_mock_server({"awg": {"installed": True}})
        self.mock_db.get_all_servers.return_value = [server]
        mock_conn = MagicMock()

        def execute_side_effect(sql, params=()):
            result = MagicMock()
            params_str = params[0] if params else ""
            if "awg2" in sql or "awg2" == params_str:
                result.fetchall.return_value = [{"id": "conn-1"}]
            else:
                result.fetchall.return_value = []
            return result

        mock_conn.execute.side_effect = execute_side_effect
        self.mock_db._connection.return_value.__enter__.return_value = mock_conn

        migrate_awg_protocol_names()

        # Should have called execute with UPDATE for awg2
        update_calls = [
            call
            for call in mock_conn.execute.call_args_list
            if "UPDATE user_connections" in str(call.args[0])
        ]
        assert len(update_calls) == 1

    def test_migrates_connection_awg_legacy_to_awg(self):
        """Connection with protocol: 'awg_legacy' gets migrated to 'awg'."""
        server = self._setup_mock_server({"awg": {"installed": True}})
        self.mock_db.get_all_servers.return_value = [server]
        mock_conn = MagicMock()

        def execute_side_effect(sql, params=()):
            result = MagicMock()
            params_str = params[0] if params else ""
            if "awg2" in sql or "awg2" == params_str:
                result.fetchall.return_value = []
            elif "awg_legacy" in sql or "awg_legacy" == params_str:
                result.fetchall.return_value = [{"id": "conn-2"}]
            else:
                result.fetchall.return_value = []
            return result

        mock_conn.execute.side_effect = execute_side_effect
        self.mock_db._connection.return_value.__enter__.return_value = mock_conn

        migrate_awg_protocol_names()

        # Should have called UPDATE with awg_legacy
        update_calls = [
            call
            for call in mock_conn.execute.call_args_list
            if "UPDATE user_connections" in str(call.args[0])
        ]
        assert len(update_calls) == 1

    def test_connection_awg_stays_unchanged(self):
        """Connection with protocol: 'awg' stays unchanged."""
        server = self._setup_mock_server({"awg": {"installed": True}})
        self.mock_db.get_all_servers.return_value = [server]
        mock_conn = MagicMock()
        # Both alias queries return empty
        mock_conn.execute.return_value.fetchall.return_value = []
        self.mock_db._connection.return_value.__enter__.return_value = mock_conn

        migrate_awg_protocol_names()

        # No UPDATE should have been executed on user_connections
        update_calls = [
            call
            for call in mock_conn.execute.call_args_list
            if "UPDATE user_connections" in str(call.args[0])
        ]
        assert len(update_calls) == 0

    def test_no_connections_needed(self):
        """When no connections need migration, log the appropriate message."""
        server = self._setup_mock_server({"awg": {"installed": True}})
        self.mock_db.get_all_servers.return_value = [server]
        mock_conn = MagicMock()
        mock_conn.execute.return_value.fetchall.return_value = []
        self.mock_db._connection.return_value.__enter__.return_value = mock_conn

        migrate_awg_protocol_names()

        # No UPDATE statements issued
        self.mock_db.update_server.assert_not_called()
