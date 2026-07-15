"""Manager for MTProxyL (MTProto proxy on telemt engine).

Uses SSH + CLI commands to communicate with MTProxyL on the remote server.
No HTTP API dependency — all operations go through `mtproxyl` CLI via SSH.

Protocol name stays "telemt" internally for backward compatibility
with DB, API endpoints, and schemas.
"""

from __future__ import annotations

import logging
import re
import uuid
from typing import Any, Optional

from app.managers.ssh_manager import SSHManager

logger = logging.getLogger(__name__)


# Human-readable size suffixes for traffic parsing
_SIZE_SUFFIXES = {
    "Б": 1,
    "КБ": 1024,
    "МБ": 1024**2,
    "ГБ": 1024**3,
    "ТБ": 1024**4,
}


class MTProxyLManager:
    """Manager for MTProxyL (MTProto proxy on telemt engine).

    Uses SSH + CLI commands to communicate with MTProxyL installed
    on the remote server. No HTTP API dependency — all operations
    go through `mtproxyl` CLI via SSH.

    Protocol name stays "telemt" internally for backward compatibility
    with DB, API endpoints, and schemas.
    """

    CONTAINER_NAME = "mtproxyl"
    CLI_PATH = "/usr/local/bin/mtproxyl"
    SECRETS_FILE = "/opt/mtproxyl/secrets.conf"
    SETTINGS_FILE = "/opt/mtproxyl/settings.conf"

    def __init__(self, ssh_manager: SSHManager):
        self.ssh = ssh_manager

    # -------------------------------------------------------------------------
    # Protocol lifecycle
    # -------------------------------------------------------------------------

    def check_protocol_installed(self) -> bool:
        """Check if MTProxyL is installed on the remote server.

        Runs `mtproxyl status --json` and checks that the status field
        is present (either "running" or "stopped").
        """
        status = self._parse_status_json()
        return status is not None

    def get_server_status(self, protocol_type: str) -> dict[str, Any]:
        """Get the current server status for the MTProxyL protocol.

        Runs `mtproxyl status --json` and counts clients from secrets.conf.
        """
        status_data = self._parse_status_json()
        secrets = self._parse_secrets()

        exists = status_data is not None
        is_running = exists and status_data.get("status") == "running"

        result: dict[str, Any] = {
            "container_exists": exists,
            "container_running": is_running,
        }

        if is_running and status_data is not None:
            result["port"] = str(status_data.get("port", ""))
            result["awg_params"] = {
                "tls_emulation": bool(status_data.get("domain")),
                "tls_domain": status_data.get("domain", ""),
                "max_connections": 0,
            }
            result["clients_count"] = len(secrets)

        return result

    def install_protocol(
        self,
        protocol_type: str = "telemt",
        port: str = "443",
        tls_emulation: bool = True,
        tls_domain: str = "",
        max_connections: int = 0,
    ) -> dict[str, Any]:
        """Install and configure MTProxyL on the remote server.

        If MTProxyL CLI is not installed, runs the install script.
        Configures port, FakeTLS domain, and starts the proxy.
        """
        results: list[str] = []

        # Install MTProxyL if not already installed
        if not self._check_mtproxyl_installed():
            results.append("Installing MTProxyL...")
            install_script = (
                "wget -qO /tmp/mtproxyl-install.sh "
                "https://raw.githubusercontent.com/Liafanx/MTProxyL/main/install.sh "
                "&& bash /tmp/mtproxyl-install.sh"
            )
            out, err, code = self.ssh.run_sudo_command(install_script, timeout=120)
            if code != 0:
                logger.error(f"MTProxyL install failed: {err}")
                return {
                    "status": "error",
                    "host": "",
                    "port": port,
                    "log": results + [err.strip()],
                }
            results.append("MTProxyL installed successfully")

        # Avoid port 443 if BunkerWeb is running
        if port == "443" and self._detect_bunkerweb_running():
            port = "18443"
            results.append("BunkerWeb detected on port 443 — using port 18443")

        # Configure port
        out, err, code = self._run_cli(f"port {port}")
        if code != 0:
            logger.error(f"Failed to set port {port}: {err}")
            return {
                "status": "error",
                "host": "",
                "port": port,
                "log": results + [f"port error: {err.strip()}"],
            }

        # Configure FakeTLS domain
        if tls_emulation and tls_domain:
            self._run_cli(f"domain {tls_domain}")
            results.append(f"FakeTLS domain set to {tls_domain}")

        # Start the proxy
        self._run_cli("start")
        results.append("MTProxyL proxy started")

        return {"status": "success", "host": "", "port": port, "log": results}

    def remove_container(self, protocol_type: Optional[str] = None) -> None:
        """Stop the MTProxyL container and clean up configuration."""
        self._run_cli("stop")
        # Optionally remove the installation (leave config dir intact)
        # self._run_cli("uninstall")  # disabled — avoid accidental data loss

    # -------------------------------------------------------------------------
    # Client CRUD
    # -------------------------------------------------------------------------

    def get_clients(self, protocol_type: str) -> list[dict[str, Any]]:
        """Get all clients by parsing secrets.conf and enriching with traffic and connection data."""
        clients = self._parse_secrets()
        traffic = self._parse_traffic()
        connections = self._parse_connections()

        for client in clients:
            label = client["clientId"]
            if label in traffic:
                client["userData"]["total_octets"] = traffic[label]["total"]
            if label in connections:
                client["userData"]["current_connections"] = connections[label]

        return clients

    def add_client(
        self,
        protocol_type: str,
        name: str,
        host: str = "",
        port: str = "",
        telemt_quota: Optional[int] = None,
        telemt_max_ips: Optional[int] = None,
        telemt_expiry: Optional[str] = None,
    ) -> dict[str, str]:
        """Add a new client via `mtproxyl secret add` and optionally set limits.

        Returns a dict with client_id, config (tg:// link), and vpn_link.
        """
        # Sanitize name: MTProxyL allows [a-zA-Z0-9_-] only, max 32 chars
        username = re.sub(r"[^a-zA-Z0-9_-]", "", name.replace(" ", "_"))
        if not username:
            username = "user_" + uuid.uuid4().hex[:8]
        username = username[:32]

        # Add the secret
        out, err, code = self._run_cli(f"secret add {username}")
        if code != 0:
            error_msg = err.strip() or "Unknown error"
            logger.error(f"Failed to add client {username}: {error_msg}")
            return {"client_id": "", "config": "", "vpn_link": "", "error": error_msg}

        # Set limits if any are provided
        if telemt_quota is not None or telemt_max_ips is not None or telemt_expiry is not None:
            limits_str = self._format_limits(telemt_quota, telemt_max_ips, telemt_expiry)
            self._run_cli(f"secret setlimits {username} {limits_str}")

        # Get the tg:// link
        link = self.get_client_config(protocol_type, username, host, port)
        return {"client_id": username, "config": link, "vpn_link": link}

    def edit_client(
        self,
        protocol_type: str,
        client_id: str,
        new_params: dict[str, Any],
    ) -> dict[str, str]:
        """Update client limits via `mtproxyl secret setlimits`.

        Supported params: telemt_quota, telemt_max_ips, telemt_expiry.
        """
        telemt_quota = new_params.get("telemt_quota")
        telemt_max_ips = new_params.get("telemt_max_ips")
        telemt_expiry = new_params.get("telemt_expiry")

        if telemt_quota is None and telemt_max_ips is None and telemt_expiry is None:
            return {"status": "success"}

        limits_str = self._format_limits(telemt_quota, telemt_max_ips, telemt_expiry)
        out, err, code = self._run_cli(f"secret setlimits {client_id} {limits_str}")

        if code != 0:
            error_msg = err.strip() or "Unknown error"
            logger.error(f"Failed to edit client {client_id}: {error_msg}")
            return {"status": "error", "message": error_msg}

        return {"status": "success"}

    def remove_client(self, protocol_type: str, client_id: str) -> None:
        """Remove a client via `mtproxyl secret remove`."""
        out, err, code = self._run_cli(f"secret remove {client_id}")
        if code != 0:
            logger.error(f"Failed to remove client {client_id}: {err.strip()}")

    def toggle_client(
        self,
        protocol_type: str,
        client_id: str,
        enable: bool,
        restart: bool = True,
    ) -> None:
        """Enable or disable a client via `mtproxyl secret enable/disable`."""
        action = "enable" if enable else "disable"
        self._run_cli(f"secret {action} {client_id}")

    def get_client_config(
        self,
        protocol_type: str,
        client_id: str,
        host: str = "",
        port: str = "",
    ) -> str:
        """Get the tg:// connection link for a client via `mtproxyl secret link`."""
        out, _, _ = self._run_cli(f"secret link {client_id}")
        # Find the tg:// URL in the output
        for line in out.strip().splitlines():
            line = line.strip()
            if line.startswith("tg://"):
                return line
        return "Not found"

    # -------------------------------------------------------------------------
    # Quota enforcement
    # -------------------------------------------------------------------------

    def _is_overquota(self, client: dict[str, Any]) -> bool:
        """Check if a client has exceeded their traffic quota.

        Args:
            client: Client dict as returned by get_clients().

        Returns:
            True if the client is over quota, False otherwise.
        """
        enabled = client.get("enabled", False)
        user_data = client.get("userData", {})
        total_octets = user_data.get("total_octets", 0)
        quota = user_data.get("quota")
        return enabled and quota is not None and total_octets >= quota

    def disable_overquota_users(self, protocol_type: str) -> list[str]:
        """Disable all clients that have exceeded their traffic quota.

        Args:
            protocol_type: The protocol type (e.g., "telemt").

        Returns:
            List of client IDs that were disabled.
        """
        clients = self.get_clients(protocol_type)
        disabled_ids = []

        for client in clients:
            if self._is_overquota(client):
                client_id: str = client.get("clientId") or ""
                username = client.get("clientName")
                total_octets = client.get("userData", {}).get("total_octets", 0)
                quota = client.get("userData", {}).get("quota")
                logger.info(
                    f"Disabling over-quota client {username} "
                    f"(traffic {total_octets} >= quota {quota})"
                )
                self.toggle_client(protocol_type, client_id, False, restart=False)
                disabled_ids.append(client_id)

        if disabled_ids:
            logger.info(f"Disabled {len(disabled_ids)} over-quota users: {disabled_ids}")

        return disabled_ids

    # -------------------------------------------------------------------------
    # Internal helpers
    # -------------------------------------------------------------------------

    def _run_cli(self, command: str) -> tuple[str, str, int]:
        """Run a mtproxyl CLI command via SSH.

        Args:
            command: The command to pass to mtproxyl (e.g., "status --json").

        Returns:
            Tuple of (stdout, stderr, return_code).
        """
        cmd = f"{self.CLI_PATH} {command}"
        return self.ssh.run_command(cmd)

    def _parse_secrets(self) -> list[dict[str, Any]]:
        """Read and parse /opt/mtproxyl/secrets.conf into client dicts.

        Format: LABEL|SECRET|CREATED_TS|ENABLED|MAX_CONNS|MAX_IPS|QUOTA_BYTES|EXPIRES|NOTES

        Returns a list of client dicts matching the get_clients() return shape
        for backward compatibility with TelemtManager.
        """
        out, _, _ = self.ssh.run_command(f"cat {self.SECRETS_FILE} 2>/dev/null")
        clients: list[dict[str, Any]] = []

        for line in out.strip().splitlines():
            stripped = line.strip()
            if stripped.startswith("#") or not stripped:
                continue
            parts = stripped.split("|")
            if len(parts) < 9:
                continue

            label, secret, created, enabled, max_conns, max_ips, quota, expires, notes = (
                parts[0],
                parts[1],
                parts[2],
                parts[3],
                parts[4],
                parts[5],
                parts[6],
                parts[7],
                parts[8],
            )

            clients.append(
                {
                    "clientId": label,
                    "clientName": label,
                    "enabled": enabled == "true",
                    "creationDate": created,
                    "userData": {
                        "clientName": label,
                        "token": secret,
                        "tg_link": "",
                        "total_octets": 0,
                        "current_connections": 0,
                        "active_ips": int(max_ips) if max_ips and max_ips != "0" else None,
                        "quota": int(quota) if quota and quota not in ("0", "") else None,
                        "expiry": expires if expires and expires not in ("0", "") else None,
                    },
                }
            )

        return clients

    def _parse_status_json(self) -> Optional[dict[str, Any]]:
        """Run `mtproxyl status --json` and parse the JSON output.

        Returns:
            Parsed status dict, or None if the command fails.
        """
        import json

        out, err, code = self._run_cli("status --json")
        if code != 0:
            return None
        try:
            return json.loads(out.strip())
        except json.JSONDecodeError:
            logger.error(f"Invalid JSON from mtproxyl status: {out[:200]}")
            return None

    def _parse_traffic(self) -> dict[str, dict[str, Any]]:
        """Parse `mtproxyl traffic` output to get per-user byte counts.

        The output is in Russian and contains lines like:
            ● tg_proxy: ↓ 1.96 ГБ  ↑ 96.64 ГБ  соед: 41

        Returns:
            Dict mapping label -> {"total": int, "connections": int}
        """
        out, _, code = self._run_cli("traffic")
        if code != 0:
            return {}

        traffic: dict[str, dict[str, Any]] = {}

        for line in out.splitlines():
            line = line.strip()
            if not line.startswith("●"):
                continue
            # Extract label (after "● " and before ":")
            colon_idx = line.find(":")
            if colon_idx == -1:
                continue
            label = line[2:colon_idx].strip()
            traffic[label] = {"total": 0, "connections": 0}

            # Extract connections
            conn_match = re.search(r"соед[.:]\s*(\d+)", line)
            if conn_match:
                traffic[label]["connections"] = int(conn_match.group(1))

            # Extract total traffic (download + upload, sum both directions)
            total_bytes = 0
            for size_str, multiplier in _SIZE_SUFFIXES.items():
                # Match patterns like "1.96 ГБ" or "96.64 ГБ"
                for match in re.finditer(r"([\d.]+)\s*" + re.escape(size_str), line):
                    total_bytes += float(match.group(1)) * multiplier
            traffic[label]["total"] = int(total_bytes)

        return traffic

    def _parse_connections(self) -> dict[str, int]:
        """Parse `mtproxyl connections` output for per-user active connection counts.

        Output format:
            ПОЛЬЗОВАТЕЛЬ СОЕД. СКАЧАНО ОТПРАВЛЕНО
            ──────────────────────────────────────
            tg_proxy                6    1.68 МБ   61.83 МБ

        Returns:
            Dict mapping label -> active_connections count.
        """
        out, _, code = self._run_cli("connections")
        if code != 0:
            return {}

        connections: dict[str, int] = {}
        in_data = False

        for line in out.splitlines():
            line = line.strip()
            # Skip until we pass the separator line
            if "─────" in line:
                in_data = True
                continue
            if not in_data or not line:
                continue
            # Skip summary lines
            if line.startswith("Всего"):
                continue
            # Match: LABEL  COUNT  ...
            # Label is alphanumeric + _ + -, count is the first integer
            match = re.match(r"^([a-zA-Z0-9_-]+)\s+(\d+)", line)
            if match:
                label = match.group(1)
                count = int(match.group(2))
                connections[label] = count

        return connections

    def _check_mtproxyl_installed(self) -> bool:
        """Check if the mtproxyl binary exists on the remote server."""
        out, _, code = self.ssh.run_command(
            f"test -f {self.CLI_PATH} && echo found || echo not_found"
        )
        return out.strip() == "found"

    def _detect_bunkerweb_running(self) -> bool:
        """Check if bunkerweb container is running on the remote server."""
        out, _, _ = self.ssh.run_command(
            "docker ps --filter name=^bunkerweb$ --format '{{.Names}}'"
        )
        return out.strip() == "bunkerweb"

    def _format_limits(
        self,
        telemt_quota: Optional[int],
        telemt_max_ips: Optional[int],
        telemt_expiry: Optional[str],
    ) -> str:
        """Format limits for `mtproxyl secret setlimits` command.

        Format: <max_conns> <max_ips> <quota_bytes> <expires>
        Use 0 for unspecified limits (unlimited).
        """
        max_conns = 0
        max_ips = telemt_max_ips if telemt_max_ips is not None else 0
        quota = telemt_quota if telemt_quota is not None else 0
        expires = telemt_expiry if telemt_expiry else "0"
        return f"{max_conns} {max_ips} {quota} {expires}"
