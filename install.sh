#!/usr/bin/env bash
set -Eeuo pipefail

REPO=""
DOMAIN=""
INSTALL_DIR="/opt/vpsopr"
CONFIG_DIR="/etc/vpsopr"
DATA_DIR="/var/lib/vpsopr"
PANEL_PORT="7777"
AGENT_PORT="8443"
ARCH=""

usage() {
  cat <<'EOF'
VPNSOPR installer
Usage:
  install.sh --repo OWNER/REPO [--domain panel.example.com]

Options:
  --repo       GitHub repository containing VPNSOPR release assets (required)
  --domain     Optional DNS name for HTTPS panel access via Caddy
  --help       Show this help

The panel is kept on localhost. Caddy exposes it on the VPS public IP over
HTTP and, when --domain is supplied, on that domain over automatic HTTPS.
EOF
}

while (($#)); do
  case "$1" in
    --repo) REPO="${2:?missing repository}"; shift 2 ;;
    --domain) DOMAIN="${2:?missing domain}"; shift 2 ;;
    --help|-h) usage; exit 0 ;;
    *) echo "Unknown option: $1" >&2; usage; exit 2 ;;
  esac
done

if [[ ! "$REPO" =~ ^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$ ]]; then
  echo "ERROR: --repo must be OWNER/REPO (example: myname/VPNSOPR)" >&2
  exit 2
fi
if [[ -n "$DOMAIN" && ! "$DOMAIN" =~ ^[A-Za-z0-9.-]+$ ]]; then
  echo "ERROR: invalid domain name" >&2
  exit 2
fi
if [[ $EUID -ne 0 ]]; then
  echo "Run with sudo/root." >&2
  exit 1
fi
if ! command -v curl >/dev/null || ! command -v tar >/dev/null || ! command -v openssl >/dev/null; then
  echo "curl and tar are required." >&2
  exit 1
fi

case "$(uname -m)" in
  x86_64|amd64) ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
  *) echo "Unsupported CPU architecture: $(uname -m)" >&2; exit 1 ;;
esac

command -v systemctl >/dev/null || { echo "systemd is required by this installer." >&2; exit 1; }

ASSET="vpsopr-linux-${ARCH}.tar.gz"
URL="https://github.com/${REPO}/releases/latest/download/${ASSET}"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

echo "[1/6] Downloading VPNSOPR release: ${REPO}/${ASSET}"
curl --fail --location --silent --show-error "$URL" -o "$TMP/$ASSET"
curl --fail --location --silent --show-error "${URL}.sha256" -o "$TMP/$ASSET.sha256"
(cd "$TMP" && sha256sum -c "$ASSET.sha256")
tar -xzf "$TMP/$ASSET" -C "$TMP"

echo "[2/6] Installing binaries and directories"
install -d -m 0755 "$INSTALL_DIR" "$CONFIG_DIR" "$DATA_DIR"
install -m 0755 "$TMP/vpsopr-server" "$INSTALL_DIR/vpsopr-server"
install -m 0755 "$TMP/vpsopr-client" "$INSTALL_DIR/vpsopr-client"

if [[ ! -f "$CONFIG_DIR/server.env" ]]; then
  PANEL_TOKEN="$(openssl rand -hex 32)"
  install -m 0600 /dev/null "$CONFIG_DIR/server.env"
  cat > "$CONFIG_DIR/server.env" <<EOF
VPNSOPR_PANEL_TOKEN=${PANEL_TOKEN}
EOF
  chmod 0600 "$CONFIG_DIR/server.env"
else
  # shellcheck disable=SC1090
  source "$CONFIG_DIR/server.env"
  PANEL_TOKEN="${VPNSOPR_PANEL_TOKEN:?Missing VPNSOPR_PANEL_TOKEN in server.env}"
fi

echo "[3/6] Creating a self-signed TLS certificate for VPS agent links"
if [[ ! -f "$CONFIG_DIR/server.crt" || ! -f "$CONFIG_DIR/server.key" ]]; then
  VPS_IP="$(curl -4fsS https://api.ipify.org || true)"
  SAN="IP:127.0.0.1"
  [[ -n "$VPS_IP" ]] && SAN="${SAN},IP:${VPS_IP}"
  [[ -n "$DOMAIN" ]] && SAN="${SAN},DNS:${DOMAIN}"
  openssl req -x509 -newkey rsa:3072 -sha256 -nodes -days 825 \
    -keyout "$CONFIG_DIR/server.key" -out "$CONFIG_DIR/server.crt" \
    -subj "/CN=VPNSOPR Agent" -addext "subjectAltName=${SAN}" >/dev/null 2>&1
  chmod 0600 "$CONFIG_DIR/server.key"
  chmod 0644 "$CONFIG_DIR/server.crt"
fi

echo "[4/6] Installing systemd service"
cat > /etc/systemd/system/vpsopr.service <<EOF
[Unit]
Description=VPNSOPR VPS Tunnel Manager
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=root
EnvironmentFile=${CONFIG_DIR}/server.env
WorkingDirectory=${DATA_DIR}
ExecStart=${INSTALL_DIR}/vpsopr-server -listen 0.0.0.0:${AGENT_PORT} -cert ${CONFIG_DIR}/server.crt -key ${CONFIG_DIR}/server.key -panel 127.0.0.1:${PANEL_PORT} -panel-token \${VPNSOPR_PANEL_TOKEN} -state ${DATA_DIR}/forwards.json -nodes ${DATA_DIR}/nodes.json
Restart=always
RestartSec=3
NoNewPrivileges=true
PrivateTmp=true
ProtectHome=true
ProtectSystem=full
ReadWritePaths=${DATA_DIR} ${CONFIG_DIR}

[Install]
WantedBy=multi-user.target
EOF
systemctl daemon-reload
systemctl enable --now vpsopr.service

echo "[5/6] Configuring public panel access"
if ! command -v caddy >/dev/null && command -v apt-get >/dev/null; then
  echo "Caddy is not installed. Trying the distribution package..."
  apt-get update -qq || true
  apt-get install -y caddy || true
fi
if command -v caddy >/dev/null; then
  CADDY_FILE="/etc/caddy/Caddyfile"
  install -d -m 0755 /etc/caddy
  if [[ -n "$DOMAIN" ]]; then
    cat > "$CADDY_FILE" <<EOF
${DOMAIN} {
    encode zstd gzip
    reverse_proxy 127.0.0.1:${PANEL_PORT}
    header {
        X-Content-Type-Options nosniff
        Referrer-Policy no-referrer
        X-Frame-Options DENY
    }
}

:80 {
    encode zstd gzip
    reverse_proxy 127.0.0.1:${PANEL_PORT}
}
EOF
  else
    cat > "$CADDY_FILE" <<EOF
:80 {
    encode zstd gzip
    reverse_proxy 127.0.0.1:${PANEL_PORT}
}
EOF
  fi
  systemctl enable --now caddy || true
  systemctl reload caddy || systemctl restart caddy || true
else
  echo "Caddy not installed. Panel remains private on 127.0.0.1:${PANEL_PORT}."
  echo "Install Caddy and configure it to reverse_proxy 127.0.0.1:${PANEL_PORT}."
fi

echo "[6/6] Installation complete"
VPS_IP="$(curl -4fsS https://api.ipify.org || true)"
echo
echo "VPNSOPR service: systemctl status vpsopr"
echo "Logs: journalctl -u vpsopr -f"
echo "Agent TLS certificate: ${CONFIG_DIR}/server.crt (copy securely to agents)"
echo "Panel API token: ${CONFIG_DIR}/server.env"
if [[ -n "$DOMAIN" ]]; then
  echo "Panel domain: https://${DOMAIN} (ensure DNS A record points to this VPS)"
fi
if [[ -n "$VPS_IP" ]]; then
  echo "Panel by IP: http://${VPS_IP} (HTTP only; use the domain for HTTPS)"
fi
echo "Panel listens internally at 127.0.0.1:${PANEL_PORT}"
echo "Open firewall ports ${AGENT_PORT}/tcp, 80/tcp and 443/tcp as needed."
echo "For production, restrict the panel by IP/VPN or add an authentication gateway."
