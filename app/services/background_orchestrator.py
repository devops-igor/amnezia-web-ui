"""BackgroundTaskOrchestrator — orchestrates periodic background operations with error isolation."""

import asyncio
import logging
from datetime import datetime
from typing import Any
from typing import Dict
from typing import List
from typing import Optional

from config import get_db
from app.services.background import perform_mass_operations
from app.services.background import sync_users_with_remnawave
from app.utils.helpers import get_protocol_manager
from app.utils.helpers import get_ssh

logger = logging.getLogger(__name__)


class BackgroundTaskOrchestrator:
    """Orchestrates periodic background operations with error isolation."""

    def __init__(self) -> None:
        self._task: Optional[asyncio.Task] = None

    # === Individual Operations ===

    async def check_expiry(
        self, now: datetime, user: Dict[str, Any], uid: str, to_disable_uids: List[str]
    ) -> None:
        """Check and disable expired users.

        Currently embedded in sync_traffic's inner loop.
        Extract expiry date checking into its own method.
        """
        exp_str = user.get("expiration_date")
        if exp_str and user.get("enabled", True):
            try:
                exp_date = datetime.fromisoformat(exp_str)
                if now > exp_date:
                    logger.info(
                        "Subscription expired for user %s (expired at %s)",
                        user["username"],
                        exp_str,
                    )
                    if uid not in to_disable_uids:
                        to_disable_uids.append(uid)
            except Exception:
                pass

    async def sync_traffic(self) -> None:
        """Sync traffic from all servers and enforce limits.

        This is the core operation from the old periodic_background_tasks.
        It includes: server traffic sync, traffic reset checks, monthly rollover,
        limit enforcement, disabling over-limit/expired users, and telemt quota.
        """
        # --- TRAFFIC SYNC & LIMITS ---
        logger.info("Starting background traffic sync...")
        db = get_db()

        servers = db.get_all_servers()
        all_conns = db.get_all_connections()

        conns_by_server: Dict[str, List[Dict]] = {}
        for uc in all_conns:
            sid = uc["server_id"]
            conns_by_server.setdefault(sid, []).append(uc)

        updates: List[tuple] = []

        ssh = None
        for server in servers:
            sid = server["id"]
            if sid not in conns_by_server:
                continue
            try:
                ssh = get_ssh(server)
                await asyncio.to_thread(ssh.connect)
                for proto in ["awg", "awg2", "awg_legacy", "xray", "telemt"]:
                    if proto in server.get("protocols", {}):
                        try:
                            manager = get_protocol_manager(ssh, proto)
                            clients = await asyncio.to_thread(manager.get_clients, proto)
                        except Exception as e:
                            logger.error(
                                "get_clients failed for server %s proto %s: %s",
                                sid,
                                proto,
                                e,
                            )
                            continue
                        client_bytes: Dict[str, Dict[str, int]] = {}
                        for c in clients:
                            rx = c.get("userData", {}).get("dataReceivedBytes", 0)
                            tx = c.get("userData", {}).get("dataSentBytes", 0)
                            client_bytes[c.get("clientId")] = {"rx": rx, "tx": tx}

                        for uc in conns_by_server[sid]:
                            if uc["protocol"] == proto and uc["client_id"] in client_bytes:
                                curr_rx = client_bytes[uc["client_id"]]["rx"]
                                curr_tx = client_bytes[uc["client_id"]]["tx"]
                                last_rx = uc.get("last_rx")
                                last_tx = uc.get("last_tx")
                                if last_rx is None and last_tx is None:
                                    last_bytes = uc.get("last_bytes", 0)
                                    last_rx = last_bytes // 2
                                    last_tx = last_bytes - last_rx
                                else:
                                    last_rx = last_rx or 0
                                    last_tx = last_tx or 0
                                rx_delta = curr_rx - last_rx if curr_rx >= last_rx else curr_rx
                                tx_delta = curr_tx - last_tx if curr_tx >= last_tx else curr_tx
                                updates.append((uc["id"], rx_delta, tx_delta, curr_rx, curr_tx))
            except asyncio.CancelledError:
                raise
            except Exception as e:
                sid = server["id"]
                logger.error("Traffic sync error for server %s: %s", sid, e, exc_info=True)
            finally:
                if ssh:
                    await asyncio.to_thread(ssh.disconnect)
        now = datetime.now()
        users_map = {u["id"]: u for u in db.get_all_users()}

        to_disable_uids: List[str] = []

        # === MONTHLY ROLLOVER: Runs unconditionally every cycle ===
        for uid, u in users_map.items():
            monthly_reset_iso = u.get("monthly_reset_at", "")
            if not monthly_reset_iso:
                db.update_user(
                    uid,
                    {
                        "monthly_rx": 0,
                        "monthly_tx": 0,
                        "monthly_reset_at": now.isoformat(),
                    },
                )
                u["monthly_rx"] = 0
                u["monthly_tx"] = 0
                u["monthly_reset_at"] = now.isoformat()
                logger.debug(
                    "Initialized monthly traffic for user %s",
                    u["username"],
                )
            else:
                try:
                    monthly_last = datetime.fromisoformat(monthly_reset_iso)
                    if now.month != monthly_last.month or now.year != monthly_last.year:
                        db.update_user(
                            uid,
                            {
                                "monthly_rx": 0,
                                "monthly_tx": 0,
                                "monthly_reset_at": now.isoformat(),
                            },
                        )
                        logger.info(
                            "Monthly rollover for user %s (reset from %s)",
                            u["username"],
                            monthly_reset_iso,
                        )
                        u["monthly_rx"] = 0
                        u["monthly_tx"] = 0
                        u["monthly_reset_at"] = now.isoformat()
                except Exception:
                    logger.warning(
                        "Invalid monthly_reset_at for user %s: %s",
                        u.get("username", "?"),
                        monthly_reset_iso,
                    )

        # === TRAFFIC DELTA PROCESSING: Only when there are updates ===
        if updates:
            for uc_id, rx_delta, tx_delta, curr_rx, curr_tx in updates:
                uc = db.get_connection_by_id(uc_id)
                if uc:
                    # Update connection's last_rx/last_tx
                    db.update_connection(uc_id, {"last_rx": curr_rx, "last_tx": curr_tx})
                    uid = uc["user_id"]
                    if uid in users_map:
                        u = users_map[uid]
                        # Check if reset is needed BEFORE adding new consumption
                        strategy = u.get("traffic_reset_strategy", "never")
                        last_reset_iso = u.get("last_reset_at")

                        reset_needed = False
                        if strategy != "never" and last_reset_iso:
                            try:
                                last = datetime.fromisoformat(last_reset_iso)
                                if strategy == "daily":
                                    reset_needed = now.date() > last.date()
                                elif strategy == "weekly":
                                    reset_needed = (
                                        now.isocalendar()[1] != last.isocalendar()[1]
                                        or now.year != last.year
                                    )
                                elif strategy == "monthly":
                                    reset_needed = now.month != last.month or now.year != last.year
                            except Exception:
                                pass

                        if reset_needed:
                            logger.info(
                                "Resetting traffic for user %s (strategy: %s)",
                                u["username"],
                                strategy,
                            )
                            db.update_user(
                                uid,
                                {
                                    "traffic_used": 0,
                                    "last_reset_at": now.isoformat(),
                                },
                            )
                            u["traffic_used"] = 0
                            u["last_reset_at"] = now.isoformat()

                        # Update both resettable and total traffic (combined RX+TX)
                        delta = rx_delta + tx_delta
                        new_used = u.get("traffic_used", 0) + delta
                        new_total = u.get("traffic_total", 0) + delta

                        # Update separate RX/TX totals
                        new_total_rx = u.get("traffic_total_rx", 0) + rx_delta
                        new_total_tx = u.get("traffic_total_tx", 0) + tx_delta

                        # Update monthly RX/TX
                        new_monthly_rx = u.get("monthly_rx", 0) + rx_delta
                        new_monthly_tx = u.get("monthly_tx", 0) + tx_delta

                        db.update_user(
                            uid,
                            {
                                "traffic_used": new_used,
                                "traffic_total": new_total,
                                "traffic_total_rx": new_total_rx,
                                "traffic_total_tx": new_total_tx,
                                "monthly_rx": new_monthly_rx,
                                "monthly_tx": new_monthly_tx,
                            },
                        )

                        # Update local cache
                        u["traffic_used"] = new_used
                        u["traffic_total"] = new_total
                        u["traffic_total_rx"] = new_total_rx
                        u["traffic_total_tx"] = new_total_tx
                        u["monthly_rx"] = new_monthly_rx
                        u["monthly_tx"] = new_monthly_tx
                        logger.debug(
                            "Traffic updated for %s: rx=%s, tx=%s, total_rx=%s, total_tx=%s",
                            u["username"],
                            rx_delta,
                            tx_delta,
                            new_total_rx,
                            new_total_tx,
                        )

                        limit = u.get("traffic_limit", 0)
                        if limit > 0 and new_used >= limit and u.get("enabled", True):
                            if uid not in to_disable_uids:
                                to_disable_uids.append(uid)

                        # Check expiration date
                        await self.check_expiry(now, u, uid, to_disable_uids)

        if to_disable_uids:
            logger.info("Traffic limit reached, disabling users: %s", to_disable_uids)
            await perform_mass_operations(toggle_uids=[(uid, False) for uid in to_disable_uids])

        # --- TELEM QUOTA ENFORCEMENT ---
        # Explicitly disable over-quota telemt users (side effect removed from get_clients)
        for server in servers:
            sid = server["id"]
            if "telemt" not in server.get("protocols", {}):
                continue
            try:
                ssh2 = get_ssh(server)
                await asyncio.to_thread(ssh2.connect)
                manager = get_protocol_manager(ssh2, "telemt")
                disabled = await asyncio.to_thread(manager.disable_overquota_users, "telemt")
                if disabled:
                    logger.info(
                        "Disabled %s over-quota users on telemt server %s",
                        len(disabled),
                        sid,
                    )
                await asyncio.to_thread(ssh2.disconnect)
            except Exception as e:
                logger.error("Error disabling over-quota users on server %s: %s", sid, e)

    async def sync_remnawave(self) -> None:
        """Sync users with Remnawave if enabled."""
        logger.info("Starting background Remnawave sync...")
        db = get_db()
        if db.get_setting("sync", {}).get("remnawave_sync_users"):
            count, msg = await sync_users_with_remnawave()
            logger.info("Background Remnawave sync finished: %s users updated. %s", count, msg)
        else:
            logger.info("Background Remnawave sync skipped (disabled in settings)")

    # === Orchestrator ===

    async def run_all(self) -> None:
        """Run all background operations. Errors in one don't prevent others."""
        operations = [
            ("traffic_sync", self.sync_traffic),
            ("remnawave_sync", self.sync_remnawave),
        ]
        for name, operation in operations:
            try:
                await operation()
            except Exception as e:
                logger.error("%s failed: %s", name, e, exc_info=True)

    # === Task Lifecycle ===

    async def start(self) -> None:
        """Start the periodic background task loop."""
        self._task = asyncio.create_task(self._run_loop())

    async def stop(self) -> None:
        """Cancel the background task."""
        if self._task and not self._task.done():
            self._task.cancel()
            try:
                await self._task
            except asyncio.CancelledError:
                logger.info("Background task cancelled successfully")

    async def _run_loop(self) -> None:
        """Main loop: sleep 60s initially, then run_all every 600s."""
        await asyncio.sleep(60)
        while True:
            try:
                await self.run_all()
            except asyncio.CancelledError:
                logger.info("Background task cancelled")
                raise
            except Exception as e:
                logger.error("Error in background task loop: %s", e, exc_info=True)
            await asyncio.sleep(600)
