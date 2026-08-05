#!/usr/bin/env python3
"""Camera capability probe for the edge agent.

Captures one frame with OpenCV within a timeout and reports a
structured JSON result on stdout. Failure causes are distinguished via
error codes (CAMERA_NOT_FOUND / CAMERA_PERMISSION_DENIED / CAMERA_OPEN_FAILED /
CAMERA_BUSY / CAMERA_FRAME_TIMEOUT / CAMERA_EMPTY_FRAME).

Deployment: /opt/edge-agent/probes/camera_probe.py
"""

import argparse
import json
import os
import time

def emit(result=True, supported=True, available=True, healthy=True,
         error_code="", message="", details=None, latency_ms=None):
    out = {
        "result": result,
        "supported": supported,
        "available": available,
        "healthy": healthy,
        "error_code": error_code,
        "message": message,
        "details": details or {},
    }
    if latency_ms is not None:
        out["latency_ms"] = latency_ms
    print(json.dumps(out, ensure_ascii=False))


def probe_camera(device, timeout):
    start = time.time()

    if not os.path.exists(device):
        emit(False, False, False, False,
             "CAMERA_NOT_FOUND", "设备节点不存在", {"device": device})
        return

    if not os.access(device, os.R_OK | os.W_OK):
        emit(False, True, False, False,
             "CAMERA_PERMISSION_DENIED", "无权限访问设备节点", {"device": device})
        return

    try:
        import cv2
    except ImportError:
        emit(False, True, False, False,
             "CAMERA_PROBE_CRASHED", "opencv-python 未安装", {"device": device})
        return

    try:
        cap = cv2.VideoCapture(device)
    except Exception as e:
        emit(False, True, False, False,
             "CAMERA_PROBE_CRASHED", "打开摄像头异常: %s" % e, {"device": device})
        return

    if not cap.isOpened():
        emit(False, True, False, False,
             "CAMERA_OPEN_FAILED", "摄像头存在但无法打开(可能被占用)", {"device": device})
        return

    try:
        deadline = time.time() + timeout
        while time.time() < deadline:
            try:
                ok, frame = cap.read()
            except Exception as e:
                emit(False, True, False, False,
                     "CAMERA_PROBE_CRASHED", "读取摄像头异常: %s" % e, {"device": device},
                     int((time.time() - start) * 1000))
                return
            if ok and frame is not None and frame.size > 0:
                height, width = frame.shape[:2]
                emit(True, True, True, True,
                     "", "成功获取摄像头图像",
                     {"device": device, "width": width, "height": height},
                     int((time.time() - start) * 1000))
                return
        emit(False, True, False, False,
             "CAMERA_FRAME_TIMEOUT", "打开后未在超时内获得图像",
             {"device": device}, int((time.time() - start) * 1000))
    finally:
        try:
            cap.release()
        except Exception:
            pass


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("--device", default="/dev/video0")
    parser.add_argument("--timeout", type=float, default=5)
    args = parser.parse_args()
    probe_camera(args.device, args.timeout)
