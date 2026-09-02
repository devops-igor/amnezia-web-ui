#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
GO_DIR="$REPO_ROOT/amnezia-web-ui-go"

# Setup hermetic temporary directory
TMP_DIR=$(mktemp -d /tmp/amnezia-e2e-XXXXXX)
SERVER_PID=""

cleanup() {
    local exit_code=$?
    if [ -n "$SERVER_PID" ]; then
        kill "$SERVER_PID" 2>/dev/null || true
        wait "$SERVER_PID" 2>/dev/null || true
    fi
    rm -rf "$TMP_DIR"
    exit "$exit_code"
}
trap cleanup EXIT INT TERM

export DATA_DIR="$TMP_DIR"
export DB_PATH="$TMP_DIR/panel.db"
export SECRET_KEY="e2e-secret-key-32-bytes-minimum-length-string-12345"
export PORT="${E2E_PORT:-8000}"
export HOST="127.0.0.1"
export LOG_LEVEL="WARN"
export E2E_TESTING="true"
export E2E_BASE_URL="http://${HOST}:${PORT}"
export E2E_ADMIN_USER="admin"
export E2E_ADMIN_PASS="AdminPass123!"

wait_for_server() {
    local ready=0
    for i in $(seq 1 30); do
        if ! kill -0 "$SERVER_PID" 2>/dev/null; then
            echo "ERROR: Server process $SERVER_PID died unexpectedly"
            return 1
        fi
        if curl -s -f "http://${HOST}:${PORT}/api/health" >/dev/null 2>&1; then
            ready=1
            break
        fi
        sleep 0.5
    done

    if [ "$ready" -ne 1 ]; then
        echo "ERROR: Server failed to start on http://${HOST}:${PORT} within 15 seconds"
        return 1
    fi

    if ! kill -0 "$SERVER_PID" 2>/dev/null; then
        echo "ERROR: Server process $SERVER_PID died immediately after readiness probe"
        return 1
    fi
    return 0
}

# 1. Build binary
echo "==> Building Go panel binary..."
(cd "$GO_DIR" && go build -trimpath -ldflags="-s -w" -o bin/panel ./cmd/panel)

# 2. Seed database
echo "==> Seeding E2E test database..."
python3 -c "
import sqlite3, bcrypt, os, time, uuid

conn = sqlite3.connect('$DB_PATH')
with open('$GO_DIR/internal/database/schema.sql') as f:
    conn.executescript(f.read())

now = time.strftime('%Y-%m-%dT%H:%M:%SZ', time.gmtime())
pw_hash = bcrypt.hashpw(b'AdminPass123!', bcrypt.gensalt(12)).decode('utf-8')
admin_id = str(uuid.uuid4())

conn.execute('''
INSERT INTO users (id, username, password_hash, role, enabled, created_at, password_change_required)
VALUES (?, 'admin', ?, 'admin', 1, ?, 0)
''', (admin_id, pw_hash, now))

conn.execute('''
INSERT INTO servers (id, name, host, ssh_user, ssh_port, ssh_pass, protocols, created_at)
VALUES (1, 'E2E Test Server', '127.0.0.1', 'root', 22, '', '{\"awg\":{\"installed\":true,\"port\":51820},\"telemt\":{\"installed\":true,\"port\":443},\"dns\":{\"installed\":true}}', ?)
''', (now,))

conn.commit()
conn.close()
"

# 3. Start server
echo "==> Starting Amnezia Go panel server on port $PORT..."
"$GO_DIR/bin/panel" &
SERVER_PID=$!

# 4. Wait for server readiness
echo "==> Waiting for server to become healthy..."
if ! wait_for_server; then
    exit 1
fi
echo "==> Server is healthy!"

# 5. Run Playwright E2E suite
echo "==> Running Playwright E2E tests..."
E2E_LOG="$TMP_DIR/e2e_pytest.log"
pytest "$REPO_ROOT/tests/e2e/" -m e2e -v -rs "$@" 2>&1 | tee "$E2E_LOG"

# Verify skip count does not exceed expected threshold (baseline: 5 skips)
MAX_EXPECTED_SKIPS="${MAX_EXPECTED_SKIPS:-5}"
python3 -c "
import re, sys

with open('$E2E_LOG', 'r') as f:
    text = f.read()

match = re.search(r'(\d+)\s+skipped', text)
skips = int(match.group(1)) if match else 0
max_skips = int('$MAX_EXPECTED_SKIPS')

print(f'==> E2E Skip Count Check: {skips} skipped tests detected (maximum allowed: {max_skips})')
if skips > max_skips:
    print(f'ERROR: Number of skipped tests ({skips}) exceeds allowed baseline ({max_skips})', file=sys.stderr)
    sys.exit(1)
"

# 6. If running default suite, also verify rate limiting on dedicated server instance
if [ "$#" -eq 0 ]; then
    echo "==> Verifying login rate-limiting test against rate-limited server..."
    kill "$SERVER_PID" 2>/dev/null || true
    wait "$SERVER_PID" 2>/dev/null || true

    unset E2E_TESTING
    "$GO_DIR/bin/panel" &
    SERVER_PID=$!
    if ! wait_for_server; then
        echo "ERROR: Restarted rate-limited server failed readiness check"
        exit 1
    fi

    pytest "$REPO_ROOT/tests/e2e/test_auth.py::test_login_rate_limiting" -m e2e -v -rs
fi

echo "==> All E2E verification successfully completed!"

