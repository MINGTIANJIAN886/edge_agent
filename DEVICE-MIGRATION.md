# 设备迁移指南(DEVICE-MIGRATION)

把 Edge Agent 部署到第二台设备(Jetson / 树莓派 4/5 / 树莓派 3B+ / x86)时,本文件说明需要
调整的所有内容。**核心结论:agent 代码不含设备硬编码,全部差异在配置层。**

## 0. 快速上手

```bash
# 从开发机(agent 仓库目录)直接部署,自动完成构建/上传/配置/systemd
./deploy.sh -h <设备IP> -u <用户> -d <device_id> --pwd <ssh密码>

# 示例:树莓派
./deploy.sh -h 192.168.1.50 -u pi -d pi-001 --pwd raspberry \
    --ota-dir /home/pi/models --with-ocr

# 示例:Jetson(架构自动探测为 arm64)
./deploy.sh -h 172.20.10.10 -u jetson -d jetson002 --pwd jetson
```

部署后验证:

| 检查项 | 方法 |
|--------|------|
| 服务状态 | `systemctl is-active agent`(应为 active) |
| 面板 | 浏览器打开 `http://<设备IP>:8080/`,看"设备参数/能力探测/当前任务&所用模型" |
| 画像 JSON | `curl http://<设备IP>:8080/profile` |
| MQTT 注册 | 向 `edge/<device_id>/mcp/call` 发 `{"method":"device_info"}` |
| OTA | 发 `{"method":"check_update"}`,或在面板 OTA 卡片看结果 |

## 1. 架构与构建

| 设备 | 架构 | 构建目标 |
|------|------|----------|
| Jetson Orin/AGX/Xavier | aarch64 | `make build-aarch64` |
| 树莓派 4 / 5 | arm64 | `make build-aarch64` |
| 树莓派 3B+ | armv7l | `make build-armv7l`(GOARM=7) |
| x86 盒子 / PC | x86_64 | `make build-amd64` |

二进制为 `CGO_ENABLED=0` 静态编译,不依赖目标机 glibc 版本。

## 2. 每设备必改配置项

以 `/etc/agent/config.yaml` 为准(`config.example.yaml` 是带注释的参考模板):

| 配置项 | 说明 |
|--------|------|
| `device_id` | 设备唯一 ID,**决定 MQTT 主题前缀** `edge/<id>/...` |
| `mqtt.client_id` | 建议 `agent-<device_id>`,MQTT 端必须唯一 |
| `mqtt.topic.*` | 全部随 device_id 变化(deploy.sh 自动生成) |
| `mqtt.username/password` | 同一 HiveMQ broker 可复用同一账号;不同 broker 则各自配置 |
| `ota.model_dir` / `current_symlink` | 模型存放目录(权限:agent 以 root 运行则无限制) |
| `capability_probe.camera.devices` | USB 摄像头节点,一般 `/dev/video0` |
| `capability_probe.web_listen` | 端口;多设备在同一网段时注意不冲突 |
| `inference.service_url` | 本机推理服务地址;留空则如实报告 NOT_CONFIGURED |
| `ota.filter.*` | 弱设备建议收紧 `max_size_mb`,提高 `min_accuracy` 等 |

不涉及设备差异的项(证书、auth、ROS 桥接、OCR)按需开启即可。

## 3. 硬件探测在各设备的预期表现

agent 启动时自动探测并写入画像,无需配置:

| 探测项 | Jetson | 树莓派 4/5 | 说明 |
|--------|--------|-----------|------|
| device_type | `NVIDIA Jetson Orin Nano Super...` | `Raspberry Pi 5 Model B Rev...` | 读 `/proc/device-tree/model` |
| gpu | `Orin` (nvidia-smi) | `none` | 无 nvidia-smi 走 compatible 备选,仍无则 none |
| cuda | `12.6` | 空 | 无 NVIDIA 驱动即空 |
| tensorrt | 有版本 | 空 | — |
| ros | `ros2` / `none` | `none` | 未安装 ROS 如实报告 |
| camera | 640x480 探测帧 | USB 摄像头同样 640x480 | 依赖 python3 + cv2 |

**OTA 模型适配**:`FilterCandidates` 会按设备能力硬过滤 `min_cpu_cores` /
`min_memory_mb` / `requires_gpu`。树莓派 `HasGPU=false`(`/dev/nvidia0` 不存在),带
`requires_gpu=true` 的候选会被自动跳过——发布侧请确保 `hardware_requirements` 填准
(见第 5 节)。

## 4. 设备端依赖

| 组件 | 依赖 | 安装 |
|------|------|------|
| 摄像头探测/实时流(必须) | python3 + opencv | `sudo apt install python3-opencv` 或 `pip3 install opencv-python-headless`(deploy.sh 自动装) |
| OCR(可选) | PaddleOCR venv | `python3 -m venv /opt/agent/ocr_env && /opt/agent/ocr_env/bin/pip install paddlepaddle paddleocr opencv-python`(deploy.sh `--with-ocr` 自动装) |
| ROS 桥(可选) | ROS1/ROS2 | 仅 Jetson/装有 ROS 的设备 |
| systemd | — | 树莓派标准系统自带 |

USB 摄像头注意事项:
- V4L2 后端直接可用(`/dev/video0`),与 jetson002 完全同路径,零改动;
- 摄像头"短暂枚举"问题(设备节点重建)已由 agent 内监督重启机制自愈,无需干预;
- CSI 摄像头需要额外的 libcamera/v4l2 驱动层,不在本脚本范围。

## 5. 发布侧:模型能力标签(配合 publish_to_gitee.py)

candidates.json 的 `min_cpu_cores` / `min_memory_mb` / `requires_gpu` 来自
model-library 的 `hardware_requirements`。**规则:**
- `requires_gpu=true` → 无 `/dev/nvidia0` 的设备(树莓派)自动不下载;
- `min_cpu_cores` / `min_memory_mb` 填设备下限,弱设备自动不下载;
- `task` 与 `tags` 参与评分(弱匹配加分),设备上执行的任务越匹配分数越高;
- 发布时脚本会打印每条候选的"设备适配档位",请核对与实际设备能力一致。

## 6. 常见问题

| 现象 | 原因/处理 |
|------|-----------|
| `systemctl is-active agent` 非 active | `journalctl -u agent -n 50` 看日志;常见:config.yaml 语法错、MQTT 账号密码错 |
| 面板打不开 | `capability_probe.enabled` 是否为 true;`web_listen` 端口是否被占用 |
| camera 卡 `CAMERA_OPEN_FAILED` | 摄像头被占用(流开着或别的进程);或设备节点不是 `/dev/video0` |
| OTA "no model passed the filter rules" | 设备能力不满足任何候选(见第 3/5 节),或 `required_format` 与候选不符 |
| inference 卡 NOT_CONFIGURED | 本机推理服务未配置,属正常状态 |
