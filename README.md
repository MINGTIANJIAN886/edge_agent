# Edge Agent

边缘设备管理 Agent，通过 MQTT 与服务器通信，支持远程命令、OTA 更新、心跳上报、MCP 工具调用。

## 一键安装

```bash
curl -fsSL https://github.com/<USER>/edge-agent/releases/latest/download/install.sh | sudo bash -s -- \
  --device-id <DEVICE_ID> \
  --mqtt-broker <HOST> \
  --mqtt-user <USER> \
  --mqtt-pass <PASS>
```

## 功能列表

| 功能 | 说明 |
|------|------|
| **心跳上报** | 定时上报设备状态到 `edge/{id}/heartbeat` |
| **远程命令** | 通过 MQTT 远程执行 Shell 命令 |
| **文件下载** | 从 HTTP URL 下载文件到设备 |
| **OTA 更新** | 自动检查版本、下载模型并重启推理服务 |
| **MCP 工具** | 注册 MCP 工具集，支持 device_info / execute_command / download_file 等 |
| **摄像头推流** | 实时推送摄像头 JPEG 帧到 `edge/{id}/cameraframe` |
| **OCR 识别** | 定时或 MQTT 触发拍照 OCR，结果上报到 `edge/{id}/ocr/result` |

## 构建

```bash
make build-all     # 编译 amd64 + arm64 + armv7l
make release       # 编译 + 压缩 + 校验
```

## OCR 功能

Agent 支持通过 PaddleOCR 在边缘设备上进行文字识别，支持定时自动触发和 MQTT 远程触发。

### 部署 OCR

```bash
# 1. 在边缘设备安装 Python 依赖
pip install paddlepaddle paddleocr opencv-python

# 2. 上传推理脚本
scp edge_ocr.py pi@192.168.x.x:/opt/agent/

# 3. 配置 config.yaml 启用 OCR
#    ocr.enabled: true
#    ocr.interval: 30  # 自动触发间隔（秒），0=关闭定时

# 4. 重新编译部署 Agent
make build-arm64
scp build/agent-aarch64 pi@192.168.x.x:/usr/local/bin/agent
ssh pi@192.168.x.x "sudo systemctl restart agent"
```

### MQTT 消息

**触发 OCR**（向 `edge/{id}/ocr/command` 发布任意消息）：
```
# 空消息即触发一次 OCR
```

**结果上报**（订阅 `edge/{id}/ocr/result`）：
```json
{
  "device_id": "pi-001",
  "success": true,
  "trigger": "timer",
  "texts": [
    {"text": "前方施工", "confidence": 0.97, "bbox": [[10,20],[100,20],[100,50],[10,50]]}
  ],
  "timestamp": 1712345678.123
}
```
