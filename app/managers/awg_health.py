"""
AmneziaWG Health Check and Protocol Client implementation.

Provides pure-Python AmneziaWG (AWG) / WireGuard handshake protocol exchange
over UDP to perform real server reachability monitoring and Auto Trials
health checks against remote VPN servers without third-party binaries.

Supported features:
- WireGuard Noise_IKpsk2_25519_ChaChaPoly_BLAKE2s handshake
- AmneziaWG custom magic headers (H1, H2, H3, H4)
- AmneziaWG random padding and junk sizes (S1, S2, S3, S4)
- AmneziaWG junk packet bursts (Jc, Jmin, Jmax)
- AmneziaWG Characteristic Packet Signatures (I1-I5 / CPS: TLS, QUIC, DNS, SIP)
- Real round-trip latency measurement and cryptographic response validation
- Auto Trials profile reachability probing
"""

import asyncio
import base64
import hashlib
import hmac
import logging
import secrets
import socket
import struct
import time
from datetime import datetime
from typing import Any, Dict, Optional, Tuple

from cryptography.hazmat.primitives import serialization
from cryptography.hazmat.primitives.asymmetric.x25519 import X25519PrivateKey, X25519PublicKey
from cryptography.hazmat.primitives.ciphers.aead import ChaCha20Poly1305

from app.managers.awg_cps import generate_mimicry_packets

logger = logging.getLogger(__name__)

# Noise Protocol Constants (WireGuard / AmneziaWG)
INITIAL_CHAIN_KEY = hashlib.blake2s(b"Noise_IKpsk2_25519_ChaChaPoly_BLAKE2s").digest()
INITIAL_HASH = hashlib.blake2s(INITIAL_CHAIN_KEY + b"WireGuard v1 zx2c4 Jason@zx2c4.com").digest()
LABEL_MAC1 = b"mac1----"

# Default AWG magic headers & sizes
DEFAULT_H1 = 1020325451
DEFAULT_H2 = 3288052141
DEFAULT_S1 = 15
DEFAULT_S2 = 18


def _hmac_blake2s(key: bytes, data: bytes) -> bytes:
    """Compute HMAC using BLAKE2s hash algorithm."""
    return hmac.new(key, data, digestmod=hashlib.blake2s).digest()


def _kdf1(key: bytes, data: bytes) -> bytes:
    """Noise KDF1: derive a new chaining key."""
    prk = _hmac_blake2s(key, data)
    return _hmac_blake2s(prk, b"\x01")


def _kdf2(key: bytes, data: bytes) -> Tuple[bytes, bytes]:
    """Noise KDF2: derive a new chaining key and encryption key."""
    prk = _hmac_blake2s(key, data)
    t1 = _hmac_blake2s(prk, b"\x01")
    t2 = _hmac_blake2s(prk, t1 + b"\x02")
    return t1, t2


def _kdf3(key: bytes, data: bytes) -> Tuple[bytes, bytes, bytes]:
    """Noise KDF3: derive chaining key, tau hash, and encryption key."""
    prk = _hmac_blake2s(key, data)
    t1 = _hmac_blake2s(prk, b"\x01")
    t2 = _hmac_blake2s(prk, t1 + b"\x02")
    t3 = _hmac_blake2s(prk, t2 + b"\x03")
    return t1, t2, t3


def _decode_key(key_val: Any) -> bytes:
    """Decode a base64 or raw 32-byte key."""
    if isinstance(key_val, bytes):
        if len(key_val) == 32:
            return key_val
        key_val = key_val.decode("utf-8")
    if isinstance(key_val, str):
        key_str = key_val.strip()
        decoded = base64.b64decode(key_str)
        if len(decoded) == 32:
            return decoded
        raise ValueError(f"Decoded key must be 32 bytes, got {len(decoded)}")
    raise TypeError(f"Invalid key type: {type(key_val)}")


def _parse_magic_header_range(header_val: Any, default: int) -> Tuple[int, int]:
    """Parse an integer or range string 'MIN-MAX' into (min_val, max_val)."""
    if header_val is None:
        return default, default
    if isinstance(header_val, int):
        return header_val, header_val
    if isinstance(header_val, str):
        val = header_val.strip()
        if "-" in val:
            parts = val.split("-", 1)
            try:
                min_v = int(parts[0].strip())
                max_v = int(parts[1].strip())
                return min(min_v, max_v), max(min_v, max_v)
            except ValueError:
                return default, default
        try:
            v = int(val)
            return v, v
        except ValueError:
            return default, default
    return default, default


def parse_cps_blob(tag_str: str) -> bytes:
    """Parse AWG binary tag string format '<b 0xHEX>' or '<r N><b 0xHEX>' to bytes."""
    if not tag_str or not isinstance(tag_str, str):
        return b""
    s = tag_str.strip()
    r_count = 0
    if s.startswith("<r "):
        r_end = s.find(">")
        if r_end != -1:
            try:
                r_count = int(s[3:r_end].strip())
            except ValueError:
                r_count = 0
            s = s[r_end + 1 :].strip()

    if s.startswith("<b 0x") and s.endswith(">"):
        hex_data = s[5:-1].strip()
        try:
            raw = bytes.fromhex(hex_data)
            if r_count > 0 and len(raw) >= r_count:
                # Replace first r_count bytes with random bytes
                raw = secrets.token_bytes(r_count) + raw[r_count:]
            return raw
        except ValueError:
            return b""
    return b""


class NoiseClientState:
    """Maintains Noise protocol handshake state across message round-trips."""

    def __init__(
        self,
        h: bytes,
        ck: bytes,
        client_e_priv: X25519PrivateKey,
        client_priv: X25519PrivateKey,
        server_pub: X25519PublicKey,
        psk: bytes,
        sender_index: int,
        mac1_key: bytes,
    ) -> None:
        self.h = h
        self.ck = ck
        self.client_e_priv = client_e_priv
        self.client_priv = client_priv
        self.server_pub = server_pub
        self.psk = psk
        self.sender_index = sender_index
        self.mac1_key = mac1_key


def build_awg_initiation_packet(
    server_public_key: Any,
    client_private_key: Optional[Any] = None,
    psk: Optional[Any] = None,
    awg_params: Optional[Dict[str, Any]] = None,
) -> Tuple[bytes, NoiseClientState]:
    """Build an AmneziaWG / WireGuard Handshake Initiation packet.

    Args:
        server_public_key: Server's static public key (base64 string or 32 bytes).
        client_private_key: Client's static private key (base64 string, 32 bytes, or None to generate).
        psk: Optional preshared key (base64 string, 32 bytes, or None for zeros).
        awg_params: Dictionary of AWG parameters (H1, H2, S1, S2, etc.).

    Returns:
        Tuple of (packet_bytes, noise_state).
    """
    server_pub_bytes = _decode_key(server_public_key)
    server_pub = X25519PublicKey.from_public_bytes(server_pub_bytes)

    if client_private_key is not None:
        client_priv_bytes = _decode_key(client_private_key)
        client_priv = X25519PrivateKey.from_private_bytes(client_priv_bytes)
    else:
        client_priv = X25519PrivateKey.generate()

    client_pub_bytes = client_priv.public_key().public_bytes(
        encoding=serialization.Encoding.Raw, format=serialization.PublicFormat.Raw
    )

    psk_bytes = _decode_key(psk) if psk else (b"\x00" * 32)
    params = awg_params or {}

    h1_min, h1_max = _parse_magic_header_range(
        params.get("init_packet_magic_header") or params.get("h1"), DEFAULT_H1
    )
    if h1_min == h1_max:
        h1 = h1_min
    else:
        h1 = secrets.randbelow(h1_max - h1_min + 1) + h1_min

    try:
        s1 = int(params.get("init_packet_junk_size") or params.get("s1") or DEFAULT_S1)
    except (ValueError, TypeError):
        s1 = DEFAULT_S1

    # Initialize Noise state
    h = hashlib.blake2s(INITIAL_HASH + server_pub_bytes).digest()
    ck = INITIAL_CHAIN_KEY

    # Ephemeral key generation
    client_e_priv = X25519PrivateKey.generate()
    client_e_pub_bytes = client_e_priv.public_key().public_bytes(
        encoding=serialization.Encoding.Raw, format=serialization.PublicFormat.Raw
    )

    # MixHash(e_pub) & MixKey(e_pub)
    h = hashlib.blake2s(h + client_e_pub_bytes).digest()
    ck = _kdf1(ck, client_e_pub_bytes)

    # ss = DH(e, S_server)
    ss = client_e_priv.exchange(server_pub)
    ck, key = _kdf2(ck, ss)

    # Encrypt client static key
    nonce0 = b"\x00" * 12
    cipher = ChaCha20Poly1305(key)
    encrypted_static = cipher.encrypt(nonce0, client_pub_bytes, h)
    h = hashlib.blake2s(h + encrypted_static).digest()

    # ss = DH(s, S_server)
    ss2 = client_priv.exchange(server_pub)
    ck, key = _kdf2(ck, ss2)

    # TAI64N timestamp (base 0x400000000000000a with leap second offset)
    now_sec = int(time.time())
    tai_sec = 0x400000000000000A + now_sec
    tai_nsec = int((time.time() - now_sec) * 1e9) & ~0xFFFFFF
    tai64n = struct.pack(">QI", tai_sec, tai_nsec)

    cipher2 = ChaCha20Poly1305(key)
    encrypted_timestamp = cipher2.encrypt(nonce0, tai64n, h)
    h = hashlib.blake2s(h + encrypted_timestamp).digest()

    # Sender index & magic header
    sender_idx = secrets.randbelow(0xFFFFFFFF)
    msg_type_bytes = struct.pack("<I", h1)
    sender_idx_bytes = struct.pack("<I", sender_idx)

    # Build the 116-byte message body (without MACs, without padding)
    msg_body = (
        msg_type_bytes
        + sender_idx_bytes
        + client_e_pub_bytes
        + encrypted_static
        + encrypted_timestamp
    )

    # Compute MAC1 over ONLY the 116-byte message body (no padding, no MACs)
    mac1_key = hashlib.blake2s(LABEL_MAC1 + server_pub_bytes).digest()
    mac1 = hashlib.blake2s(msg_body, digest_size=16, key=mac1_key).digest()
    mac2 = b"\x00" * 16

    # Full 148-byte message: body + MAC1 + MAC2
    message = msg_body + mac1 + mac2

    # Prepend S1 random padding at the FRONT of the wire packet (matching Go send.go)
    padding = secrets.token_bytes(s1) if s1 > 0 else b""
    packet = padding + message

    state = NoiseClientState(
        h=h,
        ck=ck,
        client_e_priv=client_e_priv,
        client_priv=client_priv,
        server_pub=server_pub,
        psk=psk_bytes,
        sender_index=sender_idx,
        mac1_key=mac1_key,
    )
    return packet, state


def verify_awg_response_packet(
    resp_packet: bytes, state: NoiseClientState, awg_params: Optional[Dict[str, Any]] = None
) -> bool:
    """Verify and authenticate an AmneziaWG Handshake Response packet.

    Args:
        resp_packet: Raw bytes received from server.
        state: NoiseClientState from the initiation phase.
        awg_params: Dictionary of AWG parameters (H2, S2, etc.).

    Returns:
        True if the packet header, indices, and Noise authentication tag are valid.
    """
    params = awg_params or {}
    h2_min, h2_max = _parse_magic_header_range(
        params.get("response_packet_magic_header") or params.get("h2"), DEFAULT_H2
    )

    try:
        s2 = int(params.get("response_packet_junk_size") or params.get("s2") or DEFAULT_S2)
    except (ValueError, TypeError):
        s2 = DEFAULT_S2

    expected_len = s2 + 92  # minimum total packet size
    if len(resp_packet) < expected_len:
        logger.debug(
            "AWG response packet too short: %d bytes, expected >= %d",
            len(resp_packet),
            expected_len,
        )
        return False

    # Skip S2 padding bytes at the start (matching Go receive.go)
    offset = s2
    msg_type = struct.unpack("<I", resp_packet[offset : offset + 4])[0]
    if not (h2_min <= msg_type <= h2_max or msg_type == 2):
        logger.debug(
            "AWG response magic header mismatch: expected in range [%d, %d] or 2, got %d",
            h2_min,
            h2_max,
            msg_type,
        )
        return False

    receiver_idx = struct.unpack("<I", resp_packet[offset + 8 : offset + 12])[0]
    if receiver_idx != state.sender_index:
        logger.debug(
            "AWG receiver index mismatch: expected %d, got %d", state.sender_index, receiver_idx
        )
        return False

    server_e_pub_bytes = resp_packet[offset + 12 : offset + 44]
    encrypted_empty = resp_packet[offset + 44 : offset + 60]
    # MAC1 is at resp_packet[offset + 60 : offset + 76]
    # MAC2 is at resp_packet[offset + 76 : offset + 92]

    try:
        server_e_pub = X25519PublicKey.from_public_bytes(server_e_pub_bytes)
    except Exception as e:
        logger.debug("Invalid server ephemeral key: %s", e)
        return False

    try:
        # Complete Noise handshake verification
        h = hashlib.blake2s(state.h + server_e_pub_bytes).digest()
        ck = _kdf1(state.ck, server_e_pub_bytes)

        ss3 = state.client_e_priv.exchange(server_e_pub)
        ck = _kdf1(ck, ss3)

        ss4 = state.client_priv.exchange(server_e_pub)
        ck = _kdf1(ck, ss4)

        ck, tau, key = _kdf3(ck, state.psk)
        h = hashlib.blake2s(h + tau).digest()

        nonce0 = b"\x00" * 12
        cipher = ChaCha20Poly1305(key)
        decrypted = cipher.decrypt(nonce0, encrypted_empty, h)
        return decrypted == b""
    except Exception as e:
        logger.debug("AWG response Noise authentication failed: %s", e)
        return False


def perform_awg_handshake(
    host: str,
    port: int,
    server_public_key: Any,
    client_private_key: Optional[Any] = None,
    psk: Optional[Any] = None,
    awg_params: Optional[Dict[str, Any]] = None,
    mimicry_profile: Optional[str] = None,
    timeout: float = 3.0,
) -> Dict[str, Any]:
    """Execute a synchronous AmneziaWG UDP handshake probe.

    Args:
        host: Target server hostname or IP.
        port: Target AWG UDP port.
        server_public_key: Server's public key (base64 string or bytes).
        client_private_key: Client's private key (base64 string or None).
        psk: Optional preshared key.
        awg_params: AWG obfuscation parameters dict.
        mimicry_profile: Optional profile name ('tls', 'quic', 'dns', 'sip').
        timeout: Socket timeout in seconds.

    Returns:
        Dict with status, latency_ms, reachable, handshake_complete, error.
    """
    params = dict(awg_params or {})

    # Generate mimicry packets if profile specified
    preambles = []
    if mimicry_profile:
        mimicry_packets = generate_mimicry_packets(mimicry=mimicry_profile)
        for k in ("i1", "i2", "i3", "i4", "i5"):
            blob_str = mimicry_packets.get(k)
            if blob_str:
                raw_blob = parse_cps_blob(blob_str)
                if raw_blob:
                    preambles.append(raw_blob)
    else:
        # Check explicit I1-I5 in params
        for k in ("i1", "i2", "i3", "i4", "i5"):
            val = params.get(k)
            if val:
                raw_blob = parse_cps_blob(val)
                if raw_blob:
                    preambles.append(raw_blob)

    # Check junk packet count (Jc)
    try:
        jc = int(params.get("junk_packet_count") or params.get("jc") or 0)
        jmin = int(params.get("junk_packet_min_size") or params.get("jmin") or 10)
        jmax = int(params.get("junk_packet_max_size") or params.get("jmax") or 30)
        if jc > 0 and jmax >= jmin > 0:
            valid_mimicry_count = sum(1 for p in preambles if jmin <= len(p) <= jmax)
            jc_to_add = max(0, jc - valid_mimicry_count)
            for _ in range(jc_to_add):
                pkt_len = secrets.randbelow(jmax - jmin + 1) + jmin
                preambles.append(secrets.token_bytes(pkt_len))
            logger.debug(
                "AWG junk packets: Jc=%s Jmin=%s Jmax=%s valid_mimicry=%s added=%s",
                jc,
                jmin,
                jmax,
                valid_mimicry_count,
                jc_to_add,
            )
    except (ValueError, TypeError):
        pass

    t0 = time.time()
    logger.debug(
        "AWG handshake start host=%s port=%s profile=%s client_key_provided=%s "
        "preamble_count=%s preamble_sizes=%s",
        host,
        port,
        mimicry_profile or "default",
        bool(client_private_key),
        len(preambles),
        [len(p) for p in preambles],
    )
    try:
        init_packet, state = build_awg_initiation_packet(
            server_public_key=server_public_key,
            client_private_key=client_private_key,
            psk=psk,
            awg_params=params,
        )

        sock = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
        sock.settimeout(timeout)

        # Send preamble packets (CPS / junk)
        for p_pkt in preambles:
            sock.sendto(p_pkt, (host, port))

        # Send Handshake Initiation
        sock.sendto(init_packet, (host, port))

        # Receive Response (in loop to handle potential junk/mimicry packets from server)
        valid = False
        received_count = 0
        deadline = t0 + timeout
        while time.time() < deadline:
            remaining = max(0.05, deadline - time.time())
            sock.settimeout(remaining)
            try:
                resp_data, _ = sock.recvfrom(2048)
                received_count += 1
                if verify_awg_response_packet(resp_data, state, params):
                    valid = True
                    break
            except socket.timeout:
                break
        sock.close()
        if not valid and received_count == 0:
            raise socket.timeout("Handshake timeout (no response from server)")
        t1 = time.time()
        latency = max(1, int((t1 - t0) * 1000))
        if valid:
            return {
                "reachable": True,
                "latency_ms": latency,
                "protocol": "awg",
                "handshake_complete": True,
                "profile": mimicry_profile or "default",
                "last_checked": datetime.now().isoformat(),
                "error": "",
            }
        else:
            logger.debug(
                "AWG handshake failed for %s:%s profile=%s: response verification failed",
                host,
                port,
                mimicry_profile or "default",
            )
            return {
                "reachable": False,
                "latency_ms": latency,
                "protocol": "awg",
                "handshake_complete": False,
                "profile": mimicry_profile or "default",
                "last_checked": datetime.now().isoformat(),
                "error": "Handshake response verification failed",
            }
    except socket.timeout:
        logger.debug(
            "AWG handshake timed out for %s:%s profile=%s after %.1fs "
            "(client_key_provided=%s, preamble_count=%s)",
            host,
            port,
            mimicry_profile or "default",
            timeout,
            bool(client_private_key),
            len(preambles),
        )
        return {
            "reachable": False,
            "latency_ms": 0,
            "protocol": "awg",
            "handshake_complete": False,
            "profile": mimicry_profile or "default",
            "last_checked": datetime.now().isoformat(),
            "error": "Handshake timeout (no response from server)",
        }
    except Exception as e:
        logger.debug("AWG handshake error for %s:%s - %s", host, port, e)
        return {
            "reachable": False,
            "latency_ms": 0,
            "protocol": "awg",
            "handshake_complete": False,
            "profile": mimicry_profile or "default",
            "last_checked": datetime.now().isoformat(),
            "error": str(e),
        }


async def check_awg_reachability(
    host: str,
    port: int,
    server_public_key: Any,
    client_private_key: Optional[Any] = None,
    psk: Optional[Any] = None,
    awg_params: Optional[Dict[str, Any]] = None,
    mimicry_profile: Optional[str] = None,
    timeout: float = 3.0,
) -> Dict[str, Any]:
    """Asynchronous wrapper around perform_awg_handshake."""
    return await asyncio.to_thread(
        perform_awg_handshake,
        host=host,
        port=port,
        server_public_key=server_public_key,
        client_private_key=client_private_key,
        psk=psk,
        awg_params=awg_params,
        mimicry_profile=mimicry_profile,
        timeout=timeout,
    )


async def run_auto_trial_profiles(
    host: str,
    port: int,
    server_public_key: Any,
    client_private_key: Optional[Any] = None,
    psk: Optional[Any] = None,
    awg_params: Optional[Dict[str, Any]] = None,
    timeout: float = 2.5,
) -> Dict[str, Dict[str, Any]]:
    """Test all AWG mimicry profiles (TLS, QUIC, DNS, SIP) using real AWG handshakes.

    Returns:
        Dict mapping profile name -> health check result dict.
    """
    profiles = ["tls", "quic", "dns", "sip"]
    results: Dict[str, Dict[str, Any]] = {}

    for proto in profiles:
        logger.debug(
            "Running AWG auto-trial health check for profile=%s host=%s:%s", proto, host, port
        )
        res = await check_awg_reachability(
            host=host,
            port=port,
            server_public_key=server_public_key,
            client_private_key=client_private_key,
            psk=psk,
            awg_params=awg_params,
            mimicry_profile=proto,
            timeout=timeout,
        )
        results[proto] = res
        await asyncio.sleep(0.5)

    return results
