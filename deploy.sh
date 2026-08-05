#!/usr/bin/env bash
# ============================================================
#  deploy.sh — Edge Agent 多设备一键部署脚本(开发机 -> 目标设备)
#
#  支持 Jetson(aarch64)/树莓派 4/5(arm64)、3B+(armv7l)/x86 设备。
#  自动完成:架构检测 -> 构建 -> 上传二进制与探测脚本 -> 生成
#  config.yaml -> 安装 systemd -> 健康检查。
#
#  用法:
#    ./deploy.sh -h <ip> -u <user> -d <device_id> [选项]
#
#  示例:
#    ./deploy.sh -h 172.20.10.10 -u jetson -d jetson002
#    ./deploy.sh -h 192.168.1.50 -u pi -d pi-001 --ota-dir /home/pi/models
#    ./deploy.sh -h 192.168.1.60 -u pi -d pi-002 --with-ocr --web-port 9090
#
#  选项:
#    -h <ip>        目标设备 IP(必填)
#    -u <user>      目标设备 SSH 用户(必填)
#    -d <device_id> 设备 ID(必填,同时决定 MQTT 主题前缀 edge/<id>/...)
#    --arch <a>     架构: auto|arm64|armv7l|amd64(默认 auto,远程探测)
#    --camera <dev> 摄像头设备节点(默认 /dev/video0)
#    --ota-dir <p>  OTA 模型目录(默认 $HOME/models)
#    --web-port <p> 面板监听端口(默认 8080)
#    --with-ocr     同时部署 OCR(edge_ocr.py + PaddleOCR venv 安装)
#    --no-build     跳过本地构建(使用现有 build/<arch> 产物)
#    --pwd <pw>     SSH 密码(需本机安装 sshpass;未提供时用 ssh key)
#    --keep-config  不覆盖远端已有 config.yaml
#
#  环境变量(沿用 agent.sh 约定):
#    MQTT_BROKER MQTT_PORT MQTT_USER MQTT_PASS(默认同一 HiveMQ broker)
#    OTA_SERVER  OTA_VERSION_PATH(默认 Gitee edge-ota-1 candidates.json)
# ============================================================
set -euo pipefail

REPO_DIR="$(cd "$(dirname "$0")" && pwd)"
INSTALL_DIR="/usr/local/bin"
CONFIG_DIR="/etc/agent"
PROBE_DIR="/opt/edge-agent/probes"
SERVICE_FILE="/etc/systemd/system/agent.service"

HOST=""; SSH_USER=""; DEVICE_ID=""
ARCH="auto"; CAMERA="/dev/video0"; OTA_DIR=""; WEB_PORT=8080
WITH_OCR=false; DO_BUILD=true; PASSWORD=""; KEEP_CONFIG=false

MQTT_BROKER="${MQTT_BROKER:-ca15b49bc8b442638f0cade1e45585ce.s1.eu.hivemq.cloud}"
MQTT_PORT="${MQTT_PORT:-8883}"
MQTT_USER="${MQTT_USER:-liyankun}"
MQTT_PASS="${MQTT_PASS:-liyankun152455A}"
OTA_SERVER="${OTA_SERVER:-https://gitee.com/hopelucy/edge-ota-1/raw/master}"
OTA_VERSION_PATH="${OTA_VERSION_PATH:-candidates.json}"

usage() {
  sed -n '2,28p' "$0" | sed 's/^# *//'
  exit 1
}

while [ $# -gt 0 ]; do
  case "$1" in
    -h) HOST="$2"; shift 2 ;;
    -u) SSH_USER="$2"; shift 2 ;;
    -d) DEVICE_ID="$2"; shift 2 ;;
    --arch) ARCH="$2"; shift 2 ;;
    --camera) CAMERA="$2"; shift 2 ;;
    --ota-dir) OTA_DIR="$2"; shift 2 ;;
    --web-port) WEB_PORT="$2"; shift 2 ;;
    --with-ocr) WITH_OCR=true; shift ;;
    --no-build) DO_BUILD=false; shift ;;
    --pwd) PASSWORD="$2"; shift 2 ;;
    --keep-config) KEEP_CONFIG=true; shift ;;
    *) echo "未知参数: $1"; usage ;;
  esac
done

[ -z "$HOST" ] || [ -z "$SSH_USER" ] || [ -z "$DEVICE_ID" ] && { echo "缺少必填参数"; usage; }

# ---------- SSH 助手 ----------
if [ -n "$PASSWORD" ]; then
  if ! command -v sshpass &>/dev/null; then
    echo "ERROR: 使用 --pwd 需要本机安装 sshpass(sudo apt install sshpass)" >&2
    exit 1
  fi
  SSH="sshpass -p $PASSWORD ssh -o StrictHostKeyChecking=no -o ConnectTimeout=10"
  SCP="sshpass -p $PASSWORD scp -o StrictHostKeyChecking=no -o ConnectTimeout=10"
else
  echo "提示: 未提供 --pwd。远端 sudo 需要已配置 NOPASSWD sudoers,"
  echo "      否则请改用 --pwd <ssh密码>(需要本机 sshpass)。"
  SSH="ssh -o StrictHostKeyChecking=no -o ConnectTimeout=10"
  SCP="scp -o StrictHostKeyChecking=no -o ConnectTimeout=10"
fi
run_remote() { $SSH "$SSH_USER@$HOST" "$1"; }
run_sudo() {
  if [ -n "$PASSWORD" ]; then
    run_remote "echo '$PASSWORD' | sudo -S $1"
  else
    run_remote "sudo -n $1"
  fi
}

# ---------- [1/6] 架构 ----------
if [ "$ARCH" = "auto" ]; then
  MACH="$(run_remote "uname -m")"
  case "$MACH" in
    aarch64|arm64) ARCH=arm64 ;;
    armv7l|armhf)  ARCH=armv7l ;;
    x86_64|amd64)  ARCH=amd64 ;;
    *) echo "ERROR: 无法识别的架构: $MACH"; exit 1 ;;
  esac
fi
case "$ARCH" in
  arm64) BIN="agent-aarch64" ;; armv7l) BIN="agent-armv7l" ;; amd64) BIN="agent-amd64" ;;
  *) echo "ERROR: 非法 --arch: $ARCH"; exit 1 ;;
esac

echo "=== Edge Agent Deploy ==="
echo "Target:  ${SSH_USER}@${HOST} (${ARCH})"
echo "Device:  ${DEVICE_ID}  Camera: ${CAMERA}  Web: :${WEB_PORT}"
echo "Broker:  ${MQTT_BROKER}:${MQTT_PORT}  OTA: ${OTA_SERVER}"
echo ""

# ---------- [2/6] 构建 ----------
if [ "$DO_BUILD" = true ]; then
  echo "[2/6] Building ${BIN} ..."
  ( cd "$REPO_DIR" && make build-$ARCH )
else
  echo "[2/6] Skipping build (--no-build)"
fi
if [ ! -f "$REPO_DIR/build/$BIN" ]; then
  echo "ERROR: 找不到 $REPO_DIR/build/$BIN,请先构建或去掉 --no-build" >&2
  exit 1
fi

# ---------- [3/6] 上传 ----------
echo "[3/6] Uploading binary and probe scripts ..."
$SCP "$REPO_DIR/build/$BIN" "$SSH_USER@$HOST:/tmp/$BIN"
$SCP "$REPO_DIR/probes/camera_probe.py" "$REPO_DIR/probes/camera_stream.py" "$SSH_USER@$HOST:/tmp/"
run_sudo "mkdir -p $INSTALL_DIR $CONFIG_DIR $PROBE_DIR && \
  cp /tmp/$BIN $INSTALL_DIR/agent && chmod +x $INSTALL_DIR/agent && \
  cp /tmp/camera_probe.py /tmp/camera_stream.py $PROBE_DIR/ && chmod +x $PROBE_DIR/*.py"

if [ "$WITH_OCR" = true ]; then
  echo "  -> deploying OCR script ..."
  if [ -f "$REPO_DIR/edge_ocr.py" ]; then
    $SCP "$REPO_DIR/edge_ocr.py" "$SSH_USER@$HOST:/tmp/"
    run_sudo "mkdir -p /opt/agent && cp /tmp/edge_ocr.py /opt/agent/"
  else
    run_sudo "curl -fsSL -o /opt/agent/edge_ocr.py \
      https://raw.githubusercontent.com/MINGTIANJIAN886/edge_agent/main/edge_ocr.py || true"
  fi
fi

# ---------- [4/6] 生成配置 ----------
if [ "$KEEP_CONFIG" = true ] && run_remote "test -f $CONFIG_DIR/config.yaml"; then
  echo "[4/6] Keeping existing config.yaml (--keep-config)"
else
  echo "[4/6] Generating config.yaml ..."
  [ -z "$OTA_DIR" ] && OTA_DIR="/home/$SSH_USER/models"
  run_sudo "mkdir -p $CONFIG_DIR"
  run_remote "cat > /tmp/config.yaml << 'EOF'
device_id: \"$DEVICE_ID\"
download_dir: \"/tmp/agent/downloads\"
heartbeat_interval: 30
log_dir: \"/var/log/agent\"

mqtt:
  broker: \"$MQTT_BROKER\"
  port: $MQTT_PORT
  client_id: \"agent-$DEVICE_ID\"
  username: \"$MQTT_USER\"
  password: \"$MQTT_PASS\"
  topic:
    command: \"edge/$DEVICE_ID/command\"
    download: \"edge/$DEVICE_ID/download\"
    heartbeat: \"edge/$DEVICE_ID/heartbeat\"
    result: \"edge/$DEVICE_ID/result\"
    register: \"edge/$DEVICE_ID/register\"
    mcp_register: \"edge/$DEVICE_ID/mcp/register\"
    mcp_call: \"edge/$DEVICE_ID/mcp/call\"

ota:
  server_url: \"$OTA_SERVER\"
  version_path: \"$OTA_VERSION_PATH\"
  check_interval: 300
  current_version: \"7.0\"
  model_file: \"$OTA_DIR/model.ncnn.bin\"
  model_dir: \"$OTA_DIR\"
  current_symlink: \"$OTA_DIR/current\"
  backup_count: 3
  filter:
    task_match_bonus: 4.0
    tag_bonus: 1.0
    max_size_mb: 20
    min_accuracy: 0
    required_format: \"ncnn\"
    max_latency_ms: 0
    prefer_accuracy: true
  inference_restart_cmd: \"\"

cert_api: \"\"
cert:
  cert_file: \"\"
  key_file: \"\"
  ca_file: \"/etc/ssl/certs/ca-certificates.crt\"
  auto_enroll: false
  token: \"\"

auth:
  method: \"password\"
  token: \"\"
  token_exchange: false

ros:
  enabled: false
  bridge_script_ros1: \"/opt/agent/bridge_ros1.py\"
  bridge_script_ros2: \"/opt/agent/bridge_ros2.py\"
  bridge_python: \"python3\"
  car_max_linear_speed: 2.0
  car_max_angular_speed: 3.14
  safety_watchdog_timeout: 5

# 推理服务地址(本地推理服务,如 http://127.0.0.1:8000);留空则探测报 NOT_CONFIGURED
# inference:
#   service_url: \"http://127.0.0.1:8000\"

# OCR(可选,配合 --with-ocr;按 README-OCR.md 安装 PaddleOCR venv)
# ocr:
#   enabled: true
#   interval: 0
#   script_path: \"/opt/agent/edge_ocr.py\"
#   command_topic: \"edge/$DEVICE_ID/ocr/command\"
#   result_topic: \"edge/$DEVICE_ID/ocr/result\"

capability_probe:
  enabled: true
  probe_on_startup: true
  profile_path: \"/var/lib/edge-agent/profile/robot-profile.json\"
  web_listen: \"0.0.0.0:$WEB_PORT\"
  intervals:
    ros: 60
    ota: 600
    camera: 600
  camera:
    mode: \"auto\"
    devices:
      - \"$CAMERA\"
    script_path: \"$PROBE_DIR/camera_probe.py\"
    timeout_seconds: 5
  ota:
    test_file_url: \"\"
    cache_dir: \"/var/cache/edge-agent\"
    min_free_disk_mb: 100
    check_public_key: false
  inference:
    timeout_seconds: 5
EOF
mv /tmp/config.yaml $CONFIG_DIR/config.yaml"
  echo "  -> $CONFIG_DIR/config.yaml"
fi

# ---------- [5/6] Python 依赖(摄像头探测/流需要 cv2) ----------
echo "[5/6] Checking camera python deps ..."
run_sudo "python3 -c 'import cv2' 2>/dev/null || \
  (apt-get update -qq && apt-get install -y -qq python3-opencv || \
   pip3 install --break-system-packages -q opencv-python-headless) || true"

if [ "$WITH_OCR" = true ]; then
  echo "  -> installing OCR venv (paddlepaddle/paddleocr) ..."
  run_sudo "test -d /opt/agent/ocr_env || python3 -m venv /opt/agent/ocr_env"
  run_sudo "/opt/agent/ocr_env/bin/pip install -q paddlepaddle paddleocr opencv-python 2>/dev/null || true"
fi

# ---------- [6/6] systemd + 健康检查 ----------
echo "[6/6] Installing systemd service ..."
run_sudo "cat > $SERVICE_FILE << EOF
[Unit]
Description=Edge Agent - $DEVICE_ID
After=network.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=$INSTALL_DIR/agent -config $CONFIG_DIR/config.yaml
Restart=always
RestartSec=3
RestartMaxDelaySec=15
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
EOF
systemctl daemon-reload && systemctl enable agent && systemctl restart agent"

sleep 5
IS_ACTIVE="$(run_remote "systemctl is-active agent" || true)"
echo "  -> systemctl is-active: ${IS_ACTIVE:-inactive}"
if [ "$IS_ACTIVE" != "active" ]; then
  echo "WARNING: agent 未正常运行,请查看远端日志: journalctl -u agent -n 50" >&2
fi

echo ""
echo "=== Deploy Complete ==="
echo "Device ID:  $DEVICE_ID"
echo "Dashboard:  http://$HOST:$WEB_PORT/"
echo "Config:     $CONFIG_DIR/config.yaml"
echo "Logs:       journalctl -u agent -f"
echo ""
echo "首次 OTA 检查(可选):"
echo "  python3 或 mosquitto_pub 向 edge/$DEVICE_ID/mcp/call 发送 {\"method\":\"check_update\"}"
