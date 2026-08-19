"""Tests for pure Python AmneziaWG Health Check, Protocol Handshake, and Auto Trials."""

import base64
import hashlib
import secrets
import socket
import struct
import threading
import pytest
from cryptography.hazmat.primitives import serialization
from cryptography.hazmat.primitives.asymmetric.x25519 import X25519PrivateKey, X25519PublicKey
from cryptography.hazmat.primitives.ciphers.aead import ChaCha20Poly1305

from app.managers.awg_health import (
    INITIAL_CHAIN_KEY,
    INITIAL_HASH,
    LABEL_MAC1,
    _decode_key,
    _kdf1,
    _kdf2,
    _kdf3,
    build_awg_initiation_packet,
    check_awg_reachability,
    parse_cps_blob,
    perform_awg_handshake,
    run_auto_trial_profiles,
    verify_awg_response_packet,
)


def _b64(raw: bytes) -> str:
    return base64.b64encode(raw).decode("utf-8")


class TestAWGProtocolCrypt:
    """Test cryptographic primitives, key formatting, and packet serialization."""

    def test_decode_key(self):
        priv = X25519PrivateKey.generate()
        raw = priv.private_bytes(
            encoding=serialization.Encoding.Raw,
            format=serialization.PrivateFormat.Raw,
            encryption_algorithm=serialization.NoEncryption(),
        )
        b64_key = _b64(raw)

        assert _decode_key(raw) == raw
        assert _decode_key(b64_key) == raw
        assert _decode_key(f"  {b64_key} \n") == raw

        with pytest.raises(ValueError):
            _decode_key(_b64(b"short"))

    def test_parse_cps_blob(self):
        # Hex blob
        blob = parse_cps_blob("<b 0x01020304>")
        assert blob == bytes.fromhex("01020304")

        # Random prefix blob
        blob_rand = parse_cps_blob("<r 2><b 0x0102030405>")
        assert len(blob_rand) == 5
        assert blob_rand[2:] == bytes.fromhex("030405")

        # Empty / invalid
        assert parse_cps_blob("") == b""
        assert parse_cps_blob("not_a_blob") == b""

    def test_build_awg_initiation_packet(self):
        s_priv = X25519PrivateKey.generate()
        s_pub_bytes = s_priv.public_key().public_bytes(
            encoding=serialization.Encoding.Raw, format=serialization.PublicFormat.Raw
        )
        s_pub_b64 = _b64(s_pub_bytes)

        c_priv = X25519PrivateKey.generate()
        c_priv_bytes = c_priv.private_bytes(
            encoding=serialization.Encoding.Raw,
            format=serialization.PrivateFormat.Raw,
            encryption_algorithm=serialization.NoEncryption(),
        )
        c_priv_b64 = _b64(c_priv_bytes)

        psk = secrets.token_bytes(32)
        psk_b64 = _b64(psk)

        params = {
            "init_packet_magic_header": "12345",
            "init_packet_junk_size": "20",
        }

        packet, state = build_awg_initiation_packet(
            server_public_key=s_pub_b64,
            client_private_key=c_priv_b64,
            psk=psk_b64,
            awg_params=params,
        )

        assert len(packet) == 148 + 20
        msg_type = struct.unpack("<I", packet[:4])[0]
        assert msg_type == 12345
        sender_idx = struct.unpack("<I", packet[4:8])[0]
        assert sender_idx == state.sender_index

    def test_verify_awg_response_packet_invalid(self):
        s_priv = X25519PrivateKey.generate()
        s_pub_bytes = s_priv.public_key().public_bytes(
            encoding=serialization.Encoding.Raw, format=serialization.PublicFormat.Raw
        )
        s_pub_b64 = _b64(s_pub_bytes)

        _, state = build_awg_initiation_packet(server_public_key=s_pub_b64)

        # Too short
        assert verify_awg_response_packet(b"short", state) is False

        # Wrong magic header
        fake_packet = b"\x00" * 100
        assert verify_awg_response_packet(fake_packet, state) is False


class TestAWGHandshakeLive:
    """Test live UDP loopback handshake exchange."""

    @pytest.fixture
    def mock_awg_server(self):
        s_priv = X25519PrivateKey.generate()
        s_pub_bytes = s_priv.public_key().public_bytes(
            encoding=serialization.Encoding.Raw, format=serialization.PublicFormat.Raw
        )
        s_pub_b64 = _b64(s_pub_bytes)

        c_priv = X25519PrivateKey.generate()
        c_priv_bytes = c_priv.private_bytes(
            encoding=serialization.Encoding.Raw,
            format=serialization.PrivateFormat.Raw,
            encryption_algorithm=serialization.NoEncryption(),
        )
        c_priv_b64 = _b64(c_priv_bytes)

        psk_bytes = secrets.token_bytes(32)
        psk_b64 = _b64(psk_bytes)

        H1 = 1020325451
        H2 = 3288052141
        S1 = 15
        S2 = 18

        srv_sock = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
        srv_sock.bind(("127.0.0.1", 0))
        port = srv_sock.getsockname()[1]
        stop_event = threading.Event()

        def srv_loop():
            srv_sock.settimeout(0.5)
            while not stop_event.is_set():
                try:
                    data, addr = srv_sock.recvfrom(2048)
                    if len(data) >= 148:
                        msg_type = struct.unpack("<I", data[:4])[0]
                        if msg_type == H1:
                            c_idx = struct.unpack("<I", data[4:8])[0]
                            c_e_pub_bytes = data[8:40]
                            enc_static = data[40:88]
                            enc_ts = data[88:116]

                            s_h = hashlib.blake2s(INITIAL_HASH + s_pub_bytes).digest()
                            s_ck = INITIAL_CHAIN_KEY
                            s_h = hashlib.blake2s(s_h + c_e_pub_bytes).digest()
                            s_ck = _kdf1(s_ck, c_e_pub_bytes)

                            c_e_pub = X25519PublicKey.from_public_bytes(c_e_pub_bytes)
                            ss = s_priv.exchange(c_e_pub)
                            s_ck, s_key = _kdf2(s_ck, ss)

                            nonce0 = b"\x00" * 12
                            ciph = ChaCha20Poly1305(s_key)
                            dec_c_pub = ciph.decrypt(nonce0, enc_static, s_h)
                            s_h = hashlib.blake2s(s_h + enc_static).digest()

                            c_pub = X25519PublicKey.from_public_bytes(dec_c_pub)
                            ss2 = s_priv.exchange(c_pub)
                            s_ck, s_key = _kdf2(s_ck, ss2)

                            ciph2 = ChaCha20Poly1305(s_key)
                            dec_ts = ciph2.decrypt(nonce0, enc_ts, s_h)
                            s_h = hashlib.blake2s(s_h + enc_ts).digest()

                            # Generate response
                            s_e_priv = X25519PrivateKey.generate()
                            s_e_pub_bytes = s_e_priv.public_key().public_bytes(
                                encoding=serialization.Encoding.Raw,
                                format=serialization.PublicFormat.Raw,
                            )

                            s_h = hashlib.blake2s(s_h + s_e_pub_bytes).digest()
                            s_ck = _kdf1(s_ck, s_e_pub_bytes)

                            ss3 = s_e_priv.exchange(c_e_pub)
                            s_ck = _kdf1(s_ck, ss3)

                            ss4 = s_e_priv.exchange(c_pub)
                            s_ck = _kdf1(s_ck, ss4)

                            s_ck, tau, s_key = _kdf3(s_ck, psk_bytes)
                            s_h = hashlib.blake2s(s_h + tau).digest()

                            ciph3 = ChaCha20Poly1305(s_key)
                            enc_empty = ciph3.encrypt(nonce0, b"", s_h)
                            s_h = hashlib.blake2s(s_h + enc_empty).digest()

                            resp_body = (
                                struct.pack("<I", H2)
                                + struct.pack("<I", secrets.randbelow(0xFFFFFFFF))
                                + struct.pack("<I", c_idx)
                                + s_e_pub_bytes
                                + enc_empty
                            )
                            mac1_key = hashlib.blake2s(LABEL_MAC1 + s_pub_bytes).digest()
                            resp_mac1 = hashlib.blake2s(
                                resp_body, digest_size=16, key=mac1_key
                            ).digest()
                            resp_mac2 = b"\x00" * 16
                            resp_packet = (
                                resp_body + resp_mac1 + resp_mac2 + secrets.token_bytes(S2)
                            )
                            srv_sock.sendto(resp_packet, addr)
                except socket.timeout:
                    continue
                except Exception:
                    break

        th = threading.Thread(target=srv_loop, daemon=True)
        th.start()

        yield {
            "port": port,
            "s_pub_b64": s_pub_b64,
            "c_priv_b64": c_priv_b64,
            "psk_b64": psk_b64,
            "params": {
                "init_packet_magic_header": str(H1),
                "response_packet_magic_header": str(H2),
                "init_packet_junk_size": str(S1),
                "response_packet_junk_size": str(S2),
            },
        }

        stop_event.set()
        srv_sock.close()
        th.join(timeout=1.0)

    def test_perform_awg_handshake_success(self, mock_awg_server):
        info = mock_awg_server
        res = perform_awg_handshake(
            host="127.0.0.1",
            port=info["port"],
            server_public_key=info["s_pub_b64"],
            client_private_key=info["c_priv_b64"],
            psk=info["psk_b64"],
            awg_params=info["params"],
            mimicry_profile="tls",
            timeout=2.0,
        )
        assert res["reachable"] is True
        assert res["handshake_complete"] is True
        assert res["latency_ms"] >= 0
        assert res["error"] == ""
        assert res["profile"] == "tls"

    def test_perform_awg_handshake_timeout(self, mock_awg_server):
        info = mock_awg_server
        # Target an unbound port to simulate timeout
        res = perform_awg_handshake(
            host="127.0.0.1",
            port=59999,
            server_public_key=info["s_pub_b64"],
            client_private_key=info["c_priv_b64"],
            psk=info["psk_b64"],
            awg_params=info["params"],
            timeout=0.2,
        )
        assert res["reachable"] is False
        assert res["handshake_complete"] is False
        assert "timeout" in res["error"].lower()

    @pytest.mark.asyncio
    async def test_async_check_awg_reachability(self, mock_awg_server):
        info = mock_awg_server
        res = await check_awg_reachability(
            host="127.0.0.1",
            port=info["port"],
            server_public_key=info["s_pub_b64"],
            client_private_key=info["c_priv_b64"],
            psk=info["psk_b64"],
            awg_params=info["params"],
            timeout=2.0,
        )
        assert res["reachable"] is True
        assert res["handshake_complete"] is True

    @pytest.mark.asyncio
    async def test_run_auto_trial_profiles(self, mock_awg_server):
        info = mock_awg_server
        trials = await run_auto_trial_profiles(
            host="127.0.0.1",
            port=info["port"],
            server_public_key=info["s_pub_b64"],
            client_private_key=info["c_priv_b64"],
            psk=info["psk_b64"],
            awg_params=info["params"],
            timeout=1.5,
        )
        assert set(trials.keys()) == {"tls", "quic", "dns", "sip"}
        for proto in ("tls", "quic", "dns", "sip"):
            assert trials[proto]["reachable"] is True
            assert trials[proto]["handshake_complete"] is True
            assert trials[proto]["profile"] == proto
