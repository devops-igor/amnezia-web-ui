from __future__ import annotations

import json
import logging
import os
import re
import secrets
import uuid
from typing import Any, Optional

import httpx
from ssh_manager import SSHManager

logger = logging.getLogger(__name__)


class TelemtManager:
    """Manager for the Telemt (MTProto) protocol container.

    Uses SSH for container lifecycle (install/remove) and direct HTTP API
    calls for user management (CRUD, toggle, config retrieval).
    """

    CONTAINER_NAME = "telemt"
    API_BASE = "http://telemt:9091"

    def __init__(self, ssh_manager: SSHManager, config_dir: str = "/opt/amnezia/telemt"):
        self.ssh = ssh_manager
        self._config_dir_path = config_dir

    def _config_dir(self) -> str:
        """Return the configurable Telemt config directory path."""
        return self._config_dir_path

    def _config_path(self) -> str:
        """Return the full path to config.toml."""
        return f"{self._config_dir()}/config.toml"

    def _api_request(
        self,
        method: str,
        path: str,
        data: Optional[dict[str, Any]] = None,
    ) -> Optional[dict[str, Any]]:
        """Make a direct HTTP request to the Telemt API.

        Args:
            method: HTTP method (GET, POST, PATCH, DELETE).
            path: API path starting with /v1/.
            data: Optional JSON body for mutating requests.

        Returns:
            Parsed JSON response dict, or None on failure.
        """
        url = f"{self.API_BASE}{path}"
        try:
            with httpx.Client(timeout=10.0) as client:
                headers = {"Content-Type": "application/json"}
                resp = client.request(method, url, json=data, headers=headers)
                try:
                    return resp.json()
                except json.JSONDecodeError:
                    logger.error(f"Invalid JSON from Telemt API: {resp.text[:200]}")
                    return None
        except httpx.RequestError as e:
            logger.error(f"Telemt API request failed: {method} {path} — {e}")
            return None

    def check_docker_installed(self) -> bool:
        """Check if Docker is installed on the remote server."""
        out, _, _ = self.ssh.run_command("docker --version 2>/dev/null")
        return bool(out.strip())

    def check_protocol_installed(self) -> bool:
        """Check if the Telemt container exists on the remote server."""
        out, _, _ = self.ssh.run_command(
            f"docker ps -a --filter name=^{self.CONTAINER_NAME}$ --format '{{{{.Names}}}}'"
        )
        return out.strip() == self.CONTAINER_NAME

    def get_server_status(self, protocol_type: str) -> dict[str, Any]:
        """Get the current server status for the Telemt protocol.

        Uses the /v1/health API endpoint to retrieve configuration parameters
        when the container is running.
        """
        exists = self.check_protocol_installed()
        out, _, _ = self.ssh.run_command(
            f"docker inspect -f '{{{{.State.Running}}}}' {self.CONTAINER_NAME} 2>/dev/null"
        )
        is_running = out.strip().lower() == "true"

        status: dict[str, Any] = {
            "container_exists": exists,
            "container_running": is_running,
        }

        if is_running:
            # Get external docker port mapping for 443
            out, _, _ = self.ssh.run_command(f"docker port {self.CONTAINER_NAME} 443 2>/dev/null")
            if out:
                port = out.split(":")[-1].strip()
                status["port"] = port
            else:
                status["port"] = None

            # Get params from health API
            status["awg_params"] = self._get_telemt_params_from_api()

            # Count connections from API
            clients = self.get_clients(protocol_type)
            status["clients_count"] = len(clients)

        return status

    def _get_telemt_params_from_api(self) -> dict[str, Any]:
        """Fetch Telemt configuration params from the /v1/health endpoint.

        Returns a dict with tls_emulation, tls_domain, max_connections.
        Falls back to empty dict if the API is unavailable.
        """
        params: dict[str, Any] = {}
        resp = self._api_request("GET", "/v1/health")
        if resp and resp.get("ok"):
            # The health endpoint returns basic status; for detailed params
            # we query the system info endpoint which includes config info.
            info_resp = self._api_request("GET", "/v1/system/info")
            if info_resp and info_resp.get("ok"):
                data = info_resp.get("data", {})
                # Parse config hash/path to infer parameters if available
                # For now, return what we can from health
                params["tls_emulation"] = True  # default
                params["tls_domain"] = ""
                params["max_connections"] = 0
        return params

    def install_protocol(
        self,
        protocol_type: str = "telemt",
        port: str = "443",
        tls_emulation: bool = True,
        tls_domain: str = "",
        max_connections: int = 0,
    ) -> dict[str, Any]:
        """Install the Telemt protocol container on the remote server.

        This method still uses SSH for file upload and docker-compose.
        """
        results = []
        if not self.check_docker_installed():
            results.append("Installing Docker...")
            self.ssh.run_sudo_command("curl -fsSL https://get.docker.com | sh")
            self.ssh.run_sudo_command(
                "apt-get install -y docker-buildx-plugin docker-compose-plugin"
            )

        if self.check_protocol_installed():
            self.ssh.run_sudo_command(f"docker rm -f {self.CONTAINER_NAME}")

        self.ssh.run_sudo_command(
            "apt-get install -y docker-buildx-plugin docker-compose-plugin || yum install -y docker-buildx-plugin docker-compose-plugin"
        )

        results.append("Uploading Telemt files...")
        local_dir = os.path.join(os.path.dirname(__file__), "protocol_telemt")
        remote_dir = self._config_dir()
        self.ssh.run_sudo_command(f"mkdir -p {remote_dir}")
        self.ssh.run_sudo_command(f"chmod 755 {remote_dir}")

        # Read and patch config.toml
        with open(os.path.join(local_dir, "config.toml"), "r", encoding="utf-8") as f:
            config_content = f.read()

        tls_emul_str = "true" if tls_emulation else "false"
        config_content = re.sub(
            r"tls_emulation\s*=\s*(true|false|True|False)",
            f"tls_emulation = {tls_emul_str}",
            config_content,
        )

        if tls_emulation and tls_domain:
            config_content = re.sub(
                r'tls_domain\s*=\s*".*?"', f'tls_domain = "{tls_domain}"', config_content
            )

        if max_connections is not None and max_connections > 0:
            config_content = re.sub(
                r"max_connections\s*=\s*\d+",
                f"max_connections = {max_connections}",
                config_content,
            )

        # Patch public_host and public_port for links
        if "public_host =" in config_content or "# public_host =" in config_content:
            config_content = re.sub(
                r'#?\s*public_host\s*=\s*".*?"',
                f'public_host = "{self.ssh.host}"',
                config_content,
            )
        else:
            config_content = config_content.replace(
                "[general.links]", f'[general.links]\npublic_host = "{self.ssh.host}"'
            )

        config_content = re.sub(r"public_port\s*=\s*\d+", f"public_port = {port}", config_content)

        # Remove default hello user
        config_content = re.sub(r'^hello\s*=\s*".*?"', "", config_content, flags=re.MULTILINE)

        self.ssh.upload_file_sudo(config_content, f"{remote_dir}/config.toml")

        # Patch docker-compose.yml with proper port
        with open(os.path.join(local_dir, "docker-compose.yml"), "r", encoding="utf-8") as f:
            compose_content = f.read()

        compose_content = re.sub(r'"443:443"', f'"{port}:443"', compose_content)
        self.ssh.upload_file_sudo(compose_content, f"{remote_dir}/docker-compose.yml")

        # Upload Dockerfile
        with open(os.path.join(local_dir, "Dockerfile"), "r", encoding="utf-8") as f:
            dockerfile = f.read()
            self.ssh.upload_file_sudo(dockerfile, f"{remote_dir}/Dockerfile")

        results.append("Starting Telemt container...")
        out, err, code = self.ssh.run_sudo_command(
            f"cd {remote_dir} && docker compose up -d --build", timeout=600
        )
        if code != 0:
            self.ssh.run_sudo_command(
                f"cd {remote_dir} && docker-compose up -d --build", timeout=600
            )

        return {"status": "success", "host": "", "port": port, "log": results}

    def remove_container(self, protocol_type: Optional[str] = None) -> None:
        """Remove the Telemt container and its config directory."""
        self.ssh.run_sudo_command(f"docker rm -f {self.CONTAINER_NAME}")
        self.ssh.run_sudo_command(f"rm -rf {self._config_dir()}")

    def get_clients(self, protocol_type: str) -> list[dict[str, Any]]:
        """Get all clients from the Telemt API.

        Returns a list of client dicts with the same shape as the old
        config-parsing implementation for backward compatibility.
        """
        resp = self._api_request("GET", "/v1/users")
        if not resp or not resp.get("ok"):
            logger.warning("Failed to fetch users from Telemt API")
            return []

        users_data = resp.get("data", [])
        clients = []
        needs_disable = []

        for user in users_data:
            username = user.get("username", "")
            secret = user.get("secret", "")
            links = user.get("links", {})
            enabled = bool(secret)  # Non-empty secret means enabled

            tg_link = ""
            if links.get("tls"):
                tg_link = links["tls"][0]
            elif links.get("secure"):
                tg_link = links["secure"][0]
            elif links.get("classic"):
                tg_link = links["classic"][0]

            total_octets = user.get("total_octets", 0)
            quota = user.get("data_quota_bytes")

            # Auto-disable if quota reached
            if enabled and quota and total_octets >= quota:
                logger.info(
                    f"Auto-disabling client {username} - quota reached: {total_octets} >= {quota}"
                )
                enabled = False
                needs_disable.append(username)

            clients.append(
                {
                    "clientId": username,
                    "clientName": username,
                    "enabled": enabled,
                    "creationDate": "",
                    "userData": {
                        "clientName": username,
                        "token": secret,
                        "tg_link": tg_link,
                        "total_octets": total_octets,
                        "current_connections": user.get("current_connections", 0),
                        "active_ips": user.get("active_unique_ips", 0),
                        "quota": quota,
                        "expiry": user.get("expiration_rfc3339"),
                    },
                }
            )

        # Disable users who exceeded quota
        for username in needs_disable:
            self.toggle_client(protocol_type, username, False, restart=False)

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
        """Add a new client via the Telemt POST /v1/users API.

        Returns a dict with client_id and vpn_link.
        """
        username = re.sub(r"[^a-zA-Z0-9_.-]", "", name.replace(" ", "_"))
        if not username:
            username = "user_" + uuid.uuid4().hex[:8]

        # Build request body
        body: dict[str, Any] = {"username": username}

        if telemt_quota is not None:
            body["data_quota_bytes"] = telemt_quota
        if telemt_max_ips is not None:
            body["max_unique_ips"] = telemt_max_ips
        if telemt_expiry is not None:
            body["expiration_rfc3339"] = telemt_expiry

        resp = self._api_request("POST", "/v1/users", body)
        if not resp or not resp.get("ok"):
            error_msg = "Unknown error"
            if resp and resp.get("error"):
                error_msg = resp["error"].get("message", error_msg)
            logger.error(f"Failed to add client {username}: {error_msg}")
            return {"client_id": username, "config": "", "vpn_link": ""}

        data = resp.get("data", {})
        secret = data.get("secret", "")
        link = f"tg://proxy?server={host}&port={port}&secret={secret}"
        return {"client_id": username, "config": link, "vpn_link": link}

    def edit_client(
        self,
        protocol_type: str,
        client_id: str,
        new_params: dict[str, Any],
    ) -> dict[str, str]:
        """Update an existing client via PATCH /v1/users/{username}.

        Supported params: telemt_quota, telemt_max_ips, telemt_expiry.
        """
        body: dict[str, Any] = {}
        if "telemt_quota" in new_params:
            body["data_quota_bytes"] = new_params["telemt_quota"]
        if "telemt_max_ips" in new_params:
            body["max_unique_ips"] = new_params["telemt_max_ips"]
        if "telemt_expiry" in new_params:
            body["expiration_rfc3339"] = new_params["telemt_expiry"]

        if not body:
            return {"status": "success"}

        resp = self._api_request("PATCH", f"/v1/users/{client_id}", body)
        if not resp or not resp.get("ok"):
            error_msg = "Unknown error"
            if resp and resp.get("error"):
                error_msg = resp["error"].get("message", error_msg)
            logger.error(f"Failed to edit client {client_id}: {error_msg}")
            return {"status": "error", "message": error_msg}

        return {"status": "success"}

    def remove_client(self, protocol_type: str, client_id: str) -> None:
        """Remove a client via DELETE /v1/users/{username}."""
        resp = self._api_request("DELETE", f"/v1/users/{client_id}")
        if not resp or not resp.get("ok"):
            error_msg = "Unknown error"
            if resp and resp.get("error"):
                error_msg = resp["error"].get("message", error_msg)
            logger.error(f"Failed to remove client {client_id}: {error_msg}")

    def toggle_client(
        self,
        protocol_type: str,
        client_id: str,
        enable: bool,
        restart: bool = True,
    ) -> None:
        """Toggle a client's enabled state via PATCH /v1/users/{username}.

        Uses empty secret to disable, generates a new secret to enable.
        The restart parameter is accepted for backward compatibility but
        has no effect since the API applies changes atomically.
        """
        if enable:
            # Generate a new 32-char hex secret to enable
            body: dict[str, Any] = {"secret": secrets.token_hex(16)}
        else:
            # Set empty secret to disable
            body = {"secret": ""}

        resp = self._api_request("PATCH", f"/v1/users/{client_id}", body)
        if not resp or not resp.get("ok"):
            error_msg = "Unknown error"
            if resp and resp.get("error"):
                error_msg = resp["error"].get("message", error_msg)
            logger.error(f"Failed to toggle client {client_id}: {error_msg}")

    def get_client_config(
        self,
        protocol_type: str,
        client_id: str,
        host: str = "",
        port: str = "",
    ) -> str:
        """Get the config/connection link for a specific client.

        Uses GET /v1/users/{username} to retrieve links directly.
        Falls back to get_clients() if the direct call fails.
        """
        resp = self._api_request("GET", f"/v1/users/{client_id}")
        if resp and resp.get("ok"):
            user = resp.get("data", {})
            links = user.get("links", {})
            if links.get("tls"):
                return links["tls"][0]
            if links.get("secure"):
                return links["secure"][0]
            if links.get("classic"):
                return links["classic"][0]

        # Fallback: search in all clients
        clients = self.get_clients(protocol_type)
        client = next((c for c in clients if c["clientId"] == client_id), None)
        if client:
            secret = client.get("userData", {}).get("token", "")
            if secret:
                return f"tg://proxy?server={host}&port={port}&secret={secret}"
        return "Not found"
