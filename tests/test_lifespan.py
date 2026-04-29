"""Tests for the lifespan context manager that replaced @app.on_event("startup")."""

from unittest.mock import AsyncMock, MagicMock, patch

import pytest


class TestLifespanAdminCreation:
    """Tests for admin creation during lifespan startup."""

    @pytest.mark.asyncio
    async def test_startup_creates_admin_when_no_users_exist(self):
        """Lifespan startup creates default admin when no users exist."""
        from app import lifespan, hash_password

        mock_app = MagicMock()

        mock_db = MagicMock()
        mock_db.get_all_users.return_value = []
        mock_db.get_setting.return_value = {}

        with (
            patch("app.init_db"),
            patch("app.get_db", return_value=mock_db),
            patch(
                "app.services.background_orchestrator.BackgroundTaskOrchestrator.start",
                new_callable=AsyncMock,
            ) as mock_start,
        ):
            async with lifespan(mock_app):
                pass

            assert mock_db.create_user.call_count == 1
            created_user = mock_db.create_user.call_args[0][0]
            assert created_user["username"] == "admin"
            assert created_user["role"] == "admin"
            assert created_user["enabled"] is True
            assert created_user["password_change_required"] is True
            assert isinstance(created_user["id"], str)
            assert len(created_user["id"]) > 0
            assert created_user["password_hash"] != hash_password("admin")
            mock_start.assert_awaited_once()

    @pytest.mark.asyncio
    async def test_startup_skips_admin_when_users_exist(self):
        """Lifespan startup skips admin creation when users already exist."""
        from app import lifespan

        mock_app = MagicMock()

        mock_db = MagicMock()
        mock_db.get_all_users.return_value = [{"id": "existing-user", "username": "alice"}]
        mock_db.get_setting.return_value = {}

        with (
            patch("app.init_db"),
            patch("app.get_db", return_value=mock_db),
            patch(
                "app.services.background_orchestrator.BackgroundTaskOrchestrator.start",
                new_callable=AsyncMock,
            ),
        ):
            async with lifespan(mock_app):
                pass

            mock_db.create_user.assert_not_called()


class TestLifespanShutdown:
    """Tests for shutdown cleanup during lifespan."""

    @pytest.mark.asyncio
    async def test_shutdown_cancels_background_task(self):
        """Lifespan shutdown cancels the background task cleanly."""
        from app import lifespan

        mock_app = MagicMock()

        mock_db = MagicMock()
        mock_db.get_all_users.return_value = [{"id": "u1"}]
        mock_db.get_setting.return_value = {}

        bg_mock = AsyncMock()

        with (
            patch("app.init_db"),
            patch("app.get_db", return_value=mock_db),
            patch(
                "app.services.background_orchestrator.BackgroundTaskOrchestrator.start",
                new_callable=AsyncMock,
            ),
            patch("app.tg_bot.stop_bot", new_callable=AsyncMock),
        ):
            # Should complete without unhandled exceptions
            async with lifespan(mock_app):
                pass

    @pytest.mark.asyncio
    async def test_shutdown_stops_telegram_bot(self):
        """Lifespan shutdown calls tg_bot.stop_bot()."""
        from app import lifespan

        mock_app = MagicMock()

        mock_db = MagicMock()
        mock_db.get_all_users.return_value = [{"id": "u1"}]
        mock_db.get_setting.return_value = {}

        stop_mock = AsyncMock()

        with (
            patch("app.init_db"),
            patch("app.get_db", return_value=mock_db),
            patch(
                "app.services.background_orchestrator.BackgroundTaskOrchestrator.start",
                new_callable=AsyncMock,
            ),
            patch("app.tg_bot.stop_bot", stop_mock),
        ):
            async with lifespan(mock_app):
                pass

            stop_mock.assert_awaited_once()

    @pytest.mark.asyncio
    async def test_shutdown_handles_telegram_stop_error(self):
        """Lifespan shutdown handles tg_bot.stop_bot() failure gracefully."""
        from app import lifespan

        mock_app = MagicMock()

        mock_db = MagicMock()
        mock_db.get_all_users.return_value = [{"id": "u1"}]
        mock_db.get_setting.return_value = {}

        stop_mock = AsyncMock(side_effect=RuntimeError("bot crashed"))

        with (
            patch("app.init_db"),
            patch("app.get_db", return_value=mock_db),
            patch(
                "app.services.background_orchestrator.BackgroundTaskOrchestrator.start",
                new_callable=AsyncMock,
            ),
            patch("app.tg_bot.stop_bot", stop_mock),
        ):
            async with lifespan(mock_app):
                pass

            stop_mock.assert_awaited_once()


class TestLifespanTelegramBotStartup:
    """Tests for Telegram bot launch during lifespan startup."""

    @pytest.mark.asyncio
    async def test_launches_telegram_bot_when_enabled(self):
        """Lifespan launches Telegram bot when enabled and token is set."""
        from app import lifespan, generate_vpn_link

        mock_app = MagicMock()

        mock_db = MagicMock()
        mock_db.get_all_users.return_value = [{"id": "u1"}]
        mock_db.get_setting.return_value = {"enabled": True, "token": "test-bot-token"}
        mock_db.load_data = mock_db

        with (
            patch("app.init_db"),
            patch("app.get_db", return_value=mock_db),
            patch(
                "app.services.background_orchestrator.BackgroundTaskOrchestrator.start",
                new_callable=AsyncMock,
            ),
            patch("app.tg_bot.launch_bot") as mock_launch,
        ):
            async with lifespan(mock_app):
                pass

            mock_launch.assert_called_once_with(
                "test-bot-token", mock_db.load_data, generate_vpn_link
            )

    @pytest.mark.asyncio
    async def test_skips_telegram_bot_when_disabled(self):
        """Lifespan does not launch Telegram bot when disabled."""
        from app import lifespan

        mock_app = MagicMock()

        mock_db = MagicMock()
        mock_db.get_all_users.return_value = [{"id": "u1"}]
        mock_db.get_setting.return_value = {"enabled": False, "token": "test-bot-token"}

        with (
            patch("app.init_db"),
            patch("app.get_db", return_value=mock_db),
            patch(
                "app.services.background_orchestrator.BackgroundTaskOrchestrator.start",
                new_callable=AsyncMock,
            ),
            patch("app.tg_bot.launch_bot") as mock_launch,
        ):
            async with lifespan(mock_app):
                pass

            mock_launch.assert_not_called()

    @pytest.mark.asyncio
    async def test_skips_telegram_bot_when_no_token(self):
        """Lifespan does not launch Telegram bot when token is missing."""
        from app import lifespan

        mock_app = MagicMock()

        mock_db = MagicMock()
        mock_db.get_all_users.return_value = [{"id": "u1"}]
        mock_db.get_setting.return_value = {"enabled": True, "token": ""}

        with (
            patch("app.init_db"),
            patch("app.get_db", return_value=mock_db),
            patch(
                "app.services.background_orchestrator.BackgroundTaskOrchestrator.start",
                new_callable=AsyncMock,
            ),
            patch("app.tg_bot.launch_bot") as mock_launch,
        ):
            async with lifespan(mock_app):
                pass

            mock_launch.assert_not_called()
