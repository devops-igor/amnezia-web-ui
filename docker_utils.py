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


def detect_package_manager(ssh) -> str:
    """Detect the remote server's package manager.

    Returns 'apt', 'yum', 'dnf', or 'unknown'.
    """
    _, _, code = ssh.run_command("which apt-get")
    if code == 0:
        return "apt"
    _, _, code = ssh.run_command("which yum")
    if code == 0:
        return "yum"
    _, _, code = ssh.run_command("which dnf")
    if code == 0:
        return "dnf"
    return "unknown"


def ensure_apparmor_utils(ssh) -> None:
    """Ensure apparmor_parser is available if the kernel has AppArmor enabled.

    On minimal/bare Linux systems, AppArmor can be enabled in the kernel
    but apparmor-parser missing from userspace. This causes Docker builds
    to fail because Docker tries to load the docker-default AppArmor profile
    and cannot find apparmor_parser.

    This function checks if apparmor_parser exists; if not, checks if the
    kernel has AppArmor enabled; if so, installs the apparmor package.
    """
    # Check if apparmor_parser is already available
    _, _, code = ssh.run_command("which apparmor_parser")
    if code == 0:
        return  # Already installed, nothing to do

    # Check if kernel has AppArmor enabled
    out, _, _ = ssh.run_command("cat /sys/module/apparmor/parameters/enabled 2>/dev/null")
    if "Y" not in out:
        return  # AppArmor not kernel-enabled, no need to install parser

    # Kernel has AppArmor but parser is missing — install it
    logger.info(
        "AppArmor enabled in kernel but apparmor_parser not found. " "Installing apparmor package."
    )
    pkg_mgr = detect_package_manager(ssh)
    if pkg_mgr == "apt":
        ssh.run_sudo_command("apt-get update -qq && apt-get install -y -qq apparmor")
    elif pkg_mgr == "yum":
        ssh.run_sudo_command("yum install -y apparmor")
    elif pkg_mgr == "dnf":
        ssh.run_sudo_command("dnf install -y apparmor")
    else:
        logger.warning(f"Unsupported package manager for apparmor install: {pkg_mgr}")
