"""Tests for AWG Traffic Control Manager (awg_tc.py).

All SSH/Docker calls are mocked — no real network commands are executed.

Covers IFB-based bidirectional shaping:
  - awg0 (egress): Download shaping via dst IP match.
  - ifb0 (egress): Upload shaping via src IP match (after IFB redirect).
  - Global pool: Class 1:1 on each interface.
  - Default class 1:9999: Unlimited traffic.
"""

from __future__ import annotations

import pytest
from unittest.mock import MagicMock, patch

from app.managers.awg_tc import (
    IFB_DEVICE,
    _peer_to_class_id,
    _tc_exec,
    _ip_exec,
    _setup_qdisc_on_interface,
    _find_filter_handles,
    setup_ifb,
    teardown_ifb,
    setup_qdisc,
    apply_speed_limit,
    remove_speed_limit,
    set_global_limit,
    reapply_all_limits,
    teardown_qdisc,
    _build_batch_tc_script,
)

# -------------------------------------------------------------------------- #
# TestPeerToClassId
# -------------------------------------------------------------------------- #


class TestPeerToClassId:
    """Test IP-to-class-ID conversion."""

    def test_valid_ip(self) -> None:
        assert _peer_to_class_id("10.8.1.45") == 145

    def test_valid_ip_low(self) -> None:
        assert _peer_to_class_id("10.8.1.1") == 101

    def test_valid_ip_high(self) -> None:
        assert _peer_to_class_id("10.8.1.253") == 353

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


# -------------------------------------------------------------------------- #
# TestTcExec
# -------------------------------------------------------------------------- #


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


# -------------------------------------------------------------------------- #
# TestIpExec
# -------------------------------------------------------------------------- #


class TestIpExec:
    """Test the _ip_exec helper."""

    def test_successful_link_show(self) -> None:
        ssh = MagicMock()
        ssh.run_sudo_command.return_value = ("ifb0: <POINTOPOINT>", "", 0)
        out, err, code = _ip_exec(ssh, "amnezia-awg", "link show dev ifb0")
        assert code == 0
        assert "ifb0" in out

    def test_device_not_found(self) -> None:
        ssh = MagicMock()
        ssh.run_sudo_command.return_value = ("", "Cannot find device", 1)
        out, err, code = _ip_exec(ssh, "amnezia-awg", "link show dev ifb0")
        assert code == 1


# -------------------------------------------------------------------------- #
# TestSetupQdiscOnInterface
# -------------------------------------------------------------------------- #


class TestSetupQdiscOnInterface:
    """Test _setup_qdisc_on_interface."""

    def test_already_exists_awg0(self) -> None:
        ssh = MagicMock()
        ssh.run_sudo_command.return_value = ("qdisc htb 1: root", "", 0)
        result = _setup_qdisc_on_interface(ssh, "amnezia-awg", "awg0")
        assert result["status"] == "ok"
        assert "already exists" in result["message"]

    def test_already_exists_ifb0(self) -> None:
        ssh = MagicMock()
        ssh.run_sudo_command.return_value = ("qdisc htb 1: root", "", 0)
        result = _setup_qdisc_on_interface(ssh, "amnezia-awg", IFB_DEVICE)
        assert result["status"] == "ok"
        assert "already exists" in result["message"]

    def test_create_new_qdisc(self) -> None:
        ssh = MagicMock()
        # Sequence: show (no htb) → del old → add root → add pool 1:1 → add default 1:9999
        responses = [
            ("", "Cannot find device", 1),  # show - no existing htb
            ("", "", 0),  # del old (ignore error)
            ("", "", 0),  # add root qdisc
            ("", "", 0),  # add pool class 1:1
            ("", "", 0),  # add default class 1:9999
        ]
        ssh.run_sudo_command.side_effect = responses
        result = _setup_qdisc_on_interface(ssh, "amnezia-awg", "awg0")
        assert result["status"] == "ok"
        assert "created" in result["message"]
        # Verify pool class rate command
        calls = ssh.run_sudo_command.call_args_list
        assert any("class add" in str(c) and "1:1" in str(c) for c in calls)

    def test_create_with_global_limit(self) -> None:
        ssh = MagicMock()
        responses = [
            ("", "Cannot find device", 1),  # show
            ("", "", 0),  # del
            ("", "", 0),  # add root
            ("", "", 0),  # add pool 1:1
            ("", "", 0),  # add default 1:9999
        ]
        ssh.run_sudo_command.side_effect = responses
        result = _setup_qdisc_on_interface(ssh, "amnezia-awg", "awg0", global_limit_mbps=100)
        assert result["status"] == "ok"
        # Pool rate should use the specified limit
        calls = ssh.run_sudo_command.call_args_list
        pool_call = [c for c in calls if "1:1" in str(c)][0]
        assert "100mbit" in str(pool_call)

    def test_add_root_qdisc_fails(self) -> None:
        ssh = MagicMock()
        responses = [
            ("", "Cannot find device", 1),  # show
            ("", "", 0),  # del
            ("", "RTNETLINK error", 1),  # add root fails
        ]
        ssh.run_sudo_command.side_effect = responses
        result = _setup_qdisc_on_interface(ssh, "amnezia-awg", "awg0")
        assert result["status"] == "error"
        assert "Failed to add HTB qdisc" in result["message"]

    def test_add_pool_class_fails(self) -> None:
        ssh = MagicMock()
        responses = [
            ("", "Cannot find device", 1),  # show
            ("", "", 0),  # del
            ("", "", 0),  # add root ok
            ("", "No such file or directory", 1),  # add pool class fails
        ]
        ssh.run_sudo_command.side_effect = responses
        result = _setup_qdisc_on_interface(ssh, "amnezia-awg", "awg0")
        assert result["status"] == "error"
        assert "global pool class" in result["message"]


# -------------------------------------------------------------------------- #
# TestSetupQdisc
# -------------------------------------------------------------------------- #


class TestSetupQdisc:
    """Test public setup_qdisc (thin wrapper around _setup_qdisc_on_interface)."""

    def test_already_exists(self) -> None:
        ssh = MagicMock()
        ssh.run_sudo_command.return_value = ("qdisc htb 1: root", "", 0)
        result = setup_qdisc(ssh, "amnezia-awg", "awg0")
        assert result["status"] == "ok"
        assert "already exists" in result["message"]

    def test_create_new(self) -> None:
        ssh = MagicMock()
        responses = [
            ("", "Cannot find device", 1),
            ("", "", 0),
            ("", "", 0),
            ("", "", 0),
            ("", "", 0),
        ]
        ssh.run_sudo_command.side_effect = responses
        result = setup_qdisc(ssh, "amnezia-awg", "awg0")
        assert result["status"] == "ok"
        assert "created" in result["message"]

    def test_with_global_limit(self) -> None:
        ssh = MagicMock()
        responses = [
            ("", "Cannot find device", 1),
            ("", "", 0),
            ("", "", 0),
            ("", "", 0),
            ("", "", 0),
        ]
        ssh.run_sudo_command.side_effect = responses
        result = setup_qdisc(ssh, "amnezia-awg", "awg0", global_limit_mbps=50)
        assert result["status"] == "ok"


# -------------------------------------------------------------------------- #
# TestSetupIfb
# -------------------------------------------------------------------------- #


class TestSetupIfb:
    """Test setup_ifb: creates ifb0 and redirects awg0 ingress to it."""

    def test_ifb_already_exists(self) -> None:
        ssh = MagicMock()
        responses = [
            ("ifb0: <POINTOPOINT>", "", 0),  # link show dev ifb0
            ("", "", 0),  # link set ifb0 up
            ("qdisc ingress 0: root", "", 0),  # tc qdisc show dev awg0
            ("", "", 0),  # tc filter add redirect (already exists, ignored)
        ]
        ssh.run_sudo_command.side_effect = responses
        result = setup_ifb(ssh, "amnezia-awg")
        assert result["status"] == "ok"

    def test_ifb_created_fresh(self) -> None:
        ssh = MagicMock()
        responses = [
            ("", "Cannot find device", 1),  # link show dev ifb0 (not exists)
            ("", "", 0),  # link add ifb0 type ifb
            ("", "", 0),  # link set ifb0 up
            ("", "Cannot find device", 1),  # tc qdisc show dev awg0 (no ingress)
            ("", "", 0),  # tc qdisc add dev awg0 handle ffff: ingress
            ("", "", 0),  # tc filter add redirect
        ]
        ssh.run_sudo_command.side_effect = responses
        result = setup_ifb(ssh, "amnezia-awg")
        assert result["status"] == "ok"
        assert "IFB configured" in result["message"]

    def test_ifb_create_fails(self) -> None:
        ssh = MagicMock()
        responses = [
            ("", "Cannot find device", 1),  # link show
            ("", "Operation not permitted", 1),  # link add fails
        ]
        ssh.run_sudo_command.side_effect = responses
        result = setup_ifb(ssh, "amnezia-awg")
        assert result["status"] == "error"
        assert "Failed to create ifb0" in result["message"]

    def test_ifb_up_fails(self) -> None:
        ssh = MagicMock()
        responses = [
            ("", "Cannot find device", 1),  # link show
            ("", "", 0),  # link add
            ("", "Network is down", 1),  # link set up fails
        ]
        ssh.run_sudo_command.side_effect = responses
        result = setup_ifb(ssh, "amnezia-awg")
        assert result["status"] == "error"
        assert "Failed to set ifb0 up" in result["message"]

    def test_ingress_qdisc_add_fails(self) -> None:
        ssh = MagicMock()
        responses = [
            ("", "Cannot find device", 1),  # link show
            ("", "", 0),  # link add
            ("", "", 0),  # link set up
            ("", "Cannot find device", 1),  # tc qdisc show (no ingress)
            ("", "RTNETLINK error", 1),  # tc qdisc add ingress fails
        ]
        ssh.run_sudo_command.side_effect = responses
        result = setup_ifb(ssh, "amnezia-awg")
        assert result["status"] == "error"
        assert "Failed to add ingress qdisc" in result["message"]

    def test_redirect_filter_fails(self) -> None:
        ssh = MagicMock()
        responses = [
            ("", "Cannot find device", 1),  # link show
            ("", "", 0),  # link add
            ("", "", 0),  # link set up
            ("", "Cannot find device", 1),  # tc qdisc show (no ingress)
            ("", "", 0),  # tc qdisc add ingress
            ("", "Filter exists", 1),  # tc filter add fails (not File exists)
        ]
        ssh.run_sudo_command.side_effect = responses
        result = setup_ifb(ssh, "amnezia-awg")
        assert result["status"] == "error"
        assert "Failed to add ingress redirect filter" in result["message"]


# -------------------------------------------------------------------------- #
# TestTeardownIfb
# -------------------------------------------------------------------------- #


class TestTeardownIfb:
    """Test teardown_ifb: removes ingress redirect and deletes ifb0."""

    def test_teardown_success(self) -> None:
        ssh = MagicMock()
        responses = [
            ("", "", 0),  # tc qdisc del dev awg0 handle ffff: ingress
            ("", "", 0),  # ip link del ifb0
        ]
        ssh.run_sudo_command.side_effect = responses
        result = teardown_ifb(ssh, "amnezia-awg")
        assert result["status"] == "ok"
        assert "IFB removed" in result["message"]

    def test_teardown_no_ingress(self) -> None:
        ssh = MagicMock()
        responses = [
            ("", "Cannot find device", 1),  # tc qdisc del (no ingress)
            ("", "", 0),  # ip link del ifb0
        ]
        ssh.run_sudo_command.side_effect = responses
        result = teardown_ifb(ssh, "amnezia-awg")
        assert result["status"] == "ok"

    def test_teardown_ifb_delete_fails(self) -> None:
        ssh = MagicMock()
        responses = [
            ("", "", 0),  # tc qdisc del
            ("", "Cannot find device", 1),  # ip link del ifb0 (not found is ok)
        ]
        ssh.run_sudo_command.side_effect = responses
        result = teardown_ifb(ssh, "amnezia-awg")
        assert result["status"] == "ok"  # "Cannot find device" is ignored

    def test_teardown_ifb_delete_real_error(self) -> None:
        ssh = MagicMock()
        responses = [
            ("", "", 0),  # tc qdisc del
            ("", "Operation not permitted", 1),  # ip link del fails hard
        ]
        ssh.run_sudo_command.side_effect = responses
        result = teardown_ifb(ssh, "amnezia-awg")
        assert result["status"] == "error"
        assert "Failed to delete ifb0" in result["message"]


# -------------------------------------------------------------------------- #
# TestApplySpeedLimit
# -------------------------------------------------------------------------- #


class TestApplySpeedLimit:
    """Test apply_speed_limit: creates classes/filters on both awg0 and ifb0."""

    @patch("app.managers.awg_tc.setup_ifb")
    @patch("app.managers.awg_tc._setup_qdisc_on_interface")
    @patch("app.managers.awg_tc.remove_speed_limit")
    def test_apply_limit(
        self, mock_remove: MagicMock, mock_setup_qdisc: MagicMock, mock_setup_ifb: MagicMock
    ) -> None:
        mock_setup_ifb.return_value = {"status": "ok"}
        mock_setup_qdisc.return_value = {"status": "ok"}
        mock_remove.return_value = {"status": "ok"}

        ssh = MagicMock()
        # apply_speed_limit calls _tc_exec for class+filter on awg0, then ifb0
        # 6 calls: class add awg0, filter add awg0, class add ifb0, filter add ifb0
        ssh.run_sudo_command.return_value = ("", "", 0)

        result = apply_speed_limit(ssh, "amnezia-awg", "awg0", "10.8.1.45", 10, 5)
        assert result["status"] == "ok"
        assert "10/5 Mbps" in result["message"]
        mock_setup_ifb.assert_called_once_with(ssh, "amnezia-awg")

    @patch("app.managers.awg_tc.setup_ifb")
    @patch("app.managers.awg_tc._setup_qdisc_on_interface")
    @patch("app.managers.awg_tc.remove_speed_limit")
    def test_apply_limit_with_existing(
        self, mock_remove: MagicMock, mock_setup_qdisc: MagicMock, mock_setup_ifb: MagicMock
    ) -> None:
        mock_setup_ifb.return_value = {"status": "ok"}
        mock_setup_qdisc.return_value = {"status": "ok"}
        mock_remove.return_value = {"status": "ok"}

        ssh = MagicMock()
        ssh.run_sudo_command.return_value = ("", "", 0)

        result = apply_speed_limit(ssh, "amnezia-awg", "awg0", "10.8.1.100", 50, 50)
        assert result["status"] == "ok"

    def test_invalid_ip(self) -> None:
        ssh = MagicMock()
        result = apply_speed_limit(ssh, "amnezia-awg", "awg0", "invalid", 10, 5)
        assert result["status"] == "error"

    @patch("app.managers.awg_tc.setup_ifb")
    @patch("app.managers.awg_tc._setup_qdisc_on_interface")
    @patch("app.managers.awg_tc.remove_speed_limit")
    def test_class_add_fails(
        self, mock_remove: MagicMock, mock_setup_qdisc: MagicMock, mock_setup_ifb: MagicMock
    ) -> None:
        mock_setup_ifb.return_value = {"status": "ok"}
        mock_setup_qdisc.return_value = {"status": "ok"}
        mock_remove.return_value = {"status": "ok"}

        ssh = MagicMock()
        # First class add (awg0 download) fails, upload succeeds
        ssh.run_sudo_command.side_effect = [
            ("", "RTNETLINK error", 1),  # class add awg0
            ("", "", 0),  # filter add awg0 (skipped since class failed)
            ("", "", 0),  # class add ifb0 (upload ok)
            ("", "", 0),  # filter add ifb0
        ]

        result = apply_speed_limit(ssh, "amnezia-awg", "awg0", "10.8.1.45", 10, 5)
        assert result["status"] == "ok"
        assert "errors" in result["message"].lower() or "error" in result["message"].lower()

    @patch("app.managers.awg_tc.setup_ifb")
    @patch("app.managers.awg_tc._setup_qdisc_on_interface")
    @patch("app.managers.awg_tc.remove_speed_limit")
    def test_uses_specified_rates(
        self, mock_remove: MagicMock, mock_setup_qdisc: MagicMock, mock_setup_ifb: MagicMock
    ) -> None:
        """Verify download and upload classes use their own specified rates."""
        mock_setup_ifb.return_value = {"status": "ok"}
        mock_setup_qdisc.return_value = {"status": "ok"}
        mock_remove.return_value = {"status": "ok"}

        ssh = MagicMock()
        ssh.run_sudo_command.return_value = ("", "", 0)

        result = apply_speed_limit(ssh, "amnezia-awg", "awg0", "10.8.1.2", 5, 10)
        assert result["status"] == "ok"
        # Verify awg0 uses 5mbit and ifb0 uses 10mbit
        calls = ssh.run_sudo_command.call_args_list
        class_cmds = [c[0][0] for c in calls if "class add" in c[0][0]]
        assert any("awg0" in cmd and "5mbit" in cmd for cmd in class_cmds)
        assert any("ifb0" in cmd and "10mbit" in cmd for cmd in class_cmds)

    @patch("app.managers.awg_tc.setup_ifb")
    @patch("app.managers.awg_tc._setup_qdisc_on_interface")
    @patch("app.managers.awg_tc.remove_speed_limit")
    def test_setup_ifb_error_propagates(
        self, mock_remove: MagicMock, mock_setup_qdisc: MagicMock, mock_setup_ifb: MagicMock
    ) -> None:
        mock_setup_ifb.return_value = {"status": "error", "message": "Failed to create ifb0"}
        mock_setup_qdisc.return_value = {"status": "ok"}
        mock_remove.return_value = {"status": "ok"}

        ssh = MagicMock()
        result = apply_speed_limit(ssh, "amnezia-awg", "awg0", "10.8.1.45", 10, 5)
        assert result["status"] == "error"
        assert "Failed to create ifb0" in result["message"]


# -------------------------------------------------------------------------- #
# TestFindFilterHandles
# -------------------------------------------------------------------------- #


class TestFindFilterHandles:
    """Test _find_filter_handles."""

    def test_find_handles(self) -> None:
        ssh = MagicMock()
        ssh.run_sudo_command.return_value = (
            "filter parent 1: protocol ip pref 1 u32\n"
            "filter parent 1: protocol ip pref 1 u32 fh 800::800 order 2048 "
            "key ht 800 bkt 0 flowid 1:145\n"
            "  match 0a08012d/ffffffff at 12\n"
            "filter parent 1: protocol ip pref 2 u32 fh 801::800 order 2048 "
            "key ht 801 bkt 0 flowid 1:145\n"
            "  match 0a08012d/ffffffff at 16\n",
            "",
            0,
        )
        handles = _find_filter_handles(ssh, "amnezia-awg", "awg0", 145)
        assert len(handles) == 2
        assert "800::800" in handles
        assert "801::800" in handles

    def test_empty_output(self) -> None:
        ssh = MagicMock()
        ssh.run_sudo_command.return_value = ("", "", 0)
        handles = _find_filter_handles(ssh, "amnezia-awg", "awg0", 145)
        assert handles == []

    def test_no_matching_class(self) -> None:
        ssh = MagicMock()
        ssh.run_sudo_command.return_value = (
            "filter parent 1: protocol ip pref 1 u32 fh 800::800 flowid 1:99\n",
            "",
            0,
        )
        handles = _find_filter_handles(ssh, "amnezia-awg", "awg0", 145)
        assert handles == []

    def test_different_interface(self) -> None:
        """Can search on ifb0 as well as awg0."""
        ssh = MagicMock()
        ssh.run_sudo_command.return_value = (
            "filter parent 1: protocol ip pref 1 u32 fh 900::900 flowid 1:145\n",
            "",
            0,
        )
        handles = _find_filter_handles(ssh, "amnezia-awg", IFB_DEVICE, 145)
        assert handles == ["900::900"]


# -------------------------------------------------------------------------- #
# TestRemoveSpeedLimit
# -------------------------------------------------------------------------- #


class TestRemoveSpeedLimit:
    """Test remove_speed_limit: removes from both awg0 and ifb0."""

    def test_remove_existing_class(self) -> None:
        ssh = MagicMock()
        # Sequence per direction (awg0, then ifb0):
        # filter show → class del (each)
        # No matching filters, just class deletes
        responses = [
            ("", "", 0),  # filter show awg0 (no handles)
            ("", "", 0),  # class del awg0 1:145
            ("", "", 0),  # filter show ifb0 (no handles)
            ("", "", 0),  # class del ifb0 1:145
        ]
        ssh.run_sudo_command.side_effect = responses
        result = remove_speed_limit(ssh, "amnezia-awg", "awg0", "10.8.1.45")
        assert result["status"] == "ok"
        assert "removed" in result["message"]

    def test_remove_with_filters_awg0(self) -> None:
        """Filters found on awg0 are deleted before class removal."""
        ssh = MagicMock()
        filter_show_output = (
            "filter parent 1: protocol ip pref 1 u32 fh 800::800 "
            "flowid 1:145\n  match 0a08012d/ffffffff at 12\n"
        )
        responses = [
            (filter_show_output, "", 0),  # filter show awg0
            ("", "", 0),  # filter del awg0 prio 1 handle 800::800
            ("", "", 0),  # class del awg0 1:145
            ("", "", 0),  # filter show ifb0 (no handles)
            ("", "", 0),  # class del ifb0 1:145
        ]
        ssh.run_sudo_command.side_effect = responses
        result = remove_speed_limit(ssh, "amnezia-awg", "awg0", "10.8.1.45")
        assert result["status"] == "ok"

    def test_remove_with_filters_ifb0(self) -> None:
        """Filters found on ifb0 are deleted before class removal."""
        ssh = MagicMock()
        filter_show_output_ifb = (
            "filter parent 1: protocol ip pref 2 u32 fh 901::901 "
            "flowid 1:145\n  match 0a08012d/ffffffff at 16\n"
        )
        responses = [
            ("", "", 0),  # filter show awg0 (no handles)
            ("", "", 0),  # class del awg0 1:145
            (filter_show_output_ifb, "", 0),  # filter show ifb0
            ("", "", 0),  # filter del ifb0 prio 1 handle 901::901
            ("", "", 0),  # class del ifb0 1:145
        ]
        ssh.run_sudo_command.side_effect = responses
        result = remove_speed_limit(ssh, "amnezia-awg", "awg0", "10.8.1.45")
        assert result["status"] == "ok"

    def test_remove_nonexistent_class(self) -> None:
        """Class delete errors are ignored (no existing limit)."""
        ssh = MagicMock()
        responses = [
            ("", "", 0),  # filter show awg0
            ("", "Cannot find class", 1),  # class del awg0 (not found, ignored)
            ("", "", 0),  # filter show ifb0
            ("", "Cannot find class", 1),  # class del ifb0 (not found, ignored)
        ]
        ssh.run_sudo_command.side_effect = responses
        result = remove_speed_limit(ssh, "amnezia-awg", "awg0", "10.8.1.45")
        assert result["status"] == "ok"
        assert "No existing limit" in result["message"] or "removed" in result["message"]

    def test_invalid_ip(self) -> None:
        ssh = MagicMock()
        result = remove_speed_limit(ssh, "amnezia-awg", "awg0", "bad-ip")
        assert result["status"] == "error"


# -------------------------------------------------------------------------- #
# TestSetGlobalLimit
# -------------------------------------------------------------------------- #


class TestSetGlobalLimit:
    """Test set_global_limit: changes class 1:1 rate on both awg0 and ifb0."""

    def test_set_both_directions(self) -> None:
        ssh = MagicMock()
        responses = [
            ("", "", 0),  # class change awg0 1:1 rate
            ("", "", 0),  # class change awg0 1:9999 ceil
            ("", "", 0),  # class change ifb0 1:1 rate
            ("", "", 0),  # class change ifb0 1:9999 ceil
        ]
        ssh.run_sudo_command.side_effect = responses
        result = set_global_limit(ssh, "amnezia-awg", down_mbps=100, up_mbps=50)
        assert result["status"] == "ok"
        assert "100" in result["message"]
        assert "50" in result["message"]

    def test_set_download_only(self) -> None:
        ssh = MagicMock()
        responses = [
            ("", "", 0),  # class change awg0 1:1
            ("", "", 0),  # class change awg0 1:9999
            ("", "", 0),  # class change ifb0 1:1 (unlimited → 10gbit)
            ("", "", 0),  # class change ifb0 1:9999
        ]
        ssh.run_sudo_command.side_effect = responses
        result = set_global_limit(ssh, "amnezia-awg", down_mbps=100, up_mbps=None)
        assert result["status"] == "ok"
        assert "100" in result["message"]
        assert "unlimited" in result["message"]

    def test_set_upload_only(self) -> None:
        ssh = MagicMock()
        responses = [
            ("", "", 0),  # class change awg0 1:1 (unlimited)
            ("", "", 0),  # class change awg0 1:9999
            ("", "", 0),  # class change ifb0 1:1
            ("", "", 0),  # class change ifb0 1:9999
        ]
        ssh.run_sudo_command.side_effect = responses
        result = set_global_limit(ssh, "amnezia-awg", down_mbps=None, up_mbps=75)
        assert result["status"] == "ok"
        assert "75" in result["message"]

    def test_awg0_class_change_fails(self) -> None:
        ssh = MagicMock()
        responses = [
            ("", "RTNETLINK error", 1),  # class change awg0 fails
        ]
        ssh.run_sudo_command.side_effect = responses
        result = set_global_limit(ssh, "amnezia-awg", down_mbps=100, up_mbps=50)
        assert result["status"] == "error"
        assert "download pool class" in result["message"]

    def test_ifb0_class_change_fails(self) -> None:
        ssh = MagicMock()
        responses = [
            ("", "", 0),  # class change awg0 ok
            ("", "", 0),  # class change awg0 1:9999 ok
            ("", "RTNETLINK error", 1),  # class change ifb0 fails
        ]
        ssh.run_sudo_command.side_effect = responses
        result = set_global_limit(ssh, "amnezia-awg", down_mbps=100, up_mbps=50)
        assert result["status"] == "error"
        assert "upload pool class" in result["message"]


# -------------------------------------------------------------------------- #
# TestBuildBatchTcScript
# -------------------------------------------------------------------------- #


class TestBuildBatchTcScript:
    """Test _build_batch_tc_script builds correct infra and client scripts."""

    def test_empty_clients_returns_empty_client_script(self) -> None:
        infra, client = _build_batch_tc_script("amnezia-awg", [], None, None)
        assert "docker exec" in infra
        assert client == ""

    def test_infra_contains_teardown_and_setup(self) -> None:
        infra, _ = _build_batch_tc_script("amnezia-awg", [], None, None)
        assert "tc qdisc del dev awg0 root" in infra
        assert "tc qdisc del dev ifb0 root" in infra
        assert "ip link add ifb0 type ifb" in infra
        assert "ip link set ifb0 up" in infra
        assert "tc qdisc add dev awg0 handle ffff: ingress" in infra
        assert "redirect dev ifb0" in infra
        assert "tc qdisc add dev awg0 root handle 1: htb default 9999" in infra
        assert "tc qdisc add dev ifb0 root handle 1: htb default 9999" in infra

    def test_infra_uses_global_limits(self) -> None:
        infra, _ = _build_batch_tc_script(
            "amnezia-awg", [], global_limit_down=100, global_limit_up=50
        )
        assert "100mbit" in infra
        assert "50mbit" in infra

    def test_infra_uses_default_rate_when_no_global_limit(self) -> None:
        infra, _ = _build_batch_tc_script("amnezia-awg", [], None, None)
        # Default is 10gbit
        assert "10gbit" in infra

    def test_client_script_contains_class_and_filter_commands(self) -> None:
        clients = [
            {
                "clientId": "abc",
                "clientIp": "10.8.1.2",
                "userData": {"speed_limit_down": 10, "speed_limit_up": 5},
            },
        ]
        _, client = _build_batch_tc_script("amnezia-awg", clients, None, None)
        # Class ID for 10.8.1.2 is 102 (2 + 100)
        assert "tc class add dev awg0" in client
        assert "classid 1:102" in client
        assert "rate 10mbit" in client
        assert "tc filter add dev awg0" in client
        assert "match ip dst 10.8.1.2" in client
        assert "tc class add dev ifb0" in client
        assert "match ip src 10.8.1.2" in client

    def test_client_script_skips_clients_without_limits(self) -> None:
        clients = [
            {"clientId": "abc", "clientIp": "10.8.1.2", "userData": {}},
        ]
        _, client = _build_batch_tc_script("amnezia-awg", clients, None, None)
        assert client == ""

    def test_client_script_handles_only_down_limit(self) -> None:
        clients = [
            {
                "clientId": "abc",
                "clientIp": "10.8.1.2",
                "userData": {"speed_limit_down": 10},
            },
        ]
        infra, client = _build_batch_tc_script("amnezia-awg", clients, None, None)
        # When only down is specified, up uses the down value (both = 10mbit)
        assert "rate 10mbit" in client  # per-client rate in client script
        # Infra uses default 10gbit since no global limits passed
        assert "10gbit" in infra

    def test_client_script_handles_only_up_limit(self) -> None:
        clients = [
            {
                "clientId": "abc",
                "clientIp": "10.8.1.2",
                "userData": {"speed_limit_up": 5},
            },
        ]
        infra, client = _build_batch_tc_script("amnezia-awg", clients, None, None)
        # When only up is specified, down uses the up value
        assert "rate 5mbit" in client  # awg0 download uses up value

    def test_client_script_uses_down_rate_for_awg0_even_if_only_up_set(self) -> None:
        # This tests the cross-fill: if only speed_limit_up is set,
        # effective_down = up, effective_up = up
        clients = [
            {
                "clientId": "abc",
                "clientIp": "10.8.1.2",
                "userData": {"speed_limit_up": 5},
            },
        ]
        _, client = _build_batch_tc_script("amnezia-awg", clients, None, None)
        # effective_down = up = 5, effective_up = up = 5
        # awg0 gets 5mbit, ifb0 gets 5mbit
        assert "rate 5mbit" in client


# -------------------------------------------------------------------------- #
# TestReapplyAllLimits
# -------------------------------------------------------------------------- #


class TestReapplyAllLimits:
    """Test reapply_all_limits: batch tc commands in 2 SSH calls."""

    def test_reapply_with_limits(self) -> None:
        """Two clients with limits → 2 SSH calls, applied=2, no errors."""
        ssh = MagicMock()
        ssh.run_sudo_command.return_value = ("", "", 0)

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
        result = reapply_all_limits(ssh, "amnezia-awg", "awg0", clients)
        assert result["status"] == "ok"
        assert result["applied"] == 2
        assert result["errors"] == []
        assert ssh.run_sudo_command.call_count == 2

    def test_reapply_no_limits(self) -> None:
        """Client with no limits → applied=0, infra script only (1 SSH call)."""
        ssh = MagicMock()
        ssh.run_sudo_command.return_value = ("", "", 0)

        clients = [{"clientId": "abc", "clientIp": "10.8.1.2", "userData": {}}]
        result = reapply_all_limits(ssh, "amnezia-awg", "awg0", clients)
        assert result["applied"] == 0
        # infra script called, client script is empty so not called
        assert ssh.run_sudo_command.call_count == 1

    def test_reapply_empty_list(self) -> None:
        """Empty client list → applied=0, infra script only."""
        ssh = MagicMock()
        ssh.run_sudo_command.return_value = ("", "", 0)

        result = reapply_all_limits(ssh, "amnezia-awg", "awg0", [])
        assert result["applied"] == 0
        assert ssh.run_sudo_command.call_count == 1

    def test_reapply_client_script_failure(self) -> None:
        """Client script returns non-zero → status=partial, error recorded."""
        ssh = MagicMock()
        # First call (infra) succeeds, second call (client) fails
        ssh.run_sudo_command.side_effect = [
            ("", "", 0),  # infra script
            ("", "RTNETLINK: No such file or directory", 1),  # client script
        ]

        clients = [
            {
                "clientId": "abc",
                "clientIp": "10.8.1.2",
                "userData": {"speed_limit_down": 10, "speed_limit_up": 5},
            },
        ]
        result = reapply_all_limits(ssh, "amnezia-awg", "awg0", clients)
        assert result["status"] == "partial"
        assert result["applied"] == 1
        assert len(result["errors"]) == 1
        assert "batch script error" in result["errors"][0]

    def test_reapply_with_global_limits(self) -> None:
        """Global limits are passed into the batch infra script."""
        ssh = MagicMock()
        ssh.run_sudo_command.return_value = ("", "", 0)

        clients = [
            {
                "clientId": "abc",
                "clientIp": "10.8.1.2",
                "userData": {"speed_limit_down": 10, "speed_limit_up": 5},
            },
        ]
        result = reapply_all_limits(
            ssh,
            "amnezia-awg",
            "awg0",
            clients,
            global_limit_down=100,
            global_limit_up=50,
        )
        assert result["status"] == "ok"
        assert result["applied"] == 1
        # Verify the infra script contained global limits
        infra_call = ssh.run_sudo_command.call_args_list[0]
        infra_script = infra_call[0][0]
        assert "100mbit" in infra_script
        assert "50mbit" in infra_script


# -------------------------------------------------------------------------- #
# TestTeardownQdisc
# -------------------------------------------------------------------------- #


class TestTeardownQdisc:
    """Test teardown_qdisc: removes qdisc from specified interface and ifb0."""

    def test_teardown_awg0_removes_both(self) -> None:
        """When tearing down awg0, both awg0 and ifb0 qdiscs are removed."""
        ssh = MagicMock()
        responses = [
            ("", "", 0),  # qdisc del dev awg0 root
            ("", "", 0),  # qdisc del dev ifb0 root
        ]
        ssh.run_sudo_command.side_effect = responses
        result = teardown_qdisc(ssh, "amnezia-awg", "awg0")
        assert result["status"] == "ok"
        assert "awg0" in result["message"]
        assert IFB_DEVICE in result["message"]

    def test_teardown_ifb0_removes_ifb_only(self) -> None:
        """When tearing down ifb0, only ifb0 qdisc is removed."""
        ssh = MagicMock()
        responses = [
            ("", "", 0),  # qdisc del dev ifb0 root
        ]
        ssh.run_sudo_command.side_effect = responses
        result = teardown_qdisc(ssh, "amnezia-awg", IFB_DEVICE)
        assert result["status"] == "ok"
        assert "ifb0" in result["message"]
        assert "awg0" not in result["message"]

    def test_teardown_awg0_nonexistent_ifb0_exists(self) -> None:
        """awg0 has no qdisc but ifb0 does."""
        ssh = MagicMock()
        responses = [
            ("", "Cannot find device", 1),  # qdisc del awg0 (not found)
            ("", "", 0),  # qdisc del ifb0 (exists)
        ]
        ssh.run_sudo_command.side_effect = responses
        result = teardown_qdisc(ssh, "amnezia-awg", "awg0")
        assert result["status"] == "ok"
        assert "no qdisc to remove" in result["message"]
        assert "ifb0 qdisc removed" in result["message"]

    def test_teardown_awg0_both_nonexistent(self) -> None:
        """Both awg0 and ifb0 have no qdisc to remove."""
        ssh = MagicMock()
        responses = [
            ("", "Cannot find device", 1),  # qdisc del awg0
            ("", "Cannot find device", 1),  # qdisc del ifb0
        ]
        ssh.run_sudo_command.side_effect = responses
        result = teardown_qdisc(ssh, "amnezia-awg", "awg0")
        assert result["status"] == "ok"
        assert "no qdisc to remove" in result["message"]
        assert "ifb0: no qdisc to remove" in result["message"]
