"""BackgroundTaskSupervisor — wraps BackgroundTaskOrchestrator with crash recovery."""

import asyncio
import logging
import time
from typing import Optional

logger = logging.getLogger(__name__)


class BackgroundTaskSupervisor:
    """Supervises the BackgroundTaskOrchestrator's background task.

    Adds crash recovery, restart limiting, and health visibility.
    """

    def __init__(
        self,
        orchestrator,
        max_restarts: int = 3,
        restart_window: float = 300.0,
    ) -> None:
        self._orchestrator = orchestrator
        self._task: Optional[asyncio.Task] = None
        self._max_restarts = max_restarts
        self._restart_window = restart_window  # seconds
        self._restart_timestamps: list[float] = []
        self._crash_count: int = 0
        self._last_success: Optional[float] = None
        self._stopping: bool = False
        self._restart_task: Optional[asyncio.Task] = None

    async def start(self) -> None:
        """Start the supervised background task."""
        self._task = asyncio.create_task(self._supervised_loop())
        logger.info("Background task supervisor started")

    async def stop(self, timeout: float = 10.0) -> None:
        """Gracefully stop the supervised task with timeout."""
        self._stopping = True

        # Cancel any in-flight restart delay
        if self._restart_task and not self._restart_task.done():
            self._restart_task.cancel()
            try:
                await self._restart_task
            except asyncio.CancelledError:
                pass
        self._restart_task = None

        if self._task and not self._task.done():
            # Delegate stop to orchestrator with its own timeout
            try:
                await asyncio.wait_for(self._orchestrator.stop(), timeout=timeout)
            except asyncio.TimeoutError:
                logger.warning("Orchestrator stop timed out after %.1fs, forcing cancel", timeout)
            except Exception as e:
                logger.error("Error during orchestrator stop: %s", e)

            try:
                await asyncio.wait_for(asyncio.shield(self._task), timeout=timeout)
            except asyncio.TimeoutError:
                logger.warning("Background task did not stop within %.1fs, cancelling", timeout)
                self._task.cancel()
                try:
                    await self._task
                except asyncio.CancelledError:
                    pass
            except asyncio.CancelledError:
                pass
        logger.info("Background task supervisor stopped")

    @property
    def crash_count(self) -> int:
        return self._crash_count

    @property
    def restart_count(self) -> int:
        return len(self._restart_timestamps)

    @property
    def last_success_time(self) -> Optional[float]:
        return self._last_success

    def is_healthy(self) -> bool:
        """Report health status. Unhealthy if exhausted restarts."""
        now = time.monotonic()
        recent = [t for t in self._restart_timestamps if now - t < self._restart_window]
        return len(recent) < self._max_restarts

    def _should_restart(self) -> bool:
        """Check if restart is allowed under the fixed-window limit."""
        now = time.monotonic()
        # Prune old timestamps
        self._restart_timestamps = [
            t for t in self._restart_timestamps if now - t < self._restart_window
        ]
        return len(self._restart_timestamps) < self._max_restarts

    async def _supervised_loop(self) -> None:
        """Wrap the orchestrator's run loop with supervision."""
        await self._orchestrator.start()
        # Wait for the orchestrator's task to finish
        orch_task = self._orchestrator._task
        if orch_task is None:
            logger.error("Orchestrator failed to create task")
            return

        try:
            await orch_task
        except asyncio.CancelledError:
            logger.info("Background task cancelled (normal shutdown)")
            raise
        except Exception as e:
            self._crash_count += 1
            logger.critical(
                "Background task crashed unexpectedly: %s (crash #%d)",
                e,
                self._crash_count,
                exc_info=True,
            )
            if self._stopping:
                logger.info("Supervisor is stopping, not restarting after crash")
                return
            if self._should_restart():
                self._restart_timestamps.append(time.monotonic())
                logger.warning(
                    "Restarting background task (restart %d/%d in last %ds)",
                    len(self._restart_timestamps),
                    self._max_restarts,
                    self._restart_window,
                )
                self._restart_task = asyncio.create_task(self._restart_after_delay())
            else:
                logger.critical(
                    "Background task restart limit exhausted (%d crashes in %ds). "
                    "Manual intervention required.",
                    self._max_restarts,
                    self._restart_window,
                )

    async def _restart_after_delay(self, delay: float = 5.0) -> None:
        """Restart the orchestrator after a brief delay."""
        logger.info("Waiting %.1fs before restarting background task...", delay)
        await asyncio.sleep(delay)
        if self._stopping:
            logger.info("Supervisor is stopping, skipping restart")
            return
        # Reset orchestrator internal state
        self._orchestrator._task = None
        await self.start()
