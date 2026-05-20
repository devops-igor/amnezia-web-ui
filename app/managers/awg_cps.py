"""
AWG CPS (Characteristic Packet Signature) generator for AmneziaWG 2.0 obfuscation.

Generates I1-I5 signature chain packets as hex-encoded binary blobs in
<b 0xHEXSTRING> format, ported from pumbaX awg2.sh. I1-I5 are CLIENT-only
parameters — they are never written to server config.

Profile mapping:
- Lite:  DNS I1 signature, junk only — max compatibility, MTU 1280
- Standard: QUIC I1 signature, balanced obfuscation — recommended, MTU 1280
- Pro:    Full I1-I5 QUIC chain, maximum obfuscation — more overhead, MTU 1320
"""

import secrets
import struct
import logging

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

# SIP domain and User-Agent pools for gen_sip()
SIP_POOL = [
    "sipgate.de",
    "sip.ovh.net",
    "sip.voipfone.co.uk",
    "sip.linphone.org",
    "sip.zadarma.com",
    "sip.dus.net",
    "sip.easybell.de",
    "sip.1und1.de",
    "sip.voys.nl",
    "sip.antisip.com",
    "sip.iptel.org",
    "sip.voipgate.com",
]

SIP_UA_POOL = [
    "Linphone/5.2.5 (belle-sip/5.2.0)",
    "Zoiper rv2.10.20.4",
    "MicroSIP/3.21.4",
    "Bria 6.5.1",
    "PortSIP UA 16.4",
]


# ---- Cryptographic helpers (ported from pumbaX awg2.sh) ----


def _rh(n):
    """Cryptographically secure random bytes."""
    return secrets.token_bytes(n)


def _ri(a, b):
    """Cryptographically secure random int in [a, b]."""
    if a > b:
        a, b = b, a
    return a + secrets.randbelow(b - a + 1)


def _rc(lst):
    """Cryptographically secure random choice."""
    return lst[secrets.randbelow(len(lst))]


def _u16(v):
    return struct.pack(">H", v & 0xFFFF)


def _u32(v):
    return struct.pack(">I", v & 0xFFFFFFFF)


def _rand_private_ip():
    """Generate a random private IP address."""
    kind = secrets.randbelow(3)
    if kind == 0:
        return "10.%d.%d.%d" % (_ri(1, 254), _ri(0, 255), _ri(2, 254))
    elif kind == 1:
        return "172.%d.%d.%d" % (_ri(16, 31), _ri(0, 255), _ri(2, 254))
    else:
        return "192.168.%d.%d" % (_ri(0, 255), _ri(2, 254))


# ---- Binary packet generators (ported from pumbaX awg2.sh) ----


def gen_quic_initial(domain=None):
    """Generate a realistic QUIC Initial packet (exactly 1200 bytes).

    Mimics Chrome's QUIC fingerprint: first byte 0xC0 or 0xC3,
    DCID=8B random, SCID=8B random, token=0, padded to 1200 bytes.
    """
    TARGET = 1200
    fb = _rc([0xC0, 0xC0, 0xC0, 0xC3])
    pn_len = (fb & 0x03) + 1
    dcid = _rh(8)
    scid = _rh(8)
    enc_size = TARGET - 26 - pn_len
    if enc_size < 1:
        enc_size = 1
    plen_val = pn_len + enc_size
    pl_varint = _u16(0x4000 | plen_val)
    pn = _rh(pn_len)
    payload = _rh(enc_size)
    pkt = (
        bytes([fb])
        + b"\x00\x00\x00\x01"
        + bytes([8])
        + dcid
        + bytes([8])
        + scid
        + b"\x00"
        + pl_varint
        + pn
        + payload
    )
    if len(pkt) < TARGET:
        pkt += _rh(TARGET - len(pkt))
    else:
        pkt = pkt[:TARGET]
    return pkt


def gen_quic_short():
    """Generate a QUIC Short Header (1-RTT) packet (50-100 bytes).

    Mimics Chrome's short header with random spin/key bits.
    """
    pn_len = _ri(1, 4)
    spin = _ri(0, 1) << 5
    key = _ri(0, 1) << 2
    fb = 0x40 | spin | key | (pn_len - 1)
    dcid = _rh(8)
    pn = _rh(pn_len)
    data = _rh(_ri(40, 90))
    return bytes([fb]) + dcid + pn + data


def gen_dns(domain):
    """Generate a DNS query payload for the given domain.

    Produces a realistic DNS query with EDNS0 OPT-RR and random TXID.
    The caller wraps the result with <r 2> prefix for the TXID.
    """
    flags = b"\x01\x00"  # QR=0 Query, RD=1
    counts = b"\x00\x01\x00\x00\x00\x00\x00\x01"
    qn = b""
    for lbl in domain.split("."):
        lbl_b = lbl.encode()[:63]
        qn += bytes([len(lbl_b)]) + lbl_b
    qn += b"\x00"
    qtype = b"\x00\x01"  # A record
    qclass = b"\x00\x01"  # IN
    udp_size = _rc([1232, 4096])
    do_bit = _rc([0x0000, 0x8000])
    opt_rr = b"\x00" + b"\x00\x29" + _u16(udp_size) + b"\x00\x00" + _u16(do_bit) + b"\x00\x00"
    return flags + counts + qn + qtype + qclass + opt_rr


def gen_sip():
    """Generate a realistic SIP REGISTER packet."""
    host = _rc(SIP_POOL)
    user = _rc(["alice", "bob", "100", "200", "sip", "user", "client"]) + str(_ri(10, 9999))
    lip = _rand_private_ip()
    lport = _rc([5060, 5062, 5080, 5160, str(_ri(10000, 65000))])
    if isinstance(lport, int):
        lport = str(lport)
    branch = "z9hG4bK" + secrets.token_hex(7)
    tag = secrets.token_hex(4)
    callid = "%s@%s" % (secrets.token_hex(8), host)
    cseq = _ri(1, 50)
    transport = _rc(["udp", "udp", "udp", "udp", "tcp"])
    user_agent = _rc(SIP_UA_POOL)
    lines = [
        "REGISTER sip:%s SIP/2.0" % host,
        "Via: SIP/2.0/%s %s:%s;branch=%s;rport" % (transport.upper(), lip, lport, branch),
        "Max-Forwards: 70",
        "From: <sip:%s@%s>;tag=%s" % (user, host, tag),
        "To: <sip:%s@%s>" % (user, host),
        "Call-ID: %s" % callid,
        "CSeq: %d REGISTER" % cseq,
        "Contact: <sip:%s@%s:%s;transport=%s>" % (user, lip, lport, transport),
        "User-Agent: %s" % user_agent,
        (
            "Allow: INVITE, ACK, CANCEL, BYE, REFER, OPTIONS, "
            "NOTIFY, SUBSCRIBE, PRACK, MESSAGE, INFO, UPDATE"
        ),
        "Supported: replaces, outbound, gruu, path",
        "Expires: %s" % _rc(["300", "600", "1800", "3600"]),
        "Content-Length: 0",
        "",
        "",
    ]
    return "\r\n".join(lines).encode()


# ---- AWG binary blob formatting ----


def to_cps(raw: bytes) -> str:
    """Format raw bytes as AWG binary blob tag: <b 0xHEXSTRING>"""
    return "<b 0x%s>" % raw.hex()


# ---- Domain probing ----


def select_mimicry_domain(ssh, protocol="quic", region="world"):
    """Probe candidate domains from the AWG server via SSH and return the first reachable one.

    Args:
        ssh: SSHManager instance connected to the AWG server.
        protocol: One of 'quic', 'dns', 'sip' — determines which domain pool to probe.
        region: World region (currently unused, reserved for future use).

    Returns:
        A reachable domain string, or the hardcoded fallback if probing fails.
    """
    import random

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


# ---- CPS packet generation ----


def generate_cps_packets(profile, domain=None, ssh=None):
    """Generate I1-I5 CPS packet values as hex-encoded binary blobs.

    CLIENT-only: I1-I5 should only appear in client configs, never server.
    CPS key is NOT included in the return dict (CPS=signature is invalid in AWG 2.0).

    Args:
        profile: One of 'lite', 'standard', 'pro'.
        domain: Optional domain for DNS/SIP packet generation.
        ssh: Optional SSHManager (unused directly; accepted for call-site consistency).

    Returns:
        Dict with keys 'i1' through 'i5' (no 'cps' key).
        Values are empty strings (disabled) or hex blob strings in <b 0xHEX> format.
    """
    if profile == "lite":
        # Lite: DNS I1 signature packet, no I2-I5
        dns_domain = domain if domain else "icloud.com"
        dns_payload = gen_dns(dns_domain)
        dns_txid = _rh(2)
        i1_raw = dns_txid + dns_payload
        return {
            "i1": "<r 2><b 0x%s>" % i1_raw.hex(),
            "i2": "",
            "i3": "",
            "i4": "",
            "i5": "",
        }

    if profile == "pro":
        # Full I1-I5 QUIC chain
        return {
            "i1": to_cps(gen_quic_initial(domain)),
            "i2": to_cps(gen_quic_short()),
            "i3": to_cps(gen_quic_short()),
            "i4": to_cps(gen_quic_short()),
            "i5": to_cps(gen_quic_short()),
        }

    # profile == 'standard': I1 only (QUIC Initial)
    return {
        "i1": to_cps(gen_quic_initial(domain)),
        "i2": "",
        "i3": "",
        "i4": "",
        "i5": "",
    }
