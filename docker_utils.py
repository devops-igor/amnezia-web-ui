"""Shared Docker utility functions for remote server Docker management."""

import logging

logger = logging.getLogger(__name__)


def check_docker_installed(ssh) -> bool:
    """Check if Docker is installed and running on the remote server.

    Verifies both that the docker binary exists AND the service is active.
    Uses the most comprehensive check (version + service status).

    Args:
        ssh: An SSHManager instance with .run_command() method.

    Returns:
        True if Docker is installed and the service is active/running.
    """
    out, err, code = ssh.run_command("docker --version 2>/dev/null")
    if code != 0:
        return False
    out2, _, code2 = ssh.run_command(
        "systemctl is-active docker 2>/dev/null || service docker status 2>/dev/null"
    )
    return "active" in out2 or "running" in out2.lower()
