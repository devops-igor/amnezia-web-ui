"""
SSH Manager - manages SSH connections to VPN servers.
Replicates the ServerController logic from the AmneziaVPN client.
"""

import paramiko
import io
import logging
from typing import Optional, Tuple

logger = logging.getLogger(__name__)


class SSHHostKeyError(Exception):
    """Raised when host key verification fails (MITM attack detected)."""

    pass


class SSHManager:
    """Manages SSH connections and command execution on remote servers."""

    def __init__(
        self,
        host,
        port,
        username,
        password=None,
        private_key=None,
        database=None,
        server_id: Optional[int] = None,
    ):
        self.host = host
        self.port = int(port)
        self.username = username
        self.password = password
        self.private_key = private_key
        self.client = None
        self._is_root = username == "root"
        self._database = database
        self._server_id = server_id

    def connect(self):
        """Establish SSH connection to the server with host key verification."""
        self.client = paramiko.SSHClient()
        self.client.set_missing_host_key_policy(paramiko.RejectPolicy())

        kwargs = {
            "hostname": self.host,
            "port": self.port,
            "username": self.username,
            "timeout": 15,
            "allow_agent": False,
            "look_for_keys": False,
        }

        if self.private_key:
            key_file = io.StringIO(self.private_key)
            try:
                pkey = paramiko.RSAKey.from_private_key(key_file)
            except paramiko.ssh_exception.SSHException:
                key_file.seek(0)
                try:
                    pkey = paramiko.Ed25519Key.from_private_key(key_file)
                except paramiko.ssh_exception.SSHException:
                    key_file.seek(0)
                    pkey = paramiko.ECDSAKey.from_private_key(key_file)
            kwargs["pkey"] = pkey
        elif self.password:
            kwargs["password"] = self.password

        # Host key verification
        known_fingerprint = None
        if self._database and self._server_id is not None:
            known_fingerprint = self._database.get_known_host_fingerprint(self._server_id)

        if known_fingerprint is None:
            # First connection — no stored fingerprint yet.
            # Temporarily allow the unknown key so we can retrieve and store it.
            self.client.set_missing_host_key_policy(paramiko.AutoAddPolicy())
        # else: RejectPolicy stays (set above) — paramiko will reject unknown keys.

        self.client.connect(**kwargs)

        # After successful connect, retrieve the server's actual host key
        transport = self.client.get_transport()
        host_key = transport.get_remote_server_key()
        actual_fingerprint = host_key.get_fingerprint()

        # If bytes, convert to hex string for consistent storage/comparison
        if isinstance(actual_fingerprint, bytes):
            actual_fingerprint = actual_fingerprint.hex()

        if known_fingerprint is None and self._database and self._server_id is not None:
            # First connection — store the fingerprint for future verification
            self._database.save_known_host_fingerprint(self._server_id, actual_fingerprint)
            logger.warning(
                "New host key for %s: %s (stored for future connections)",
                self.host,
                actual_fingerprint,
            )
        elif known_fingerprint is not None and actual_fingerprint != known_fingerprint:
            # Fingerprint mismatch — possible MITM attack
            logger.error(
                "Host key mismatch for %s: expected %s, got %s",
                self.host,
                known_fingerprint,
                actual_fingerprint,
            )
            self.client.close()
            raise SSHHostKeyError(
                f"Host key mismatch for {self.host}. "
                f"This may indicate a man-in-the-middle attack. "
                f"Expected: {known_fingerprint[:16]}..., "
                f"Got: {actual_fingerprint[:16]}..."
            )

        # Switch back to RejectPolicy for the rest of the session
        self.client.set_missing_host_key_policy(paramiko.RejectPolicy())

        return True

    def disconnect(self):
        """Close SSH connection."""
        if self.client:
            self.client.close()
            self.client = None

    def run_command(self, command, timeout=60):
        """Execute command on remote server."""
        if not self.client:
            raise ConnectionError("Not connected to server")

        logger.info("Running command: %s...", command[:100])
        stdin, stdout, stderr = self.client.exec_command(command, timeout=timeout)
        exit_code = stdout.channel.recv_exit_status()
        out = stdout.read().decode("utf-8", errors="replace").strip()
        err = stderr.read().decode("utf-8", errors="replace").strip()

        if exit_code != 0:
            logger.warning("Command exited with code %s: %s", exit_code, err)

        return out, err, exit_code

    def _run_sudo_command(self, command: str, timeout: int = 60) -> Tuple[str, str, int]:
        """
        Execute command with sudo using stdin for password delivery.

        This method avoids shell string interpolation of the password,
        preventing command injection through specially crafted passwords.
        """
        if not self.client:
            raise ConnectionError("Not connected to server")

        logger.info("Running sudo command: %s...", command[:100])

        # Execute sudo command directly - password will be written to stdin
        stdin, stdout, stderr = self.client.exec_command(f"sudo {command}", timeout=timeout)

        # Send password via stdin - never interpolated into shell string
        if self.password:
            stdin.write(self.password + "\n")
            stdin.flush()
            stdin.channel.shutdown_write()

        exit_code = stdout.channel.recv_exit_status()
        out = stdout.read().decode("utf-8", errors="replace").strip()
        err = stderr.read().decode("utf-8", errors="replace").strip()

        if exit_code != 0:
            logger.warning("Sudo command exited with code %s: %s", exit_code, err)

        return out, err, exit_code

    def run_sudo_command(self, command, timeout=60):
        """
        Execute command with sudo, automatically handling password.

        Strips 'sudo ' from the beginning of command if present,
        and re-adds it with password handling via stdin.
        """
        # Remove existing sudo prefix if present
        clean_cmd = command
        if clean_cmd.strip().startswith("sudo "):
            clean_cmd = clean_cmd.strip()[5:]

        if self._is_root:
            return self.run_command(clean_cmd, timeout=timeout)

        return self._run_sudo_command(clean_cmd, timeout=timeout)

    def run_sudo_script(self, script, timeout=120):
        """
        Execute a multi-line script with sudo/root privileges.
        Writes script to /tmp via SFTP, then runs with sudo bash.
        """
        if self._is_root:
            return self.run_script(script, timeout=timeout)

        # Write script to temp file via SFTP (avoids heredoc/pipe conflicts)
        import hashlib

        script_hash = hashlib.md5(script.encode()).hexdigest()[:8]
        tmp_script = f"/tmp/_amnz_script_{script_hash}.sh"
        self.upload_file(script, tmp_script)

        # Move to target with sudo
        self._run_sudo_command(
            f"mv {tmp_script} /root/ 2>/dev/null || mv {tmp_script} {tmp_script}"
        )
        result = self._run_sudo_command(f"bash {tmp_script}; rm -f {tmp_script}", timeout=timeout)
        return result

    def run_script(self, script, timeout=120):
        """Execute a multi-line script on remote server."""
        return self.run_command(script, timeout=timeout)

    def upload_file(self, content, remote_path):
        """Upload text content to a remote file via SFTP."""
        if not self.client:
            raise ConnectionError("Not connected to server")

        # Normalize line endings (Windows CRLF -> Unix LF)
        content = content.replace("\r\n", "\n")

        sftp = self.client.open_sftp()
        try:
            with sftp.file(remote_path, "w") as f:
                f.write(content)
        finally:
            sftp.close()

    def upload_file_sudo(self, content, remote_path):
        """
        Upload text content to a remote file that requires root access.
        Uses SFTP to write to /tmp, then sudo mv to the target path.
        Also normalizes line endings to Unix-style (LF).
        """
        if not self.client:
            raise ConnectionError("Not connected to server")

        # Normalize line endings (Windows CRLF -> Unix LF)
        content = content.replace("\r\n", "\n")

        # Write to temp file via SFTP (no sudo needed for /tmp)
        import hashlib

        tmp_name = f"/tmp/_amnz_{hashlib.md5(remote_path.encode()).hexdigest()[:8]}"
        self.upload_file(content, tmp_name)

        # Move to target with sudo
        self.run_sudo_command(f"mv {tmp_name} {remote_path}")
        self.run_sudo_command(f"chmod 644 {remote_path}")
        return True

    def download_file(self, remote_path):
        """Download text content from a remote file."""
        if not self.client:
            raise ConnectionError("Not connected to server")

        sftp = self.client.open_sftp()
        try:
            with sftp.file(remote_path, "r") as f:
                return f.read().decode("utf-8", errors="replace")
        finally:
            sftp.close()

    def file_exists(self, remote_path):
        """Check if a remote file exists."""
        if not self.client:
            raise ConnectionError("Not connected to server")

        sftp = self.client.open_sftp()
        try:
            sftp.stat(remote_path)
            return True
        except FileNotFoundError:
            return False
        finally:
            sftp.close()

    def test_connection(self):
        """Test SSH connection and return server info."""
        out, err, code = self.run_command("uname -sr && cat /etc/os-release 2>/dev/null | head -2")
        return out

    def write_file(self, remote_path, content):
        """Write content to a remote file with sudo."""
        return self.upload_file_sudo(content, remote_path)

    def __enter__(self):
        self.connect()
        return self

    def __exit__(self, *args):
        self.disconnect()
