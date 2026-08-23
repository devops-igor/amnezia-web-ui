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

TLS_DOMAINS = [
    "www.google.com",
    "www.cloudflare.com",
    "www.microsoft.com",
    "www.apple.com",
    "aws.amazon.com",
    "www.wikipedia.org",
]

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
    "dns": 53,
    "sip": 5060,
    "tls": 443,
}

# Fallback domains when probing fails entirely
FALLBACK_DOMAINS = {
    "quic": "google.com",
    "dns": "one.one.one.one",
    "sip": "sip.linphone.org",
    "tls": "www.google.com",
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
    """Generate a realistic QUIC Initial packet (compact 216 bytes).

    Mimics Chrome's QUIC fingerprint: first byte 0xC0 or 0xC3,
    DCID=8B random, SCID=8B random, token=0, compact realistic Initial.
    """
    TARGET = 216
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


def gen_sip(domain=None):
    """Generate a realistic SIP REGISTER packet."""
    host = domain or _rc(SIP_POOL)
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


def gen_tls(domain=None):
    """Generate a realistic TLS 1.3 / 1.2 ClientHello packet.

    Mimics a modern browser TLS ClientHello handshake record with
    valid SNI, supported groups, cipher suites, and key share.
    """
    target_domain = domain if domain else _rc(TLS_DOMAINS)
    # SNI extension (type 0x0000)
    domain_bytes = target_domain.encode("utf-8")
    sni_server_name = (
        b"\x00" + _u16(len(domain_bytes)) + domain_bytes
    )  # name_type (0 = host_name) + length + name
    sni_ext_data = _u16(len(sni_server_name)) + sni_server_name
    ext_sni = b"\x00\x00" + _u16(len(sni_ext_data)) + sni_ext_data

    # Supported groups (type 0x000a): x25519 (0x001d), secp256r1 (0x0017), secp384r1 (0x0018)
    groups = b"\x00\x1d\x00\x17\x00\x18"
    ext_groups = b"\x00\x0a" + _u16(len(groups) + 2) + _u16(len(groups)) + groups

    # EC Point Formats (type 0x000b): uncompressed (0x00)
    ext_ec_points = b"\x00\x0b\x00\x02\x01\x00"

    # Signature Algorithms (type 0x000d)
    sig_algs = b"\x04\x03\x08\x04\x04\x01\x05\x03\x08\x05\x05\x01\x08\x06\x06\x01\x02\x01"
    ext_sig_algs = b"\x00\x0d" + _u16(len(sig_algs) + 2) + _u16(len(sig_algs)) + sig_algs

    # Supported Versions (type 0x002b): TLS 1.3 (0x0304), TLS 1.2 (0x0303)
    versions = b"\x03\x04\x03\x03"
    ext_versions = b"\x00\x2b" + _u16(len(versions) + 1) + bytes([len(versions)]) + versions

    # Key Share (type 0x0033): x25519 (0x001d) + 32-byte public key
    key_pub = _rh(32)
    key_share_entry = b"\x00\x1d" + _u16(len(key_pub)) + key_pub
    key_share_data = _u16(len(key_share_entry)) + key_share_entry
    ext_key_share = b"\x00\x33" + _u16(len(key_share_data)) + key_share_data

    # ALPN (type 0x0010): h2, http/1.1
    alpn_list = b"\x02h2\x08http/1.1"
    alpn_data = _u16(len(alpn_list)) + alpn_list
    ext_alpn = b"\x00\x10" + _u16(len(alpn_data)) + alpn_data

    # Combine all extensions
    all_extensions = (
        ext_sni
        + ext_groups
        + ext_ec_points
        + ext_sig_algs
        + ext_versions
        + ext_key_share
        + ext_alpn
    )
    extensions_block = _u16(len(all_extensions)) + all_extensions

    # Client Hello payload
    client_version = b"\x03\x03"  # TLS 1.2 record version for compatibility
    client_random = _rh(32)
    session_id = _rh(32)
    session_id_block = bytes([len(session_id)]) + session_id

    # Cipher suites (32 bytes / 16 suites)
    cipher_suites = (
        b"\x13\x01\x13\x02\x13\x03"  # TLS 1.3
        b"\xc0\x2b\xc0\x2f\xc0\x2c\xc0\x30"  # ECDHE-ECDSA/RSA AES-GCM
        b"\xcc\xa9\xcc\xa8"  # CHACHA20-POLY1305
        b"\xc0\x13\xc0\x14\x00\x9c\x00\x9d\x00\x2f\x00\x35"
    )
    cipher_block = _u16(len(cipher_suites)) + cipher_suites
    compression_methods = b"\x01\x00"  # 1 compression method: null

    handshake_payload = (
        client_version
        + client_random
        + session_id_block
        + cipher_block
        + compression_methods
        + extensions_block
    )

    # Handshake header: msg_type=1 (ClientHello) + 3-byte length
    handshake_msg = b"\x01" + struct.pack(">I", len(handshake_payload))[1:] + handshake_payload

    # TLS Record header: content_type=0x16 (Handshake), version=0x0301 (TLS 1.0), length=2 bytes
    record_header = b"\x16\x03\x01" + _u16(len(handshake_msg))
    return record_header + handshake_msg


# ---- AWG binary blob formatting ----


def to_cps(raw: bytes) -> str:
    """Format raw bytes as AWG binary blob tag: <b 0xHEXSTRING>"""
    return "<b 0x%s>" % raw.hex()


# ---- Domain probing ----


def select_mimicry_domain(ssh, protocol="quic", region="world"):
    """Probe candidate domains from the AWG server via SSH and return the first reachable one.

    Args:
        ssh: SSHManager instance connected to the AWG server.
        protocol: One of 'quic', 'dns', 'sip', 'tls' — determines which domain pool to probe.
        region: World region (currently unused, reserved for future use).

    Returns:
        A reachable domain string, or the hardcoded fallback if probing fails.
    """
    import random

    domain_pool = {
        "quic": QUIC_DOMAINS,
        "dns": DNS_DOMAINS,
        "sip": SIP_DOMAINS,
        "tls": TLS_DOMAINS,
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


def generate_mimicry_packets(mimicry="auto", domain=None, ssh=None):
    """Generate I1-I5 signature packets for a specific mimicry profile.

    Profiles:
    - 'tls':  TLS ClientHello handshake (HTTPS obfuscation)
    - 'quic': QUIC Initial packet (HTTP/3 obfuscation)
    - 'dns':  DNS Query with random TXID (<r 2> DNS obfuscation)
    - 'sip':  SIP REGISTER message (VoIP obfuscation)
    - 'auto': Reachability-probed best profile (defaults to TLS or QUIC)

    Args:
        mimicry: One of 'auto', 'tls', 'quic', 'dns', 'sip'.
        domain: Optional target domain for SNI/query.
        ssh: Optional SSHManager for live domain probing.

    Returns:
        Dict with keys 'i1' through 'i5'.
    """
    m = (mimicry or "auto").lower()

    if m == "auto":
        if ssh:
            domain = select_mimicry_domain(ssh, protocol="tls")
            m = "tls"
        else:
            m = "tls"

    if m == "tls":
        target_domain = domain if domain else _rc(TLS_DOMAINS)
        return {
            "i1": to_cps(gen_tls(target_domain)),
            "i2": "",
            "i3": "",
            "i4": "",
            "i5": "",
        }
    elif m == "dns":
        dns_domain = domain if domain else "one.one.one.one"
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
    elif m == "sip":
        return {
            "i1": to_cps(gen_sip()),
            "i2": "",
            "i3": "",
            "i4": "",
            "i5": "",
        }
    elif m == "quic":
        return {
            "i1": to_cps(gen_quic_initial(domain)),
            "i2": "",
            "i3": "",
            "i4": "",
            "i5": "",
        }
    else:
        return {
            "i1": to_cps(gen_quic_initial(domain)),
            "i2": "",
            "i3": "",
            "i4": "",
            "i5": "",
        }


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


def generate_connection_kit(base_config: str, domain: str = None, ssh=None) -> dict:
    """Generate a multi-config Connection Kit (TLS, QUIC, DNS, SIP) from a base AWG config.

    Replaces or adds the I1-I5 parameters in the client configuration for each mimicry profile.
    """
    profiles = ["tls", "quic", "dns", "sip"]
    kit = {}

    for proto in profiles:
        packets = generate_mimicry_packets(mimicry=proto, domain=domain, ssh=ssh)
        lines = base_config.splitlines()
        new_lines = []
        in_interface = False
        saw_interface = False
        i_keys_added = False

        for line in lines:
            trimmed = line.strip()
            if trimmed.startswith("[Interface]"):
                in_interface = True
                saw_interface = True
                new_lines.append(line)
                continue
            elif trimmed.startswith("[") and trimmed.endswith("]"):
                if in_interface and not i_keys_added:
                    for k in ("i1", "i2", "i3", "i4", "i5"):
                        if packets.get(k):
                            new_lines.append(f"{k.upper()} = {packets[k]}")
                    i_keys_added = True
                in_interface = False
                new_lines.append(line)
                continue

            # Strip existing I1-I5 lines
            if in_interface and any(
                trimmed.upper().startswith(f"I{num}") for num in (1, 2, 3, 4, 5)
            ):
                continue

            new_lines.append(line)

        if in_interface and not i_keys_added:
            for k in ("i1", "i2", "i3", "i4", "i5"):
                if packets.get(k):
                    new_lines.append(f"{k.upper()} = {packets[k]}")
            i_keys_added = True

        kit[proto] = "\n".join(new_lines) + "\n"

    return kit
