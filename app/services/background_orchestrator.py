"""BackgroundTaskOrchestrator â€” orchestrates periodic background operations with error isolation."""

import asyncio
import logging
import re
from datetime import datetime
from typing import Any
from typing import Dict
from typing import List
from typing import Optional

from app.core.config import get_db
from app.services.background import perform_mass_operations
from app.services.background import sync_users_with_remnawave
from app.utils.helpers import get_protocol_manager
from app.utils.helpers import get_ssh
from app.models.schemas import normalize_protocol

logger = logging.getLogger(__name__)


class BackgroundTaskOrchestrator:
    """Orchestrates periodic background operations with error isolation."""

    HEALTH_PROBE_CLIENT_NAME: str = "Health Probe"
    _server_reachability: Dict[Any, Dict[str, Any]] = {}
    _health_probe_keys: Dict[Any, Dict[str, str]] = {}

    def __init__(self) -> None:
        self._task: Optional[asyncio.Task] = None

    # === Individual Operations ===

    async def check_expiry(
        self, now: datetime, user: Dict[str, Any], uid: str, to_disable_uids: List[str]
    ) -> None:
        """Check and disable expired users.

        Extracts expiry date checking into its own method.
        Supports both expires_at and expiration_date.
        """
        exp_str = user.get("expires_at") or user.get("expiration_date")
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
            except (ValueError, TypeError):
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
                for proto in ["awg", "xray", "telemt"]:
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
                            if (
                                normalize_protocol(uc["protocol"]) == proto
                                and uc["client_id"] in client_bytes
                            ):
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
        # Step 1: Snapshot previous month's leaderboard data ONCE before zeroing counters
        rollover_snapshot_taken = False
        for u in users_map.values():
            monthly_reset_iso = u.get("monthly_reset_at", "")
            if monthly_reset_iso:
                try:
                    monthly_last = datetime.fromisoformat(monthly_reset_iso)
                    if (
                        now.month != monthly_last.month or now.year != monthly_last.year
                    ) and not rollover_snapshot_taken:
                        snapshot_year = monthly_last.year
                        snapshot_month = monthly_last.month
                        saved_count = db.save_leaderboard_snapshot(snapshot_year, snapshot_month)
                        if saved_count > 0:
                            logger.info(
                                "Saved leaderboard snapshot for %d-%d (%d entries) before rollover",
                                snapshot_year,
                                snapshot_month,
                                saved_count,
                            )
                        rollover_snapshot_taken = True
                        break
                except (ValueError, TypeError):
                    pass

        # Step 2: Reset monthly counters for each user
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
                except (ValueError, TypeError):
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
                    # Accumulate per-connection traffic totals in one write
                    new_total_rx = uc.get("traffic_total_rx", 0) + rx_delta
                    new_total_tx = uc.get("traffic_total_tx", 0) + tx_delta
                    new_total = uc.get("traffic_total", 0) + rx_delta + tx_delta
                    db.update_connection(
                        uc_id,
                        {
                            "last_rx": curr_rx,
                            "last_tx": curr_tx,
                            "traffic_total_rx": new_total_rx,
                            "traffic_total_tx": new_total_tx,
                            "traffic_total": new_total,
                        },
                    )
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
                            except (ValueError, TypeError):
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

        # Unconditional check_expiry for all users in users_map
        for uid, u in users_map.items():
            await self.check_expiry(now, u, uid, to_disable_uids)

        if to_disable_uids:
            logger.info("Traffic limit or expiration reached, disabling users: %s", to_disable_uids)
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

    async def check_server_reachability(self) -> Dict[Any, Dict[str, Any]]:
        """Test server reachability using AmneziaWG protocol for AWG servers and socket probe for others.

        Runs every 5 minutes as part of the background orchestrator schedule (Auto Trials).
        Client statuses and handshakes are NEVER monitored.
        """
        import time
        from app.managers.awg_health import check_awg_reachability, run_auto_trial_profiles

        logger.info("Starting background server reachability check...")
        db = get_db()
        servers = db.get_all_servers()
        results: Dict[Any, Dict[str, Any]] = {}

        for server in servers:
            sid = server["id"]
            host = server.get("host", "")
            if not host:
                continue

            preserved_auto_trials = None
            protocols = server.get("protocols", {})
            awg_info = protocols.get("awg", {})

            if awg_info.get("installed"):
                port = int(awg_info.get("port") or 55424)
                awg_params = awg_info.get("awg_params") or {}
                server_pub = awg_info.get("public_key") or ""
                psk = awg_info.get("psk") or ""

                cached_probe = BackgroundTaskOrchestrator._health_probe_keys.get(sid, {})
                client_priv = cached_probe.get("client_priv")
                if not server_pub:
                    server_pub = cached_probe.get("server_pub", "")
                if not psk:
                    psk = cached_probe.get("psk", "")

                # If server_pub or client_priv is missing, attempt to fetch/provision dedicated Health Probe peer via SSH
                if not server_pub or not client_priv:
                    ssh = None
                    try:
                        ssh = get_ssh(server)
                        await asyncio.to_thread(ssh.connect)
                        mgr = get_protocol_manager(ssh, "awg")
                        if not server_pub:
                            server_pub = await asyncio.to_thread(mgr._get_server_public_key, "awg")
                        if not psk:
                            psk = await asyncio.to_thread(mgr._get_server_psk, "awg")

                        if not client_priv:
                            # Look for existing dedicated "Health Probe" client on server
                            clients = await asyncio.to_thread(mgr.get_clients, "awg")
                            for cl in clients:
                                u_data = cl.get("userData", {})
                                c_name = u_data.get("clientName", "")
                                if c_name.strip().lower() == "health probe":
                                    priv = u_data.get("clientPrivateKey")
                                    if priv:
                                        client_priv = priv
                                        break

                            # If no "Health Probe" peer exists, provision one
                            if not client_priv:
                                logger.info(
                                    "Health Probe client not found on server %s; provisioning dedicated peer...",
                                    sid,
                                )
                                new_client = await asyncio.to_thread(
                                    mgr.add_client,
                                    "awg",
                                    self.HEALTH_PROBE_CLIENT_NAME,
                                    host,
                                    port,
                                    stored_awg_params=awg_params,
                                    server_protocols=protocols,
                                )
                                if isinstance(new_client, dict):
                                    conf_str = new_client.get("config", "")
                                    match = re.search(r"PrivateKey\s*=\s*(\S+)", conf_str)
                                    if match:
                                        client_priv = match.group(1).strip()
                                    if not client_priv:
                                        updated_clients = await asyncio.to_thread(
                                            mgr.get_clients, "awg"
                                        )
                                        for cl in updated_clients:
                                            if (
                                                cl.get("userData", {})
                                                .get("clientName", "")
                                                .strip()
                                                .lower()
                                                == "health probe"
                                            ):
                                                client_priv = cl.get("userData", {}).get(
                                                    "clientPrivateKey"
                                                )
                                                break
                                logger.info(
                                    "Provisioned Health Probe peer for server %s (key_found=%s)",
                                    sid,
                                    bool(client_priv),
                                )

                        if client_priv and server_pub:
                            BackgroundTaskOrchestrator._health_probe_keys[sid] = {
                                "client_priv": client_priv,
                                "server_pub": server_pub,
                                "psk": psk,
                            }
                    except Exception as e:
                        logger.debug(
                            "Could not fetch/provision AWG health probe via SSH for server %s: %s",
                            sid,
                            e,
                        )
                    finally:
                        if ssh:
                            try:
                                await asyncio.to_thread(ssh.disconnect)
                            except Exception:
                                pass

                if server_pub:
                    # Perform real AmneziaWG health check using dedicated health probe
                    try:
                        reach_res = await check_awg_reachability(
                            host=host,
                            port=port,
                            server_public_key=server_pub,
                            client_private_key=client_priv,
                            psk=psk,
                            awg_params=awg_params,
                            timeout=3.0,
                        )
                        # Also run Auto Trials profile probes
                        auto_trials = await run_auto_trial_profiles(
                            host=host,
                            port=port,
                            server_public_key=server_pub,
                            client_private_key=client_priv,
                            psk=psk,
                            awg_params=awg_params,
                            timeout=2.0,
                        )
                        reach_res["auto_trials"] = auto_trials

                        if reach_res["reachable"]:
                            results[sid] = reach_res
                            continue
                        else:
                            logger.debug(
                                "AWG UDP check failed (%s), falling back to SSH probe",
                                reach_res.get("error"),
                            )
                            preserved_auto_trials = reach_res.get("auto_trials")
                    except Exception as err:
                        logger.debug("AWG reachability check failed for server %s: %s", sid, err)

            # Fallback for non-AWG servers or if AWG handshake could not be initiated
            port = int(server.get("ssh_port") or server.get("port") or 22)
            t0 = time.time()
            try:
                reader, writer = await asyncio.wait_for(
                    asyncio.open_connection(host, port), timeout=3.0
                )
                writer.close()
                await writer.wait_closed()
                latency = int((time.time() - t0) * 1000)
                results[sid] = {
                    "reachable": True,
                    "latency_ms": latency,
                    "protocol": "tcp",
                    "last_checked": datetime.now().isoformat(),
                    "error": "",
                }
                if preserved_auto_trials is not None:
                    results[sid]["auto_trials"] = preserved_auto_trials
            except Exception as e:
                results[sid] = {
                    "reachable": False,
                    "latency_ms": 0,
                    "protocol": "tcp",
                    "last_checked": datetime.now().isoformat(),
                    "error": str(e),
                }
                if preserved_auto_trials is not None:
                    results[sid]["auto_trials"] = preserved_auto_trials

        BackgroundTaskOrchestrator._server_reachability = results
        return results

    @classmethod
    def get_cached_server_reachability(cls) -> Dict[Any, Dict[str, Any]]:
        """Return cached server reachability results without fake fallbacks."""
        return cls._server_reachability

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
            ("server_reachability", self.check_server_reachability),
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
        """Main loop: sleep 60s initially, then run_all every 300s (5 minutes)."""
        await asyncio.sleep(60)
        while True:
            try:
                await self.run_all()
            except asyncio.CancelledError:
                logger.info("Background task cancelled")
                raise
            except Exception as e:
                logger.error("Error in background task loop: %s", e, exc_info=True)
            await asyncio.sleep(300)
