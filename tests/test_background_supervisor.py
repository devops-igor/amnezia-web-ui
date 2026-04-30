"""Unit tests for BackgroundTaskSupervisor (TASK: background-task-no-supervision).

Tests cover:
- Normal start/stop cycle
- Crash recovery and automatic restart
- Restart limit enforcement
- Health check (is_healthy)
- Graceful shutdown with timeout
- CancelledError not treated as crash
"""

import asyncio
from unittest.mock import MagicMock

import pytest

# ---------------------------------------------------------------------------
# Mock Orchestrators
# ---------------------------------------------------------------------------


class MockOrchestrator:
    """Simple mock orchestrator with configurable crash/hang behavior.

    To simulate a crash: set _crash_trigger, then wait for the task to die.
    """

    def __init__(self, hang_on_stop: bool = False) -> None:
        self._task = None
        self.hang_on_stop = hang_on_stop
        self.start_calls = 0
        self.stop_calls = 0
        self._crash_trigger: asyncio.Event = asyncio.Event()

    async def start(self) -> None:
        self.start_calls += 1
        self._crash_trigger.clear()

        async def _run() -> None:
            # Wait for either cancel signal or crash trigger
            cancel_evt = asyncio.Event()

            async def _watch_crash() -> None:
                await self._crash_trigger.wait()
                cancel_evt.set()

            asyncio.ensure_future(_watch_crash())

            await cancel_evt.wait()
            if self._crash_trigger.is_set():
                raise RuntimeError("Simulated crash")

        self._task = asyncio.create_task(_run())

    async def stop(self) -> None:
        self.stop_calls += 1
        if self.hang_on_stop:
            await asyncio.Event().wait()  # hang forever
        if self._task and not self._task.done():
            self._task.cancel()
            try:
                await self._task
            except asyncio.CancelledError:
                pass

    async def trigger_crash(self) -> None:
        """Signal the running task to crash."""
        self._crash_trigger.set()
        # Give the task a chance to process
        await asyncio.sleep(0.05)


class CountingCrashOrchestrator:
    """Orchestrator that crashes a fixed number of times, then runs normally."""

    def __init__(self, num_crashes: int = 1) -> None:
        self._task = None
        self._calls = 0
        self._num_crashes = num_crashes
        self.start_calls = 0
        self.stop_calls = 0

    async def start(self) -> None:
        self._calls += 1
        self.start_calls += 1
        should_crash = self._calls <= self._num_crashes

        async def _run() -> None:
            if should_crash:
                raise RuntimeError(f"Mock crash #{self._calls}")
            await asyncio.Event().wait()

        self._task = asyncio.create_task(_run())

    async def stop(self) -> None:
        self.stop_calls += 1
        if self._task and not self._task.done():
            self._task.cancel()
            try:
                await self._task
            except asyncio.CancelledError:
                pass


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------


def _patch_restart_immediate(supervisor) -> None:
    """Replace _restart_after_delay with an immediate restart (no sleep)."""

    async def immediate_restart(delay: float = 0) -> None:
        supervisor._orchestrator._task = None
        await supervisor.start()

    supervisor._restart_after_delay = immediate_restart


@pytest.fixture
def mock_orch():
    """Return a fresh MockOrchestrator for each test."""
    return MockOrchestrator()


# ---------------------------------------------------------------------------
# 1. Normal Start/Stop
# ---------------------------------------------------------------------------


class TestStartStop:
    @pytest.mark.asyncio
    async def test_start_creates_supervised_task(self, mock_orch):
        from app.services.background_supervisor import BackgroundTaskSupervisor

        supervisor = BackgroundTaskSupervisor(mock_orch)
        assert supervisor._task is None

        await supervisor.start()
        assert supervisor._task is not None
        assert not supervisor._task.done()

        await supervisor.stop()

    @pytest.mark.asyncio
    async def test_stop_cleans_up_without_crash_increment(self, mock_orch):
        from app.services.background_supervisor import BackgroundTaskSupervisor

        supervisor = BackgroundTaskSupervisor(mock_orch)
        await supervisor.start()
        await asyncio.sleep(0.02)

        await supervisor.stop()

        assert supervisor.crash_count == 0
        assert supervisor.restart_count == 0

    @pytest.mark.asyncio
    async def test_stop_calls_orchestrator_stop(self, mock_orch):
        from app.services.background_supervisor import BackgroundTaskSupervisor

        supervisor = BackgroundTaskSupervisor(mock_orch)
        await supervisor.start()
        await asyncio.sleep(0.02)

        assert mock_orch.start_calls == 1
        await supervisor.stop()
        assert mock_orch.stop_calls == 1

    @pytest.mark.asyncio
    async def test_initial_properties(self, mock_orch):
        from app.services.background_supervisor import BackgroundTaskSupervisor

        supervisor = BackgroundTaskSupervisor(mock_orch)
        assert supervisor.crash_count == 0
        assert supervisor.restart_count == 0
        assert supervisor.last_success_time is None
        assert supervisor.is_healthy()


# ---------------------------------------------------------------------------
# 2. Crash Recovery
# ---------------------------------------------------------------------------


class TestCrashRecovery:
    @pytest.mark.asyncio
    async def test_single_crash_triggers_restart(self, mock_orch):
        from app.services.background_supervisor import BackgroundTaskSupervisor

        supervisor = BackgroundTaskSupervisor(mock_orch)
        _patch_restart_immediate(supervisor)

        await supervisor.start()
        await asyncio.sleep(0.02)

        # Trigger crash from inside the mock task
        await mock_orch.trigger_crash()
        await asyncio.sleep(0.15)

        assert supervisor.crash_count == 1
        assert supervisor.restart_count == 1
        assert supervisor.is_healthy()
        assert mock_orch.start_calls >= 2  # original + restart

        await supervisor.stop()

    @pytest.mark.asyncio
    async def test_restart_creates_new_orchestrator_task(self, mock_orch):
        from app.services.background_supervisor import BackgroundTaskSupervisor

        supervisor = BackgroundTaskSupervisor(mock_orch)
        _patch_restart_immediate(supervisor)

        await supervisor.start()
        await asyncio.sleep(0.02)

        old_task = mock_orch._task
        await mock_orch.trigger_crash()
        await asyncio.sleep(0.15)

        # After restart, a new orchestrator task should exist
        assert mock_orch._task is not None
        assert mock_orch._task is not old_task

        await supervisor.stop()

    @pytest.mark.asyncio
    async def test_multiple_crashes_increment_count(self, mock_orch):
        from app.services.background_supervisor import BackgroundTaskSupervisor

        supervisor = BackgroundTaskSupervisor(mock_orch, max_restarts=5)
        _patch_restart_immediate(supervisor)

        await supervisor.start()
        await asyncio.sleep(0.02)

        # Crash twice
        await mock_orch.trigger_crash()
        await asyncio.sleep(0.15)
        assert supervisor.crash_count == 1

        await mock_orch.trigger_crash()
        await asyncio.sleep(0.15)

        assert supervisor.crash_count == 2
        assert supervisor.restart_count == 2
        assert supervisor.is_healthy()

        await supervisor.stop()


# ---------------------------------------------------------------------------
# 3. Restart Limit
# ---------------------------------------------------------------------------


class TestRestartLimit:
    @pytest.mark.asyncio
    async def test_max_restarts_enforced(self):
        """After max_restarts crashes, supervisor stops restarting."""
        from app.services.background_supervisor import BackgroundTaskSupervisor

        orch = CountingCrashOrchestrator(num_crashes=10)
        supervisor = BackgroundTaskSupervisor(orch, max_restarts=2)
        _patch_restart_immediate(supervisor)

        await supervisor.start()
        await asyncio.sleep(0.3)

        # max_restarts=2: 1 initial crash + 2 restarts = 3 total crashes
        assert supervisor.crash_count == 3
        assert supervisor.restart_count == 2
        assert not supervisor.is_healthy()

        await supervisor.stop()

    @pytest.mark.asyncio
    async def test_restart_count_cannot_exceed_max(self):
        from app.services.background_supervisor import BackgroundTaskSupervisor

        orch = CountingCrashOrchestrator(num_crashes=10)
        supervisor = BackgroundTaskSupervisor(orch, max_restarts=1)
        _patch_restart_immediate(supervisor)

        await supervisor.start()
        await asyncio.sleep(0.2)

        # Only 1 restart allowed, so restart_count max is 1
        assert supervisor.restart_count <= 1
        assert not supervisor.is_healthy()

        await supervisor.stop()


# ---------------------------------------------------------------------------
# 4. Health Check
# ---------------------------------------------------------------------------


class TestHealthCheck:
    @pytest.mark.asyncio
    async def test_is_healthy_when_no_crashes(self, mock_orch):
        from app.services.background_supervisor import BackgroundTaskSupervisor

        supervisor = BackgroundTaskSupervisor(mock_orch)
        assert supervisor.is_healthy()

        await supervisor.start()
        assert supervisor.is_healthy()

        await supervisor.stop()
        assert supervisor.is_healthy()

    @pytest.mark.asyncio
    async def test_is_healthy_after_single_crash(self, mock_orch):
        from app.services.background_supervisor import BackgroundTaskSupervisor

        supervisor = BackgroundTaskSupervisor(mock_orch, max_restarts=5)
        _patch_restart_immediate(supervisor)

        await supervisor.start()
        await asyncio.sleep(0.02)
        await mock_orch.trigger_crash()
        await asyncio.sleep(0.15)

        # Single crash within limit = still healthy
        assert supervisor.is_healthy()
        await supervisor.stop()

    @pytest.mark.asyncio
    async def test_is_unhealthy_when_restart_limit_reached(self):
        from app.services.background_supervisor import BackgroundTaskSupervisor

        orch = CountingCrashOrchestrator(num_crashes=10)
        supervisor = BackgroundTaskSupervisor(orch, max_restarts=2)
        _patch_restart_immediate(supervisor)

        await supervisor.start()
        await asyncio.sleep(0.3)

        assert not supervisor.is_healthy()
        await supervisor.stop()

    def test_is_healthy_ignores_stale_timestamps(self):
        """Restart timestamps outside the window should not affect health."""
        import time

        from app.services.background_supervisor import BackgroundTaskSupervisor

        orch = MagicMock()
        supervisor = BackgroundTaskSupervisor(orch, max_restarts=2, restart_window=10)

        now = time.monotonic()
        # All timestamps are > 10s old, so they should be ignored
        supervisor._restart_timestamps = [now - 20.0, now - 15.0, now - 12.0]

        assert supervisor.is_healthy()

    def test_is_healthy_respects_recent_timestamps(self):
        """Recent restart timestamps within the window should affect health."""
        import time

        from app.services.background_supervisor import BackgroundTaskSupervisor

        orch = MagicMock()
        supervisor = BackgroundTaskSupervisor(orch, max_restarts=2, restart_window=60)

        now = time.monotonic()
        # 3 recent restarts exceed max_restarts=2
        supervisor._restart_timestamps = [now - 5.0, now - 10.0, now - 15.0]

        assert not supervisor.is_healthy()


# ---------------------------------------------------------------------------
# 5. Graceful Shutdown with Timeout
# ---------------------------------------------------------------------------


class TestStopTimeout:
    @pytest.mark.asyncio
    async def test_stop_timeout_when_orchestrator_hangs(self):
        """Stop should not hang forever when orchestrator.stop() blocks."""
        from app.services.background_supervisor import BackgroundTaskSupervisor

        orch = MockOrchestrator(hang_on_stop=True)
        supervisor = BackgroundTaskSupervisor(orch)

        await supervisor.start()
        await asyncio.sleep(0.02)

        # This should not hang — timeout kicks in and cancels
        await supervisor.stop(timeout=0.1)

        # Supervisor should have stopped
        assert supervisor._task is None or supervisor._task.done()


# ---------------------------------------------------------------------------
# 6. CancelledError Propagation
# ---------------------------------------------------------------------------


class TestCancelledError:
    @pytest.mark.asyncio
    async def test_cancelled_error_not_treated_as_crash(self, mock_orch):
        from app.services.background_supervisor import BackgroundTaskSupervisor

        supervisor = BackgroundTaskSupervisor(mock_orch)
        await supervisor.start()
        await asyncio.sleep(0.02)

        # Normal stop: orchestrator task gets cancelled
        await supervisor.stop()

        # CancelledError is NOT a crash — counters stay at 0
        assert supervisor.crash_count == 0
        assert supervisor.restart_count == 0

    @pytest.mark.asyncio
    async def test_cancelled_error_during_crash_recovery_respected(self, mock_orch):
        """If stop() is called mid-restart-delay, the restart should be cancelled."""
        from app.services.background_supervisor import BackgroundTaskSupervisor

        supervisor = BackgroundTaskSupervisor(mock_orch)
        _patch_restart_immediate(supervisor)

        await supervisor.start()
        await asyncio.sleep(0.02)

        # Cause a crash, which triggers _restart_after_delay
        await mock_orch.trigger_crash()
        await asyncio.sleep(0.05)

        # While restart delay is running, call stop
        await supervisor.stop()

        # The stop should cancel the restart task
        assert supervisor._restart_task is None
