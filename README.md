# Edge Agent

通用边缘设备管理 Agent，通过 MQTT 实现远程控制、文件下发、OTA 模型更新、ROS1/ROS2 小车控制。支持任意 Linux 设备：树莓派、Jetson、x86 工控机、ARM 开发板等。

## 特性

- 远程命令执行 · 文件下发 · OTA 模型更新与回滚
- ROS1/ROS2 小车控制（自动检测版本）
- 统一设备配置（Raspberry Pi、Jetson L4T R32/R35/R36、通用 Linux）
- 公共配置 + `config.d` 设备覆盖
- OCR 文字识别（PaddleOCR）
- MCP 工具协议（设备、模型、OCR 与 ROS 管理工具）
- mTLS 证书自动签发
- 设备心跳（CPU、内存、运行时间）

## 快速开始

```bash
read -rsp "MQTT password: " MQTT_PASS && echo
export MQTT_PASS
curl -fsSL https://raw.githubusercontent.com/MINGTIANJIAN886/edge_agent/main/agent.sh \
  | sudo --preserve-env=MQTT_PASS bash
unset MQTT_PASS
```

自定义参数：

```bash
export MQTT_PASS="<your-password>"
curl -fsSL https://raw.githubusercontent.com/MINGTIANJIAN886/edge_agent/main/agent.sh \
  | sudo --preserve-env=MQTT_PASS bash -s -- my-device --bridge
unset MQTT_PASS
```

指定设备 Profile 和 ROS 工作空间：

```bash
export MQTT_PASS="<your-password>"
export DEVICE_ID="jetson-orin-01"
export DEVICE_PROFILE="jetson-r36"
export ROS_ENABLED=true
export ROS_WORKSPACE_SETUP="/home/jetson/ros2_ws/install/setup.bash"
curl -fsSL https://raw.githubusercontent.com/MINGTIANJIAN886/edge_agent/main/agent.sh \
  | sudo --preserve-env=MQTT_PASS,DEVICE_ID,DEVICE_PROFILE,ROS_ENABLED,ROS_WORKSPACE_SETUP bash
unset MQTT_PASS DEVICE_ID DEVICE_PROFILE ROS_ENABLED ROS_WORKSPACE_SETUP
```

安装器保留默认 MQTT Broker、用户名和 Topic，密码必须在部署时通过 `MQTT_PASS` 提供，不写入 Git 仓库。
再次安装默认保留已有配置；设备差异建议写入 `/etc/agent/config.d/*.yaml`。

## 功能 README

| 文档 | 说明 |
|------|------|
| [配置说明](README-CONFIG.md) | config.yaml 配置 + 环境变量表 |
| [MQTT 命令参考](README-MQTT.md) | 全部 MQTT 主题与命令示例 |
| [OTA 更新](README-OTA.md) | OTA 流程、manifest 格式、服务器搭建 |
| [ROS 小车控制](README-ROS.md) | ROS 三层架构、部署、控制命令 |
| [OCR 文字识别](README-OCR.md) | 部署步骤、使用方式 |
| [开发指南](README-DEVELOPMENT.md) | 编译、运行、发布 |

## 支持平台

| 架构 | 设备示例 |
|------|---------|
| `linux/amd64` | PC、服务器、工控机 |
| `linux/arm64` | 树莓派 3B+/4B/5、Jetson Nano/Orin |
| `linux/armv7l` | 树莓派 Zero/2W/3B |

## 项目结构

```
├── cmd/agent/main.go         # 入口
├── internal/                  # Go 核心逻辑
├── scripts/                  # Python 桥接脚本
├── edge_ocr.py               # PaddleOCR 推理脚本
├── agent.sh                  # 一键安装脚本
├── Makefile                  # 编译 & 发布
├── README.md                 # 本文件
└── README-*.md               # 功能说明文档
```
