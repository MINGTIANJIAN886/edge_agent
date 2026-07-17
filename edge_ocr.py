import argparse
import json
import sys
import time

import cv2
from paddleocr import PaddleOCR


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--conf", type=float, default=0.5)
    args = parser.parse_args()

    ocr = PaddleOCR(use_angle_cls=True, lang='ch', show_log=False)
    cap = cv2.VideoCapture(0)
    ret, frame = cap.read()
    cap.release()

    if not ret:
        print(json.dumps({"success": False, "error": "camera failed", "timestamp": time.time()}))
        return

    result = ocr.ocr(frame, cls=True)
    texts = []
    if result and result[0]:
        for line in result[0]:
            bbox, (text, conf) = line
            if conf >= args.conf:
                texts.append({"text": text, "confidence": conf, "bbox": bbox})

    print(json.dumps({"success": True, "texts": texts, "timestamp": time.time()}))


if __name__ == "__main__":
    main()
