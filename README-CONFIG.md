# 配置说明

配置文件路径：`/etc/agent/config.yaml`（可通过 `-config` 参数修改）。

## 完整配置

```yaml
device_id: "pi-001"
download_dir: "/tmp/agent/downloads"
heartbeat_interval: 30
log_dir: "/var/log/agent"

mqtt:
  broker: "ca15b49bc8b442638f0cade1e45585ce.s1.eu.hivemq.cloud"
  port: 8883
  client_id: "agent-pi-001"
  username: "liyankun"
  password: "liyankun152455A"
  topic:
    command: "edge/pi-001/command"
    download: "edge/pi-001/download"
    heartbeat: "edge/pi-001/heartbeat"
    result: "edge/pi-001/result"
    register: "edge/pi-001/register"
    mcp_register: "edge/pi-001/mcp/register"
    mcp_call: "edge/pi-001/mcp/call"

ota:
  server_url: "https://amplifier-badge-awoke.ngrok-free.dev"
  version_path: "version.json"
  check_interval: 300
  model_file: "/home/pi/models/model.ncnn.bin"
  model_dir: "/home/pi/models"
  current_symlink: "/home/pi/models/current"
  backup_count: 3
  inference_restart_cmd: "systemctl restart yolov8"

inference:
  service_url: "http://localhost:8080"
  timeout: 30

ocr:
  enabled: false
  script_path: "/opt/agent/edge_ocr.py"
  interval: 30
  conf_threshold: 0.5
  command_topic: "edge/pi-001/ocr/command"
  result_topic: "edge/pi-001/ocr/result"

ros:
  enabled: false
  bridge_script_ros1: "/opt/agent/bridge_ros1.py"
  bridge_script_ros2: "/opt/agent/bridge_ros2.py"
  bridge_python: "python3"
  car_max_linear_speed: 2.0
  car_max_angular_speed: 3.14
  safety_watchdog_timeout: 5
  cmd_vel_topic: "edge/pi-001/car/cmd_vel"
  bridge_result_topic: "edge/pi-001/bridge/result"

auth:
  method: "password"
  token: ""

cert:
  cert_file: "/etc/agent/certs/client.crt"
  key_file: "/etc/agent/certs/client.key"
  ca_file: "/etc/ssl/certs/ca-certificates.crt"
```

## 环境变量

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `DEVICE_ID` | `pi-001` | 设备唯一 ID |
| `MQTT_BROKER` | `ca15b49bc8b442638f0cade1e45585ce.s1.eu.hivemq.cloud` | MQTT 地址 |
| `MQTT_PORT` | `8883` | MQTT 端口 |
| `MQTT_USER` | `liyankun` | MQTT 用户名 |
| `MQTT_PASS` | `liyankun152455A` | MQTT 密码 |
| `OTA_SERVER` | `https://amplifier-badge-awoke.ngrok-free.dev` | OTA 服务器 |
| `ROS_ENABLED` | `false` | 启用 ROS 桥接 |
| `ROS_MAX_LINEAR` | `2.0` | 最大线速度 |
| `ROS_MAX_ANGULAR` | `3.14` | 最大角速度 |
| `ROS_WATCHDOG` | `5` | 看门狗超时 |
| `OCR_ENABLED` | `false` | 启用 OCR |
| `OCR_INTERVAL` | `30` | OCR 间隔 |
| `OCR_CONF_THRESHOLD` | `0.5` | OCR 置信度 |

## 认证方式

```yaml
auth:
  method: "password"  # "password" | "token" | "cert"
```
