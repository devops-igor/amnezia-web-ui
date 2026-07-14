"""Manager modules for SSH, WireGuard (AWG), Xray, Telemt, and MTProxyL."""

from app.managers.ssh_manager import SSHManager, SSHHostKeyError
from app.managers.awg_manager import (
    AWGManager,
    generate_wg_keypair,
    generate_psk,
    generate_awg_params,
)
from app.managers.xray_manager import XrayManager, XRAY_VERSION
from app.managers.telemt_manager import TelemtManager
from app.managers.mtproxyl_manager import MTProxyLManager

__all__ = [
    "SSHManager",
    "SSHHostKeyError",
    "AWGManager",
    "generate_wg_keypair",
    "generate_psk",
    "generate_awg_params",
    "XrayManager",
    "XRAY_VERSION",
    "TelemtManager",
    "MTProxyLManager",
]
