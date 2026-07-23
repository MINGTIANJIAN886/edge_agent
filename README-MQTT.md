# MQTT 命令参考

所有命令将 `pi-001` 替换为你的设备 ID。

## 通用参数

```bash
BROKER="ca15b49bc8b442638f0cade1e45585ce.s1.eu.hivemq.cloud"
PORT=8883
USER="liyankun"
PASS="liyankun152455A"
```

## 下载文件

```bash
mosquitto_pub -h "$BROKER" -p $PORT --cafile /etc/ssl/certs/ca-certificates.crt \
  -u "$USER" -P "$PASS" \
  -t "edge/pi-001/download" \
  -m '{"url":"https://example.com/file.bin","dest_dir":"/home/pi/downloads","dest_name":"file.bin"}'
```

结果返回到 `edge/pi-001/download/result`。

## 执行命令

```bash
mosquitto_pub -h "$BROKER" -p $PORT --cafile /etc/ssl/certs/ca-certificates.crt \
  -u "$USER" -P "$PASS" \
  -t "edge/pi-001/command" \
  -m '{"id":"cmd1","command":"uptime && free -h","timeout":10}'
```

结果返回到 `edge/pi-001/command/result`。

## 设备信息

```bash
mosquitto_pub -h "$BROKER" -p $PORT --cafile /etc/ssl/certs/ca-certificates.crt \
  -u "$USER" -P "$PASS" \
  -t "edge/pi-001/mcp/call" \
  -m '{"id":"info","method":"device_info","params":{}}'
```

## OTA 更新

```bash
# 检查更新
mosquitto_pub ... -t "edge/pi-001/mcp/call" \
  -m '{"id":"ota","method":"check_update","params":{}}'

# 回滚模型
mosquitto_pub ... -t "edge/pi-001/mcp/call" \
  -m '{"id":"roll","method":"rollback_model","params":{}}'

# 重启服务
mosquitto_pub ... -t "edge/pi-001/mcp/call" \
  -m '{"id":"svc","method":"restart_service","params":{"service_name":"yolov8"}}'

# 获取日志
mosquitto_pub ... -t "edge/pi-001/mcp/call" \
  -m '{"id":"log","method":"get_logs","params":{"unit":"agent","lines":50}}'
```

## 查看所有消息

```bash
mosquitto_sub -h "$BROKER" -p $PORT --cafile /etc/ssl/certs/ca-certificates.crt \
  -u "$USER" -P "$PASS" -t "#" -v
```

## MCP 工具列表

| 工具 | 说明 |
|------|------|
| `device_info` | 系统信息（CPU、内存、磁盘） |
| `execute_command` | 执行 shell 命令 |
| `download_file` | 下载文件 |
| `restart_service` | 重启 systemd 服务 |
| `get_logs` | 获取 journald 日志 |
| `detect_objects` | YOLO 目标检测 |
| `check_update` | OTA 更新检查 |
| `rollback_model` | 模型版本回滚 |
| `run_ocr` | 触发 OCR 识别 |
| `ros_version` | 检测 ROS 版本 |
| `ros_node_list` / `ros_topic_list` / `ros_service_list` | 发现 ROS 节点/话题/服务 |
| `ros_topic_echo` | 订阅话题返回最新消息 |
| `ros_service_call` | 调用 ROS 服务 |
| `ros_param_get` / `ros_param_set` | ROS 参数读写 |
| `bridge_start` / `bridge_stop` / `bridge_status` | 桥接生命周期管理 |
| `car_cmd_vel` | 小车速度指令 |
| `car_emergency_stop` | 急停 |

## 证书管理

```bash
./agent -config /etc/agent/config.yaml -enroll
```
