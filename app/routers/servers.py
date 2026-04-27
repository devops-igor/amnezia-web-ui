"""Server API routes - all /api/servers/* endpoints for managing servers, protocols, and connections."""

import asyncio
import logging
import uuid
from datetime import datetime

from fastapi import APIRouter, Depends, Query, Request
from fastapi.responses import JSONResponse

from app.utils.helpers import _sanitize_error, generate_vpn_link, get_ssh, get_protocol_manager
from config import get_db
from dependencies import get_current_user, require_admin
from schemas import (
    AddConnectionRequest,
    AddServerRequest,
    ConnectionActionRequest,
    EditConnectionRequest,
    InstallProtocolRequest,
    ProtocolRequest,
    ServerConfigSaveRequest,
    ToggleConnectionRequest,
)
from awg_manager import AWGManager
from ssh_manager import SSHManager
from xray_manager import XrayManager

logger = logging.getLogger(__name__)

router = APIRouter(prefix="/api/servers")


@router.get("/")
async def api_list_servers(request: Request, user: dict = Depends(get_current_user)):
    """Return all servers as JSON."""
    db = get_db()
    servers = db.get_all_servers()
    return servers


CONTAINER_NAMES = {
    "awg": "amnezia-awg",
    "awg2": "amnezia-awg2",
    "awg_legacy": "amnezia-awg-legacy",
    "xray": "amnezia-xray",
    "telemt": "telemt",
    "dns": "amnezia-dns",
}


@router.post("/add")
async def api_add_server(
    request: Request, req: AddServerRequest, user: dict = Depends(require_admin)
):
    try:
        host = req.host.strip()
        username = req.username.strip()
        name = req.name.strip() or host
        if not host or not username:
            return JSONResponse({"error": "Host and username are required"}, status_code=400)
        if not req.password and not req.private_key:
            return JSONResponse({"error": "Password or SSH key is required"}, status_code=400)

        ssh = SSHManager(host, req.ssh_port, username, req.password, req.private_key)
        try:
            await asyncio.to_thread(ssh.connect)
            server_info = await asyncio.to_thread(ssh.test_connection)
            await asyncio.to_thread(ssh.disconnect)
        except Exception as e:
            return JSONResponse(
                {"error": f"Connection failed: {_sanitize_error(str(e))}"}, status_code=400
            )

        server = {
            "name": name,
            "host": host,
            "ssh_port": req.ssh_port,
            "username": username,
            "password": req.password,
            "private_key": req.private_key,
            "server_info": server_info,
            "protocols": {},
        }
        db = get_db()
        db.create_server(server)
        server_count = db.get_server_count()
        return {
            "status": "success",
            "server_id": server_count - 1,
            "server_info": server_info,
        }
    except Exception as e:
        logger.exception("Error adding server")
        return JSONResponse({"error": _sanitize_error(str(e))}, status_code=500)


@router.post("/{server_id}/delete")
async def api_delete_server(request: Request, server_id: int, user: dict = Depends(require_admin)):
    try:
        db = get_db()
        if db.get_server_by_id(server_id) is None:
            return JSONResponse({"error": "Server not found"}, status_code=404)
        db.delete_server(server_id)
        return {"status": "success"}
    except Exception as e:
        return JSONResponse({"error": _sanitize_error(str(e))}, status_code=500)


@router.post("/{server_id}/reboot")
async def api_reboot_server(request: Request, server_id: int, user: dict = Depends(require_admin)):
    try:
        db = get_db()
        server = db.get_server_by_id(server_id)
        if server is None:
            return JSONResponse({"error": "Server not found"}, status_code=404)
        ssh = get_ssh(server)
        await asyncio.to_thread(ssh.connect)
        try:
            await asyncio.to_thread(ssh.run_sudo_command, "nohup reboot > /dev/null 2>&1 &")
        except Exception:
            pass
        try:
            await asyncio.to_thread(ssh.disconnect)
        except:
            pass
        return {"status": "success"}
    except Exception as e:
        logger.exception("Error rebooting server")
        return JSONResponse({"error": _sanitize_error(str(e))}, status_code=500)


@router.post("/{server_id}/clear")
async def api_clear_server(request: Request, server_id: int, user: dict = Depends(require_admin)):
    try:
        db = get_db()
        server = db.get_server_by_id(server_id)
        if server is None:
            return JSONResponse({"error": "Server not found"}, status_code=404)
        ssh = get_ssh(server)
        await asyncio.to_thread(ssh.connect)
        containers = [
            "amnezia-awg",
            "amnezia-awg2",
            "amnezia-awg-legacy",
            "amnezia-xray",
            "telemt",
            "amnezia-dns",
        ]
        for c in containers:
            await asyncio.to_thread(ssh.run_sudo_command, f"docker stop {c} || true")
            await asyncio.to_thread(ssh.run_sudo_command, f"docker rm {c} || true")
        await asyncio.to_thread(ssh.run_sudo_command, "docker network rm amnezia-dns-net || true")
        await asyncio.to_thread(ssh.run_sudo_command, "rm -rf /opt/amnezia")

        db.update_server(server["id"], {"protocols": {}})
        await asyncio.to_thread(ssh.disconnect)
        return {"status": "success"}
    except Exception as e:
        logger.exception("Error clearing server")
        return JSONResponse({"error": _sanitize_error(str(e))}, status_code=500)


@router.post("/{server_id}/stats")
async def api_server_stats(request: Request, server_id: int, user: dict = Depends(require_admin)):
    try:
        db = get_db()
        server = db.get_server_by_id(server_id)
        if server is None:
            return JSONResponse({"error": "Server not found"}, status_code=404)
        ssh = get_ssh(server)
        await asyncio.to_thread(ssh.connect)
        stats = {}
        out, _, _ = await asyncio.to_thread(
            ssh.run_command,
            "top -bn1 | grep 'Cpu(s)' | awk '{print $2}' | cut -d'%' -f1 2>/dev/null || "
            'awk \'{u=$2+$4; t=$2+$4+$5; if(NR==1){pu=u;pt=t} else printf "%.1f", '
            "(u-pu)/(t-pt)*100}' "
            "<(grep 'cpu ' /proc/stat) <(sleep 0.5 && grep 'cpu ' /proc/stat) 2>/dev/null",
        )
        try:
            stats["cpu"] = round(float(out.strip().split("\n")[0]), 1)
        except (ValueError, IndexError):
            stats["cpu"] = 0
        out, _, _ = await asyncio.to_thread(
            ssh.run_command, "free -b | awk 'NR==2{printf \"%d %d\", $3, $2}'"
        )
        try:
            parts = out.strip().split()
            used, total = int(parts[0]), int(parts[1])
            stats.update(
                ram_used=used,
                ram_total=total,
                ram_percent=round(used / total * 100, 1) if total > 0 else 0,
            )
        except (ValueError, IndexError):
            stats.update(ram_used=0, ram_total=0, ram_percent=0)
        out, _, _ = await asyncio.to_thread(
            ssh.run_command, "df -B1 / | awk 'NR==2{printf \"%d %d\", $3, $2}'"
        )
        try:
            parts = out.strip().split()
            used, total = int(parts[0]), int(parts[1])
            stats.update(
                disk_used=used,
                disk_total=total,
                disk_percent=round(used / total * 100, 1) if total > 0 else 0,
            )
        except (ValueError, IndexError):
            stats.update(disk_used=0, disk_total=0, disk_percent=0)
        out, _, _ = await asyncio.to_thread(
            ssh.run_command,
            "DEV=$(ip route | awk '/default/ {print $5}' | head -1); "
            'cat /proc/net/dev | awk -v dev="$DEV:" \'$1==dev{printf "%d %d", $2, $10}\'',
        )
        try:
            parts = out.strip().split()
            stats["net_rx"], stats["net_tx"] = int(parts[0]), int(parts[1])
        except (ValueError, IndexError):
            stats["net_rx"] = stats["net_tx"] = 0
        out, _, _ = await asyncio.to_thread(ssh.run_command, "uptime -p 2>/dev/null || uptime")
        stats["uptime"] = out.strip()
        await asyncio.to_thread(ssh.disconnect)
        return stats
    except Exception as e:
        logger.exception("Error getting server stats")
        return JSONResponse({"error": _sanitize_error(str(e))}, status_code=500)


@router.post("/{server_id}/check")
async def api_check_server(request: Request, server_id: int, user: dict = Depends(require_admin)):
    try:
        db = get_db()
        server = db.get_server_by_id(server_id)
        if server is None:
            return JSONResponse({"error": "Server not found"}, status_code=404)
        ssh = get_ssh(server)
        await asyncio.to_thread(ssh.connect)
        # Just use awg's docker checker since it uses the same command
        manager = get_protocol_manager(ssh, "awg")
        status = {
            "connection": "ok",
            "docker_installed": await asyncio.to_thread(manager.check_docker_installed),
            "protocols": {},
        }

        changed = False
        if "protocols" not in server:
            server["protocols"] = {}

        async def check_proto(proto: str):
            try:
                p_manager = get_protocol_manager(ssh, proto)
                result = await asyncio.to_thread(p_manager.get_server_status, proto)
                db_proto = server.get("protocols", {}).get(proto, {})
                if not result.get("port") and db_proto.get("port"):
                    result["port"] = db_proto["port"]
                return proto, result, None
            except Exception as e:
                return proto, None, str(e)

        check_results = await asyncio.gather(
            *[check_proto(p) for p in ["awg", "awg2", "awg_legacy", "xray", "telemt", "dns"]]
        )
        for proto, result, err in check_results:
            if err:
                status["protocols"][proto] = {"error": err}
            else:
                status["protocols"][proto] = result
                if result.get("container_exists"):
                    if proto not in server["protocols"]:
                        server["protocols"][proto] = {
                            "installed": True,
                            "port": result.get("port", "55424"),
                            "awg_params": result.get("awg_params", {}),
                        }
                        changed = True
                else:
                    if proto in server["protocols"]:
                        del server["protocols"][proto]
                        changed = True

        if changed:
            db.update_server(server["id"], {"protocols": server["protocols"]})

        await asyncio.to_thread(ssh.disconnect)
        return status
    except Exception as e:
        logger.exception("Error checking server")
        return JSONResponse(
            {"error": _sanitize_error(str(e)), "connection": "failed"}, status_code=500
        )


@router.post("/{server_id}/install")
async def api_install_protocol(
    request: Request,
    server_id: int,
    req: InstallProtocolRequest,
    user: dict = Depends(require_admin),
):
    try:
        db = get_db()
        server = db.get_server_by_id(server_id)
        if server is None:
            return JSONResponse({"error": "Server not found"}, status_code=404)
        if req.protocol not in ["awg", "awg2", "awg_legacy", "xray", "telemt", "dns"]:
            return JSONResponse({"error": "Invalid protocol type"}, status_code=400)

        ssh = get_ssh(server)
        await asyncio.to_thread(ssh.connect)
        manager = get_protocol_manager(ssh, req.protocol)

        # Pass parameters to installer
        if req.protocol == "telemt":
            result = await asyncio.to_thread(
                manager.install_protocol,
                protocol_type=req.protocol,
                port=req.port,
                tls_emulation=req.tls_emulation if req.tls_emulation is not None else True,
                tls_domain=req.tls_domain,
                max_connections=req.max_connections if req.max_connections is not None else 0,
            )
        elif req.protocol == "xray":
            result = await asyncio.to_thread(manager.install_protocol, port=req.port)
        else:
            result = await asyncio.to_thread(manager.install_protocol, req.protocol, port=req.port)

        new_protocols = dict(server.get("protocols", {}))
        new_protocols[req.protocol] = {
            "installed": True,
            "port": req.port,
            "awg_params": result.get("awg_params", {}),
        }
        db.update_server(server["id"], {"protocols": new_protocols})
        await asyncio.to_thread(ssh.disconnect)
        return result
    except Exception as e:
        logger.exception("Error installing protocol")
        return JSONResponse({"error": _sanitize_error(str(e))}, status_code=500)


@router.post("/{server_id}/uninstall")
async def api_uninstall_protocol(
    request: Request, server_id: int, req: ProtocolRequest, user: dict = Depends(require_admin)
):
    try:
        db = get_db()
        server = db.get_server_by_id(server_id)
        if server is None:
            return JSONResponse({"error": "Server not found"}, status_code=404)
        ssh = get_ssh(server)
        await asyncio.to_thread(ssh.connect)
        manager = get_protocol_manager(ssh, req.protocol)
        if req.protocol == "xray":
            await asyncio.to_thread(manager.remove_container)
        else:
            await asyncio.to_thread(manager.remove_container, req.protocol)
        new_protocols = dict(server.get("protocols", {}))
        if req.protocol in new_protocols:
            del new_protocols[req.protocol]
            db.update_server(server["id"], {"protocols": new_protocols})
        await asyncio.to_thread(ssh.disconnect)
        return {"status": "success"}
    except Exception as e:
        logger.exception("Error uninstalling protocol")
        return JSONResponse({"error": _sanitize_error(str(e))}, status_code=500)


CONTAINER_NAMES = {
    "awg": "amnezia-awg",
    "awg2": "amnezia-awg2",
    "awg_legacy": "amnezia-awg-legacy",
    "xray": "amnezia-xray",
    "telemt": "telemt",
    "dns": "amnezia-dns",
}


@router.post("/{server_id}/container/toggle")
async def api_container_toggle(
    request: Request, server_id: int, req: ProtocolRequest, user: dict = Depends(require_admin)
):
    """Start or stop a protocol Docker container."""
    try:
        db = get_db()
        server = db.get_server_by_id(server_id)
        if server is None:
            return JSONResponse({"error": "Server not found"}, status_code=404)
        container = CONTAINER_NAMES.get(req.protocol)
        if not container:
            return JSONResponse({"error": "Unknown protocol"}, status_code=400)
        ssh = get_ssh(server)
        await asyncio.to_thread(ssh.connect)
        # Check current state
        out, _, _ = await asyncio.to_thread(
            ssh.run_sudo_command,
            f"docker inspect -f '{{{{.State.Running}}}}' {container} 2>/dev/null",
        )
        is_running = out.strip().lower() == "true"
        if is_running:
            await asyncio.to_thread(ssh.run_sudo_command, f"docker stop {container}")
            action = "stopped"
        else:
            await asyncio.to_thread(ssh.run_sudo_command, f"docker start {container}")
            action = "started"
        await asyncio.to_thread(ssh.disconnect)
        return {"status": "success", "action": action, "container": container}
    except Exception as e:
        logger.exception("Error toggling container")
        return JSONResponse({"error": _sanitize_error(str(e))}, status_code=500)


@router.post("/{server_id}/server_config")
async def api_server_config(
    request: Request, server_id: int, req: ProtocolRequest, user: dict = Depends(require_admin)
):
    """Get the raw server-side WireGuard/Xray configuration."""
    try:
        db = get_db()
        server = db.get_server_by_id(server_id)
        if server is None:
            return JSONResponse({"error": "Server not found"}, status_code=404)
        ssh = get_ssh(server)
        await asyncio.to_thread(ssh.connect)
        if req.protocol == "xray":
            mgr = XrayManager(ssh)
            data_json = await asyncio.to_thread(mgr._get_server_json)
            import json as _json

            config = _json.dumps(data_json, indent=2, ensure_ascii=False) if data_json else ""
        elif req.protocol == "telemt":
            from telemt_manager import TelemtManager

            mgr = TelemtManager(ssh)
            config = await asyncio.to_thread(mgr._get_server_config)
        else:
            mgr = AWGManager(ssh)
            config = await asyncio.to_thread(mgr._get_server_config, req.protocol)
        await asyncio.to_thread(ssh.disconnect)
        return {"config": config}
    except Exception as e:
        logger.exception("Error getting server config")
        return JSONResponse({"error": _sanitize_error(str(e))}, status_code=500)


@router.post("/{server_id}/server_config/save")
async def api_server_config_save(
    request: Request,
    server_id: int,
    req: ServerConfigSaveRequest,
    user: dict = Depends(require_admin),
):
    """Save the raw server-side WireGuard/Xray configuration and apply changes."""
    try:
        db = get_db()
        server = db.get_server_by_id(server_id)
        if server is None:
            return JSONResponse({"error": "Server not found"}, status_code=404)
        ssh = get_ssh(server)
        await asyncio.to_thread(ssh.connect)
        if req.protocol == "xray":
            mgr = XrayManager(ssh)
            import json as _json

            try:
                data_json = _json.loads(req.config)
            except Exception:
                await asyncio.to_thread(ssh.disconnect)
                return JSONResponse({"error": "Invalid JSON format"}, status_code=400)
            await asyncio.to_thread(mgr._save_server_json, data_json)
        elif req.protocol == "telemt":
            from telemt_manager import TelemtManager

            mgr = TelemtManager(ssh)
            await asyncio.to_thread(mgr.save_server_config, req.protocol, req.config)
        else:
            mgr = AWGManager(ssh)
            await asyncio.to_thread(mgr.save_server_config, req.protocol, req.config)
        await asyncio.to_thread(ssh.disconnect)
        return {"status": "success"}
    except Exception as e:
        logger.exception("Error saving server config")
        return JSONResponse({"error": _sanitize_error(str(e))}, status_code=500)


@router.get("/{server_id}/connections")
async def api_get_connections(
    request: Request,
    server_id: int,
    protocol: str = Query(default="awg"),
    user: dict = Depends(require_admin),
):
    if not protocol:
        protocol = "awg"
    try:
        db = get_db()
        server = db.get_server_by_id(server_id)
        if server is None:
            return JSONResponse({"error": "Server not found"}, status_code=404)
        ssh = get_ssh(server)
        await asyncio.to_thread(ssh.connect)
        manager = get_protocol_manager(ssh, protocol)
        clients = await asyncio.to_thread(manager.get_clients, protocol)
        await asyncio.to_thread(ssh.disconnect)

        # Enrich with user info from user_connections
        user_conns = db.get_connections_by_server_and_protocol(server_id, protocol)
        users = db.get_all_users()
        users_map = {u["id"]: u for u in users}
        for client in clients:
            cid = client.get("clientId", "")
            for uc in user_conns:
                if uc.get("client_id") == cid:
                    uid = uc.get("user_id")
                    u = users_map.get(uid)
                    if u:
                        client["assigned_user"] = u["username"]
                        client["assigned_user_id"] = uid
                    break
        return {"clients": clients}
    except Exception as e:
        logger.exception("Error getting connections")
        return JSONResponse({"error": _sanitize_error(str(e))}, status_code=500)


@router.post("/{server_id}/connections/add")
async def api_add_connection(
    request: Request, server_id: int, req: AddConnectionRequest, user: dict = Depends(require_admin)
):
    try:
        db = get_db()
        server = db.get_server_by_id(server_id)
        if server is None:
            return JSONResponse({"error": "Server not found"}, status_code=404)
        proto_info = server.get("protocols", {}).get(req.protocol, {})
        port = proto_info.get("port", "55424")
        ssh = get_ssh(server)
        await asyncio.to_thread(ssh.connect)
        manager = get_protocol_manager(ssh, req.protocol)

        if req.protocol == "telemt":
            result = await asyncio.to_thread(
                manager.add_client,
                req.protocol,
                req.name,
                server["host"],
                port,
                telemt_quota=req.telemt_quota,
                telemt_max_ips=req.telemt_max_ips,
                telemt_expiry=req.telemt_expiry,
            )
        else:
            result = await asyncio.to_thread(
                manager.add_client, req.protocol, req.name, server["host"], port
            )
        await asyncio.to_thread(ssh.disconnect)

        if result.get("config"):
            result["vpn_link"] = generate_vpn_link(result["config"])
        else:
            # API call failed — do not write to data.json, return error
            error_msg = result.get("error", "Failed to create connection")
            logger.error(f"Failed to add connection for {req.name}: {error_msg}")
            return JSONResponse({"error": error_msg}, status_code=500)

        # Link connection to user if specified
        if req.user_id:
            conn = {
                "id": str(uuid.uuid4()),
                "user_id": req.user_id,
                "server_id": server_id,
                "protocol": req.protocol,
                "client_id": result["client_id"],
                "name": req.name,
                "created_at": datetime.now().isoformat(),
            }
            db.create_connection(conn)

        return result
    except Exception as e:
        logger.exception("Error adding connection")
        return JSONResponse({"error": _sanitize_error(str(e))}, status_code=500)


@router.post("/{server_id}/connections/remove")
async def api_remove_connection(
    request: Request,
    server_id: int,
    req: ConnectionActionRequest,
    user: dict = Depends(require_admin),
):
    try:
        db = get_db()
        server = db.get_server_by_id(server_id)
        if server is None:
            return JSONResponse({"error": "Server not found"}, status_code=404)
        if not req.client_id:
            return JSONResponse({"error": "Client ID is required"}, status_code=400)
        ssh = get_ssh(server)
        await asyncio.to_thread(ssh.connect)
        manager = get_protocol_manager(ssh, req.protocol)
        await asyncio.to_thread(manager.remove_client, req.protocol, req.client_id)
        await asyncio.to_thread(ssh.disconnect)
        # Remove from user_connections
        db.delete_connection_by_client_id(req.client_id, server_id)
        return {"status": "success"}
    except Exception as e:
        logger.exception("Error removing connection")
        return JSONResponse({"error": _sanitize_error(str(e))}, status_code=500)


@router.post("/{server_id}/connections/edit")
async def api_edit_connection(
    request: Request,
    server_id: int,
    req: EditConnectionRequest,
    user: dict = Depends(require_admin),
):
    try:
        db = get_db()
        server = db.get_server_by_id(server_id)
        if server is None:
            return JSONResponse({"error": "Server not found"}, status_code=404)

        ssh = get_ssh(server)
        await asyncio.to_thread(ssh.connect)
        manager = get_protocol_manager(ssh, req.protocol)

        edit_params = {}
        if req.protocol == "telemt":
            edit_params["telemt_quota"] = req.telemt_quota
            edit_params["telemt_max_ips"] = req.telemt_max_ips
            edit_params["telemt_expiry"] = req.telemt_expiry

        result = await asyncio.to_thread(
            manager.edit_client, req.protocol, req.client_id, edit_params
        )
        await asyncio.to_thread(ssh.disconnect)
        return result
    except Exception as e:
        logger.exception("Error editing connection")
        return JSONResponse({"error": _sanitize_error(str(e))}, status_code=500)


@router.post("/{server_id}/connections/config")
async def api_get_connection_config(
    request: Request,
    server_id: int,
    req: ConnectionActionRequest,
    user: dict = Depends(get_current_user),
):
    try:
        db = get_db()
        server = db.get_server_by_id(server_id)
        if server is None:
            return JSONResponse({"error": "Server not found"}, status_code=404)
        # Users can only view their own connections
        if user["role"] == "user":
            all_conns = db.get_connections_by_server_and_protocol(server_id, req.protocol)
            owned = any(
                c
                for c in all_conns
                if c.get("client_id") == req.client_id and c.get("user_id") == user["id"]
            )
            if not owned:
                return JSONResponse({"error": "Forbidden"}, status_code=403)
        if not req.client_id:
            return JSONResponse({"error": "Client ID is required"}, status_code=400)
        proto_info = server.get("protocols", {}).get(req.protocol, {})
        port = proto_info.get("port", "55424")
        ssh = get_ssh(server)
        await asyncio.to_thread(ssh.connect)
        manager = get_protocol_manager(ssh, req.protocol)
        config = await asyncio.to_thread(
            manager.get_client_config, req.protocol, req.client_id, server["host"], port
        )
        await asyncio.to_thread(ssh.disconnect)
        vpn_link = generate_vpn_link(config) if config else ""
        return {"config": config, "vpn_link": vpn_link}
    except Exception as e:
        logger.exception("Error getting connection config")
        return JSONResponse({"error": _sanitize_error(str(e))}, status_code=500)


@router.post("/{server_id}/connections/toggle")
async def api_toggle_connection(
    request: Request,
    server_id: int,
    req: ToggleConnectionRequest,
    user: dict = Depends(require_admin),
):
    try:
        db = get_db()
        server = db.get_server_by_id(server_id)
        if server is None:
            return JSONResponse({"error": "Server not found"}, status_code=404)
        if not req.client_id:
            return JSONResponse({"error": "Client ID is required"}, status_code=400)
        ssh = get_ssh(server)
        await asyncio.to_thread(ssh.connect)
        manager = get_protocol_manager(ssh, req.protocol)
        await asyncio.to_thread(manager.toggle_client, req.protocol, req.client_id, req.enable)
        await asyncio.to_thread(ssh.disconnect)
        status = "enabled" if req.enable else "disabled"
        return {"status": "success", "enabled": req.enable, "message": f"Connection {status}"}
    except Exception as e:
        logger.exception("Error toggling connection")
        return JSONResponse({"error": _sanitize_error(str(e))}, status_code=500)


@router.get("/{server_id}/{protocol}/clients")
async def api_get_server_clients(
    request: Request, server_id: int, protocol: str, user: dict = Depends(require_admin)
):
    try:
        db = get_db()
        server = db.get_server_by_id(server_id)
        if server is None:
            return JSONResponse({"error": "Server not found"}, status_code=404)
        ssh = get_ssh(server)
        await asyncio.to_thread(ssh.connect)
        manager = get_protocol_manager(ssh, protocol)
        clients = await asyncio.to_thread(manager.get_clients, protocol)
        await asyncio.to_thread(ssh.disconnect)

        # Filter: only show clients that are not assigned to anyone in the panel
        assigned_conns = db.get_connections_by_server_and_protocol(server_id, protocol)
        assigned_ids = {c["client_id"] for c in assigned_conns}

        filtered = []
        for c in clients:
            if c["clientId"] not in assigned_ids:
                filtered.append(
                    {
                        "id": c["clientId"],
                        "name": c.get("userData", {}).get("clientName", "Unnamed"),
                    }
                )

        return {"clients": filtered}
    except Exception as e:
        logger.exception("Error getting server clients")
        return JSONResponse({"error": _sanitize_error(str(e))}, status_code=500)
