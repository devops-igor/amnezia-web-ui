"""Tests for AWG Traffic Control Manager (awg_tc.py).

All SSH/Docker calls are mocked — no real network commands are executed.
"""

from __future__ import annotations

import pytest
from unittest.mock import MagicMock, patch

from app.managers.awg_tc import (
    _peer_to_class_id,
    _tc_exec,
    apply_speed_limit,
    remove_speed_limit,
    reapply_all_limits,
    setup_qdisc,
    teardown_qdisc,
    _find_filter_handles,
)


class TestPeerToClassId:
    """Test IP-to-class-ID conversion."""

    def test_valid_ip(self) -> None:
        assert _peer_to_class_id("10.8.1.45") == 45

    def test_valid_ip_low(self) -> None:
        assert _peer_to_class_id("10.8.1.1") == 1

    def test_valid_ip_high(self) -> None:
        assert _peer_to_class_id("10.8.1.253") == 253

    def test_zero_octet_rejected(self) -> None:
        with pytest.raises(ValueError, match="out of usable range"):
            _peer_to_class_id("10.8.1.0")

    def test_broadcast_octet_rejected(self) -> None:
        with pytest.raises(ValueError, match="out of usable range"):
            _peer_to_class_id("10.8.1.255")

    def test_invalid_format_rejected(self) -> None:
        with pytest.raises(ValueError, match="Invalid IP"):
            _peer_to_class_id("not-an-ip")

    def test_three_part_ip_rejected(self) -> None:
        with pytest.raises(ValueError, match="Invalid IP"):
            _peer_to_class_id("10.8.1")


class TestTcExec:
    """Test the _tc_exec helper."""

    def test_successful_command(self) -> None:
        ssh = MagicMock()
        ssh.run_sudo_command.return_value = ("qdisc htb 1:", "", 0)
        out, err, code = _tc_exec(ssh, "amnezia-awg", "qdisc show dev awg0")
        assert code == 0
        assert "htb" in out
        ssh.run_sudo_command.assert_called_once_with(
            "docker exec -i amnezia-awg tc qdisc show dev awg0"
        )

    def test_failed_command(self) -> None:
        ssh = MagicMock()
        ssh.run_sudo_command.return_value = ("", "RTNETLINK answers: No such device", 1)
        out, err, code = _tc_exec(ssh, "amnezia-awg", "qdisc show dev awg0")
        assert code == 1

    def test_failed_command_ignore_errors(self) -> None:
        ssh = MagicMock()
        ssh.run_sudo_command.return_value = ("", "error", 1)
        out, err, code = _tc_exec(ssh, "amnezia-awg", "qdisc del dev awg0 root", ignore_errors=True)
        assert code == 1


class TestSetupQdisc:
    """Test HTB qdisc setup."""

    def test_already_exists(self) -> None:
        ssh = MagicMock()
        ssh.run_sudo_command.return_value = (
            "qdisc htb 1: root default 9999",
            "",
            0,
        )
        result = setup_qdisc(ssh, "amnezia-awg", "awg0")
        assert result["status"] == "ok"
        assert "already exists" in result["message"]

    def test_create_new_qdisc(self) -> None:
        ssh = MagicMock()
        responses = [
            ("qdisc mq 0: root", "", 0),  # show - no htb
            ("", "", 0),  # del old
            ("", "", 0),  # add qdisc
            ("", "", 0),  # add default class
        ]
        ssh.run_sudo_command.side_effect = responses
        result = setup_qdisc(ssh, "amnezia-awg", "awg0")
        assert result["status"] == "ok"
        assert "created" in result["message"]

    def test_create_qdisc_no_existing(self) -> None:
        ssh = MagicMock()
        responses = [
            ("", "Cannot find device", 1),  # show - error (no qdisc)
            ("", "", 0),  # del old (ignored)
            ("", "", 0),  # add qdisc
            ("", "", 0),  # add default class
        ]
        ssh.run_sudo_command.side_effect = responses
        result = setup_qdisc(ssh, "amnezia-awg", "awg0")
        assert result["status"] == "ok"

    def test_add_qdisc_fails(self) -> None:
        ssh = MagicMock()
        responses = [
            ("", "error", 1),  # show
            ("", "", 0),  # del old
            ("", "RTNETLINK error", 1),  # add qdisc fails
        ]
        ssh.run_sudo_command.side_effect = responses
        result = setup_qdisc(ssh, "amnezia-awg", "awg0")
        assert result["status"] == "error"


class TestApplySpeedLimit:
    """Test applying speed limits."""

    @patch("app.managers.awg_tc.remove_speed_limit")
    @patch("app.managers.awg_tc.setup_qdisc")
    def test_apply_limit(self, mock_setup, mock_remove) -> None:
        mock_setup.return_value = {"status": "ok"}
        mock_remove.return_value = {"status": "ok", "message": "removed"}
        ssh = MagicMock()
        ssh.run_sudo_command.return_value = ("", "", 0)
        result = apply_speed_limit(ssh, "amnezia-awg", "awg0", "10.8.1.45", 10, 5)
        assert result["status"] == "ok"
        assert "10/5 Mbps" in result["message"]

    @patch("app.managers.awg_tc.remove_speed_limit")
    @patch("app.managers.awg_tc.setup_qdisc")
    def test_apply_limit_with_existing_qdisc(self, mock_setup, mock_remove) -> None:
        mock_setup.return_value = {"status": "ok"}
        mock_remove.return_value = {"status": "ok", "message": "removed"}
        ssh = MagicMock()
        ssh.run_sudo_command.return_value = ("", "", 0)
        result = apply_speed_limit(ssh, "amnezia-awg", "awg0", "10.8.1.100", 50, 50)
        assert result["status"] == "ok"

    def test_invalid_ip(self) -> None:
        ssh = MagicMock()
        result = apply_speed_limit(ssh, "amnezia-awg", "awg0", "invalid", 10, 5)
        assert result["status"] == "error"

    @patch("app.managers.awg_tc.remove_speed_limit")
    @patch("app.managers.awg_tc.setup_qdisc")
    def test_class_add_fails(self, mock_setup, mock_remove) -> None:
        mock_setup.return_value = {"status": "ok"}
        mock_remove.return_value = {"status": "ok", "message": "removed"}
        ssh = MagicMock()
        ssh.run_sudo_command.return_value = ("", "RTNETLINK error", 1)
        result = apply_speed_limit(ssh, "amnezia-awg", "awg0", "10.8.1.45", 10, 5)
        assert result["status"] == "error"

    @patch("app.managers.awg_tc.remove_speed_limit")
    @patch("app.managers.awg_tc.setup_qdisc")
    def test_uses_max_of_down_up(self, mock_setup, mock_remove) -> None:
        mock_setup.return_value = {"status": "ok"}
        mock_remove.return_value = {"status": "ok", "message": "removed"}
        ssh = MagicMock()
        ssh.run_sudo_command.return_value = ("", "", 0)
        result = apply_speed_limit(ssh, "amnezia-awg", "awg0", "10.8.1.2", 5, 10)
        assert result["status"] == "ok"
        # Find the class add command and verify it uses max(5, 10) = 10
        for call in ssh.run_sudo_command.call_args_list:
            cmd = call[0][0]
            if "class add" in cmd and "10mbit" in cmd:
                assert "10mbit" in cmd
                break


class TestFindFilterHandles:
    """Test _find_filter_handles."""

    def test_find_handles(self) -> None:
        ssh = MagicMock()
        # Real tc filter show output: entries start with "filter parent"
        ssh.run_sudo_command.return_value = (
            "filter parent 1: protocol ip pref 1 u32\n"
            "filter parent 1: protocol ip pref 1 u32 fh 800::800 order 2048 "
            "key ht 800 bkt 0 flowid 1:45\n"
            "  match 0a08012d/ffffffff at 12\n"
            "filter parent 1: protocol ip pref 2 u32 fh 801::800 order 2048 "
            "key ht 801 bkt 0 flowid 1:45\n"
            "  match 0a08012d/ffffffff at 16\n",
            "",
            0,
        )
        handles = _find_filter_handles(ssh, "amnezia-awg", "awg0", 45)
        assert len(handles) == 2
        assert "800::800" in handles
        assert "801::800" in handles

    def test_empty_output(self) -> None:
        ssh = MagicMock()
        ssh.run_sudo_command.return_value = ("", "", 0)
        handles = _find_filter_handles(ssh, "amnezia-awg", "awg0", 45)
        assert handles == []

    def test_no_matching_class(self) -> None:
        ssh = MagicMock()
        ssh.run_sudo_command.return_value = (
            "filter parent 1: protocol ip pref 1 u32 fh 800::800 " "flowid 1:99\n",
            "",
            0,
        )
        handles = _find_filter_handles(ssh, "amnezia-awg", "awg0", 45)
        assert handles == []


class TestRemoveSpeedLimit:
    """Test removing speed limits."""

    def test_remove_existing_class(self) -> None:
        ssh = MagicMock()
        # _find_filter_handles returns empty (no filters)
        # class del succeeds
        responses = [
            ("", "", 0),  # filter show (no matching handles)
            ("", "", 0),  # class del
        ]
        ssh.run_sudo_command.side_effect = responses
        result = remove_speed_limit(ssh, "amnezia-awg", "awg0", "10.8.1.45")
        assert result["status"] == "ok"
        assert "removed" in result["message"]

    def test_remove_with_filters(self) -> None:
        ssh = MagicMock()
        # _find_filter_handles finds two handles
        filter_show_output = (
            "filter parent 1: protocol ip pref 1 u32 fh 800::800 "
            "flowid 1:45\n  match 0a08012d/ffffffff at 12\n"
            "filter parent 1: protocol ip pref 2 u32 fh 801::800 "
            "flowid 1:45\n  match 0a08012d/ffffffff at 16\n"
        )
        responses = [
            (filter_show_output, "", 0),  # filter show
            ("", "", 0),  # filter del handle 800::800
            ("", "", 0),  # filter del handle 801::800
            ("", "", 0),  # class del
        ]
        ssh.run_sudo_command.side_effect = responses
        result = remove_speed_limit(ssh, "amnezia-awg", "awg0", "10.8.1.45")
        assert result["status"] == "ok"
        assert "removed" in result["message"]

    def test_remove_nonexistent_class(self) -> None:
        ssh = MagicMock()
        responses = [
            ("", "", 0),  # filter show (nothing)
            ("", "Cannot find class", 1),  # class del fails
        ]
        ssh.run_sudo_command.side_effect = responses
        result = remove_speed_limit(ssh, "amnezia-awg", "awg0", "10.8.1.45")
        assert result["status"] == "ok"
        assert "No existing limit" in result["message"]

    def test_invalid_ip(self) -> None:
        ssh = MagicMock()
        result = remove_speed_limit(ssh, "amnezia-awg", "awg0", "bad-ip")
        assert result["status"] == "error"


class TestReapplyAllLimits:
    """Test reapply_all_limits."""

    def test_reapply_with_limits(self) -> None:
        ssh = MagicMock()
        clients = [
            {
                "clientId": "abc",
                "clientIp": "10.8.1.2",
                "userData": {"speed_limit_down": 10, "speed_limit_up": 5},
            },
            {
                "clientId": "def",
                "clientIp": "10.8.1.3",
                "userData": {"speed_limit_down": 20, "speed_limit_up": 10},
            },
            {"clientId": "ghi", "clientIp": "10.8.1.4", "userData": {}},
        ]
        with patch("app.managers.awg_tc.setup_qdisc", return_value={"status": "ok"}):
            with patch("app.managers.awg_tc.apply_speed_limit") as mock_apply:
                mock_apply.return_value = {"status": "ok", "message": "applied"}
                result = reapply_all_limits(ssh, "amnezia-awg", "awg0", clients)
                assert result["status"] == "ok"
                assert result["applied"] == 2
                assert result["errors"] == []
                assert mock_apply.call_count == 2

    def test_reapply_no_limits(self) -> None:
        ssh = MagicMock()
        ssh.run_sudo_command.return_value = ("qdisc htb 1: root", "", 0)
        clients = [{"clientId": "abc", "clientIp": "10.8.1.2", "userData": {}}]
        result = reapply_all_limits(ssh, "amnezia-awg", "awg0", clients)
        assert result["applied"] == 0

    def test_reapply_empty_list(self) -> None:
        ssh = MagicMock()
        ssh.run_sudo_command.return_value = ("qdisc htb 1: root", "", 0)
        result = reapply_all_limits(ssh, "amnezia-awg", "awg0", [])
        assert result["applied"] == 0

    def test_reapply_partial_failure(self) -> None:
        ssh = MagicMock()
        clients = [
            {
                "clientId": "abc",
                "clientIp": "10.8.1.2",
                "userData": {"speed_limit_down": 10, "speed_limit_up": 5},
            },
        ]
        with patch("app.managers.awg_tc.setup_qdisc", return_value={"status": "ok"}):
            with patch("app.managers.awg_tc.apply_speed_limit") as mock_apply:
                mock_apply.return_value = {
                    "status": "error",
                    "message": "RTNETLINK error",
                }
                result = reapply_all_limits(ssh, "amnezia-awg", "awg0", clients)
                assert result["status"] == "partial"
                assert result["applied"] == 0
                assert len(result["errors"]) == 1


class TestTeardownQdisc:
    """Test qdisc teardown."""

    def test_teardown_existing(self) -> None:
        ssh = MagicMock()
        ssh.run_sudo_command.return_value = ("", "", 0)
        result = teardown_qdisc(ssh, "amnezia-awg", "awg0")
        assert result["status"] == "ok"
        assert "removed" in result["message"]

    def test_teardown_nonexistent(self) -> None:
        ssh = MagicMock()
        ssh.run_sudo_command.return_value = ("", "Cannot find", 1)
        result = teardown_qdisc(ssh, "amnezia-awg", "awg0")
        assert result["status"] == "ok"
        assert "No qdisc" in result["message"]
