# 统一配置说明

Agent 使用一个公共配置文件和可选的设备覆盖目录：

```text
/etc/agent/config.yaml       # MQTT、OTA、OCR、ROS 等公共设置
/etc/agent/config.d/*.yaml   # 单台设备或平台的差异设置
```

覆盖文件按照文件名排序加载，例如 `10-platform.yaml` 先于
`20-device.yaml`。未在覆盖文件中出现的字段会继续使用公共配置。

完整示例见 [`configs/config.example.yaml`](configs/config.example.yaml)。

## 设备自动识别

设置 `device_profile: auto` 后，Agent 会在启动时自动识别：

| 检测结果 | Profile |
|---|---|
| 普通 Linux、x86 工控机、其他 ARM 板卡 | `generic-linux` |
| Raspberry Pi | `raspberry-pi` |
| NVIDIA Jetson，无法确定 L4T 版本 | `jetson` |
| Jetson L4T R32 / JetPack 4 | `jetson-r32` |
| Jetson L4T R35 / JetPack 5 | `jetson-r35` |
| Jetson L4T R36 / JetPack 6 | `jetson-r36` |

也可以在设备覆盖文件里显式指定 Profile。Profile、ROS setup 和工作空间
setup 会出现在 `device_info` 结果及设备心跳中。

## 推荐的公共配置

```yaml
schema_version: 1
device_id: edge-001
device_profile: auto
config_dir: /etc/agent/config.d

runtime:
  command_shell: /bin/bash
  ros_setup: auto
  workspace_setup: ""

mqtt:
  broker: mqtt.example.com
  port: 8883
  client_id: ""
  username: edge-agent
  password: <set-on-device>
  topic: {}

download_dir: /tmp/agent/downloads
heartbeat_interval: 30

ocr:
  enabled: false
  python_bin: /opt/agent/ocr_env/bin/python3
  script_path: /opt/agent/edge_ocr.py
  interval: 30
  conf_threshold: 0.5
  command_topic: ""
  result_topic: ""

ros:
  enabled: false
  cmd_vel_topic: /cmd_vel
  car_max_linear_speed: 2.0
  car_max_angular_speed: 3.14
  safety_watchdog_timeout: 5
  bridge_result_topic: ""
```

`client_id`、MQTT Topic、OCR Topic 和 bridge result Topic 留空时，会根据
最终的 `device_id` 自动生成，避免复制配置后仍然使用旧设备 Topic。

## 不同设备只写差异

### Raspberry Pi

`/etc/agent/config.d/20-device.yaml`：

```yaml
device_id: raspberry-pi-01
device_profile: raspberry-pi

runtime:
  workspace_setup: /home/pi/robot_ws/install/setup.bash

ros:
  enabled: true
  cmd_vel_topic: /cmd_vel
```

### 旧款 Jetson R32

```yaml
device_id: jetson-r32-01
device_profile: jetson-r32

runtime:
  ros_setup: /opt/ros/melodic/setup.bash
  workspace_setup: /home/agilex/agilex_ws/devel/setup.bash

ros:
  enabled: true
  cmd_vel_topic: /cmd_vel
```

### Jetson Orin / JetPack 6

```yaml
device_id: jetson-orin-01
device_profile: jetson-r36

runtime:
  ros_setup: /opt/ros/humble/setup.bash
  workspace_setup: /home/jetson/ros2_ws/install/setup.bash

ros:
  enabled: true
  cmd_vel_topic: /cmd_vel
```

仓库中的 [`configs/overlays`](configs/overlays) 提供了可复制的模板。

## ROS 环境

`runtime.ros_setup` 支持：

- `auto`：根据 `ROS_DISTRO`、设备 Profile 和 `/opt/ros` 自动选择。
- `none`：不加载 ROS 环境。
- 绝对路径：例如 `/opt/ros/noetic/setup.bash`。

`runtime.workspace_setup` 用于加载小车自己的工作空间，例如：

- ROS1：`/home/agilex/agilex_ws/devel/setup.bash`
- ROS2：`/home/jetson/ros2_ws/install/setup.bash`

远程 Shell、MCP `execute_command` 和 ROS 管理工具都会使用相同环境，因此可以
直接执行 `roslaunch`、`ros2 launch`、`rostopic` 和 `ros2 topic`。

任意远程 Shell 功能保持开启。请为每台设备配置独立 MQTT Topic ACL，并优先
使用 TLS/mTLS；获得命令 Topic 发布权限等同于获得该设备上 Agent 服务的执行权限。

## 安装环境变量

| 变量 | 默认值 | 说明 |
|---|---|---|
| `DEVICE_ID` | `pi-001` | 设备唯一 ID |
| `DEVICE_PROFILE` | `auto` | 设备 Profile |
| `MQTT_BROKER` | 项目默认 Broker | MQTT 地址 |
| `MQTT_PORT` | `8883` | MQTT 端口 |
| `MQTT_USER` | 项目默认用户 | MQTT 用户名 |
| `MQTT_PASS` | 无 | 新建或强制重建配置时必须设置 |
| `ROS_ENABLED` | `false` | 部署 ROS bridge |
| `ROS_SETUP` | `auto` | ROS setup 路径 |
| `ROS_WORKSPACE_SETUP` | 空 | 项目工作空间 setup 路径 |
| `ROS_CMD_VEL_TOPIC` | `/cmd_vel` | 小车速度 Topic |
| `ROS_MAX_LINEAR` | `2.0` | 最大线速度 |
| `ROS_MAX_ANGULAR` | `3.14` | 最大角速度 |
| `ROS_WATCHDOG` | `5` | 停车看门狗秒数 |
| `OCR_ENABLED` | `false` | 启用 OCR |
| `OCR_INTERVAL` | `30` | OCR 间隔 |
| `OCR_CONF_THRESHOLD` | `0.5` | OCR 置信度 |

安装器再次运行时默认保留现有 `/etc/agent/config.yaml`。只有显式传入
`--force-config` 才会备份并重建配置。

## 配置校验

配置使用严格字段校验：

- 未知字段会导致 Agent 拒绝启动，避免拼写错误被静默忽略。
- `schema_version` 当前必须为 `1`。
- `runtime.command_shell` 必须是绝对路径。
- 配置保存权限为 `0600`。

修改后可通过日志确认最终配置：

```bash
sudo systemctl restart agent
sudo journalctl -u agent -n 50 --no-pager
```
