"""Tests for the lifespan context manager that replaced @app.on_event("startup")."""

from unittest.mock import AsyncMock, MagicMock, patch

import pytest


class TestLifespanAdminCreation:
    """Tests for admin creation during lifespan startup."""

    @pytest.mark.asyncio
    async def test_startup_creates_admin_when_no_users_exist(self):
        """Lifespan startup creates default admin when no users exist."""
        from app import lifespan
        from app.utils.helpers import hash_password

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
