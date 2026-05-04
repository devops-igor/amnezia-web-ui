"""Settings routes - settings page and API endpoints."""

import json
import logging

from fastapi import APIRouter, Depends, Request, UploadFile, File
from fastapi.responses import JSONResponse, Response

from app.utils.helpers import _sanitize_error, serialize_protocols
from app.utils.templates import tpl
from config import get_db
from dependencies import require_admin
from schemas import SaveSettingsRequest

logger = logging.getLogger(__name__)

router = APIRouter(tags=["settings"])


@router.get("/settings")
async def settings_page(request: Request, user: dict = Depends(require_admin)):
    db = get_db()
    return tpl(
        request, "settings.html", settings=db.get_all_settings(), servers=db.get_all_servers()
    )


@router.get("/api/settings")
async def api_get_settings(request: Request, user: dict = Depends(require_admin)):
    db = get_db()
    return db.get_all_settings()


@router.post("/api/settings/save")
async def save_settings(
    request: Request, payload: SaveSettingsRequest, user: dict = Depends(require_admin)
):
    db = get_db()
    db.update_setting("appearance", payload.appearance.model_dump())
    db.update_setting("sync", payload.sync.model_dump())
    db.update_setting("captcha", payload.captcha.model_dump())
    db.update_setting("telegram", payload.telegram)
    db.update_setting("ssl", payload.ssl.model_dump())
    db.update_setting("limits", payload.limits.model_dump())
    db.update_setting("protocol_paths", payload.protocol_paths.model_dump())
    logger.info("Settings saved")

    return {"status": "success"}


@router.post("/api/settings/sync_now")
async def api_sync_now(request: Request, user: dict = Depends(require_admin)):
    from app.services.background import sync_users_with_remnawave

    count, msg = await sync_users_with_remnawave()
    return {"status": "success", "count": count, "message": msg}


@router.post("/api/settings/sync_delete")
async def api_sync_delete(request: Request, user: dict = Depends(require_admin)):
    from app.services.background import perform_mass_operations

    db = get_db()
    all_users = db.get_all_users()
    to_delete_ids = [u["id"] for u in all_users if u.get("remnawave_uuid")]
    if to_delete_ids:
        await perform_mass_operations(delete_uids=to_delete_ids)
    return {"status": "success", "count": len(to_delete_ids)}


@router.get("/api/settings/backup/download")
async def api_backup_download(request: Request, user: dict = Depends(require_admin)):
    try:
        db = get_db()
        backup_data = db.load_data()
        # Strip credentials from backup — they must not be exported
        for srv in backup_data.get("servers", []):
            srv.pop("password", None)
            srv.pop("private_key", None)
            # Also strip sensitive protocol fields (defense-in-depth)
            if isinstance(srv.get("protocols"), dict):
                srv["protocols"] = serialize_protocols(srv["protocols"])
        backup_data["credentials_excluded"] = True
        backup_json = json.dumps(backup_data, indent=2, ensure_ascii=False)
        return Response(
            content=backup_json,
            media_type="application/json",
            headers={"Content-Disposition": "attachment; filename=data.json"},
        )
    except Exception as e:
        logger.exception("Error creating backup")
        return JSONResponse({"error": _sanitize_error(str(e))}, status_code=500)


@router.post("/api/settings/backup/restore")
async def api_backup_restore(
    request: Request, user: dict = Depends(require_admin), file: UploadFile = File(...)
):
    try:
        content = await file.read()
        if not content:
            return JSONResponse({"error": "Empty file"}, status_code=400)

        try:
            backup_data = json.loads(content)
        except json.JSONDecodeError:
            return JSONResponse({"error": "Invalid JSON format"}, status_code=400)

        # Basic structure validation
        required_keys = ["servers", "users"]
        missing = [k for k in required_keys if k not in backup_data]
        if missing:
            return JSONResponse(
                {"error": f'Invalid structure. Missing keys: {", ".join(missing)}'},
                status_code=400,
            )

        # Ensure types are correct
        if not isinstance(backup_data["servers"], list) or not isinstance(
            backup_data["users"], list
        ):
            return JSONResponse(
                {"error": "Invalid structure: servers and users must be lists"},
                status_code=400,
            )

        # If backup has credentials_excluded flag, set empty strings so
        # restore works without error (credentials must be re-entered)
        if backup_data.get("credentials_excluded"):
            logger.warning(
                "Restoring backup without credentials — "
                "SSH passwords and keys must be re-entered manually"
            )
            for srv in backup_data.get("servers", []):
                srv["password"] = ""
                srv["private_key"] = ""

        # Save the new data
        db = get_db()
        db.save_data(backup_data)

        # In a real app we might want to restart or re-init background tasks
        return {"status": "success"}
    except Exception as e:
        logger.exception("Error during restore")
        return JSONResponse({"error": _sanitize_error(str(e))}, status_code=500)
