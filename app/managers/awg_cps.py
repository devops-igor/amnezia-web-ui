"""
AWG CPS (Characteristic Packet Signature) generator for AmneziaWG 2.0 obfuscation.

Generates I1-I5 signature chain packet sizes and handles domain probing from
the AWG server for realistic mimicry domain selection.

Profile mapping:
- Lite:  No CPS (all empty), MTU 1280
- Standard: I1 only (single QUIC signature packet), MTU 1280
- Pro:    Full I1-I5 chain (protocol mimicry), MTU 1320
"""

import hashlib
import logging
import random

logger = logging.getLogger(__name__)

# Hardcoded domain pools (used as fallback when probing fails)
QUIC_DOMAINS = [
    "google.com",
    "youtube.com",
    "cdn.jsdelivr.net",
    "unpkg.com",
    "icloud.com",
    "fastly.net",
    "github.com",
]

DNS_DOMAINS = [
    "google.com",
    "cloudflare.com",
    "one.one.one.one",
]

SIP_DOMAINS = [
    "sip.zadarma.com",
    "sip.iptel.org",
    "sip.linphone.org",
]

# Protocol ports for reachability probing
PROTOCOL_PORTS = {
    "quic": 443,
    "dns": 443,
    "sip": 5060,
}

# Fallback domains when probing fails entirely
FALLBACK_DOMAINS = {
    "quic": "google.com",
    "dns": "one.one.one.one",
    "sip": "sip.linphone.org",
}


def select_mimicry_domain(ssh, protocol="quic", region="world"):
    """Probe candidate domains from the AWG server via SSH and return the first reachable one.

    Args:
        ssh: SSHManager instance connected to the AWG server.
        protocol: One of 'quic', 'dns', 'sip' — determines which domain pool to probe.
        region: World region (currently unused, reserved for future use).

    Returns:
        A reachable domain string, or the hardcoded fallback if probing fails.
    """
    domain_pool = {
        "quic": QUIC_DOMAINS,
        "dns": DNS_DOMAINS,
        "sip": SIP_DOMAINS,
    }.get(protocol, QUIC_DOMAINS)

    port = PROTOCOL_PORTS.get(protocol, 443)
    fallback = FALLBACK_DOMAINS.get(protocol, "google.com")

    # Randomize pool order so each call tries different domains first
    candidates = list(domain_pool)
    random.shuffle(candidates)

    # Try up to 5 random domains
    for domain in candidates[:5]:
        try:
            cmd = (
                f"timeout 2 bash -c 'echo > /dev/tcp/{domain}/{port}' "
                f"2>/dev/null && echo OK || echo FAIL"
            )
            out, err, code = ssh.run_command(cmd)
            if "OK" in out:
                logger.info(f"Domain {domain} is reachable on port {port}")
                return domain
        except Exception as e:
            logger.debug(f"Domain probing failed for {domain}: {e}")
            continue

    logger.warning(
        f"No reachable domains found for protocol={protocol}, " f"using fallback: {fallback}"
    )
    return fallback


def generate_cps_packets(profile, domain=None, ssh=None):
    """Generate I1-I5 CPS packet size values based on the obfuscation profile.

    Args:
        profile: One of 'lite', 'standard', 'pro'.
        domain: Optional domain to embed in CPS (used only for I1 generation
                with domain-based entropy; not written to config).
        ssh: Optional SSHManager (unused directly here; accepted for call-site
             consistency with select_mimicry_domain).

    Returns:
        Dict with keys 'i1' through 'i5', and 'cps'.
        - 'lite': all values are empty strings.
        - 'standard': i1 is a string integer (50-90), others empty, cps='signature'.
        - 'pro': i1-i5 are string integers (50-90 each), cps='signature'.
    """
    if profile == "lite":
        return {
            "i1": "",
            "i2": "",
            "i3": "",
            "i4": "",
            "i5": "",
            "cps": "",
        }

    if profile == "pro":
        # Full I1-I5 chain — all five signature packets
        i1 = str(random.randint(50, 90))
        # Use domain as entropy for I1 variation (hashes the domain to get a
        # deterministic but unique-ish offset)
        if domain:
            seed = int(hashlib.sha256(domain.encode()).hexdigest()[:8], 16) % 41
            i1 = str(50 + seed)
        i2 = str(random.randint(50, 90))
        i3 = str(random.randint(50, 90))
        i4 = str(random.randint(50, 90))
        i5 = str(random.randint(50, 90))
        return {
            "i1": i1,
            "i2": i2,
            "i3": i3,
            "i4": i4,
            "i5": i5,
            "cps": "signature",
        }

    # profile == 'standard': I1 only
    i1 = str(random.randint(50, 90))
    if domain:
        seed = int(hashlib.sha256(domain.encode()).hexdigest()[:8], 16) % 41
        i1 = str(50 + seed)
    return {
        "i1": i1,
        "i2": "",
        "i3": "",
        "i4": "",
        "i5": "",
        "cps": "signature",
    }
