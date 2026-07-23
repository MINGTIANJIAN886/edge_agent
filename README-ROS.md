# ROS1/ROS2 小车控制

三层架构实现与任意 ROS 版本通信：

| 层级 | 方式 | 延迟 |
|------|------|------|
| CLI 发现层 | `ros2` / `rostopic` CLI | 50-200ms |
| Python 桥 | `bridge_ros1.py` / `bridge_ros2.py` | 5-20ms |
| 原生 Go | rclgo（预留） | 1-5ms |

Agent 运行时自动检测 ROS 版本，无需重新编译。

## 部署

```bash
sudo mkdir -p /opt/agent
sudo curl -fsSL https://raw.githubusercontent.com/MINGTIANJIAN886/edge_agent/main/scripts/bridge_ros2.py -o /opt/agent/bridge_ros2.py
sudo curl -fsSL https://raw.githubusercontent.com/MINGTIANJIAN886/edge_agent/main/scripts/bridge_ros1.py -o /opt/agent/bridge_ros1.py
```

config.yaml 启用：

```yaml
ros:
  enabled: true
  bridge_script_ros1: "/opt/agent/bridge_ros1.py"
  bridge_script_ros2: "/opt/agent/bridge_ros2.py"
  car_max_linear_speed: 2.0
  car_max_angular_speed: 3.14
  safety_watchdog_timeout: 5
```

## 桥接生命周期

```bash
BROKER="ca15b49bc8b442638f0cade1e45585ce.s1.eu.hivemq.cloud"
PORT=8883
USER="liyankun"
PASS="liyankun152455A"

mosquitto_pub -h "$BROKER" -p $PORT --cafile /etc/ssl/certs/ca-certificates.crt \
  -u "$USER" -P "$PASS" \
  -t "edge/pi-001/bridge/control" -m '{"cmd":"start"}'  # start | stop | status | restart
```

结果反馈: `edge/pi-001/bridge/result`

## 小车控制

```bash
mosquitto_pub ... -t "edge/pi-001/car/cmd_vel" -m '{"linear_x":0.5,"angular_z":0.0}'        # 前进
mosquitto_pub ... -t "edge/pi-001/car/cmd_vel" -m '{"linear_x":0.0,"angular_z":0.3}'         # 左转
mosquitto_pub ... -t "edge/pi-001/car/emergency_stop" -m '{}'                                 # 急停
```

## 话题订阅

```bash
mosquitto_pub ... -t "edge/pi-001/car/param" \
  -m '{"cmd":"subscribe","topic":"/odom","msg_type":"nav_msgs/msg/Odometry"}'
```

## 服务调用

```bash
mosquitto_pub ... -t "edge/pi-001/car/service_call" \
  -m '{"cmd":"service_call","service":"/reset_odometry","msg_type":"std_srvs/srv/Empty","data":{}}'
```

## ROS1 格式

```
ROS2: geometry_msgs/msg/Twist  →  ROS1: geometry_msgs/Twist
ROS2: std_srvs/srv/Empty       →  ROS1: std_srvs/Empty
```

## 发现操作（无需桥接）

```bash
mosquitto_pub ... -t "edge/pi-001/mcp/call" -m '{"id":"r1","method":"ros_topic_list","params":{}}'
mosquitto_pub ... -t "edge/pi-001/mcp/call" -m '{"id":"r2","method":"ros_node_list","params":{}}'
```
