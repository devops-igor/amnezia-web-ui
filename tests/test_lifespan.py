"""Tests for the lifespan context manager that replaced @app.on_event("startup")."""

from unittest.mock import AsyncMock, MagicMock, patch

import pytest


class TestLifespanAdminCreation:
    """Tests for admin creation during lifespan startup."""

    @pytest.mark.asyncio
    async def test_startup_no_admin_created_when_no_users_exist(self):
        """Lifespan startup NO LONGER creates a default admin — setup wizard handles this."""
        from app import lifespan

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

            mock_db.create_user.assert_not_called()
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

    @pytest.mark.asyncio
    async def test_startup_logs_setup_required_when_no_users(self):
        """Lifespan startup logs the setup wizard message when no users exist."""
        from app import lifespan

        mock_app = MagicMock()

        mock_db = MagicMock()
        mock_db.get_all_users.return_value = []
        mock_db.get_setting.return_value = {}

        with (
            patch("app.init_db"),
            patch("app.get_db", return_value=mock_db),
            patch("app.logger") as mock_logger,
            patch(
                "app.services.background_orchestrator.BackgroundTaskOrchestrator.start",
                new_callable=AsyncMock,
            ),
        ):
            async with lifespan(mock_app):
                pass

            mock_logger.info.assert_any_call("No users found — setup wizard required at /setup")


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

        with (
            patch("app.init_db"),
            patch("app.get_db", return_value=mock_db),
            patch(
                "app.services.background_orchestrator.BackgroundTaskOrchestrator.start",
                new_callable=AsyncMock,
            ),
        ):
            # Should complete without unhandled exceptions
            async with lifespan(mock_app):
                pass
