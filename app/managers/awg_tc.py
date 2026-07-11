"""
AWG Traffic Control Manager - manages Linux tc (traffic control) rules
for per-peer speed limiting inside the amnezia-awg Docker container.

Uses HTB (Hierarchical Token Bucket) qdisc on the awg0 interface (egress)
and ifb0 interface (egress after IFB redirect) to enforce independent
upload/download bandwidth caps per WireGuard peer IP.

Architecture:
  - awg0 (egress): Download shaping. Filters match 'dst IP = peer_ip'.
  - ifb0 (egress): Upload shaping. awg0 ingress is redirected to ifb0,
    then filters match 'src IP = peer_ip'.
  - Global pool: Class 1:1 on each interface caps total AWG bandwidth.
  - Default class 1:9999: Unclassified/unlimited traffic (child of pool).
"""

from __future__ import annotations

import logging
import re

logger = logging.getLogger(__name__)

# Default interface name inside the AWG container
DEFAULT_INTERFACE = "awg0"

# Default container name
DEFAULT_CONTAINER = "amnezia-awg"

# IFB device name (virtual interface for ingress shaping)
IFB_DEVICE = "ifb0"

# Default class ID for unlimited traffic (no limit)
DEFAULT_CLASS_ID = 9999

# Default rate for the unlimited class (10 Gbps — effectively uncapped)
DEFAULT_CLASS_RATE = "10gbit"

# Global pool class ID (used on both awg0 and ifb0)
GLOBAL_POOL_CLASS_ID = 1


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


def _ip_exec(
    ssh, container_name: str, args: str, ignore_errors: bool = False
) -> tuple[str, str, int]:
    """Execute an ip command inside the AWG container via SSH.

    Args:
        ssh: SSHManager instance.
        container_name: Docker container name.
        args: ip command arguments (everything after 'ip ').
        ignore_errors: If True, don't log errors on non-zero exit.

    Returns:
        Tuple of (stdout, stderr, exit_code).
    """
    cmd = f"docker exec -i {container_name} ip {args}"
    logger.info(f"Running ip command: {cmd}")
    out, err, code = ssh.run_sudo_command(cmd)
    if code != 0 and not ignore_errors:
        logger.warning(f"ip command failed (code={code}): {err.strip()}")
    return out, err, code


def _setup_qdisc_on_interface(
    ssh,
    container_name: str,
    interface: str,
    global_limit_mbps: int | None = None,
) -> dict:
    """Create root HTB qdisc with global pool class 1:1 and default class 1:9999.

    Idempotent — safe to call repeatedly. If the qdisc is already set up,
    this is a no-op.

    Args:
        ssh: SSHManager instance.
        container_name: Docker container name.
        interface: Network interface name inside the container.
        global_limit_mbps: Optional global bandwidth cap in Mbps for class 1:1.
            None means 10 Gbit (effectively unlimited).

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

    # Determine global pool rate
    pool_rate = f"{global_limit_mbps}mbit" if global_limit_mbps else DEFAULT_CLASS_RATE

    # Add root HTB qdisc with default class for unlimited traffic
    _, err, code = _tc_exec(
        ssh,
        container_name,
        f"qdisc add dev {interface} root handle 1: htb default {DEFAULT_CLASS_ID}",
    )
    if code != 0:
        return {"status": "error", "message": f"Failed to add HTB qdisc: {err.strip()}"}

    # Add the global pool class 1:1 (parent of all limited and unlimited classes)
    _, err, code = _tc_exec(
        ssh,
        container_name,
        f"class add dev {interface} parent 1: classid 1:{GLOBAL_POOL_CLASS_ID}"
        f" htb rate {pool_rate}",
    )
    if code != 0:
        return {
            "status": "error",
            "message": f"Failed to add global pool class: {err.strip()}",
        }

    # Add the default unlimited class as child of the pool
    _, err, code = _tc_exec(
        ssh,
        container_name,
        f"class add dev {interface} parent 1:{GLOBAL_POOL_CLASS_ID} classid 1:{DEFAULT_CLASS_ID}"
        f" htb rate {DEFAULT_CLASS_RATE} ceil {pool_rate}",
    )
    if code != 0:
        return {
            "status": "error",
            "message": f"Failed to add default HTB class: {err.strip()}",
        }

    logger.info(
        f"HTB qdisc set up on {interface} with pool 1:{GLOBAL_POOL_CLASS_ID}"
        f" (rate={pool_rate}) and default 1:{DEFAULT_CLASS_ID}"
    )
    return {"status": "ok", "message": "HTB qdisc created"}


def setup_ifb(ssh, container_name: str = DEFAULT_CONTAINER) -> dict:
    """Create ifb0 and redirect awg0 ingress to ifb0 for upload shaping.

    Idempotent — safe to call repeatedly. Must be called before setting up
    upload (ifb0) qdiscs.

    Packet flow:
        Peer sends encrypted packet → awg0 ingress → redirect to ifb0 →
        ifb0 egress → shaped by ifb0 HTB qdisc (src match = peer's IP).

    Args:
        ssh: SSHManager instance.
        container_name: Docker container name.

    Returns:
        Dict with 'status' ('ok' or 'error') and optional 'message'.
    """
    # Check if ifb0 already exists
    out, _, code = _ip_exec(ssh, container_name, "link show dev ifb0", ignore_errors=True)
    if code == 0 and "ifb0" in out:
        logger.info("ifb0 already exists")
    else:
        # Create ifb0
        _, err, code = _ip_exec(
            ssh,
            container_name,
            "link add ifb0 type ifb",
            ignore_errors=True,
        )
        if code != 0 and "File exists" not in err:
            return {"status": "error", "message": f"Failed to create ifb0: {err.strip()}"}

    # Bring ifb0 up
    _, err, code = _ip_exec(ssh, container_name, "link set ifb0 up")
    if code != 0:
        return {"status": "error", "message": f"Failed to set ifb0 up: {err.strip()}"}

    # Set up ingress redirect from awg0 to ifb0
    # First, add ingress qdisc to awg0 if not present
    out, _, code = _tc_exec(
        ssh, container_name, f"qdisc show dev {DEFAULT_INTERFACE}", ignore_errors=True
    )
    has_ingress = code == 0 and "ingress" in out

    if not has_ingress:
        _, err, code = _tc_exec(
            ssh,
            container_name,
            f"qdisc add dev {DEFAULT_INTERFACE} handle ffff: ingress",
        )
        if code != 0 and "File exists" not in err:
            return {
                "status": "error",
                "message": f"Failed to add ingress qdisc on awg0: {err.strip()}",
            }

    # Add filter to redirect all ingress to ifb0
    _, err, code = _tc_exec(
        ssh,
        container_name,
        f"filter add dev {DEFAULT_INTERFACE} parent ffff: protocol ip u32"
        f" match u32 0 0 action mirred egress redirect dev {IFB_DEVICE}",
    )
    if code != 0 and "File exists" not in err:
        return {
            "status": "error",
            "message": f"Failed to add ingress redirect filter: {err.strip()}",
        }

    logger.info("IFB setup complete: awg0 ingress → ifb0 egress")
    return {"status": "ok", "message": "IFB configured for upload shaping"}


def teardown_ifb(ssh, container_name: str = DEFAULT_CONTAINER) -> dict:
    """Remove ingress redirect and delete ifb0.

    Args:
        ssh: SSHManager instance.
        container_name: Docker container name.

    Returns:
        Dict with 'status' ('ok' or 'error') and optional 'message'.
    """
    # Remove ingress qdisc from awg0 (this also removes the redirect filter)
    _, err, code = _tc_exec(
        ssh,
        container_name,
        f"qdisc del dev {DEFAULT_INTERFACE} handle ffff: ingress",
        ignore_errors=True,
    )
    if code != 0:
        logger.info(f"No ingress qdisc to remove from {DEFAULT_INTERFACE}: {err.strip()}")

    # Delete ifb0
    _, err, code = _ip_exec(
        ssh,
        container_name,
        "link del ifb0",
        ignore_errors=True,
    )
    if code != 0 and "Cannot find device" not in err:
        return {"status": "error", "message": f"Failed to delete ifb0: {err.strip()}"}

    logger.info("IFB teardown complete")
    return {"status": "ok", "message": "IFB removed"}


def setup_qdisc(
    ssh,
    container_name: str = DEFAULT_CONTAINER,
    interface: str = DEFAULT_INTERFACE,
    global_limit_mbps: int | None = None,
) -> dict:
    """Create root HTB qdisc with global pool on the specified interface.

    Idempotent — safe to call repeatedly. For awg0 (download), sets up the
    pool class 1:1 with optional global download cap. For ifb0 (upload),
    sets up the pool class 1:1 with optional global upload cap.

    Args:
        ssh: SSHManager instance.
        container_name: Docker container name.
        interface: Network interface name inside the container.
        global_limit_mbps: Optional global bandwidth cap in Mbps.
            None means 10 Gbit (effectively unlimited).

    Returns:
        Dict with 'status' ('ok' or 'error') and optional 'message'.
    """
    return _setup_qdisc_on_interface(ssh, container_name, interface, global_limit_mbps)


def apply_speed_limit(
    ssh,
    container_name: str,
    interface: str,
    peer_ip: str,
    down_mbps: int,
    up_mbps: int,
) -> dict:
    """Apply HTB speed limits for a WireGuard peer.

    Creates HTB classes and u32 filters on BOTH awg0 (download, dst match)
    and ifb0 (upload, src match). Each direction gets its own class so they
    are independently limited.

    If classes already exist for this peer, they are replaced.

    Args:
        ssh: SSHManager instance.
        container_name: Docker container name.
        interface: Ignored (always uses awg0 for download).
            Kept for API compatibility; download is always on awg0,
            upload always on ifb0.
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

    # Ensure IFB is set up for upload shaping
    ifb_result = setup_ifb(ssh, container_name)
    if ifb_result["status"] == "error":
        return ifb_result

    # Ensure qdisc is set up on awg0 (download)
    down_result = _setup_qdisc_on_interface(ssh, container_name, DEFAULT_INTERFACE)
    if down_result["status"] == "error":
        return down_result

    # Ensure qdisc is set up on ifb0 (upload)
    up_result = _setup_qdisc_on_interface(ssh, container_name, IFB_DEVICE)
    if up_result["status"] == "error":
        return up_result

    # Remove existing classes/filters for this peer (both directions)
    remove_speed_limit(ssh, container_name, interface, peer_ip)

    errors: list[str] = []

    # Download: class on awg0, dst match
    _, err, code = _tc_exec(
        ssh,
        container_name,
        f"class add dev {DEFAULT_INTERFACE} parent 1:{GLOBAL_POOL_CLASS_ID} classid 1:{class_id}"
        f" htb rate {down_mbps}mbit ceil {down_mbps}mbit",
    )
    if code != 0:
        errors.append(f"download class: {err.strip()}")
    else:
        # Filter: match ip dst = peer_ip on awg0 egress (parent 1:)
        _, err, code = _tc_exec(
            ssh,
            container_name,
            f"filter add dev {DEFAULT_INTERFACE} parent 1: protocol ip prio 1"
            f" u32 match ip dst {peer_ip} flowid 1:{class_id}",
        )
        if code != 0:
            errors.append(f"download filter: {err.strip()}")

    # Upload: class on ifb0, src match
    _, err, code = _tc_exec(
        ssh,
        container_name,
        f"class add dev {IFB_DEVICE} parent 1:{GLOBAL_POOL_CLASS_ID} classid 1:{class_id}"
        f" htb rate {up_mbps}mbit ceil {up_mbps}mbit",
    )
    if code != 0:
        errors.append(f"upload class: {err.strip()}")
    else:
        # Filter: match ip src = peer_ip on ifb0 egress (parent 1:)
        _, err, code = _tc_exec(
            ssh,
            container_name,
            f"filter add dev {IFB_DEVICE} parent 1: protocol ip prio 1"
            f" u32 match ip src {peer_ip} flowid 1:{class_id}",
        )
        if code != 0:
            errors.append(f"upload filter: {err.strip()}")

    if errors:
        logger.warning(f"Speed limit partially applied for {peer_ip}: {', '.join(errors)}")
        return {"status": "ok", "message": f"Speed limit applied with errors: {', '.join(errors)}"}

    logger.info(
        f"Speed limit applied for {peer_ip}: "
        f"down=class 1:{class_id}@{down_mbps}mbit, "
        f"up=class 1:{class_id}@{up_mbps}mbit"
    )
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
    # tc filter show output uses "filter protocol" or "filter parent" as entry
    # delimiters. Split on either pattern that starts each filter entry.
    entries = re.split(r"(?=filter (?:parent \d+: )?protocol)", out.strip())
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


def _remove_filter(ssh, container_name: str, interface: str, handle: str, prio: str = "1") -> None:
    """Delete a specific filter by handle and prio.

    Args:
        ssh: SSHManager instance.
        container_name: Docker container name.
        interface: Network interface name.
        handle: Filter handle string (e.g. '800::800').
        prio: Filter priority string.
    """
    _tc_exec(
        ssh,
        container_name,
        f"filter del dev {interface} parent 1: protocol ip prio {prio}" f" handle {handle} u32",
        ignore_errors=True,
    )


def remove_speed_limit(
    ssh,
    container_name: str,
    interface: str,
    peer_ip: str,
) -> dict:
    """Remove HTB speed limits for a WireGuard peer (both directions).

    Deletes filters and HTB classes on both awg0 (download) and ifb0
    (upload). The peer falls back to the default class (1:9999 = unlimited).

    Args:
        ssh: SSHManager instance.
        container_name: Docker container name.
        interface: Network interface name (ignored; both directions removed).
            Kept for API compatibility.
        peer_ip: Peer's WireGuard IP.

    Returns:
        Dict with 'status' ('ok' or 'error') and optional 'message'.
    """
    try:
        class_id = _peer_to_class_id(peer_ip)
    except ValueError as e:
        return {"status": "error", "message": str(e)}

    # Remove download (awg0) filters and class
    handles_down = _find_filter_handles(ssh, container_name, DEFAULT_INTERFACE, class_id)
    for handle in handles_down:
        _remove_filter(ssh, container_name, DEFAULT_INTERFACE, handle)

    _, _, code = _tc_exec(
        ssh,
        container_name,
        f"class del dev {DEFAULT_INTERFACE} parent 1:{GLOBAL_POOL_CLASS_ID} classid 1:{class_id}",
        ignore_errors=True,
    )
    if code != 0:
        logger.info(f"No download class 1:{class_id} to remove for {peer_ip}")

    # Remove upload (ifb0) filters and class
    handles_up = _find_filter_handles(ssh, container_name, IFB_DEVICE, class_id)
    for handle in handles_up:
        _remove_filter(ssh, container_name, IFB_DEVICE, handle)

    _, _, code = _tc_exec(
        ssh,
        container_name,
        f"class del dev {IFB_DEVICE} parent 1:{GLOBAL_POOL_CLASS_ID} classid 1:{class_id}",
        ignore_errors=True,
    )
    if code != 0:
        logger.info(f"No upload class 1:{class_id} to remove for {peer_ip}")

    logger.info(f"Speed limit removed for {peer_ip}: classes 1:{class_id} deleted")
    return {"status": "ok", "message": f"Speed limit removed for {peer_ip}"}


def set_global_limit(
    ssh,
    container_name: str,
    down_mbps: int | None,
    up_mbps: int | None,
) -> dict:
    """Change the global pool class 1:1 rate on awg0 and ifb0.

    When global limits are set, class 1:1 rate is changed on both interfaces.
    None means 10 Gbit (effectively unlimited).

    Args:
        ssh: SSHManager instance.
        container_name: Docker container name.
        down_mbps: Global download cap in Mbps. None = unlimited.
        up_mbps: Global upload cap in Mbps. None = unlimited.

    Returns:
        Dict with 'status' ('ok' or 'error') and optional 'message'.
    """
    down_rate = f"{down_mbps}mbit" if down_mbps else DEFAULT_CLASS_RATE
    up_rate = f"{up_mbps}mbit" if up_mbps else DEFAULT_CLASS_RATE

    # Update class 1:1 on awg0 (download pool)
    _, err, code = _tc_exec(
        ssh,
        container_name,
        f"class change dev {DEFAULT_INTERFACE} parent 1: classid 1:{GLOBAL_POOL_CLASS_ID}"
        f" htb rate {down_rate}",
    )
    if code != 0:
        return {
            "status": "error",
            "message": f"Failed to change download pool class: {err.strip()}",
        }

    # Update default class 1:9999 ceil to match new download pool rate
    _, err, code = _tc_exec(
        ssh,
        container_name,
        f"class change dev {DEFAULT_INTERFACE} parent 1:{GLOBAL_POOL_CLASS_ID} classid 1:{DEFAULT_CLASS_ID}"
        f" htb rate {DEFAULT_CLASS_RATE} ceil {down_rate}",
    )
    if code != 0:
        logger.warning(f"Failed to change download default class ceil: {err.strip()}")

    # Update class 1:1 on ifb0 (upload pool)
    _, err, code = _tc_exec(
        ssh,
        container_name,
        f"class change dev {IFB_DEVICE} parent 1: classid 1:{GLOBAL_POOL_CLASS_ID}"
        f" htb rate {up_rate}",
    )
    if code != 0:
        return {"status": "error", "message": f"Failed to change upload pool class: {err.strip()}"}

    # Update default class 1:9999 ceil to match new upload pool rate
    _, err, code = _tc_exec(
        ssh,
        container_name,
        f"class change dev {IFB_DEVICE} parent 1:{GLOBAL_POOL_CLASS_ID} classid 1:{DEFAULT_CLASS_ID}"
        f" htb rate {DEFAULT_CLASS_RATE} ceil {up_rate}",
    )
    if code != 0:
        logger.warning(f"Failed to change upload default class ceil: {err.strip()}")

    logger.info(f"Global limits changed: download={down_rate}, upload={up_rate}")
    return {
        "status": "ok",
        "message": f"Global limits updated: down={down_mbps or 'unlimited'}, up={up_mbps or 'unlimited'}",
    }


def reapply_all_limits(
    ssh,
    container_name: str,
    interface: str,
    clients_table_data: list[dict],
    global_limit_down: int | None = None,
    global_limit_up: int | None = None,
) -> dict:
    """Tear down existing tc rules, rebuild with current limits + global pool.

    Called after awg syncconf or container restart to restore tc rules
    that were lost when the interface was reset.

    Args:
        ssh: SSHManager instance.
        container_name: Docker container name.
        interface: Network interface name (ignored; always uses awg0/ifb0).
            Kept for API compatibility.
        clients_table_data: List of client dicts. Each must have
            'clientIp' (str) and may have 'speed_limit_down' (int|null)
            and 'speed_limit_up' (int|null) in 'userData'.
        global_limit_down: Optional global download cap in Mbps.
        global_limit_up: Optional global upload cap in Mbps.

    Returns:
        Dict with 'status', 'applied' (count), and 'errors' (list).
    """
    # Tear down existing rules
    teardown_qdisc(ssh, container_name, DEFAULT_INTERFACE)
    teardown_ifb(ssh, container_name)

    # Set up IFB (for upload shaping)
    ifb_result = setup_ifb(ssh, container_name)
    if ifb_result["status"] == "error":
        return {"status": "error", "applied": 0, "errors": [ifb_result["message"]]}

    # Set up HTB qdiscs with global pool on both interfaces
    down_result = _setup_qdisc_on_interface(
        ssh, container_name, DEFAULT_INTERFACE, global_limit_down
    )
    if down_result["status"] == "error":
        return {"status": "error", "applied": 0, "errors": [down_result["message"]]}

    up_result = _setup_qdisc_on_interface(ssh, container_name, IFB_DEVICE, global_limit_up)
    if up_result["status"] == "error":
        return {"status": "error", "applied": 0, "errors": [up_result["message"]]}

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

        # Use the other direction's limit if one is missing
        # (each direction gets its own class with the specified rate)
        effective_down = down if down is not None else up
        effective_up = up if up is not None else down

        if effective_down is None or effective_up is None:
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
    """Remove all tc rules from the interface and ifb0.

    Removes the root qdisc from the specified interface (and ifb0 when
    interface=awg0), which destroys all classes and filters.

    Args:
        ssh: SSHManager instance.
        container_name: Docker container name.
        interface: Network interface name.

    Returns:
        Dict with 'status' ('ok' or 'error') and optional 'message'.
    """
    messages: list[str] = []

    # Remove qdisc from specified interface
    _, err, code = _tc_exec(
        ssh,
        container_name,
        f"qdisc del dev {interface} root handle 1:",
        ignore_errors=True,
    )
    if code != 0:
        messages.append(f"{interface}: no qdisc to remove")
    else:
        messages.append(f"{interface} qdisc removed")

    # If removing from awg0, also clean up ifb0
    if interface == DEFAULT_INTERFACE:
        _, err, code = _tc_exec(
            ssh,
            container_name,
            f"qdisc del dev {IFB_DEVICE} root handle 1:",
            ignore_errors=True,
        )
        if code == 0:
            messages.append(f"{IFB_DEVICE} qdisc removed")
        else:
            messages.append(f"{IFB_DEVICE}: no qdisc to remove")

    logger.info(f"Teardown complete: {', '.join(messages)}")
    return {"status": "ok", "message": ", ".join(messages)}
