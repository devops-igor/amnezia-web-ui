"""
Tests for AWG CPS (Characteristic Packet Signature) generation and domain probing.

Covers generate_cps_packets() and select_mimicry_domain() from app/managers/awg_cps.py.
"""

from unittest.mock import MagicMock

from app.managers.awg_cps import (
    FALLBACK_DOMAINS,
    generate_cps_packets,
    select_mimicry_domain,
)


class TestCPSPacketGeneration:
    """Tests for generate_cps_packets() across all profiles."""

    def test_cps_generation_lite(self):
        """Lite returns all empty strings."""
        result = generate_cps_packets(profile="lite")
        assert result == {"i1": "", "i2": "", "i3": "", "i4": "", "i5": "", "cps": ""}

    def test_cps_generation_standard(self):
        """Standard returns I1 with nonzero value, I2-I5 empty, cps='signature'."""
        for _ in range(20):
            result = generate_cps_packets(profile="standard")
            i1 = int(result["i1"])
            assert 50 <= i1 <= 90, f"I1={i1} out of range [50,90]"
            assert result["i2"] == ""
            assert result["i3"] == ""
            assert result["i4"] == ""
            assert result["i5"] == ""
            assert result["cps"] == "signature"

    def test_cps_generation_pro(self):
        """Pro returns all I1-I5 with nonzero values, cps='signature'."""
        for _ in range(20):
            result = generate_cps_packets(profile="pro")
            for key in ("i1", "i2", "i3", "i4", "i5"):
                val = int(result[key])
                assert 50 <= val <= 90, f"{key}={val} out of range [50,90]"
            assert result["cps"] == "signature"

    def test_cps_values_are_valid_integers(self):
        """All I1-I5 values are valid integer strings or empty."""
        for profile in ("lite", "standard", "pro"):
            for _ in range(10):
                result = generate_cps_packets(profile=profile)
                for key in ("i1", "i2", "i3", "i4", "i5"):
                    val = result[key]
                    if val:
                        int(val)  # Must not raise
                    else:
                        assert val == ""

    def test_cps_signature_value(self):
        """CPS is 'signature' for Standard/Pro, empty for Lite."""
        result_lite = generate_cps_packets(profile="lite")
        assert result_lite["cps"] == ""

        result_std = generate_cps_packets(profile="standard")
        assert result_std["cps"] == "signature"

        result_pro = generate_cps_packets(profile="pro")
        assert result_pro["cps"] == "signature"

    def test_domain_influences_i1(self):
        """When a domain is provided, I1 is deterministically derived from it."""
        # Same domain produces same I1
        i1_a = generate_cps_packets(profile="standard", domain="google.com")["i1"]
        i1_b = generate_cps_packets(profile="standard", domain="google.com")["i1"]
        assert i1_a == i1_b

        # Different domain may produce different I1 (statistically likely)
        i1_c = generate_cps_packets(profile="standard", domain="youtube.com")["i1"]
        # Both should be in valid range
        assert 50 <= int(i1_a) <= 90
        assert 50 <= int(i1_c) <= 90


class TestDomainProbing:
    """Tests for select_mimicry_domain()."""

    def test_domain_probing_fallback(self):
        """When all domains fail (mocked), returns fallback."""
        mock_ssh = MagicMock()
        # SSH command always returns FAIL
        mock_ssh.run_command.return_value = ("FAIL\n", "", 0)

        domain = select_mimicry_domain(mock_ssh, protocol="quic")
        assert domain == FALLBACK_DOMAINS["quic"]

    def test_domain_selection_quic(self):
        """When a QUIC domain responds (mocked), returns it."""
        mock_ssh = MagicMock()
        # First call returns OK for the first domain tried
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
        # Fail first two, succeed on third
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
        # Verify it probed on port 5060
        args = mock_ssh.run_command.call_args[0][0]
        assert "/5060" in args

    def test_domain_probing_unknown_protocol(self):
        """Unknown protocol falls back to QUIC pool and google.com."""
        mock_ssh = MagicMock()
        mock_ssh.run_command.return_value = ("FAIL\n", "", 0)

        domain = select_mimicry_domain(mock_ssh, protocol="unknown")
        assert domain == "google.com"
