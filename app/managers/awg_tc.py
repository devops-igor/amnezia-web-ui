"""
AWG Traffic Control Manager - manages Linux tc (traffic control) rules
for per-peer speed limiting inside the amnezia-awg Docker container.

Uses HTB (Hierarchical Token Bucket) qdisc on the awg0 interface to
enforce upload/download bandwidth caps per WireGuard peer IP.
"""

from __future__ import annotations

import logging
import re

logger = logging.getLogger(__name__)

# Default interface name inside the AWG container
DEFAULT_INTERFACE = "awg0"

# Default container name
DEFAULT_CONTAINER = "amnezia-awg"

# Default class ID for unlimited traffic (no limit)
DEFAULT_CLASS_ID = 9999

# Default rate for the unlimited class (10 Gbps — effectively uncapped)
DEFAULT_CLASS_RATE = "10gbit"


def _peer_to_class_id(peer_ip: str) -> int:
    """Convert a peer IP address to an HTB class ID (last octet).

    Args:
        peer_ip: WireGuard peer IP, e.g. '10.8.1.45'.

    Returns:
        Integer class ID derived from the last octet (e.g. 45).

    Raises:
        ValueError: If the IP is not in a valid format.
    """
    parts = peer_ip.split(".")
    if len(parts) != 4:
        raise ValueError(f"Invalid IP address: {peer_ip}")
    last_octet = int(parts[3])
    if last_octet < 1 or last_octet > 253:
        raise ValueError(f"IP last octet {last_octet} out of usable range 1-253: {peer_ip}")
    return last_octet


def _tc_exec(
    ssh,
    container_name: str,
    args: str,
    ignore_errors: bool = False,
) -> tuple[str, str, int]:
    """Execute a tc command inside the AWG container via SSH.

    Args:
        ssh: SSHManager instance for remote command execution.
        container_name: Docker container name (e.g. 'amnezia-awg').
        args: tc command arguments (everything after 'tc ').
        ignore_errors: If True, don't log errors on non-zero exit.

    Returns:
        Tuple of (stdout, stderr, exit_code).
    """
    cmd = f"docker exec -i {container_name} tc {args}"
    logger.info(f"Running tc command: {cmd}")
    out, err, code = ssh.run_sudo_command(cmd)
    if code != 0 and not ignore_errors:
        logger.warning(f"tc command failed (code={code}): {err.strip()}")
    return out, err, code


def setup_qdisc(
    ssh,
    container_name: str = DEFAULT_CONTAINER,
    interface: str = DEFAULT_INTERFACE,
) -> dict:
    """Create root HTB qdisc on the interface if it doesn't already exist.

    Idempotent — safe to call repeatedly. If the qdisc is already set up,
    this is a no-op.

    Args:
        ssh: SSHManager instance.
        container_name: Docker container name.
        interface: Network interface name inside the container.

    Returns:
        Dict with 'status' ('ok' or 'error') and optional 'message'.
    """
    # Check if HTB qdisc already exists on this interface
    out, _, code = _tc_exec(ssh, container_name, f"qdisc show dev {interface}", ignore_errors=True)
    if code == 0 and "htb" in out:
        logger.info(f"HTB qdisc already exists on {interface}")
        return {"status": "ok", "message": "HTB qdisc already exists"}

    # Remove any existing qdisc first (e.g. default fq_codel)
    _tc_exec(
        ssh,
        container_name,
        f"qdisc del dev {interface} root 2>/dev/null",
        ignore_errors=True,
    )

    # Add root HTB qdisc with default class for unlimited traffic
    _, err, code = _tc_exec(
        ssh,
        container_name,
        f"qdisc add dev {interface} root handle 1: htb default {DEFAULT_CLASS_ID}",
    )
    if code != 0:
        return {"status": "error", "message": f"Failed to add HTB qdisc: {err.strip()}"}

    # Add the default unlimited class
    _, err, code = _tc_exec(
        ssh,
        container_name,
        f"class add dev {interface} parent 1: classid 1:{DEFAULT_CLASS_ID}"
        f" htb rate {DEFAULT_CLASS_RATE}",
    )
    if code != 0:
        return {
            "status": "error",
            "message": f"Failed to add default HTB class: {err.strip()}",
        }

    logger.info(f"HTB qdisc set up on {interface} with default class 1:{DEFAULT_CLASS_ID}")
    return {"status": "ok", "message": "HTB qdisc created"}


def apply_speed_limit(
    ssh,
    container_name: str,
    interface: str,
    peer_ip: str,
    down_mbps: int,
    up_mbps: int,
) -> dict:
    """Apply HTB speed limits for a WireGuard peer.

    Creates an HTB class and u32 filters to match the peer's traffic by
    source IP (download) and destination IP (upload).

    If a class already exists for this peer, it is replaced with the new limits.

    Args:
        ssh: SSHManager instance.
        container_name: Docker container name.
        interface: Network interface name.
        peer_ip: Peer's WireGuard IP (e.g. '10.8.1.45').
        down_mbps: Download speed limit in Mbps.
        up_mbps: Upload speed limit in Mbps.

    Returns:
        Dict with 'status' ('ok' or 'error') and optional 'message'.
    """
    try:
        class_id = _peer_to_class_id(peer_ip)
    except ValueError as e:
        return {"status": "error", "message": str(e)}

    # Ensure qdisc is set up
    result = setup_qdisc(ssh, container_name, interface)
    if result["status"] == "error":
        return result

    # Remove existing class/filters for this peer (if any) before re-adding
    remove_speed_limit(ssh, container_name, interface, peer_ip)

    # Add HTB class. Both directions share one class with rate = max(down, up).
    # For VPN use, max(down, up) is a practical simplification.
    total_mbps = max(down_mbps, up_mbps)

    _, err, code = _tc_exec(
        ssh,
        container_name,
        f"class add dev {interface} parent 1: classid 1:{class_id}"
        f" htb rate {total_mbps}mbit ceil {total_mbps}mbit",
    )
    if code != 0:
        return {"status": "error", "message": f"Failed to add HTB class: {err.strip()}"}

    # Download filter: match packets with srcip = peer_ip
    _, err, code = _tc_exec(
        ssh,
        container_name,
        f"filter add dev {interface} parent 1: protocol ip prio 1"
        f" u32 match ip src {peer_ip} flowid 1:{class_id}",
    )
    if code != 0:
        logger.warning(f"Failed to add download filter: {err.strip()}")

    # Upload filter: match packets with dstip = peer_ip
    _, err, code = _tc_exec(
        ssh,
        container_name,
        f"filter add dev {interface} parent 1: protocol ip prio 2"
        f" u32 match ip dst {peer_ip} flowid 1:{class_id}",
    )
    if code != 0:
        logger.warning(f"Failed to add upload filter: {err.strip()}")

    logger.info(f"Speed limit applied for {peer_ip}: " f"class 1:{class_id}, rate={total_mbps}mbit")
    return {
        "status": "ok",
        "message": f"Speed limit applied: {down_mbps}/{up_mbps} Mbps for {peer_ip}",
    }


def _find_filter_handles(
    ssh,
    container_name: str,
    interface: str,
    class_id: int,
) -> list[str]:
    """Find tc filter handles that route to a specific class.

    Parses `tc filter show` output to find handles matching flowid 1:<class_id>.

    Args:
        ssh: SSHManager instance.
        container_name: Docker container name.
        interface: Network interface name.
        class_id: HTB class ID to search for.

    Returns:
        List of filter handle strings (e.g. ['800::800', '801::800']).
    """
    out, _, _ = _tc_exec(
        ssh, container_name, f"filter show dev {interface} parent 1:", ignore_errors=True
    )
    if not out:
        return []

    handles: list[str] = []
    # tc filter show output uses "filter parent" as entry delimiters.
    # Split on the pattern that starts each filter entry.
    entries = re.split(r"(?=filter parent 1: protocol)", out.strip())
    for entry in entries:
        if not entry.strip():
            continue
        if f"flowid 1:{class_id}" not in entry:
            continue
        # Find handle in this entry (format: "fh XXXX::YYYY")
        handle_match = re.search(r"fh ([0-9a-f]+::[0-9a-f]+)", entry)
        if handle_match:
            handles.append(handle_match.group(1))

    return handles


def remove_speed_limit(
    ssh,
    container_name: str,
    interface: str,
    peer_ip: str,
) -> dict:
    """Remove HTB speed limits for a WireGuard peer.

    Deletes the filters and HTB class for this peer. The peer falls back
    to the default class (1:9999 = unlimited).

    Args:
        ssh: SSHManager instance.
        container_name: Docker container name.
        interface: Network interface name.
        peer_ip: Peer's WireGuard IP.

    Returns:
        Dict with 'status' ('ok' or 'error') and optional 'message'.
    """
    try:
        class_id = _peer_to_class_id(peer_ip)
    except ValueError as e:
        return {"status": "error", "message": str(e)}

    # Delete specific filters for this peer before deleting the class.
    # tc class del fails with "HTB class in use" if filters still reference it.
    handles = _find_filter_handles(ssh, container_name, interface, class_id)
    for handle in handles:
        _tc_exec(
            ssh,
            container_name,
            f"filter del dev {interface} parent 1: handle {handle}",
            ignore_errors=True,
        )

    # Now delete the class
    _, _, code = _tc_exec(
        ssh,
        container_name,
        f"class del dev {interface} parent 1: classid 1:{class_id}",
        ignore_errors=True,
    )
    if code != 0:
        # Class might not exist — that's fine
        logger.info(f"No existing class 1:{class_id} to remove for {peer_ip}")
        return {"status": "ok", "message": f"No existing limit for {peer_ip}"}

    logger.info(f"Speed limit removed for {peer_ip}: class 1:{class_id} deleted")
    return {"status": "ok", "message": f"Speed limit removed for {peer_ip}"}


def reapply_all_limits(
    ssh,
    container_name: str,
    interface: str,
    clients_table_data: list[dict],
) -> dict:
    """Re-apply all speed limits from the clientsTable.

    Called after awg syncconf or container restart to restore tc rules
    that were lost when the interface was reset.

    Args:
        ssh: SSHManager instance.
        container_name: Docker container name.
        interface: Network interface name.
        clients_table_data: List of client dicts. Each must have
            'clientIp' (str) and may have 'speed_limit_down' (int|null)
            and 'speed_limit_up' (int|null) in 'userData'.

    Returns:
        Dict with 'status', 'applied' (count), and 'errors' (list).
    """
    # Ensure qdisc is set up first
    result = setup_qdisc(ssh, container_name, interface)
    if result["status"] == "error":
        return result

    applied = 0
    errors: list[str] = []

    for client in clients_table_data:
        user_data = client.get("userData", {})
        peer_ip = client.get("clientIp") or user_data.get("clientIp")
        if not peer_ip:
            continue

        speed_down = user_data.get("speed_limit_down")
        speed_up = user_data.get("speed_limit_up")

        # Skip if no limits (both null or both 0)
        if not speed_down and not speed_up:
            continue

        # Normalize 0 to None (unlimited)
        down = speed_down if speed_down else None
        up = speed_up if speed_up else None

        # Only apply if at least one direction has a limit
        if down is None and up is None:
            continue

        # Use reasonable defaults if one direction is unlimited
        effective_down = down if down else up
        effective_up = up if up else down

        if effective_down is None or effective_up is None:
            # Shouldn't reach here, but skip if so
            continue

        result = apply_speed_limit(
            ssh, container_name, interface, peer_ip, effective_down, effective_up
        )
        if result["status"] == "ok":
            applied += 1
        else:
            errors.append(f"{peer_ip}: {result.get('message', 'unknown error')}")

    logger.info(f"Re-applied {applied} speed limits, {len(errors)} errors")
    return {
        "status": "ok" if not errors else "partial",
        "applied": applied,
        "errors": errors,
    }


def teardown_qdisc(
    ssh,
    container_name: str = DEFAULT_CONTAINER,
    interface: str = DEFAULT_INTERFACE,
) -> dict:
    """Remove all tc rules from the interface.

    Removes the root qdisc, which destroys all classes and filters.

    Args:
        ssh: SSHManager instance.
        container_name: Docker container name.
        interface: Network interface name.

    Returns:
        Dict with 'status' ('ok' or 'error') and optional 'message'.
    """
    _, err, code = _tc_exec(
        ssh,
        container_name,
        f"qdisc del dev {interface} root handle 1:",
        ignore_errors=True,
    )
    if code != 0:
        logger.info(f"No qdisc to remove from {interface}")
        return {"status": "ok", "message": f"No qdisc on {interface}"}

    logger.info(f"All tc rules removed from {interface}")
    return {"status": "ok", "message": f"Qdisc removed from {interface}"}
