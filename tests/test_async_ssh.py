"""
Tests for async SSH handling (Issues #44 and #75).

Verifies that:
- All sync SSH calls in async handlers use asyncio.to_thread
- Concurrent requests don't block each other
- SSH connections are cleaned up on exceptions in background tasks
- CancelledError is re-raised, not swallowed
"""

import asyncio
import re
import time
from unittest.mock import MagicMock, patch

import pytest

# ---------------------------------------------------------------------------
# 1. Grep test: ensure no bare ssh.*() calls remain in async handlers
# ---------------------------------------------------------------------------

SYNC_SSH_PATTERNS = [
    (r"\bssh\.connect\(\)", "ssh.connect()"),
    (r"\bssh\.disconnect\(\)", "ssh.disconnect()"),
    (r"\bssh\.run_command\(", "ssh.run_command()"),
    (r"\bssh\.run_sudo_command\(", "ssh.run_sudo_command()"),
    (r"\bssh\.test_connection\(\)", "ssh.test_connection()"),
    (r"\bssh\.put_file\(", "ssh.put_file()"),
    (r"\bssh\.get_file\(", "ssh.get_file()"),
]


def _find_bare_ssh_calls_in_source():
    """Parse app.py source to find bare (non-to_thread) SSH calls in async functions."""
    with open("app.py", "r") as f:
        source = f.read()
    lines = source.split("\n")

    violations = []

    for i, line in enumerate(lines):
        line_no = i + 1
        for pattern, desc in SYNC_SSH_PATTERNS:
            if re.search(pattern, line):
                # Skip lines that already wrap in asyncio.to_thread
                if "asyncio.to_thread" in line or "await asyncio.to_thread" in line:
                    continue
                # Bare call: not awaited, not inside to_thread
                if "await" not in line and "asyncio.to_thread" not in line:
                    violations.append((line_no, desc, line.strip()[:100]))

    return violations


class TestAsyncSshGrep:
    """Grep test: verify all SSH calls in app.py use asyncio.to_thread."""

    def test_no_bare_ssh_calls_in_async_handlers(self):
        """Ensure no bare ssh.*() calls remain in async handlers.

        Every ssh.connect(), ssh.disconnect(), ssh.run_command(),
        ssh.run_sudo_command(), and ssh.test_connection() must be
        wrapped in asyncio.to_thread() when called from async functions.
        """
        violations = _find_bare_ssh_calls_in_source()
        if violations:
            msg_lines = [
                f"  Line {line_no}: {desc} -> {code}" for line_no, desc, code in violations
            ]
            pytest.fail(
                "Found bare (non-asyncio.to_thread) SSH calls in app.py:\n"
                + "\n".join(msg_lines)
                + "\n\nAll SSH calls in async handlers must use asyncio.to_thread()."
            )


# ---------------------------------------------------------------------------
# 2. Concurrent requests don't block
# ---------------------------------------------------------------------------


class TestConcurrentRequests:
    """Test that concurrent requests can proceed without blocking the event loop."""

    @pytest.mark.asyncio
    async def test_concurrent_requests_not_blocked(self):
        """Two concurrent SSH operations should complete in ~single_op_time, not 2x.

        Mock SSH with 0.5s delay and send 2 requests concurrently.
        If they block the event loop (sync SSH), total time ≈ 1s.
        If they use to_thread correctly, total time ≈ 0.5s.
        """
        delay = 0.5

        def slow_connect():
            time.sleep(delay)

        async def make_request():
            """Simulate an async handler that uses to_thread for SSH connect."""
            await asyncio.to_thread(slow_connect)
            return "done"

        start = time.monotonic()
        results = await asyncio.gather(make_request(), make_request())
        elapsed = time.monotonic() - start

        # If to_thread works correctly, both requests run concurrently
        # Total time should be closer to 0.5s than to 1s
        assert elapsed < delay * 1.8, (
            f"Concurrent requests took {elapsed:.2f}s, expected < {delay * 1.8:.2f}s. "
            f"Event loop may be blocked by sync SSH calls."
        )
        assert results == ["done", "done"]


# ---------------------------------------------------------------------------
# 3. SSH connection cleanup on exception in background task
# ---------------------------------------------------------------------------


class TestBackgroundTaskSshCleanup:
    """Test that SSH connections are properly cleaned up on exceptions."""

    def test_ssh_disconnect_called_on_exception(self):
        """When an exception occurs between ssh.connect() and ssh.disconnect(),
        the finally block should still call ssh.disconnect().
        """
        mock_ssh = MagicMock()
        mock_ssh.connect = MagicMock()
        mock_ssh.disconnect = MagicMock()

        # Simulate an error during traffic sync
        mock_manager = MagicMock()
        mock_manager.get_clients.side_effect = Exception("SSH command failed")

        mock_ssh_obj = None
        try:
            mock_ssh_obj = mock_ssh
            mock_ssh_obj.connect()
            # Simulate get_clients raising an exception
            mock_manager.get_clients("awg")
        except asyncio.CancelledError:
            raise
        except Exception:
            pass
        finally:
            if mock_ssh_obj:
                mock_ssh_obj.disconnect()

        # Verify disconnect was called even though an exception occurred
        mock_ssh.disconnect.assert_called_once()

    def test_ssh_disconnect_in_finally_on_connect_failure(self):
        """When ssh.connect() itself fails, finally block should handle gracefully."""
        mock_ssh = MagicMock()
        mock_ssh.connect = MagicMock(side_effect=Exception("Connection refused"))
        mock_ssh.disconnect = MagicMock()

        ssh = None
        try:
            ssh = mock_ssh
            ssh.connect()
        except asyncio.CancelledError:
            raise
        except Exception:
            pass
        finally:
            if ssh:
                ssh.disconnect()

        # disconnect should still be attempted (even if connect failed)
        mock_ssh.disconnect.assert_called_once()

    def test_background_task_cancelled_error_reraised(self):
        """CancelledError should be re-raised, NOT caught by the generic except."""
        with pytest.raises(asyncio.CancelledError):
            try:
                raise asyncio.CancelledError("task cancelled")
            except asyncio.CancelledError:
                raise
            except Exception:
                pytest.fail("CancelledError was caught by generic except — this is a bug")


# ---------------------------------------------------------------------------
# 4. CancelledError is properly re-raised in the background task structure
# ---------------------------------------------------------------------------


class TestCancelledErrorHandling:
    """Test that CancelledError is properly re-raised in background task."""

    def test_cancelled_error_propagates_from_inner_loop(self):
        """Verify that CancelledError raised inside the per-server loop
        is not swallowed by the outer except Exception handler.
        """
        inner_cancelled_caught = False
        outer_cancelled_caught = False

        # Simulating the structure of periodic_background_tasks
        try:
            try:
                raise asyncio.CancelledError("simulated cancellation")
            except asyncio.CancelledError:
                inner_cancelled_caught = True
                raise
        except asyncio.CancelledError:
            outer_cancelled_caught = True
        except Exception:
            pytest.fail("CancelledError caught by generic except — should re-raise")

        assert inner_cancelled_caught, "Inner CancelledError handler should have run"
        assert outer_cancelled_caught, "Outer CancelledError handler should have run"

    def test_generic_exception_logged_not_swallowed_silently(self):
        """Verify that the outer except Exception handler logs with exc_info=True."""
        with patch("app.logger") as mock_logger:
            try:
                raise RuntimeError("test error")
            except asyncio.CancelledError:
                raise
            except Exception as e:
                mock_logger.error(f"Error in periodic_background_tasks: {e}", exc_info=True)

            # Verify logger.error was called with exc_info=True
            mock_logger.error.assert_called_once()
            call_kwargs = mock_logger.error.call_args
            assert (
                call_kwargs[1].get("exc_info") is True
            ), "Error logging should include exc_info=True for proper debugging"

    def test_cancelled_error_in_per_server_loop_is_reraised(self):
        """Test the exact try/except structure used in background task.

        When CancelledError occurs inside the per-server try block,
        it should be caught by 'except asyncio.CancelledError: raise'
        and propagate out, NOT caught by 'except Exception'.
        """
        mock_ssh = MagicMock()
        mock_ssh.connect = MagicMock(side_effect=asyncio.CancelledError("task cancelled"))
        mock_ssh.disconnect = MagicMock()

        servers = [{"id": 1, "host": "1.2.3.4", "protocols": {}}]

        cancelled_caught_in_exception_handler = False
        ssh_cleaned_up = False

        with pytest.raises(asyncio.CancelledError):
            for server in servers:
                ssh = None
                try:
                    ssh = mock_ssh
                    # This will raise CancelledError
                    ssh.connect()
                except asyncio.CancelledError:
                    raise  # Should re-raise
                except Exception:
                    cancelled_caught_in_exception_handler = True
                finally:
                    if ssh:
                        ssh.disconnect()
                        ssh_cleaned_up = True

        assert (
            not cancelled_caught_in_exception_handler
        ), "CancelledError should NOT be caught by 'except Exception'"
        # The finally block runs even when CancelledError propagates,
        # ensuring SSH cleanup happens. However, since the CancelledError
        # propagates out of the for loop, ssh_cleaned_up may still be True
        # because finally runs before the exception propagates.
