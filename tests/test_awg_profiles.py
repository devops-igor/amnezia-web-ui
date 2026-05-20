"""
Tests for AWG obfuscation profile support (Issue #207 Batch 1).

Tests profile-aware parameter generation via generate_awg_params(),
quadrant header generation, profile integration with install_protocol(),
and API-level integration with POST /api/servers/{id}/install.
"""

import os
import tempfile
from unittest.mock import MagicMock, patch

from app.managers.awg_manager import (
    AWG_PROFILES,
    AWGManager,
    _generate_quadrant_headers,
    generate_awg_params,
)
from app.utils.helpers import hash_password
from database import Database
from dependencies import get_current_user
from tests.conftest import create_csrf_client

TEST_SECRET_KEY = "test-awg-profiles-...tes!"


class TestAWGProfileParams:
    """Tests for profile-aware parameter generation."""

    def _verify_ranges(self, params, profile_name):
        """Verify all params are within the expected profile ranges."""
        ranges = AWG_PROFILES[profile_name]
        # Jc
        jc = int(params["junk_packet_count"])
        assert ranges["junk_packet_count"][0] <= jc <= ranges["junk_packet_count"][1]
        # Jmin
        jmin = int(params["junk_packet_min_size"])
        assert ranges["junk_packet_min_size"][0] <= jmin <= ranges["junk_packet_min_size"][1]
        # Jmax — must be > jmin, and within expected max bound
        jmax = int(params["junk_packet_max_size"])
        jmax_max = ranges["junk_packet_max_size"][1]
        assert jmax > jmin
        assert jmax - jmin >= 10
        assert jmax <= jmin + jmax_max
        # S1, S2, S3, S4
        s1 = int(params["init_packet_junk_size"])
        s2 = int(params["response_packet_junk_size"])
        s3 = int(params["cookie_reply_packet_junk_size"])
        s4 = int(params["transport_packet_junk_size"])
        assert ranges["init_packet_junk_size"][0] <= s1 <= ranges["init_packet_junk_size"][1]
        assert (
            ranges["response_packet_junk_size"][0] <= s2 <= ranges["response_packet_junk_size"][1]
        )
        assert abs(s1 - s2) >= 10
        assert (
            ranges["cookie_reply_packet_junk_size"][0]
            <= s3
            <= ranges["cookie_reply_packet_junk_size"][1]
        )
        assert (
            ranges["transport_packet_junk_size"][0] <= s4 <= ranges["transport_packet_junk_size"][1]
        )

    def test_profile_lite_ranges(self):
        """Generate 100 params with profile='lite', verify every param is within Lite ranges."""
        for _ in range(100):
            params = generate_awg_params(profile="lite")
            self._verify_ranges(params, "lite")

    def test_profile_standard_ranges(self):
        """Generate 100 params with profile='standard', verify every param is within Standard ranges."""
        for _ in range(100):
            params = generate_awg_params(profile="standard")
            self._verify_ranges(params, "standard")

    def test_profile_pro_ranges(self):
        """Generate 100 params with profile='pro', verify every param is within Pro ranges."""
        for _ in range(100):
            params = generate_awg_params(profile="pro")
            self._verify_ranges(params, "pro")

    def test_jmax_always_greater_than_jmin(self):
        """For all profiles (100 iterations each), jmax > jmin with gap >= 10."""
        for profile in ("lite", "standard", "pro"):
            for _ in range(100):
                params = generate_awg_params(profile=profile)
                jmin = int(params["junk_packet_min_size"])
                jmax = int(params["junk_packet_max_size"])
                assert jmax > jmin, f"{profile}: jmax={jmax} <= jmin={jmin}"
                assert jmax - jmin >= 10, f"{profile}: gap={jmax-jmin} < 10"

    def test_s1_s2_gap_requirement(self):
        """For all profiles, |S1-S2| >= 10."""
        for profile in ("lite", "standard", "pro"):
            for _ in range(100):
                params = generate_awg_params(profile=profile)
                s1 = int(params["init_packet_junk_size"])
                s2 = int(params["response_packet_junk_size"])
                assert abs(s1 - s2) >= 10, f"{profile}: |S1-S2| = {abs(s1-s2)}"

    def test_headers_are_unique(self):
        """H1-H4 are 4 unique values."""
        for profile in ("lite", "standard", "pro"):
            for _ in range(50):
                params = generate_awg_params(profile=profile)
                headers = {
                    params["init_packet_magic_header"],
                    params["response_packet_magic_header"],
                    params["underload_packet_magic_header"],
                    params["transport_packet_magic_header"],
                }
                assert len(headers) == 4, f"{profile}: headers not unique"

    def test_headers_quadrant_distribution(self):
        """Each header falls in its respective quadrant."""
        max_val = 2147483647  # 2^31 - 1
        q_size = max_val // 4

        for profile in ("lite", "standard", "pro"):
            for _ in range(50):
                params = generate_awg_params(profile=profile)
                h1 = int(params["init_packet_magic_header"])
                h2 = int(params["response_packet_magic_header"])
                h3 = int(params["underload_packet_magic_header"])
                h4 = int(params["transport_packet_magic_header"])

                # H1: quadrant 0 [5, q_size + 1]
                assert 5 <= h1 <= q_size + 1, f"{profile} H1={h1} out of quadrant 0"
                # H2: quadrant 1 [q_size + 5, 2*q_size + 1]
                assert q_size + 5 <= h2 <= 2 * q_size + 1, f"{profile} H2={h2} out of quadrant 1"
                # H3: quadrant 2 [2*q_size + 5, 3*q_size + 1]
                assert (
                    2 * q_size + 5 <= h3 <= 3 * q_size + 1
                ), f"{profile} H3={h3} out of quadrant 2"
                # H4: quadrant 3 [3*q_size + 5, max_val]
                assert 3 * q_size + 5 <= h4 <= max_val, f"{profile} H4={h4} out of quadrant 3"

    def test_all_params_are_strings(self):
        """Every value in the returned dict is a string."""
        for profile in ("lite", "standard", "pro"):
            params = generate_awg_params(profile=profile)
            assert isinstance(params, dict), f"{profile}: result not a dict"
            for key, val in params.items():
                assert isinstance(
                    val, str
                ), f"{profile}: {key} is {type(val).__name__}, expected str"

    # --- Backward compatibility tests ---

    def test_default_no_profile_backward_compatible(self):
        """generate_awg_params() with no profile produces same ranges as before."""
        # Test without profile: no crash, valid dict, same keys as before
        params = generate_awg_params()
        assert isinstance(params, dict)
        expected_keys = {
            "junk_packet_count",
            "junk_packet_min_size",
            "junk_packet_max_size",
            "init_packet_junk_size",
            "response_packet_junk_size",
            "cookie_reply_packet_junk_size",
            "transport_packet_junk_size",
            "init_packet_magic_header",
            "response_packet_magic_header",
            "underload_packet_magic_header",
            "transport_packet_magic_header",
            "mtu",
            "i1",
            "i2",
            "i3",
            "i4",
            "i5",
        }
        assert set(params.keys()) == expected_keys
        # All values are strings
        for v in params.values():
            assert isinstance(v, str)

        # jc: 1..10, jmin: 5..20, jmax: jmin+10..jmin+50
        jc = int(params["junk_packet_count"])
        jmin = int(params["junk_packet_min_size"])
        jmax = int(params["junk_packet_max_size"])
        assert 1 <= jc <= 10
        assert 5 <= jmin <= 20
        assert jmin + 10 <= jmax <= jmin + 50
        # s1..s4: 10..50
        for key in (
            "init_packet_junk_size",
            "response_packet_junk_size",
            "cookie_reply_packet_junk_size",
            "transport_packet_junk_size",
        ):
            s = int(params[key])
            assert 10 <= s <= 50, f"{key}={s} out of [10,50]"
        # headers: 100000000..4294967295 (legacy)
        for key in (
            "init_packet_magic_header",
            "response_packet_magic_header",
            "underload_packet_magic_header",
            "transport_packet_magic_header",
        ):
            h = int(params[key])
            assert 100000000 <= h <= 4294967295, f"{key}={h} out of legacy range"

    def test_use_ranges_still_works(self):
        """generate_awg_params(use_ranges=True) still works."""
        params = generate_awg_params(use_ranges=True)
        assert isinstance(params, dict)
        # Headers should be in AWG2 range: 1_000_000_000..4_294_967_295
        for key in (
            "init_packet_magic_header",
            "response_packet_magic_header",
            "underload_packet_magic_header",
            "transport_packet_magic_header",
        ):
            h = int(params[key])
            assert 1000000000 <= h <= 4294967295, f"{key}={h} out of AWG2 range"

    def test_invalid_profile_falls_back(self):
        """generate_awg_params(use_ranges=True, profile='invalid') produces valid params."""
        params = generate_awg_params(use_ranges=True, profile="invalid")
        assert isinstance(params, dict)
        assert "junk_packet_count" in params
        # Should fall back to use_ranges=True behavior (no crash)
        for key in (
            "init_packet_magic_header",
            "response_packet_magic_header",
            "underload_packet_magic_header",
            "transport_packet_magic_header",
        ):
            h = int(params[key])
            assert 1000000000 <= h <= 4294967295


class TestQuadrantHeaders:
    """Tests for _generate_quadrant_headers() helper."""

    def test_returns_dict_with_four_keys(self):
        headers = _generate_quadrant_headers()
        assert isinstance(headers, dict)
        assert len(headers) == 4
        assert "init_packet_magic_header" in headers
        assert "response_packet_magic_header" in headers
        assert "underload_packet_magic_header" in headers
        assert "transport_packet_magic_header" in headers

    def test_all_values_are_numeric_strings(self):
        headers = _generate_quadrant_headers()
        for key, val in headers.items():
            assert isinstance(val, str), f"{key} is not a string"
            assert val.isdigit(), f"{key}='{val}' is not numeric"

    def test_headers_in_correct_quadrants(self):
        """Each header should be in its respective quadrant."""
        max_val = 2147483647
        q_size = max_val // 4
        for _ in range(50):
            headers = _generate_quadrant_headers()
            h1 = int(headers["init_packet_magic_header"])
            h2 = int(headers["response_packet_magic_header"])
            h3 = int(headers["underload_packet_magic_header"])
            h4 = int(headers["transport_packet_magic_header"])

            assert 5 <= h1 <= q_size + 1
            assert q_size + 5 <= h2 <= 2 * q_size + 1
            assert 2 * q_size + 5 <= h3 <= 3 * q_size + 1
            assert 3 * q_size + 5 <= h4 <= max_val

    def test_headers_unique(self):
        """All four headers should be unique."""
        for _ in range(50):
            headers = _generate_quadrant_headers()
            vals = set(headers.values())
            assert len(vals) == 4


class TestAWGManagerInstallProtocolProfile:
    """Integration tests for install_protocol with awg_profile parameter."""

    def setup_method(self):
        self.mock_ssh = MagicMock()
        self.manager = AWGManager(self.mock_ssh)

    def test_install_protocol_accepts_profile(self):
        """install_protocol with awg_profile='standard' passes profile to generate_awg_params."""
        self.mock_ssh.run_sudo_command.return_value = ("", "", 0)
        self.mock_ssh.run_command.return_value = ("", "", 0)
        self.mock_ssh.upload_file.return_value = None

        with patch("app.managers.awg_manager.check_docker_installed", return_value=True):
            with patch.object(self.manager, "check_protocol_installed", return_value=False):
                with patch.object(self.manager, "prepare_host"):
                    with patch.object(self.manager, "_wait_container_running"):
                        with patch.object(self.manager, "_configure_container"):
                            with patch.object(self.manager, "_upload_start_script"):
                                with patch.object(self.manager, "setup_firewall"):
                                    with patch("app.managers.awg_manager.ensure_apparmor_utils"):
                                        with patch(
                                            "app.managers.awg_manager.generate_awg_params"
                                        ) as mock_gen:
                                            mock_gen.return_value = {
                                                "junk_packet_count": "5",
                                                "junk_packet_min_size": "40",
                                                "junk_packet_max_size": "150",
                                                "init_packet_junk_size": "55",
                                                "response_packet_junk_size": "45",
                                                "cookie_reply_packet_junk_size": "24",
                                                "transport_packet_junk_size": "15",
                                                "init_packet_magic_header": "100000001",
                                                "response_packet_magic_header": "600000001",
                                                "underload_packet_magic_header": "1200000001",
                                                "transport_packet_magic_header": "1800000001",
                                            }
                                            result = self.manager.install_protocol(
                                                protocol_type="awg",
                                                port="55424",
                                                awg_profile="standard",
                                            )

                                            # Verify generate_awg_params was called with profile
                                            mock_gen.assert_called_once_with(
                                                use_ranges=True,
                                                profile="standard",
                                            )
                                            assert result["status"] == "success"

    def test_install_protocol_default_no_profile(self):
        """install_protocol without awg_profile works as before."""
        self.mock_ssh.run_sudo_command.return_value = ("", "", 0)
        self.mock_ssh.run_command.return_value = ("", "", 0)
        self.mock_ssh.upload_file.return_value = None

        with patch("app.managers.awg_manager.check_docker_installed", return_value=True):
            with patch.object(self.manager, "check_protocol_installed", return_value=False):
                with patch.object(self.manager, "prepare_host"):
                    with patch.object(self.manager, "_wait_container_running"):
                        with patch.object(self.manager, "_configure_container"):
                            with patch.object(self.manager, "_upload_start_script"):
                                with patch.object(self.manager, "setup_firewall"):
                                    with patch("app.managers.awg_manager.ensure_apparmor_utils"):
                                        with patch(
                                            "app.managers.awg_manager.generate_awg_params"
                                        ) as mock_gen:
                                            mock_gen.return_value = {
                                                "junk_packet_count": "3",
                                                "junk_packet_min_size": "10",
                                                "junk_packet_max_size": "30",
                                                "init_packet_junk_size": "20",
                                                "response_packet_junk_size": "25",
                                                "cookie_reply_packet_junk_size": "30",
                                                "transport_packet_junk_size": "35",
                                                "init_packet_magic_header": "100000001",
                                                "response_packet_magic_header": "200000001",
                                                "underload_packet_magic_header": "300000001",
                                                "transport_packet_magic_header": "400000001",
                                            }
                                            result = self.manager.install_protocol(
                                                protocol_type="awg",
                                                port="55424",
                                            )

                                            # No profile => None passed
                                            mock_gen.assert_called_once_with(
                                                use_ranges=True,
                                                profile=None,
                                            )
                                            assert result["status"] == "success"


class TestApiInstallProfile:
    """API-level tests for POST /api/servers/{id}/install with awg_profile."""

    def setup_method(self):
        self.tmp_db = tempfile.NamedTemporaryFile(suffix=".db", delete=False)
        self.tmp_db_path = self.tmp_db.name
        self.tmp_db.close()
        os.environ["SECRET_KEY"] = TEST_SECRET_KEY
        self.db = Database(self.tmp_db_path, secret_key=TEST_SECRET_KEY)

        self.db.create_user(
            {
                "id": "admin-1",
                "username": "admin",
                "password_hash": hash_password("AdminPass123"),
                "enabled": True,
                "traffic_limit": 0,
                "traffic_used": 0,
                "role": "admin",
                "limits": {},
            }
        )
        self.db.create_server(
            {
                "name": "Test Server",
                "host": "10.0.0.1",
                "username": "root",
                "password": "***",
                "ssh_port": 22,
                "protocols": {},
            }
        )

    def teardown_method(self):
        conn = self.db._get_conn()
        conn.close()
        os.unlink(self.tmp_db_path)

    @patch("app.routers.auth.get_db")
    @patch("app.routers.servers.get_db")
    def test_api_install_awg_with_profile(self, mock_servers_db, mock_auth_db):
        """POST /api/servers/{id}/install with protocol=awg and awg_profile=standard."""
        mock_auth_db.return_value = self.db
        mock_servers_db.return_value = self.db

        mock_ssh = MagicMock()
        mock_ssh.connect.return_value = None
        mock_ssh.run_sudo_command.return_value = ("", "", 0)
        mock_ssh.run_command.return_value = ("", "", 0)
        mock_ssh.upload_file.return_value = None

        import app

        client = create_csrf_client()

        # Login
        login_resp = client.post(
            "/api/auth/login",
            json={"username": "admin", "password": "AdminPass123"},
        )
        assert login_resp.status_code == 200
        for hv in login_resp.headers.get_list("set-cookie"):
            if hv.startswith("session="):
                client.cookies.set("session", hv.split("session=")[1].split(";")[0])
                break

        app.app.dependency_overrides[get_current_user] = lambda: self.db.get_user("admin-1")
        try:
            with patch("app.routers.servers.get_ssh", return_value=mock_ssh):
                with patch("app.routers.servers.get_protocol_manager") as mock_get_pm:
                    mock_manager = MagicMock()
                    mock_manager.install_protocol.return_value = {
                        "status": "success",
                        "protocol": "awg",
                        "port": "55424",
                        "awg_params": {"junk_packet_count": "5"},
                        "log": ["AWG installed"],
                    }
                    mock_get_pm.return_value = mock_manager

                    with patch(
                        "app.managers.awg_manager.check_docker_installed", return_value=True
                    ):
                        server_id = self.db.get_all_servers()[0]["id"]
                        resp = client.post(
                            f"/api/servers/{server_id}/install",
                            json={"protocol": "awg", "port": "55424", "awg_profile": "standard"},
                        )
                        assert resp.status_code == 200
                        data = resp.json()
                        assert data["status"] == "success"

                        # Verify install_protocol was called with awg_profile
                        mock_manager.install_protocol.assert_called_once_with(
                            protocol_type="awg",
                            port="55424",
                            awg_profile="standard",
                            awg_cps_protocol=None,
                        )
        finally:
            app.app.dependency_overrides.clear()

    @patch("app.routers.auth.get_db")
    @patch("app.routers.servers.get_db")
    def test_api_install_awg_without_profile(self, mock_servers_db, mock_auth_db):
        """POST /api/servers/{id}/install with protocol=awg without awg_profile — backward compat."""
        mock_auth_db.return_value = self.db
        mock_servers_db.return_value = self.db

        mock_ssh = MagicMock()
        mock_ssh.connect.return_value = None
        mock_ssh.run_sudo_command.return_value = ("", "", 0)
        mock_ssh.run_command.return_value = ("", "", 0)
        mock_ssh.upload_file.return_value = None

        import app

        client = create_csrf_client()

        # Login
        login_resp = client.post(
            "/api/auth/login",
            json={"username": "admin", "password": "AdminPass123"},
        )
        assert login_resp.status_code == 200
        for hv in login_resp.headers.get_list("set-cookie"):
            if hv.startswith("session="):
                client.cookies.set("session", hv.split("session=")[1].split(";")[0])
                break

        app.app.dependency_overrides[get_current_user] = lambda: self.db.get_user("admin-1")
        try:
            with patch("app.routers.servers.get_ssh", return_value=mock_ssh):
                with patch("app.routers.servers.get_protocol_manager") as mock_get_pm:
                    mock_manager = MagicMock()
                    mock_manager.install_protocol.return_value = {
                        "status": "success",
                        "protocol": "awg",
                        "port": "55424",
                        "awg_params": {"junk_packet_count": "3"},
                        "log": ["AWG installed"],
                    }
                    mock_get_pm.return_value = mock_manager

                    with patch(
                        "app.managers.awg_manager.check_docker_installed", return_value=True
                    ):
                        server_id = self.db.get_all_servers()[0]["id"]
                        resp = client.post(
                            f"/api/servers/{server_id}/install",
                            json={"protocol": "awg", "port": "55424"},
                        )
                        assert resp.status_code == 200
                        data = resp.json()
                        assert data["status"] == "success"

                        # Verify install_protocol was called WITHOUT awg_profile
                        mock_manager.install_protocol.assert_called_once_with(
                            protocol_type="awg",
                            port="55424",
                            awg_profile=None,
                            awg_cps_protocol=None,
                        )
        finally:
            app.app.dependency_overrides.clear()

    @patch("app.routers.auth.get_db")
    @patch("app.routers.servers.get_db")
    def test_api_install_awg_invalid_profile_422(self, mock_servers_db, mock_auth_db):
        """POST with invalid awg_profile returns 422 validation error."""
        mock_auth_db.return_value = self.db
        mock_servers_db.return_value = self.db

        import app

        client = create_csrf_client()

        login_resp = client.post(
            "/api/auth/login",
            json={"username": "admin", "password": "AdminPass123"},
        )
        assert login_resp.status_code == 200
        for hv in login_resp.headers.get_list("set-cookie"):
            if hv.startswith("session="):
                client.cookies.set("session", hv.split("session=")[1].split(";")[0])
                break

        app.app.dependency_overrides[get_current_user] = lambda: self.db.get_user("admin-1")
        try:
            server_id = self.db.get_all_servers()[0]["id"]
            resp = client.post(
                f"/api/servers/{server_id}/install",
                json={"protocol": "awg", "port": "55424", "awg_profile": "invalid_profile"},
            )
            assert resp.status_code == 422
        finally:
            app.app.dependency_overrides.clear()

    @patch("app.routers.auth.get_db")
    @patch("app.routers.servers.get_db")
    def test_api_install_xray_ignores_awg_profile(self, mock_servers_db, mock_auth_db):
        """POST with protocol=xray and awg_profile — profile is ignored for non-AWG protocols."""
        mock_auth_db.return_value = self.db
        mock_servers_db.return_value = self.db

        mock_ssh = MagicMock()
        mock_ssh.connect.return_value = None
        mock_ssh.run_sudo_command.return_value = ("", "", 0)
        mock_ssh.run_command.return_value = ("", "", 0)
        mock_ssh.upload_file.return_value = None

        import app

        client = create_csrf_client()

        login_resp = client.post(
            "/api/auth/login",
            json={"username": "admin", "password": "AdminPass123"},
        )
        assert login_resp.status_code == 200
        for hv in login_resp.headers.get_list("set-cookie"):
            if hv.startswith("session="):
                client.cookies.set("session", hv.split("session=")[1].split(";")[0])
                break

        app.app.dependency_overrides[get_current_user] = lambda: self.db.get_user("admin-1")
        try:
            with patch("app.routers.servers.get_ssh", return_value=mock_ssh):
                with patch("app.routers.servers.get_protocol_manager") as mock_get_pm:
                    mock_manager = MagicMock()
                    mock_manager.install_protocol.return_value = {
                        "status": "success",
                        "protocol": "xray",
                        "port": "443",
                        "awg_params": {},
                        "log": ["Xray installed"],
                    }
                    mock_get_pm.return_value = mock_manager

                    with patch(
                        "app.managers.awg_manager.check_docker_installed", return_value=True
                    ):
                        server_id = self.db.get_all_servers()[0]["id"]
                        resp = client.post(
                            f"/api/servers/{server_id}/install",
                            json={"protocol": "xray", "port": "443", "awg_profile": "standard"},
                        )
                        assert resp.status_code == 200
                        data = resp.json()
                        assert data["status"] == "success"

                        # Xray should NOT receive awg_profile
                        call_args = mock_manager.install_protocol.call_args
                        assert "awg_profile" not in call_args.kwargs
        finally:
            app.app.dependency_overrides.clear()


class TestGenerateAwgParamsCPS:
    """Tests for CPS integration in generate_awg_params()."""

    def test_generate_awg_params_has_cps_for_standard(self):
        """Standard profile includes I1 as <b 0x...> QUIC Initial, I2-I5 empty, no cps."""
        params = generate_awg_params(profile="standard")
        assert params["i1"].startswith("<b 0x"), f"I1 format: {params['i1'][:50]}"
        assert len(params["i1"]) >= 2400  # 1200 bytes hex
        assert params["i2"] == ""
        assert params["i3"] == ""
        assert params["i4"] == ""
        assert params["i5"] == ""
        assert "cps" not in params

    def test_generate_awg_params_has_cps_for_pro(self):
        """Pro profile includes all I1-I5 as <b 0x...> format, no cps."""
        params = generate_awg_params(profile="pro")
        for key in ("i1", "i2", "i3", "i4", "i5"):
            assert params[key].startswith("<b 0x"), f"{key} format: {params[key][:50]}"
        assert "cps" not in params

    def test_generate_awg_params_no_cps_for_lite(self):
        """Lite profile has empty I1-I5, no cps key."""
        params = generate_awg_params(profile="lite")
        assert params["i1"] == ""
        assert params["i2"] == ""
        assert params["i3"] == ""
        assert params["i4"] == ""
        assert params["i5"] == ""
        assert "cps" not in params

    def test_generate_awg_params_mtu_pro(self):
        """Pro profile sets MTU=1320."""
        params = generate_awg_params(profile="pro")
        assert params["mtu"] == "1320"

    def test_generate_awg_params_mtu_standard(self):
        """Standard profile sets MTU=1280."""
        params = generate_awg_params(profile="standard")
        assert params["mtu"] == "1280"

    def test_generate_awg_params_mtu_lite(self):
        """Lite profile sets MTU=1280."""
        params = generate_awg_params(profile="lite")
        assert params["mtu"] == "1280"

    def test_generate_awg_params_no_profile_has_empty_cps(self):
        """Without a profile, CPS values are empty (backward compat), no cps key."""
        params = generate_awg_params(use_ranges=True)
        assert params["i1"] == ""
        assert "cps" not in params
        assert params["mtu"] == "1280"
