#!/bin/bash
set -euo pipefail

# ============================================================
#  Edge Agent Installer
#  Usage:
#    curl -fsSL https://github.com/<USER>/<REPO>/releases/latest/download/install.sh | sudo bash -s -- \
#      --device-id pi-001 \
#      --mqtt-broker ca15b49bc8b442638f0cade1e45585ce.s1.eu.hivemq.cloud \
#      --mqtt-user liyankun \
#      --mqtt-pass liyankun152455A
# ============================================================

REPO="${REPO:-your-username/edge-agent}"
INSTALL_DIR="/usr/local/bin"
CONFIG_DIR="/etc/agent"
SYSTEMD_DIR="/etc/systemd/system"
AGENT_BIN="${INSTALL_DIR}/agent"
CONFIG_FILE="${CONFIG_DIR}/config.yaml"
SERVICE_FILE="${SYSTEMD_DIR}/agent.service"
LOG_DIR="/var/log/agent"

DOWNLOAD_BASE="https://github.com/${REPO}/releases/latest/download"
DEVICE_ID=""
MQTT_BROKER=""
MQTT_PORT="8883"
MQTT_USER=""
MQTT_PASS=""

usage() {
    cat << EOF
Usage: $0 [options]
  --device-id <ID>       Device ID (required)
  --mqtt-broker <HOST>   MQTT broker host (required)
  --mqtt-port <PORT>     MQTT TLS port (default: 8883)
  --mqtt-user <USER>     MQTT username
  --mqtt-pass <PASS>     MQTT password
  --repo <USER/REPO>     GitHub repo (default: $REPO)
  --help                 Show this help
EOF
    exit 1
}

while [[ $# -gt 0 ]]; do
    case "$1" in
        --device-id)    DEVICE_ID="$2"; shift 2 ;;
        --mqtt-broker)  MQTT_BROKER="$2"; shift 2 ;;
        --mqtt-port)    MQTT_PORT="$2"; shift 2 ;;
        --mqtt-user)    MQTT_USER="$2"; shift 2 ;;
        --mqtt-pass)    MQTT_PASS="$2"; shift 2 ;;
        --repo)         REPO="$2"; DOWNLOAD_BASE="https://github.com/${REPO}/releases/latest/download"; shift 2 ;;
        --help)         usage ;;
        *) echo "Unknown: $1"; usage ;;
    esac
done

if [[ -z "${DEVICE_ID}" || -z "${MQTT_BROKER}" ]]; then
    echo "ERROR: --device-id and --mqtt-broker required"; usage
fi

ARCH=$(uname -m)
case "$ARCH" in
    x86_64|amd64)  BINARY="agent-amd64" ;;
    aarch64|arm64) BINARY="agent-aarch64" ;;
    armv7l|armhf)  BINARY="agent-armv7l" ;;
    *) echo "Unsupported: $ARCH"; exit 1 ;;
esac

echo "=== Edge Agent Installer ==="
echo "Device: ${DEVICE_ID} | Arch: ${ARCH} | Broker: ${MQTT_BROKER}:${MQTT_PORT}"
echo ""

# [1/4] Directories
echo "[1/4] Creating directories..."
mkdir -p "${INSTALL_DIR}" "${CONFIG_DIR}" "${LOG_DIR}"

# [2/4] Download binary
echo "[2/4] Downloading agent binary..."
BINARY_URL="${DOWNLOAD_BASE}/${BINARY}"
curl -fsSL -o "${AGENT_BIN}" "${BINARY_URL}"
chmod +x "${AGENT_BIN}"
echo "  -> ${AGENT_BIN} ($(ls -lh "${AGENT_BIN}" | awk '{print $5}'))"

# [3/4] Generate config
echo "[3/4] Generating configuration..."
cat > "${CONFIG_FILE}" << EOF
device_id: "${DEVICE_ID}"
download_dir: "/tmp/agent/downloads"
heartbeat_interval: 30
log_dir: "${LOG_DIR}"

mqtt:
  broker: "${MQTT_BROKER}"
  port: ${MQTT_PORT}
  client_id: "agent-${DEVICE_ID}"
  username: "${MQTT_USER}"
  password: "${MQTT_PASS}"
  topic:
    command: "edge/${DEVICE_ID}/command"
    download: "edge/${DEVICE_ID}/download"
    heartbeat: "edge/${DEVICE_ID}/heartbeat"
    result: "edge/${DEVICE_ID}/result"
    register: "edge/${DEVICE_ID}/register"
    mcp_register: "edge/${DEVICE_ID}/mcp/register"
    mcp_call: "edge/${DEVICE_ID}/mcp/call"

cert_api: ""

cert:
  cert_file: ""
  key_file: ""
  ca_file: ""
  auto_enroll: false
  token: ""

auth:
  method: "password"
  token: ""
  token_exchange: false
EOF
echo "  -> ${CONFIG_FILE}"

# [4/4] Systemd service
echo "[4/4] Installing systemd service..."
cat > "${SERVICE_FILE}" << EOF
[Unit]
Description=Edge Agent - ${DEVICE_ID}
After=network.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=${AGENT_BIN} -config ${CONFIG_FILE}
Restart=always
RestartSec=10
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable agent
systemctl restart agent

echo ""
echo "=== Install Complete ==="
echo "Status: systemctl status agent"
echo "Logs:   journalctl -u agent -f"
