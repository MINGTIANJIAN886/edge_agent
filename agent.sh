#!/usr/bin/env bash
set -euo pipefail

# ============================================================
#  Edge Agent 一键安装脚本
#  用法:
#    curl -fsSL https://raw.githubusercontent.com/MINGTIANJIAN886/edge_agent/main/agent.sh | sudo bash
#    curl -fsSL https://raw.githubusercontent.com/MINGTIANJIAN886/edge_agent/main/agent.sh | sudo bash -s -- --bridge
# ============================================================

REPO="MINGTIANJIAN886/edge_agent"
INSTALL_DIR="/usr/local/bin"
CONFIG_DIR="/etc/agent"
LOG_DIR="/var/log/agent"
DOWNLOAD_DIR="/tmp/agent/downloads"
SERVICE_DIR="/etc/systemd/system"

AGENT_BIN="${INSTALL_DIR}/agent"
CONFIG_FILE="${CONFIG_DIR}/config.yaml"
SERVICE_FILE="${SERVICE_DIR}/agent.service"
BRIDGE_SCRIPT1="/opt/agent/bridge_ros1.py"
BRIDGE_SCRIPT2="/opt/agent/bridge_ros2.py"

ARCH=$(uname -m)
case "$ARCH" in
    x86_64|amd64)  BINARY="agent-amd64" ;;
    aarch64|arm64) BINARY="agent-aarch64" ;;
    armv7l|armhf)  BINARY="agent-armv7l" ;;
    *) echo "Unsupported architecture: $ARCH"; exit 1 ;;
esac

# 默认参数（可通过环境变量覆盖）
DEVICE_ID="${DEVICE_ID:-pi-001}"
MQTT_BROKER="${MQTT_BROKER:-ca15b49bc8b442638f0cade1e45585ce.s1.eu.hivemq.cloud}"
MQTT_PORT="${MQTT_PORT:-8883}"
MQTT_USER="${MQTT_USER:-liyankun}"
MQTT_PASS="${MQTT_PASS:-liyankun152455A}"
OTA_SERVER="${OTA_SERVER:-https://amplifier-badge-awoke.ngrok-free.dev}"
ROS_BRIDGE_SCRIPT1="${ROS_BRIDGE1:-/opt/agent/bridge_ros1.py}"
ROS_BRIDGE_SCRIPT2="${ROS_BRIDGE2:-/opt/agent/bridge_ros2.py}"
ROS_MAX_LINEAR="${ROS_MAX_LINEAR:-2.0}"
ROS_MAX_ANGULAR="${ROS_MAX_ANGULAR:-3.14}"
ROS_WATCHDOG="${ROS_WATCHDOG:-5}"
INSTALL_BRIDGE=false

for arg in "$@"; do
  case "$arg" in
    --bridge) INSTALL_BRIDGE=true ;;
    --help) echo "Usage: $0 [--bridge] [DEVICE_ID]"; exit 0 ;;
  esac
done

if [ $# -gt 0 ] && [[ "$1" != --* ]]; then
  DEVICE_ID="$1"
fi

echo "=== Edge Agent Installer ==="
echo "Device: ${DEVICE_ID} | Arch: ${ARCH}"
echo "Broker: ${MQTT_BROKER}:${MQTT_PORT}"
echo "OTA:    ${OTA_SERVER}"
echo "Bridge: ${INSTALL_BRIDGE}"
echo ""

# [1/5] 创建目录
echo "[1/5] Creating directories..."
mkdir -p "${INSTALL_DIR}" "${CONFIG_DIR}" "${LOG_DIR}" "${DOWNLOAD_DIR}"

# [2/5] 下载 agent 二进制
if [ ! -f "${AGENT_BIN}" ]; then
  echo "[2/5] Downloading agent (${BINARY}) from GitHub Release..."
  DOWNLOAD_URL="https://github.com/${REPO}/releases/latest/download/${BINARY}"
  MIRROR_URL="https://ghproxy.com/${DOWNLOAD_URL}"

  if curl -fsSL --connect-timeout 10 --max-time 120 -o "${AGENT_BIN}" "${DOWNLOAD_URL}"; then
    echo "  -> downloaded from GitHub Releases"
  elif curl -fsSL --connect-timeout 10 --max-time 120 -o "${AGENT_BIN}" "${MIRROR_URL}"; then
    echo "  -> downloaded from mirror (ghproxy.com)"
  else
    echo "WARNING: Cannot download binary from GitHub Releases."
    echo "  Try: make build && scp build/${BINARY} ${DEVICE_ID}:${AGENT_BIN}"
    echo "  Or set up GitHub Actions Release (push to main to trigger build)"
    touch "${AGENT_BIN}"
  fi
  chmod +x "${AGENT_BIN}" 2>/dev/null || true
else
  echo "[2/5] Agent already installed at ${AGENT_BIN}"
fi

# [3/5] 生成配置
echo "[3/5] Generating configuration..."
cat > "${CONFIG_FILE}" << EOF
device_id: "${DEVICE_ID}"
download_dir: "${DOWNLOAD_DIR}"
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

ota:
  server_url: "${OTA_SERVER}"
  version_path: "version.json"
  check_interval: 300
  current_version: "5.0"
  model_file: "/home/liyankun/models/model.ncnn.bin"
  model_dir: "/home/liyankun/models"
  current_symlink: "/home/liyankun/models/current"
  backup_count: 3
  inference_restart_cmd: ""

cert_api: ""
cert:
  cert_file: ""
  key_file: ""
  ca_file: "/etc/ssl/certs/ca-certificates.crt"
  auto_enroll: false
  token: ""

auth:
  method: "password"
  token: ""
  token_exchange: false

ros:
  enabled: ${INSTALL_BRIDGE}
  bridge_script_ros1: "${ROS_BRIDGE_SCRIPT1}"
  bridge_script_ros2: "${ROS_BRIDGE_SCRIPT2}"
  bridge_python: "python3"
  car_max_linear_speed: ${ROS_MAX_LINEAR}
  car_max_angular_speed: ${ROS_MAX_ANGULAR}
  safety_watchdog_timeout: ${ROS_WATCHDOG}
EOF
echo "  -> ${CONFIG_FILE}"

# [4/5] 安装 systemd 服务
echo "[4/5] Installing systemd service..."
if [ "${INSTALL_BRIDGE}" = true ]; then
  cat > "${SERVICE_FILE}" << EOF
[Unit]
Description=Edge Agent - ${DEVICE_ID}
After=network.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=/bin/bash -c "VER=\$(ls /opt/ros/ 2>/dev/null | head -1); source /opt/ros/\$VER/setup.bash 2>/dev/null; exec ${AGENT_BIN} -config ${CONFIG_FILE}"
Restart=always
RestartSec=3
RestartMaxDelaySec=15
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
EOF
else
  cat > "${SERVICE_FILE}" << EOF
[Unit]
Description=Edge Agent - ${DEVICE_ID}
After=network.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=${AGENT_BIN} -config ${CONFIG_FILE}
Restart=always
RestartSec=3
RestartMaxDelaySec=15
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
EOF
fi

if command -v systemctl &>/dev/null; then
    systemctl daemon-reload
    systemctl enable agent
    systemctl restart agent
    echo "  -> systemd service installed and started"
else
    nohup "${AGENT_BIN}" -config "${CONFIG_FILE}" > "${LOG_DIR}/agent.log" 2>&1 &
    echo "  -> PID: $!"
fi

# [5/5] 可选：部署 ROS 桥接脚本
if [ "${INSTALL_BRIDGE}" = true ]; then
  echo "[5/5] Deploying ROS bridge scripts..."
  mkdir -p /opt/agent

  echo "  -> downloading bridge_ros2.py..."
  if curl -fsSL -o /opt/agent/bridge_ros2.py \
    "https://raw.githubusercontent.com/${REPO}/main/scripts/bridge_ros2.py"; then
    chmod +x /opt/agent/bridge_ros2.py
    echo "       /opt/agent/bridge_ros2.py"
  else
    echo "       WARNING: download failed (agent will use CLI discovery fallback)"
  fi

  curl -fsSL -o /opt/agent/bridge_ros1.py \
    "https://raw.githubusercontent.com/${REPO}/main/scripts/bridge_ros1.py" 2>/dev/null && \
    chmod +x /opt/agent/bridge_ros1.py || true

  echo "  -> ROS bridge enabled in config (ros.enabled=true)"
  echo "  -> Agent auto-launches bridge on startup (no separate service)"
fi

echo ""
echo "=== Install Complete ==="
echo "Binary: ${AGENT_BIN}"
echo "Config: ${CONFIG_FILE}"
echo "Logs:   journalctl -u agent -f"
echo ""
echo "Commands:"
echo "  sudo systemctl status agent"
echo "  journalctl -u agent -f"
echo ""
echo "To trigger OTA update:"
echo "  mosquitto_pub ... -t edge/${DEVICE_ID}/mcp/call -m '{\"id\":\"o\",\"method\":\"check_update\",\"params\":{}}'"
