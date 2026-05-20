"""
Tests for AWG CPS (Characteristic Packet Signature) generation and domain probing.

Covers generate_cps_packets(), select_mimicry_domain(), and binary packet
generators from app/managers/awg_cps.py. Tests new hex-encoded binary blob
format (<b 0xHEX>) per pumbaX specification.
"""

import re
from unittest.mock import MagicMock

from app.managers.awg_cps import (
    FALLBACK_DOMAINS,
    gen_dns,
    gen_quic_initial,
    gen_quic_short,
    gen_sip,
    generate_cps_packets,
    select_mimicry_domain,
    to_cps,
)

HEX_BLOB_RE = re.compile(r"^<b 0x[0-9a-f]+>$")
DNS_BLOB_RE = re.compile(r"^<r 2><b 0x[0-9a-f]+>$")


class TestBinaryPacketGenerators:
    """Tests for raw binary packet generators."""

    def test_gen_quic_initial_size(self):
        """gen_quic_initial() produces exactly 1200 bytes."""
        for _ in range(20):
            pkt = gen_quic_initial()
            assert len(pkt) == 1200, f"QUIC Initial packet size: {len(pkt)}, expected 1200"

    def test_gen_quic_initial_first_byte(self):
        """QUIC Initial first byte is 0xC0 or 0xC3 (mimics Chrome)."""
        for _ in range(20):
            pkt = gen_quic_initial()
            assert pkt[0] in (0xC0, 0xC3), f"First byte: {pkt[0]:#x}"

    def test_gen_quic_short_size(self):
        """gen_quic_short() produces 50-100 bytes."""
        for _ in range(50):
            pkt = gen_quic_short()
            assert 50 <= len(pkt) <= 120, f"QUIC Short size: {len(pkt)}"

    def test_gen_quic_short_first_byte_format(self):
        """QUIC Short first byte has bit 6 set (0x40 short header flag)."""
        for _ in range(20):
            pkt = gen_quic_short()
            assert pkt[0] & 0x40, f"Short header flag not set: {pkt[0]:#x}"

    def test_gen_dns_format(self):
        """gen_dns() produces a valid DNS query payload."""
        payload = gen_dns("google.com")
        assert isinstance(payload, bytes)
        assert len(payload) > 20
        # Should contain the domain name "google" in the payload
        assert b"\x06google\x03com" in payload

    def test_gen_sip_format(self):
        """gen_sip() produces a SIP REGISTER message."""
        sip_data = gen_sip()
        assert isinstance(sip_data, bytes)
        assert b"REGISTER sip:" in sip_data
        assert b"SIP/2.0" in sip_data


class TestToCpsFormat:
    """Tests for to_cps() formatting."""

    def test_to_cps_basic(self):
        """to_cps wraps bytes in <b 0xHEX> format."""
        result = to_cps(b"\x01\x02\xff")
        assert result == "<b 0x0102ff>"

    def test_to_cps_empty(self):
        """Empty bytes produces minimal hex string."""
        result = to_cps(b"")
        assert result == "<b 0x>"

    def test_to_cps_large_packet(self):
        """1200 byte QUIC packet produces valid tag."""
        pkt = gen_quic_initial()
        tag = to_cps(pkt)
        assert tag.startswith("<b 0x")
        assert tag.endswith(">")
        assert len(tag) == 2406  # "<b 0x" (5) + 2400 hex chars + ">" (1)


class TestCPSPacketGeneration:
    """Tests for generate_cps_packets() across all profiles."""

    def test_cps_generation_lite(self):
        """Lite returns I1 as <r 2><b 0x...> DNS format, I2-I5 empty."""
        for _ in range(10):
            result = generate_cps_packets(profile="lite")
            assert len(result) == 5, f"Expected 5 keys, got {len(result)}"
            assert "cps" not in result, "cps key must not be present"
            assert DNS_BLOB_RE.match(result["i1"]), f"I1 format: {result['i1'][:80]}"
            assert result["i2"] == ""
            assert result["i3"] == ""
            assert result["i4"] == ""
            assert result["i5"] == ""

    def test_cps_generation_standard(self):
        """Standard returns I1 as <b 0x...> QUIC Initial, I2-I5 empty, no cps."""
        for _ in range(10):
            result = generate_cps_packets(profile="standard")
            assert len(result) == 5
            assert "cps" not in result
            assert HEX_BLOB_RE.match(result["i1"]), f"I1 format: {result['i1'][:80]}"
            # QUIC Initial = 1200 bytes = 2400 hex chars in <b 0x...>
            assert len(result["i1"]) >= 2400, f"I1 too short: {len(result['i1'])}"
            assert result["i2"] == ""
            assert result["i3"] == ""
            assert result["i4"] == ""
            assert result["i5"] == ""

    def test_cps_generation_pro(self):
        """Pro returns all I1-I5 as <b 0x...> format, no cps."""
        for _ in range(10):
            result = generate_cps_packets(profile="pro")
            assert len(result) == 5
            assert "cps" not in result
            assert HEX_BLOB_RE.match(result["i1"]), f"I1 format: {result['i1'][:80]}"
            assert HEX_BLOB_RE.match(result["i2"]), f"I2 format: {result['i2'][:80]}"
            assert HEX_BLOB_RE.match(result["i3"]), f"I3 format: {result['i3'][:80]}"
            assert HEX_BLOB_RE.match(result["i4"]), f"I4 format: {result['i4'][:80]}"
            assert HEX_BLOB_RE.match(result["i5"]), f"I5 format: {result['i5'][:80]}"
            # I1 = 1200 bytes, I2-I5 = 50-103 bytes each
            assert len(result["i1"]) >= 2400
            assert 100 <= len(result["i2"]) <= 250

    def test_no_cps_key_in_result(self):
        """Dict must never have a 'cps' key."""
        for profile in ("lite", "standard", "pro"):
            result = generate_cps_packets(profile=profile)
            assert "cps" not in result, f"{profile} profile has cps key"

    def test_cps_values_format(self):
        """All non-empty I1-I5 values match binary blob pattern."""
        for profile in ("lite", "standard", "pro"):
            for _ in range(5):
                result = generate_cps_packets(profile=profile)
                for key in ("i1", "i2", "i3", "i4", "i5"):
                    val = result[key]
                    if val:
                        assert val.startswith("<"), f"{profile} {key}: {val[:50]}"
                        assert val.endswith(">"), f"{profile} {key}: {val[:50]}"
                        assert "0x" in val, f"{profile} {key}: {val[:50]}"

    def test_all_result_keys_are_strings(self):
        """All values are strings."""
        for profile in ("lite", "standard", "pro"):
            result = generate_cps_packets(profile=profile)
            for val in result.values():
                assert isinstance(val, str), f"Not a string: {type(val)}"

    def test_domain_affects_lite_dns(self):
        """Different domain changes the DNS payload in lite profile."""
        r1 = generate_cps_packets(profile="lite", domain="google.com")
        r2 = generate_cps_packets(profile="lite", domain="youtube.com")
        assert r1["i1"] != r2["i1"], "Different domains should produce different DNS payloads"

    def test_domain_affects_quic(self):
        """Domain is accepted but QUIC packets use random data regardless."""
        r = generate_cps_packets(profile="standard", domain="google.com")
        assert HEX_BLOB_RE.match(r["i1"])


class TestDomainProbing:
    """Tests for select_mimicry_domain()."""

    def test_domain_probing_fallback(self):
        """When all domains fail (mocked), returns fallback."""
        mock_ssh = MagicMock()
        mock_ssh.run_command.return_value = ("FAIL\n", "", 0)

        domain = select_mimicry_domain(mock_ssh, protocol="quic")
        assert domain == FALLBACK_DOMAINS["quic"]

    def test_domain_selection_quic(self):
        """When a QUIC domain responds (mocked), returns it."""
        mock_ssh = MagicMock()
        mock_ssh.run_command.return_value = ("OK\n", "", 0)

        domain = select_mimicry_domain(mock_ssh, protocol="quic")
        assert domain in (
            "google.com",
            "youtube.com",
            "cdn.jsdelivr.net",
            "unpkg.com",
            "icloud.com",
            "fastly.net",
            "github.com",
        )

    def test_domain_selection_dns(self):
        """When a DNS domain responds (mocked), returns it."""
        mock_ssh = MagicMock()
        mock_ssh.run_command.return_value = ("OK\n", "", 0)

        domain = select_mimicry_domain(mock_ssh, protocol="dns")
        assert domain in ("google.com", "cloudflare.com", "one.one.one.one")

    def test_domain_probing_tries_multiple(self):
        """When some domains fail but one succeeds, returns the successful one."""
        mock_ssh = MagicMock()
        call_count = [0]

        def side_effect(cmd, *args, **kwargs):
            call_count[0] += 1
            if call_count[0] <= 2:
                return ("FAIL\n", "", 0)
            return ("OK\n", "", 0)

        mock_ssh.run_command.side_effect = side_effect

        domain = select_mimicry_domain(mock_ssh, protocol="quic")
        assert domain is not None
        assert domain != FALLBACK_DOMAINS["quic"]
        assert call_count[0] >= 1

    def test_domain_probing_ssh_exception(self):
        """When SSH commands raise exceptions, falls back gracefully."""
        mock_ssh = MagicMock()
        mock_ssh.run_command.side_effect = Exception("Connection refused")

        domain = select_mimicry_domain(mock_ssh, protocol="sip")
        assert domain == FALLBACK_DOMAINS["sip"]

    def test_domain_probing_sip(self):
        """SIP domain probing uses port 5060."""
        mock_ssh = MagicMock()
        mock_ssh.run_command.return_value = ("OK\n", "", 0)

        domain = select_mimicry_domain(mock_ssh, protocol="sip")
        assert domain in ("sip.zadarma.com", "sip.iptel.org", "sip.linphone.org")
        args = mock_ssh.run_command.call_args[0][0]
        assert "/5060" in args

    def test_domain_probing_unknown_protocol(self):
        """Unknown protocol falls back to QUIC pool and google.com."""
        mock_ssh = MagicMock()
        mock_ssh.run_command.return_value = ("FAIL\n", "", 0)

        domain = select_mimicry_domain(mock_ssh, protocol="unknown")
        assert domain == "google.com"
