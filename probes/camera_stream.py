#!/usr/bin/env python3
"""Camera live stream source for the edge agent.

Opens the camera once, continuously captures frames, encodes them as
JPEG and writes them to stdout with a 4-byte big-endian length prefix.
The Go agent reads this stream and serves it as MJPEG to browsers.

Deployment: /opt/edge-agent/probes/camera_stream.py
"""

import argparse
import struct
import sys
import time

def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--device", default="/dev/video0")
    parser.add_argument("--fps", type=float, default=10)
    parser.add_argument("--quality", type=int, default=85)
    args = parser.parse_args()

    try:
        import cv2
    except ImportError:
        sys.stderr.write("CAMERA_STREAM_ERROR: opencv-python not installed\n")
        sys.exit(1)

    cap = None
    for attempt in range(3):
        try:
            cap = cv2.VideoCapture(args.device, cv2.CAP_V4L2)
        except Exception:
            cap = None
        if cap is not None and cap.isOpened():
            break
        if cap is not None:
            cap.release()
        time.sleep(0.5)
    if cap is None or not cap.isOpened():
        sys.stderr.write("CAMERA_STREAM_ERROR: cannot open %s after retries\n" % args.device)
        sys.exit(1)

    out = sys.stdout.buffer
    interval = 1.0 / max(args.fps, 1.0)
    enc = [cv2.IMWRITE_JPEG_QUALITY, args.quality]

    try:
        while True:
            start = time.time()
            ok, frame = cap.read()
            if not ok or frame is None or frame.size == 0:
                sys.stderr.write("CAMERA_STREAM_ERROR: read failed\n")
                break
            ok, buf = cv2.imencode(".jpg", frame, enc)
            if not ok:
                continue
            data = buf.tobytes()
            out.write(struct.pack("<I", len(data)))
            out.write(data)
            out.flush()
            elapsed = time.time() - start
            if elapsed < interval:
                time.sleep(interval - elapsed)
    finally:
        cap.release()


if __name__ == "__main__":
    main()
