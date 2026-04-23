from __future__ import annotations

from typing import Optional


def format_bytes(n: Optional[int | float]) -> str:
    """Format byte count as human-readable string (e.g. '1.50 GB').

    Uses SI-style units (B, KB, MB, GB, TB, PB). Handles None and
    negative values gracefully.

    Args:
        n: Byte count. None is treated as 0.

    Returns:
        Human-readable string with 2 decimal places for KB+, no decimals for bytes.
    """
    if n is None:
        n = 0
    if n == 0:
        return "0 B"

    negative = n < 0
    n = abs(n)

    for unit in ["B", "KB", "MB", "GB", "TB", "PB"]:
        if n < 1024.0:
            if unit == "B":
                result = f"{int(n)} {unit}"
            else:
                result = f"{n:.2f} {unit}"
            return f"-{result}" if negative else result
        n /= 1024.0

    result = f"{n:.2f} EB"
    return f"-{result}" if negative else result
