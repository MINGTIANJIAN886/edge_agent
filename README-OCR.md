# OCR 文字识别

通过 PaddleOCR 在边缘设备上进行文字识别，支持定时自动触发和 MQTT 远程触发。

## 安装依赖

```bash
sudo python3 -m venv /opt/agent/ocr_env
sudo /opt/agent/ocr_env/bin/pip install paddlepaddle paddleocr opencv-python
```

ARM 设备加速：

```bash
sudo apt install -y libhdf5-dev libatlas-base-dev
```

所有平台统一使用 `/opt/agent/ocr_env`，但 Raspberry Pi、Jetson R32 和
Jetson JetPack 6 应分别在设备本机创建虚拟环境并安装与其 Python/CUDA 版本匹配的
依赖，不要直接复制另一台设备的虚拟环境目录。

## 部署脚本

```bash
sudo mkdir -p /opt/agent
sudo curl -fsSL https://raw.githubusercontent.com/MINGTIANJIAN886/edge_agent/main/edge_ocr.py -o /opt/agent/edge_ocr.py && sudo chmod +x /opt/agent/edge_ocr.py
```

## 配置

```yaml
ocr:
  enabled: true
  python_bin: "/opt/agent/ocr_env/bin/python3"
  script_path: "/opt/agent/edge_ocr.py"
  interval: 30
  conf_threshold: 0.5
  command_topic: "edge/pi-001/ocr/command"
  result_topic: "edge/pi-001/ocr/result"
```

一键安装：

```bash
export MQTT_PASS="<your-password>"
export OCR_ENABLED=true
export OCR_INTERVAL=60
curl -fsSL https://raw.githubusercontent.com/MINGTIANJIAN886/edge_agent/main/agent.sh \
  | sudo --preserve-env=MQTT_PASS,OCR_ENABLED,OCR_INTERVAL bash
unset MQTT_PASS OCR_ENABLED OCR_INTERVAL
```

## MQTT 指令

| 指令 | 说明 |
|------|------|
| `{"action":"start"}` | 开始定时 OCR |
| `{"action":"start","interval":10}` | 开始定时，自定义间隔 |
| `{"action":"stop"}` | 停止定时 |
| `{"action":"snapshot"}` | 立即执行一次 |
| `{}` | 默认 snapshot |

## 结果格式

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

## 验证

```bash
sudo journalctl -u agent -f

mosquitto_pub -h "ca15b49bc8b442638f0cade1e45585ce.s1.eu.hivemq.cloud" \
  -p 8883 --cafile /etc/ssl/certs/ca-certificates.crt \
  -u "liyankun" -P "${MQTT_PASS:?export MQTT_PASS first}" \
  -t "edge/pi-001/ocr/command" -m '{}'

mosquitto_sub -h "ca15b49bc8b442638f0cade1e45585ce.s1.eu.hivemq.cloud" \
  -p 8883 --cafile /etc/ssl/certs/ca-certificates.crt \
  -u "liyankun" -P "${MQTT_PASS:?export MQTT_PASS first}" \
  -t "edge/pi-001/ocr/result" -v
```
