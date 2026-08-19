"""Tests for AWG Mimicry Profiles, Connection Kit, and Profile Rotation (Phase 1)."""

from unittest.mock import MagicMock, patch
from app.managers.awg_cps import (
    gen_tls,
    gen_quic_initial,
    gen_dns,
    gen_sip,
    generate_mimicry_packets,
    generate_connection_kit,
)
from app.managers.awg_manager import AWGManager


class TestMimicryGenerators:
    """Test packet format and binary signatures for all mimicry profiles."""

    def test_gen_tls_format(self):
        pkt = gen_tls("www.cloudflare.com")
        assert isinstance(pkt, bytes)
        assert pkt.startswith(b"\x16\x03\x01")  # Handshake, TLS 1.0 record layer
        assert b"www.cloudflare.com" in pkt

    def test_gen_tls_random_domain(self):
        pkt = gen_tls()
        assert isinstance(pkt, bytes)
        assert pkt.startswith(b"\x16\x03\x01")

    def test_gen_quic_initial_format(self):
        pkt = gen_quic_initial("google.com")
        assert isinstance(pkt, bytes)
        # Total byte length of QUIC Initial should be 1200 bytes
        assert len(pkt) == 1200
        assert pkt[0] in (0xC0, 0xC3)

    def test_gen_dns_format(self):
        pkt = gen_dns("cloudflare.com")
        assert isinstance(pkt, bytes)
        assert pkt.startswith(b"\x01\x00\x00\x01")

    def test_gen_sip_format(self):
        pkt = gen_sip("sip.linphone.org")
        assert isinstance(pkt, bytes)
        assert b"REGISTER sip:sip.linphone.org SIP/2.0" in pkt

    def test_generate_mimicry_packets_profiles(self):
        for profile in ["auto", "tls", "quic", "dns", "sip"]:
            packets = generate_mimicry_packets(profile)
            assert "i1" in packets
            assert "i2" in packets
            assert "i3" in packets
            assert "i4" in packets
            assert "i5" in packets
            assert packets["i1"].startswith("<")
            assert packets["i1"].endswith(">")

    def test_generate_connection_kit(self):
        base_config = """[Interface]
Address = 10.8.1.2/32
PrivateKey = aaaaaa
MTU = 1280
I1 = <b 0x1234>

[Peer]
PublicKey = bbbbbb
Endpoint = 1.2.3.4:55424
"""
        kit = generate_connection_kit(base_config)
        assert set(kit.keys()) == {"tls", "quic", "dns", "sip"}
        for proto, conf in kit.items():
            assert "[Interface]" in conf
            assert "[Peer]" in conf
            assert "Endpoint = 1.2.3.4:55424" in conf
            assert "I1 =" in conf


class TestAWGManagerMimicry:
    """Test AWGManager integration with mimicry and connection kit."""

    def test_add_client_with_mimicry_profile(self):
        mock_ssh = MagicMock()
        manager = AWGManager(mock_ssh)
        manager._get_server_config = MagicMock(return_value="[Interface]\nPrivateKey = servkey\n")
        manager._get_server_public_key = MagicMock(return_value="servpubkey")
        manager._get_server_psk = MagicMock(return_value="servpsk")
        manager._get_next_ip = MagicMock(return_value="10.8.1.5")
        manager._get_awg_params_from_config = MagicMock(return_value={"port": "55424"})
        manager._get_clients_table = MagicMock(return_value=[])
        manager._save_clients_table = MagicMock()

        res = manager.add_client("awg", "test_user", "1.2.3.4", "55424", awg_mimicry="tls")
        assert res["awg_mimicry"] == "tls"
        assert "config" in res
        assert "I1 =" in res["config"]
        assert "connection_kit" in res
        assert set(res["connection_kit"].keys()) == {"tls", "quic", "dns", "sip"}

    def test_rotate_client_mimicry(self):
        mock_ssh = MagicMock()
        manager = AWGManager(mock_ssh)
        clients = [
            {
                "clientId": "client123",
                "userData": {
                    "clientName": "alice",
                    "awg_mimicry": "tls",
                },
            }
        ]
        manager._get_clients_table = MagicMock(return_value=clients)
        manager._save_clients_table = MagicMock()

        rot = manager.rotate_client_mimicry("awg", "client123")
        assert rot["client_id"] == "client123"
        assert rot["awg_mimicry"] == "quic"  # tls rotates to quic
        assert rot["dpi_blocked"] is True
        assert clients[0]["userData"]["awg_mimicry"] == "quic"

        # Next rotation: quic -> dns
        rot2 = manager.rotate_client_mimicry("awg", "client123")
        assert rot2["awg_mimicry"] == "dns"


class TestMimicryEndpoints:
    """Test API router endpoints for mimicry rotation and connection kits."""

    def test_rotate_mimicry_endpoint(self, csrf_client):
        from app.main import app
        from app.core.dependencies import get_current_user, require_admin

        admin_user = {"id": "admin-1", "username": "admin", "role": "admin", "enabled": True}
        mock_db = MagicMock()
        mock_db.get_server_by_id.return_value = {
            "id": 1,
            "name": "Test Server",
            "host": "1.2.3.4",
            "protocols": {"awg": {"port": 55424}},
        }
        mock_db.get_connections_by_server_and_protocol.return_value = [
            {"id": "conn1", "client_id": "client_abc", "awg_mimicry": "tls"}
        ]
        mock_ssh = MagicMock()

        app.dependency_overrides[get_current_user] = lambda: admin_user
        app.dependency_overrides[require_admin] = lambda: admin_user

        try:
            with (
                patch("app.routers.servers.get_db", return_value=mock_db),
                patch("app.routers.servers.get_ssh", return_value=mock_ssh),
                patch("app.routers.servers.get_protocol_manager") as mock_get_mgr,
            ):
                mock_mgr = MagicMock()
                mock_mgr.rotate_client_mimicry.return_value = {
                    "client_id": "client_abc",
                    "awg_mimicry": "quic",
                    "rotated_at": "2026-08-19T23:50:00",
                    "dpi_blocked": True,
                }
                mock_get_mgr.return_value = mock_mgr

                resp = csrf_client.post(
                    "/api/servers/1/connections/client_abc/rotate-mimicry",
                    json={"next_mimicry": "quic"},
                )
                assert resp.status_code == 200
                data = resp.json()
                assert data["status"] == "success"
                assert data["rotation"]["awg_mimicry"] == "quic"
        finally:
            app.dependency_overrides.clear()

    def test_network_health_endpoint(self, csrf_client):
        from app.main import app
        from app.core.dependencies import get_current_user

        admin_user = {"id": "admin-1", "username": "admin", "role": "admin", "enabled": True}
        app.dependency_overrides[get_current_user] = lambda: admin_user

        try:
            with patch(
                "app.services.background_orchestrator.BackgroundTaskOrchestrator.get_cached_network_health"
            ) as mock_health:
                mock_health.return_value = {
                    "tls": {"status": "operational", "latency_ms": 45},
                    "quic": {"status": "operational", "latency_ms": 32},
                }

                resp = csrf_client.get("/api/servers/1/network-health")
                assert resp.status_code == 200
                data = resp.json()
                assert data["status"] == "success"
                health = data.get("network_health") or data.get("health", {})
                assert "tls" in health
        finally:
            app.dependency_overrides.clear()

    def test_connection_kit_endpoint(self, csrf_client):
        from app.main import app
        from app.core.dependencies import get_current_user

        admin_user = {"id": "admin-1", "username": "admin", "role": "admin", "enabled": True}
        mock_db = MagicMock()
        mock_db.get_server_by_id.return_value = {
            "id": 1,
            "name": "Test Server",
            "host": "1.2.3.4",
            "protocols": {"awg": {"port": 55424}},
        }
        mock_ssh = MagicMock()

        app.dependency_overrides[get_current_user] = lambda: admin_user

        try:
            with (
                patch("app.routers.servers.get_db", return_value=mock_db),
                patch("app.routers.servers.get_ssh", return_value=mock_ssh),
                patch("app.routers.servers.get_protocol_manager") as mock_get_mgr,
            ):
                mock_mgr = MagicMock()
                mock_mgr.get_connection_kit.return_value = {
                    "tls": "[Interface]\nI1 = <b 0x1111>",
                    "quic": "[Interface]\nI1 = <b 0x2222>",
                    "dns": "[Interface]\nI1 = <b 0x3333>",
                    "sip": "[Interface]\nI1 = <b 0x4444>",
                }
                mock_get_mgr.return_value = mock_mgr

                resp = csrf_client.post(
                    "/api/servers/1/connections/kit",
                    json={"protocol": "awg", "client_id": "client_abc"},
                )
                assert resp.status_code == 200
                data = resp.json()
                assert data["status"] == "success"
                assert set(data["kit"].keys()) == {"tls", "quic", "dns", "sip"}
        finally:
            app.dependency_overrides.clear()
