import os
import logging
import uvicorn
from app.main import app
from app.core.config import get_db

logging.basicConfig(level=logging.INFO)
logger = logging.getLogger(__name__)

if __name__ == "__main__":
    db = get_db()
    settings = db.get_all_settings()
    ssl_conf = settings.get("ssl", {})

    cert_file = ssl_conf.get("cert_path")
    key_file = ssl_conf.get("key_path")

    # If text is provided, create temporary files
    temp_dir = os.path.join(os.getcwd(), "ssl_temp")
    if ssl_conf.get("enabled"):
        if ssl_conf.get("cert_text") or ssl_conf.get("key_text"):
            if not os.path.exists(temp_dir):
                os.makedirs(temp_dir)

            if ssl_conf.get("cert_text"):
                cert_file = os.path.join(temp_dir, "cert.pem")
                with open(cert_file, "w") as f:
                    f.write(ssl_conf["cert_text"].strip() + "\n")

            if ssl_conf.get("key_text"):
                key_file = os.path.join(temp_dir, "key.pem")
                with open(key_file, "w") as f:
                    f.write(ssl_conf["key_text"].strip() + "\n")

    uvicorn_kwargs = {"app": app, "host": "0.0.0.0", "port": ssl_conf.get("panel_port", 5000)}

    if ssl_conf.get("enabled") and cert_file and key_file:
        if os.path.exists(cert_file) and os.path.exists(key_file):
            logger.info(
                f"Starting panel with HTTPS enabled on domain: {ssl_conf.get('domain')} at port {uvicorn_kwargs['port']}"
            )
            uvicorn_kwargs["ssl_certfile"] = cert_file
            uvicorn_kwargs["ssl_keyfile"] = key_file
        else:
            logger.error("SSL certificates not found at specified paths. Starting with HTTP.")

    uvicorn.run(**uvicorn_kwargs)
