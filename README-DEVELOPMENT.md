# 开发指南

## 编译

```bash
git clone https://github.com/MINGTIANJIAN886/edge_agent.git
cd edge_agent
make deps        # 安装 Go 依赖
make build       # 编译当前平台
make build-aarch64   # 树莓派 arm64
make build-armv7l    # 树莓派 32 位
make build-all       # 全部平台
```

编译产物在 `build/` 目录。

## 运行

```bash
./build/agent-amd64 -config /etc/agent/config.yaml
```

## 发布

```bash
make release
```

## Docker

```bash
docker run -d --name edge-agent \
  -v /etc/agent:/etc/agent \
  --network host \
  ghcr.io/MINGTIANJIAN886/edge-agent:latest
```

## 系统管理

```bash
sudo systemctl status agent
sudo journalctl -u agent -f
sudo systemctl status car_bridge   # --bridge 模式
```

## 项目结构

```
├── cmd/agent/main.go              # 入口
├── internal/
│   ├── bridge/                    # ROS 桥管理器
│   ├── config/                    # YAML 配置解析
│   ├── download/                  # 文件下载
│   ├── enroll/                    # 证书自动签发
│   ├── heartbeat/                 # 心跳上报
│   ├── mcp/                       # MCP 工具注册 & 调度
│   ├── ocr/                       # OCR 执行 & 调度
│   ├── ota/                       # OTA 更新 & 回滚
│   ├── remote/                    # 远程命令
│   └── ros/                       # ROS 发现层
├── scripts/                       # ROS 桥接脚本
├── edge_ocr.py                    # PaddleOCR 推理脚本
├── agent.sh                       # 一键安装
└── Makefile
```
