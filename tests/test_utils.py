from __future__ import annotations

from utils import format_bytes


class TestFormatBytes:
    """Tests for the shared format_bytes utility."""

    def test_none_returns_zero_bytes(self) -> None:
        assert format_bytes(None) == "0 B"

    def test_zero_returns_zero_bytes(self) -> None:
        assert format_bytes(0) == "0 B"

    def test_bytes_no_decimals(self) -> None:
        assert format_bytes(500) == "500 B"

    def test_exact_kb(self) -> None:
        assert format_bytes(1024) == "1.00 KB"

    def test_exact_mb(self) -> None:
        assert format_bytes(1048576) == "1.00 MB"

    def test_exact_gb(self) -> None:
        assert format_bytes(1073741824) == "1.00 GB"

    def test_exact_tb(self) -> None:
        assert format_bytes(1099511627776) == "1.00 TB"

    def test_exact_pb(self) -> None:
        assert format_bytes(1125899906842624) == "1.00 PB"

    def test_large_pb(self) -> None:
        result = format_bytes(1500000000000000)
        assert "PB" in result

    def test_negative_starts_with_dash(self) -> None:
        result = format_bytes(-1)
        assert result.startswith("-")

    def test_negative_kb(self) -> None:
        result = format_bytes(-2048)
        assert result.startswith("-")
        assert "KB" in result

    def test_fractional_value(self) -> None:
        assert format_bytes(1536) == "1.50 KB"

    def test_one_byte(self) -> None:
        assert format_bytes(1) == "1 B"

    def test_float_input(self) -> None:
        assert format_bytes(1024.0) == "1.00 KB"

    def test_exabyte_fallback(self) -> None:
        result = format_bytes(1152921504606846976)
        assert "EB" in result

    def test_negative_zero(self) -> None:
        # -0.0 should still show "0 B"
        assert format_bytes(-0) == "0 B"
