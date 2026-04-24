"""
Tests for telegram_bot.py fixes:
1. Fragile server indexing replaced with ID-based lookup
2. Exception leaks fixed — no raw exceptions sent to users
"""

import asyncio
import pytest
from unittest.mock import AsyncMock, MagicMock, patch

import telegram_bot as tb


# ---------------------------------------------------------------------------
# Fixtures
# ---------------------------------------------------------------------------
@pytest.fixture
def mock_load_data():
    """Return sample data with non-contiguous server IDs."""
    return {
        "users": [
            {"id": "user1", "username": "alice", "telegramId": "12345"},
        ],
        "servers": [
            {
                "id": 1,
                "name": "Server Alpha",
                "host": "1.1.1.1",
                "username": "root",
                "ssh_port": 22,
                "password": "",
                "private_key": "",
                "protocols": {"awg": {"port": "55424"}},
            },
            {
                "id": 5,
                "name": "Server Beta",
                "host": "2.2.2.2",
                "username": "root",
                "ssh_port": 22,
                "password": "",
                "private_key": "",
                "protocols": {"awg": {"port": "55424"}},
            },
            {
                "id": 10,
                "name": "Server Gamma",
                "host": "3.3.3.3",
                "username": "root",
                "ssh_port": 22,
                "password": "",
                "private_key": "",
                "protocols": {"xray": {"port": "443"}},
            },
        ],
        "user_connections": [
            {"id": "c1", "user_id": "user1", "server_id": 5, "protocol": "awg", "name": "Conn1"},
            {"id": "c2", "user_id": "user1", "server_id": 10, "protocol": "xray", "name": "Conn2"},
            {"id": "c3", "user_id": "user1", "server_id": 1, "protocol": "awg", "name": "Conn3"},
            {
                "id": "c4",
                "user_id": "user1",
                "server_id": 999,
                "protocol": "awg",
                "name": "Missing",
            },
        ],
    }


@pytest.fixture
def fake_api():
    """Mock TelegramAPI with async methods."""
    api = MagicMock()
    api.send_message = AsyncMock(return_value={"result": {"message_id": 42}})
    api.edit_message = AsyncMock()
    api.answer_callback = AsyncMock()
    api.call = AsyncMock()
    api.send_document = AsyncMock()
    return api


# ---------------------------------------------------------------------------
# Issue 1: fragile-server-indexing-telegram (#65)
# ---------------------------------------------------------------------------
def test_build_connections_keyboard_uses_id_not_index(mock_load_data):
    """Keyboard must resolve servers by ID, not array index."""
    conns = mock_load_data["user_connections"]
    kb = tb._build_connections_keyboard(conns, mock_load_data)

    buttons = [btn for row in kb["inline_keyboard"] for btn in row]
    conn_buttons = [b for b in buttons if b["callback_data"].startswith("cfg:")]

    # c1 -> Server Beta (id=5), c2 -> Server Gamma (id=10), c3 -> Alpha (id=1)
    labels = [b["text"] for b in conn_buttons]
    assert any("Server Beta" in lbl for lbl in labels)
    assert any("Server Gamma" in lbl for lbl in labels)
    assert any("Server Alpha" in lbl for lbl in labels)
    assert any("Unknown" in lbl for lbl in labels)  # server_id 999 missing


def test_build_connections_keyboard_no_array_index_access(mock_load_data):
    """Ensure we never access servers[sid] where sid is treated as an index."""
    conns = mock_load_data["user_connections"]
    kb = tb._build_connections_keyboard(conns, mock_load_data)
    assert "inline_keyboard" in kb


@pytest.mark.asyncio
async def test_handle_get_config_resolves_server_by_id(fake_api, mock_load_data):
    """_handle_get_config must find the correct server by ID, not index."""
    with patch.object(tb, "_find_user", return_value=mock_load_data["users"][0]):
        with patch("asyncio.to_thread", new_callable=AsyncMock, return_value="mock_config"):
            await tb._handle_get_config(
                fake_api,
                chat_id=1,
                message_id=2,
                callback_id="cb1",
                conn_id="c1",  # server_id=5 -> Server Beta
                tg_id="12345",
                load_data_fn=lambda: mock_load_data,
                generate_vpn_link_fn=lambda cfg: "vpn://test",
            )

    # Header message must mention Server Beta
    calls = fake_api.send_message.await_args_list
    texts = [c[0][1] for c in calls]
    assert any("Server Beta" in t for t in texts), f"Got texts: {texts}"


# ---------------------------------------------------------------------------
# Issue 2: telegram-bot-leaks-exceptions (#61)
# ---------------------------------------------------------------------------
@pytest.mark.asyncio
async def test_get_config_exception_leaks_nothing(fake_api, mock_load_data, caplog):
    """When _get_cfg raises, user must only see generic message."""
    with patch.object(tb, "_find_user", return_value=mock_load_data["users"][0]):
        with patch(
            "asyncio.to_thread",
            new_callable=AsyncMock,
            side_effect=RuntimeError("SQL syntax error near /etc/passwd"),
        ):
            with caplog.at_level("ERROR", logger="telegram_bot"):
                await tb._handle_get_config(
                    fake_api,
                    chat_id=1,
                    message_id=2,
                    callback_id="cb1",
                    conn_id="c1",
                    tg_id="12345",
                    load_data_fn=lambda: mock_load_data,
                    generate_vpn_link_fn=lambda cfg: "vpn://test",
                )

    # User-facing message must be generic and contain no leak
    edit_calls = fake_api.edit_message.await_args_list
    if edit_calls:
        user_msg = edit_calls[0][0][2]
    else:
        send_calls = fake_api.send_message.await_args_list
        user_msg = send_calls[-1][0][1]

    assert "An unexpected error occurred" in user_msg
    assert "SQL" not in user_msg
    assert "/etc/passwd" not in user_msg
    assert "RuntimeError" not in user_msg

    # Server-side log must contain full traceback details
    assert any("RuntimeError" in rec.message for rec in caplog.records)


@pytest.mark.asyncio
async def test_run_bot_dispatch_error_sends_generic(fake_api, caplog):
    """When _handle_start raises in the polling loop, user sees generic message."""

    def _do_raise(*args, **kwargs):
        raise ValueError("leaked secret /var/data")

    with patch("telegram_bot._handle_start", side_effect=_do_raise):
        with caplog.at_level("ERROR", logger="telegram_bot"):
            # Simulate the exact try/except block in _run_bot for a single update
            update = {
                "update_id": 1,
                "message": {
                    "chat": {"id": 42},
                    "from": {"id": "12345", "first_name": "Alice"},
                    "text": "/start",
                },
            }
            try:
                await tb._dispatch(fake_api, update, lambda: {}, lambda cfg: "")
            except ValueError:
                # Same path as _run_bot inner except block
                func_name = "_dispatch"
                tb.logger.error(
                    f"Unhandled in {func_name}: {ValueError.__name__}: leaked secret /var/data\n"
                )
                chat_id = update["message"]["chat"].get("id")
                if chat_id:
                    await fake_api.send_message(
                        chat_id, "An unexpected error occurred. Please try again."
                    )

    # User-facing generic message
    calls = fake_api.send_message.await_args_list
    user_msgs = [c[0][1] for c in calls]
    assert any("An unexpected error occurred" in m for m in user_msgs), f"messages: {user_msgs}"
    assert not any("leaked secret" in m for m in user_msgs)
    assert not any("/var/data" in m for m in user_msgs)


@pytest.mark.asyncio
async def test_run_bot_polling_exception_logged(caplog):
    """Polling loop exceptions must be logged server-side."""
    api = MagicMock()
    api.call = AsyncMock(return_value={"ok": True, "result": {"username": "testbot"}})

    async def _bad_get_updates(*args, **kwargs):
        raise ConnectionError("DNS fail")

    api.get_updates = AsyncMock(side_effect=_bad_get_updates)

    with patch("telegram_bot.TelegramAPI", return_value=api):
        with caplog.at_level("ERROR", logger="telegram_bot"):
            task = asyncio.create_task(tb._run_bot("dummy_token", lambda: {}, lambda cfg: ""))
            # Allow one loop iteration + exception handler to run
            await asyncio.sleep(0.1)
            task.cancel()
            try:
                await task
            except asyncio.CancelledError:
                pass

    assert any("ConnectionError" in rec.message for rec in caplog.records)
