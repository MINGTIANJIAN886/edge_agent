#!/usr/bin/env bash
set -euo pipefail

# ============================================================
#  Edge Agent 一键安装脚本
#  用法:
#    curl -fsSL https://raw.githubusercontent.com/MINGTIANJIAN886/edge_agent/main/agent.sh | sudo bash
#    curl -fsSL https://raw.githubusercontent.com/MINGTIANJIAN886/edge_agent/main/agent.sh | sudo bash -s -- --bridge
# ============================================================

REPO="MINGTIANJIAN886/edge_agent"
INSTALL_DIR="${INSTALL_DIR:-/usr/local/bin}"
CONFIG_DIR="${CONFIG_DIR:-/etc/edge-agent}"
DATA_DIR="${DATA_DIR:-/var/lib/edge-agent}"
MODEL_DIR="${MODEL_DIR:-${DATA_DIR}/models}"
LOG_DIR="${LOG_DIR:-/var/log/edge-agent}"
DOWNLOAD_DIR="${DOWNLOAD_DIR:-/var/cache/edge-agent/downloads}"
SCRIPT_DIR="${SCRIPT_DIR:-/opt/edge-agent}"
SERVICE_DIR="/etc/systemd/system"

AGENT_BIN="${INSTALL_DIR}/edge-agent"
CONFIG_FILE="${CONFIG_DIR}/config.yaml"
SERVICE_FILE="${SERVICE_DIR}/edge-agent.service"

BRIDGE_SCRIPT1="${SCRIPT_DIR}/bridge_ros1.py"
BRIDGE_SCRIPT2="${SCRIPT_DIR}/bridge_ros2.py"
OCR_SCRIPT="${SCRIPT_DIR}/edge_ocr.py"

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
OCR_ENABLED="${OCR_ENABLED:-false}"
OCR_INTERVAL="${OCR_INTERVAL:-30}"
OCR_CONF_THRESHOLD="${OCR_CONF_THRESHOLD:-0.5}"
INFERENCE_URL="${INFERENCE_URL:-http://127.0.0.1:8080}"
ROS_PYTHON="${ROS_PYTHON:-python3}"
ROS_BRIDGE_SCRIPT1="${ROS_BRIDGE1:-${SCRIPT_DIR}/bridge_ros1.py}"
ROS_BRIDGE_SCRIPT2="${ROS_BRIDGE2:-${SCRIPT_DIR}/bridge_ros2.py}"
ROS_MAX_LINEAR="${ROS_MAX_LINEAR:-2.0}"
ROS_MAX_ANGULAR="${ROS_MAX_ANGULAR:-3.14}"
ROS_WATCHDOG="${ROS_WATCHDOG:-5}"
INSTALL_BRIDGE=false
FORCE_CONFIG=false

show_help() {
  echo "Usage: $0 [--bridge] [--force-config] [DEVICE_ID]"
  echo ""
  echo "Options:"
  echo "  --bridge         Install ROS bridge scripts and service"
  echo "  --force-config   Force regenerate config.yaml even if it exists"
  echo ""
  echo "Environment variables:"
  echo "  DEVICE_ID       Device identifier (default: pi-001)"
  echo "  MQTT_BROKER     MQTT broker host"
  echo "  MQTT_PORT       MQTT broker port (default: 8883)"
  echo "  MQTT_USER       MQTT username"
  echo "  MQTT_PASS       MQTT password"
  echo "  OTA_SERVER      OTA update server URL"
  echo "  MODEL_DIR       Model directory (default: /var/lib/edge-agent/models)"
  echo "  INSTALL_DIR     Binary install directory (default: /usr/local/bin)"
  echo "  CONFIG_DIR      Config directory (default: /etc/edge-agent)"
  echo "  SCRIPT_DIR      Scripts directory (default: /opt/edge-agent)"
  echo "  LOG_DIR         Log directory (default: /var/log/edge-agent)"
  echo "  DOWNLOAD_DIR    Download cache (default: /var/cache/edge-agent/downloads)"
  echo "  INFERENCE_URL   Inference service URL (default: http://127.0.0.1:8080)"
  echo "  OCR_ENABLED     Enable OCR (default: false)"
  echo "  ROS_MAX_LINEAR  Max linear speed (default: 2.0)"
  echo "  ROS_MAX_ANGULAR Max angular speed (default: 3.14)"
  echo "  ROS_WATCHDOG    Safety watchdog timeout (default: 5)"
}

for arg in "$@"; do
  case "$arg" in
    --bridge) INSTALL_BRIDGE=true ;;
    --force-config) FORCE_CONFIG=true ;;
    --help) show_help; exit 0 ;;
    --*) echo "Unknown option: $arg" >&2; exit 1 ;;
    *) DEVICE_ID="$arg" ;;
  esac
done

echo "=== Edge Agent Installer ==="
echo "Device: ${DEVICE_ID} | Arch: ${ARCH}"
echo "Broker: ${MQTT_BROKER}:${MQTT_PORT}"
echo "OTA:    ${OTA_SERVER}"
echo "Bridge: ${INSTALL_BRIDGE}"
echo ""

# [1/5] 创建目录
echo "[1/5] Creating directories..."
mkdir -p \
  "${INSTALL_DIR}" \
  "${CONFIG_DIR}" \
  "${DATA_DIR}" \
  "${MODEL_DIR}" \
  "${LOG_DIR}" \
  "${DOWNLOAD_DIR}" \
  "${SCRIPT_DIR}"

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
if [ -f "${CONFIG_FILE}" ] && [ "${FORCE_CONFIG:-false}" != "true" ]; then
  echo "[3/5] Keeping existing configuration: ${CONFIG_FILE}"
  echo "  -> Set FORCE_CONFIG=true or remove the file to regenerate"
else
  if [ "${FORCE_CONFIG:-false}" = "true" ]; then
    echo "[3/5] Force-regenerating configuration (--force-config)..."
  else
    echo "[3/5] Generating initial configuration..."
  fi
  cat > "${CONFIG_FILE}" << EOF
device_id: "${DEVICE_ID}"
download_dir: "${DOWNLOAD_DIR}"
heartbeat_interval: 30
log_dir: "${LOG_DIR}"

mqtt:
  broker: "${MQTT_BROKER}"
  port: ${MQTT_PORT}
  client_id: "edge-agent-${DEVICE_ID}"
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
  current_version: ""
  model_file: "${MODEL_DIR}/model.ncnn.bin"
  model_dir: "${MODEL_DIR}"
  current_symlink: "${MODEL_DIR}/current"
  backup_count: 3
  inference_restart_cmd: ""

inference:
  service_url: "${INFERENCE_URL}"
  timeout: 30

ocr:
  enabled: ${OCR_ENABLED}
  script_path: "${OCR_SCRIPT}"
  interval: ${OCR_INTERVAL}
  conf_threshold: ${OCR_CONF_THRESHOLD}
  command_topic: "edge/${DEVICE_ID}/ocr/command"
  result_topic: "edge/${DEVICE_ID}/ocr/result"

ros:
  enabled: ${INSTALL_BRIDGE}
  bridge_script_ros1: "${ROS_BRIDGE_SCRIPT1}"
  bridge_script_ros2: "${ROS_BRIDGE_SCRIPT2}"
  bridge_python: "${ROS_PYTHON}"
  car_max_linear_speed: ${ROS_MAX_LINEAR}
  car_max_angular_speed: ${ROS_MAX_ANGULAR}
  safety_watchdog_timeout: ${ROS_WATCHDOG}
  cmd_vel_topic: "edge/${DEVICE_ID}/car/cmd_vel"
  bridge_result_topic: "edge/${DEVICE_ID}/bridge/result"

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
EOF
  echo "  -> ${CONFIG_FILE}"
fi

# Secure configuration file
chown root:root "${CONFIG_FILE}" 2>/dev/null || true
chmod 600 "${CONFIG_FILE}"

# Migrate legacy agent.service → edge-agent.service
if command -v systemctl &>/dev/null; then
  if systemctl list-unit-files agent.service &>/dev/null 2>&1; then
    echo "  -> Detected legacy agent.service, migrating..."
    systemctl disable --now agent.service 2>/dev/null || true
    rm -f "/etc/systemd/system/agent.service"
    systemctl daemon-reload 2>/dev/null || true
  fi
  # Backup old config if new path doesn't exist yet
  if [ -f /etc/agent/config.yaml ] && [ ! -f "${CONFIG_FILE}" ]; then
    cp -a /etc/agent/config.yaml "${CONFIG_DIR}/config.yaml.legacy"
    echo "  -> Backed up old config to ${CONFIG_DIR}/config.yaml.legacy"
  fi
fi

# [4/5] 安装 systemd 服务
echo "[4/5] Installing systemd services..."

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

if command -v systemctl &>/dev/null; then
    systemctl daemon-reload
    systemctl enable edge-agent
    systemctl restart edge-agent
    echo "  -> edge-agent.service installed and started"
else
    nohup "${AGENT_BIN}" -config "${CONFIG_FILE}" > "${LOG_DIR}/edge-agent.log" 2>&1 &
    echo "  -> PID: $!"
fi

# [5/5] 部署脚本 (OCR + ROS 桥接)
echo "[5/5] Deploying scripts..."
mkdir -p "${SCRIPT_DIR}"

# OCR script (always download if OCR is enabled)
if [ "${OCR_ENABLED}" = true ]; then
  echo "  -> downloading edge_ocr.py..."
  if curl -fsSL -o "${SCRIPT_DIR}/edge_ocr.py" \
    "https://raw.githubusercontent.com/${REPO}/main/edge_ocr.py"; then
    chmod +x "${SCRIPT_DIR}/edge_ocr.py"
    echo "       ${SCRIPT_DIR}/edge_ocr.py"
  else
    echo "       WARNING: OCR script download failed"
  fi
fi

# ROS bridge scripts and service
if [ "${INSTALL_BRIDGE}" = true ]; then
  echo "  -> downloading bridge_ros2.py..."
  if curl -fsSL -o "${SCRIPT_DIR}/bridge_ros2.py" \
    "https://raw.githubusercontent.com/${REPO}/main/scripts/bridge_ros2.py"; then
    chmod +x "${SCRIPT_DIR}/bridge_ros2.py"
    echo "       ${SCRIPT_DIR}/bridge_ros2.py"
  else
    echo "       WARNING: download failed, bridge will not work"
  fi

  curl -fsSL -o "${SCRIPT_DIR}/bridge_ros1.py" \
    "https://raw.githubusercontent.com/${REPO}/main/scripts/bridge_ros1.py" 2>/dev/null && \
    chmod +x "${SCRIPT_DIR}/bridge_ros1.py" || true

  cat > "${SERVICE_DIR}/car_bridge.service" << EOF
[Unit]
Description=ROS Car Bridge (reads /tmp/edge_bridge_cmd)
After=network.target

[Service]
Type=simple
ExecStart=/bin/bash -c "VER=\$(ls /opt/ros/ 2>/dev/null | head -1); source /opt/ros/\$VER/setup.bash 2>/dev/null; exec ${ROS_PYTHON} ${SCRIPT_DIR}/bridge_ros2.py"
Restart=always
RestartSec=3

[Install]
WantedBy=multi-user.target
EOF

  if command -v systemctl &>/dev/null; then
    systemctl daemon-reload
    systemctl enable car_bridge
    systemctl restart car_bridge 2>/dev/null || true
    echo "  -> car_bridge.service installed and started"
  fi

  echo "  -> ROS bridge enabled in config (ros.enabled=true)"
  echo "  -> car_bridge.service manages bridge lifecycle (separate from edge-agent)"
fi

echo ""
echo "=== Install Complete ==="
echo "Binary: ${AGENT_BIN}"
echo "Config: ${CONFIG_FILE}"
echo ""
echo "Commands:"
echo "  sudo systemctl status edge-agent"
echo "  journalctl -u edge-agent -f"
echo ""
echo "Paths (overridable via env vars):"
echo "  Binary:  ${AGENT_BIN}"
echo "  Config:  ${CONFIG_FILE}"
echo "  Data:    ${MODEL_DIR}"
echo "  Scripts: ${SCRIPT_DIR}"
echo "  Logs:    ${LOG_DIR}"
echo "  Cache:   ${DOWNLOAD_DIR}"
echo ""
echo "To trigger OTA update:"
echo "  mosquitto_pub ... -t edge/${DEVICE_ID}/mcp/call -m '{\"id\":\"o\",\"method\":\"check_update\",\"params\":{}}'"
