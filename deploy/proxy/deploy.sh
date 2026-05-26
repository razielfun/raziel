#!/usr/bin/env bash
# Deploy the raziel-proxy binary to a Hetzner VPS.
#
# Usage:
#   PROXY_HOST=1.2.3.4 \
#   APP_URL=https://raziel-web.vercel.app \
#   PROXY_SECRET=<secret> \
#   ./deploy/proxy/deploy.sh
set -euo pipefail

: "${PROXY_HOST:?Set PROXY_HOST to the proxy VPS IP}"
: "${APP_URL:?Set APP_URL to the web app base URL}"
: "${PROXY_SECRET:?Set PROXY_SECRET to the shared routing secret}"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_DIR="$(dirname "$(dirname "$SCRIPT_DIR")")"
BINARY="/tmp/raziel-proxy-linux-amd64"

echo "==> Building proxy binary..."
cd "$REPO_DIR"
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o "$BINARY" ./cmd/proxy

echo "==> Copying binary to $PROXY_HOST..."
scp "$BINARY" "root@${PROXY_HOST}:/usr/local/bin/raziel-proxy"
ssh "root@${PROXY_HOST}" "chmod +x /usr/local/bin/raziel-proxy"

echo "==> Writing env file..."
ssh "root@${PROXY_HOST}" bash <<ENVSSH
mkdir -p /etc/raziel-proxy
cat > /etc/raziel-proxy/env <<EOF
APP_URL=${APP_URL}
PROXY_SECRET=${PROXY_SECRET}
BASE_DOMAIN=razi.lol
LISTEN_ADDR=:80
AGENT_PORT=8000
EOF
ENVSSH

echo "==> Installing systemd service..."
scp "$SCRIPT_DIR/raziel-proxy.service" "root@${PROXY_HOST}:/etc/systemd/system/raziel-proxy.service"
ssh "root@${PROXY_HOST}" "systemctl daemon-reload && systemctl enable raziel-proxy && systemctl restart raziel-proxy"

echo "==> Status:"
ssh "root@${PROXY_HOST}" "systemctl status raziel-proxy --no-pager"

echo ""
echo "Done. Proxy running on ${PROXY_HOST}:80"
echo "Ensure Cloudflare *.razi.lol → ${PROXY_HOST}"
